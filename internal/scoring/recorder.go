package scoring

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Window is how long a score still changes after its job ends (a diff merged
// or rejected later, a Burn piece sent back, the person's 👍/👎).
const Window = 7 * 24 * time.Hour

// Recorder writes the scores of finished jobs and keeps them up to date.
type Recorder struct {
	St  storage.Store
	Now func() time.Time
}

func (r Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// JobEnded scores a finished job: what it ran on (its last call), its labels
// from the person's request, and its quality so far.
func (r Recorder) JobEnded(ctx context.Context, job storage.Job, request string, src Source) error {
	s := storage.JobScore{JobID: job.ID, ProjectID: job.ProjectID, ConversationID: job.ConversationID, MessageID: job.MessageID,
		AgentID: job.AgentID, Status: job.Status, FinishedAt: r.now()}
	if job.FinishedAt != nil {
		s.FinishedAt = job.FinishedAt.UTC()
	}
	s.TaskKind, s.Difficulty = Classify(request, src)
	runs, err := r.St.Runs().List(ctx, storage.RunFilter{JobID: job.ID, Limit: 500})
	if err != nil {
		return err
	}
	for i, run := range runs { // newest first
		if run.CostUSD != nil {
			s.CostUSD += *run.CostUSD
		}
		s.DurationMS += run.DurationMS
		if s.Model == "" && (run.Status == "ok" || i == len(runs)-1) {
			s.ProviderID, s.Model, s.Effort = run.ProviderID, run.Model, run.Effort
			if s.AgentID == "" {
				s.AgentID = run.AgentID
			}
		}
	}
	yes := true
	s.Parts.NoCorrection = &yes
	if err := r.fill(ctx, &s); err != nil {
		return err
	}
	return r.St.Scores().Upsert(ctx, s)
}

// fill works out the parts that follow from the project's records and Q.
func (r Recorder) fill(ctx context.Context, s *storage.JobScore) error {
	s.UpdatedAt = r.now()
	if s.Status != "done" {
		s.Q = nil
		return nil
	}
	var err error
	if s.Parts.Merged, err = r.merged(ctx, *s); err != nil {
		return err
	}
	s.Parts.NoRework = r.noRework(ctx, *s)
	s.Parts.ChecksGreen = r.checksGreen(ctx, *s)
	if s.Feedback != 0 {
		ok := s.Feedback > 0
		s.Parts.NoCorrection = &ok
	}
	s.Q = Quality(s.Parts)
	return nil
}

func (r Recorder) merged(ctx context.Context, s storage.JobScore) (*bool, error) {
	if s.ConversationID == "" || s.MessageID == "" {
		return nil, nil
	}
	ps, err := r.St.Chat().PatchRefs(ctx, s.ConversationID)
	if err != nil {
		return nil, err
	}
	var any, open bool
	for _, p := range ps {
		if p.MessageID != s.MessageID {
			continue
		}
		any = true
		switch p.Status {
		case "rejected", "failed":
			f := false
			return &f, nil
		case "pending", "queued":
			open = true
		}
	}
	if !any || open {
		return nil, nil
	}
	t := true
	return &t, nil
}

// noRework: a Burn piece's work chat done without being sent back.
func (r Recorder) noRework(ctx context.Context, s storage.JobScore) *bool {
	if s.ConversationID == "" {
		return nil
	}
	sess, err := r.St.Burn().SessionByConversation(ctx, s.ConversationID)
	if err != nil {
		return nil
	}
	items, err := r.St.Burn().Items(ctx, sess.ID)
	if err != nil {
		return nil
	}
	for _, it := range items {
		if it.WorkConversationID == s.ConversationID {
			ok := it.Attempts <= 1
			return &ok
		}
	}
	return nil
}

var checkCmds = []string{"go test", "go build", "go vet", "typecheck", "npm test", "pnpm test", "npm run test", "pytest", "make test", "cargo test", "cargo build", "tsc"}

// checksGreen: the build/test/typecheck commands the answer ran all passed.
func (r Recorder) checksGreen(ctx context.Context, s storage.JobScore) *bool {
	if s.MessageID == "" {
		return nil
	}
	msg, err := r.St.Chat().MessageHead(ctx, s.MessageID)
	if err != nil {
		return nil
	}
	var ran, failed bool
	for _, t := range msg.Tools {
		if hasAny(strings.ToLower(t.Summary), checkCmds) {
			ran = true
			failed = failed || t.Error
		}
	}
	if !ran {
		return nil
	}
	ok := !failed
	return &ok
}

// Final tells whether a score no longer changes: frozen, or its job ended
// longer than Window ago (even if no refresh froze it yet).
func Final(s storage.JobScore, now time.Time) bool {
	return s.Frozen || s.FinishedAt.Before(now.Add(-Window))
}

var (
	refreshMu   sync.Mutex
	lastRefresh = map[string]time.Time{}
)

// RefreshEvery is how often a project's open scores are re-scored at most.
const RefreshEvery = time.Minute

// Refresh re-scores a project's open scores (those that changed only) and
// freezes those past Window; at most once per RefreshEvery for a project.
// A score changed meanwhile (a 👍/👎) is left as it is.
func (r Recorder) Refresh(ctx context.Context, projectID string) error {
	now := r.now()
	refreshMu.Lock()
	if last, ok := lastRefresh[projectID]; ok && now.Sub(last) < RefreshEvery && now.After(last) {
		refreshMu.Unlock()
		return nil
	}
	lastRefresh[projectID] = now
	refreshMu.Unlock()
	cut := now.Add(-Window)
	old, err := r.St.Scores().Open(ctx, cut)
	if err != nil {
		return err
	}
	for _, s := range old {
		if s.ProjectID != projectID {
			continue
		}
		prev := s.UpdatedAt
		if err := r.fill(ctx, &s); err != nil {
			return err
		}
		s.Frozen = true
		if err := r.St.Scores().Update(ctx, s, prev); err != nil && !errors.Is(err, storage.ErrStale) {
			return err
		}
	}
	list, err := r.St.Scores().List(ctx, storage.ScoreFilter{ProjectID: projectID, Since: cut})
	if err != nil {
		return err
	}
	for _, s := range list {
		if s.Frozen {
			continue
		}
		prev, prevQ, prevParts := s.UpdatedAt, s.Q, s.Parts
		if err := r.fill(ctx, &s); err != nil {
			return err
		}
		if sameQ(prevQ, s.Q) && reflect.DeepEqual(prevParts, s.Parts) {
			continue
		}
		if err := r.St.Scores().Update(ctx, s, prev); err != nil && !errors.Is(err, storage.ErrStale) {
			return err
		}
	}
	return nil
}

func sameQ(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// change applies fn to a score and writes it back unless it changed
// meanwhile, then tries again (a few times) on the fresh one. A final score
// is not changed: ErrStale.
func (r Recorder) change(ctx context.Context, get func() (storage.JobScore, error), fn func(*storage.JobScore) bool) error {
	for range 3 {
		s, err := get()
		if err != nil {
			return err
		}
		if Final(s, r.now()) {
			return storage.ErrStale
		}
		prev := s.UpdatedAt
		if !fn(&s) {
			return nil
		}
		if err := r.fill(ctx, &s); err != nil {
			return err
		}
		if err := r.St.Scores().Update(ctx, s, prev); !errors.Is(err, storage.ErrStale) {
			return err
		}
	}
	return storage.ErrStale
}

// MarkCorrection: the person's message corrects the last answer of the chat
// (unless they rated that answer themselves, or it is final).
func (r Recorder) MarkCorrection(ctx context.Context, conversationID string) error {
	get := func() (storage.JobScore, error) {
		list, err := r.St.Scores().List(ctx, storage.ScoreFilter{ConversationID: conversationID, Since: r.now().Add(-Window)})
		if err != nil {
			return storage.JobScore{}, err
		}
		if len(list) == 0 {
			return storage.JobScore{}, storage.ErrNotFound
		}
		return list[0], nil // newest
	}
	err := r.change(ctx, get, func(s *storage.JobScore) bool {
		if s.Feedback != 0 {
			return false
		}
		no := false
		s.Parts.NoCorrection = &no
		return true
	})
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrStale) {
		return nil
	}
	return err
}

// SetFeedback is the person's 👍 (1) / 👎 (-1) / none (0) on an answer.
// ErrStale: the score is final.
func (r Recorder) SetFeedback(ctx context.Context, messageID string, v int) error {
	if v < -1 || v > 1 {
		return errors.New("feedback phải là -1, 0 hoặc 1")
	}
	return r.change(ctx, func() (storage.JobScore, error) { return r.St.Scores().ByMessage(ctx, messageID) }, func(s *storage.JobScore) bool {
		s.Feedback = v
		if v == 0 {
			yes := true
			s.Parts.NoCorrection = &yes
		}
		return true
	})
}
