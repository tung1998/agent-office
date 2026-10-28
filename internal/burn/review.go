package burn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/verdict"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Review stages (ADR-112), in the order a piece meets them: the problem
// (is it real, worth doing), the plan (do it, and how), the result (keep it).
// A mechanism: which are reviewed, and by whom, is the person's review
// profile (ADR-113); none, a piece a scan found is done as it is.
var ReviewStages = []string{"issue", "plan", "result"}

// reviewer is who reviews stage under the Burn's profile, as it is now (it
// may be edited while the Burn runs); ok is false when stage is not reviewed.
func (s *Service) reviewer(ctx context.Context, b storage.BurnSession, stage string) (storage.BurnReviewStage, bool) {
	if b.ReviewProfileID == "" {
		return storage.BurnReviewStage{}, false
	}
	p, err := s.store.Burn().ReviewProfile(ctx, b.ReviewProfileID)
	if err != nil {
		return storage.BurnReviewStage{}, false
	}
	r, ok := p.Stages[stage]
	if r.AgentID == "" {
		r.AgentID = b.AgentID
	}
	return r, ok
}

// CheckReviewers refuses a profile whose reviewer is paused: its review
// would only get the "agent is off" notice, read as a no.
func (s *Service) CheckReviewers(ctx context.Context, profileID string) error {
	if profileID == "" {
		return nil
	}
	p, err := s.store.Burn().ReviewProfile(ctx, profileID)
	if err != nil {
		return errors.New("không tìm thấy hồ sơ review của Burn: hãy chọn hồ sơ khác")
	}
	for _, stage := range ReviewStages {
		if r, ok := p.Stages[stage]; ok {
			if err := s.CheckAgent(ctx, r.AgentID); err != nil {
				return fmt.Errorf("%s (hồ sơ %s): %w", reviewLabel[stage], p.Name, err)
			}
		}
	}
	return nil
}

func (s *Service) reviews(ctx context.Context, b storage.BurnSession, stage string) bool {
	_, ok := s.reviewer(ctx, b, stage)
	return ok
}

// gate runs the reviews a chosen piece meets before it is done (problem,
// plan): go is false when it was turned down (skipped) or the review could
// not run (failed: the loop waits, then asks again).
func (s *Service) gate(ctx context.Context, b storage.BurnSession, it storage.BurnItem) (proceed, failed bool) {
	if it.Status != "queued" { // started before: it goes on
		return true, false
	}
	for _, stage := range []string{"issue", "plan"} {
		r, on := s.reviewer(ctx, b, stage)
		if !on || slices.Contains(it.Reviewed, stage) || stage == "issue" && it.Kind == KindQuest { // a quest's problem is the person's call
			continue
		}
		agents := s.stageAgents(ctx, b, r)
		if _, hit := s.limitsHit(ctx, agents...); hit { // out of quota: not asked, the Burn waits
			return false, true
		}
		ok, note, err := s.review(ctx, b, &it, stage)
		if err != nil {
			return false, s.reviewErrAttempt(ctx, b, it, stage, agents, err)
		}
		s.unblock(ctx, b.ProjectID)
		it.ReviewErrAttempts = 0
		from := it.Status
		it.ReviewNote = note
		if ok {
			it.Reviewed = append(it.Reviewed, stage)
		} else {
			it.Status, it.Summary = "skipped", reviewLabel[stage]+": không làm. "+oneLine(note, 400)
		}
		// the person may have moved it meanwhile: theirs wins
		if err := s.store.Burn().UpdateItemFrom(context.WithoutCancel(ctx), it, from); err != nil || !ok {
			if err == nil {
				s.sayItem(ctx, b, it)
			}
			return false, false
		}
	}
	return true, false
}

// finish reviews a piece reported done (status "review"): agreed, it is done
// (committed to its branch, or its diff put up); not, it goes again with what
// the reviewer said, then fails. failed: the review could not run.
func (s *Service) finish(ctx context.Context, b storage.BurnSession, it storage.BurnItem) (failed bool) {
	r, ok := s.reviewer(ctx, b, "result")
	var agents []string
	if ok {
		agents = s.stageAgents(ctx, b, r)
		if _, hit := s.limitsHit(ctx, agents...); hit { // out of quota: not asked, the Burn waits
			return true
		}
	}
	from := it.Status
	ok, note, err := s.review(ctx, b, &it, "result")
	if err != nil {
		return s.reviewErrAttempt(ctx, b, it, "result", agents, err) // stays "review": asked again, unless past the limit
	}
	s.unblock(ctx, b.ProjectID)
	it.ReviewErrAttempts = 0
	ctx = context.WithoutCancel(ctx)
	it.ReviewNote = note
	if ok {
		it.Status, it.Reviewed = "done", append(it.Reviewed, "result")
		// the person may have moved it meanwhile (skip, requeue): theirs
		// wins, and deliver (which merges the diff into the run) must not
		// run until the claim above succeeds.
		if err := s.store.Burn().UpdateItemFrom(ctx, it, from); err != nil {
			return false
		}
		cur, err := s.store.Burn().Item(ctx, it.ID) // re-read: priority etc. may have changed since the claim
		if err != nil || cur.Status != "done" {
			// the person moved it again between the claim above and this
			// read (eg. skip right after): theirs wins, nothing is merged.
			return false
		}
		it = cur
		s.deliver(ctx, b, &it)
		if err := s.store.Burn().UpdateItemFrom(ctx, it, "done"); err != nil {
			// the person skipped it after it was claimed/merged: their
			// choice wins; the merge already happened and stays.
			return false
		}
	} else {
		it.Status, it.Summary = reviewAgain(it, reviewLabel["result"]+": chưa đạt. "+oneLine(note, 400))
		if err := s.store.Burn().UpdateItemFrom(ctx, it, from); err != nil {
			return false
		}
	}
	s.sayItem(ctx, b, it)
	return false
}

// deliver hands a finished piece over: merged into the run (ADR-123). Its
// changes no longer apply (another piece changed the same lines): it goes
// again, from the run as it is now.
func (s *Service) deliver(ctx context.Context, b storage.BurnSession, it *storage.BurnItem) {
	err := s.integrate(ctx, b, it)
	if errors.Is(err, errConflict) {
		// replayed on the run as it is now: merged at once when that is clean,
		// else back to its worker with the clashing files (not a failed try)
		files, rerr := s.replayItem(ctx, b, *it)
		switch {
		case rerr == nil && len(files) == 0:
			err = s.integrate(ctx, b, it)
		case rerr == nil:
			it.Status, it.Attempts = "queued", max(it.Attempts-1, 0)
			it.Summary = "Xung đột khi gộp với việc khác đã gộp: " + strings.Join(files, ", ")
			it.ReviewNote = strings.TrimSpace(it.ReviewNote + "\n\nOffice put your changes on top of the run's latest code; these files now have conflict markers (<<<<<<< / >>>>>>>): " +
				strings.Join(files, ", ") + ". Resolve them keeping both sides' intent, run the build/test again, then report done.")
			return
		}
	}
	switch {
	case errors.Is(err, errConflict):
		_ = s.DropWorktree(ctx, *it)
		it.Worktree = ""
		it.Status, it.Summary = s.failedOrAgain(*it, "Chưa gộp được vào lần chạy ("+err.Error()+"): làm lại trên bản mới nhất.")
	case err != nil:
		it.Status, it.Summary = s.failedOrAgain(*it, strings.TrimSpace(it.Summary+"\n(Chưa giao được kết quả: "+err.Error()+")"))
	}
}

var reviewLabel = map[string]string{"issue": "Review vấn đề", "plan": "Review cách làm", "result": "Review kết quả"}

// maxReviewErrAttempts: how many times in a row review() may fail with a
// system error (an agent gone, a bad review profile) before the piece is
// failed instead of asked again forever (ADR-120). The AI connection's limit
// and a technical error (signed out, refused, down: ADR-152) are not counted
// here: the first waits for its reset, the second blocks the whole Burn.
const maxReviewErrAttempts = 3

// reviewErrAttempt counts a system error from review(): past the threshold,
// the piece is marked failed instead of looping at this stage forever.
// agents are the stage's reviewer and its workflow's agents (ADR-124):
// their connections hitting their limit is not counted, and is not failed, since
// the loop already waits a minute and asks again (step, waitLimit). retry
// reports whether it is still worth asking again (false once failed, or
// once the context was cancelled: no point saving then). it is saved only
// if its status is still what this call started from, so a change made
// elsewhere while review() ran (paused, skipped, a person acting on it) is
// not overwritten.
func (s *Service) reviewErrAttempt(ctx context.Context, b storage.BurnSession, it storage.BurnItem, stage string, agents []string, err error) (retry bool) {
	if ctx.Err() != nil {
		return false
	}
	if _, hit := s.limitsHit(ctx, agents...); hit || chat.OutOfTokens(err) {
		return true
	}
	if technical(err) { // the machine's, not the piece's: the Burn waits (ADR-152)
		s.block(ctx, b.ProjectID, reviewLabel[stage]+": "+err.Error())
		return true
	}
	ctx = context.WithoutCancel(ctx)
	from := it.Status
	it.ReviewErrAttempts++
	if it.ReviewErrAttempts < maxReviewErrAttempts {
		_ = s.store.Burn().UpdateItemFrom(ctx, it, from)
		return true
	}
	it.Status = "failed"
	it.Summary = fmt.Sprintf("%s: lỗi hệ thống liên tục (%d lần liền): %s", reviewLabel[stage], it.ReviewErrAttempts, err.Error())
	if s.store.Burn().UpdateItemFrom(ctx, it, from) == nil {
		s.sayItem(ctx, b, it)
		s.watch(ctx, b) // the same reason again and again: the process, not the piece (ADR-143)
	}
	return false
}

// review asks the stage's reviewer (an agent, or the workflow it runs) about
// a piece, in the stage's review chat, read-only: agreed or not, and why.
// Not reviewed (the profile changed meanwhile): agreed.
func (s *Service) review(ctx context.Context, b storage.BurnSession, it *storage.BurnItem, stage string) (bool, string, error) {
	r, ok := s.reviewer(ctx, b, stage)
	if !ok {
		return true, "", nil
	}
	conv, err := s.ensureReviewConversation(ctx, b, it, stage, r.AgentID)
	if err != nil {
		return false, "", err
	}
	items, _ := s.store.Burn().Items(ctx, b.ID)
	diff := ""
	if stage == "result" {
		diff = reviewDiff(ctx, it.Worktree)
	}
	text := reviewPrompt(b, *it, stage, diff, items...)
	if r.Workflow != "" {
		text = workflow.Call(r.Workflow, text)
	}
	rctx := chat.WithCeiling(chat.WithTurnTimeout(actor.With(ctx, "burn:"+b.StartedBy), 0), perm.Read)
	if stage == "result" { // it reads the piece's own worktree
		rctx = chat.WithTree(rctx, "burn-"+it.ID, true)
	} else if s.RunTree(b) != "" { // the run's code, with what was merged in it, not the project's (ADR-144)
		rctx = chat.WithTree(rctx, runTree(b), true)
	}
	res, err := s.run(rctx, conv, text)
	if err == nil && res.failed != "" {
		err = errors.New(res.failed)
	}
	if err != nil {
		return false, "", err
	}
	return accepted(stage, res.text), strings.TrimSpace(res.text), nil
}

// openCriterion is an acceptance line not met: "- [ ] …" (ADR-223).
var openCriterion = regexp.MustCompile(`(?m)^\s*[-*]\s*\[ \]`)

// accepted reads a review's verdict; a result agreed with an acceptance line
// still open is not agreed: the evidence wins over the verdict (ADR-223).
func accepted(stage, text string) bool {
	return verdictOf(text) == "yes" && (stage != "result" || !openCriterion.MatchString(text))
}

// ensureReviewConversation gives a piece's review at stage a hidden chat of
// its own with the reviewer (ADR-114), a new one once the reviewer was
// changed; it is kept on the piece, to read from the Burn.
func (s *Service) ensureReviewConversation(ctx context.Context, b storage.BurnSession, it *storage.BurnItem, stage, agentID string) (string, error) {
	if id := it.ReviewConversations[stage]; id != "" {
		c, err := s.store.Chat().GetConversation(ctx, id)
		if err == nil && (agentID == "" || c.AgentID == agentID) && c.Cleaned == "" {
			return id, nil
		}
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return "", err
		}
	}
	conv, err := s.chat.StartConversationPurpose(ctx, b.ProjectID, agentID, chat.BurnReviewPurpose)
	if err != nil {
		return "", err
	}
	conv.Title = oneLine(reviewLabel[stage]+": "+it.Title, 80)
	_ = s.store.Chat().UpdateConversation(ctx, conv)
	if it.ReviewConversations == nil {
		it.ReviewConversations = map[string]string{}
	}
	it.ReviewConversations[stage] = conv.ID
	// saved on the latest piece: the person may have moved it meanwhile
	cur, err := s.store.Burn().Item(ctx, it.ID)
	if err != nil {
		return "", err
	}
	cur.ReviewConversations = it.ReviewConversations
	if err := s.store.Burn().UpdateItem(ctx, cur); err != nil {
		return "", err
	}
	return conv.ID, nil
}

// reviewDiffMax bounds the diff put in a result review's prompt: past it the
// reviewers get the list of files and read the rest themselves.
const reviewDiffMax = 40000

// reviewDiff is what a piece changed in its worktree since it started, for
// its result review: given in the prompt, the reviewers (three of them in a
// workflow) need not each go looking for it, reading a growing session at
// every step. "" when there is none or git fails (they read it themselves).
func reviewDiff(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	base, err := gitIn(ctx, dir, "", "rev-parse", "--verify", "-q", baseRef)
	if err != nil {
		base = "HEAD" // started before ADR-123
	}
	if _, err := gitIn(ctx, dir, "", "add", "-A"); err != nil { // new files too, as finish takes them
		return ""
	}
	stat, err := gitIn(ctx, dir, "", "diff", "--cached", "--stat", "--no-renames", base)
	if err != nil || stat == "" {
		return ""
	}
	diff, err := gitIn(ctx, dir, "", "diff", "--cached", "--no-renames", base)
	if err != nil {
		return stat
	}
	if len(diff) > reviewDiffMax {
		return stat + "\n\n(the full diff is too long to give here: read the files above in the worktree)"
	}
	return stat + "\n\n" + diff
}

// reviewPrompt asks a stage's reviewer about a piece (burn/review.md);
// diff is what the piece changed (the result stage); items are the Burn's,
// for the run's other pieces on the same files.
func reviewPrompt(b storage.BurnSession, it storage.BurnItem, stage, diff string, items ...storage.BurnItem) string {
	d := struct {
		Stage, ID, Kind, Title, Detail, Focus, ReviewNote string
		Summary, Worktree, Agree, Disagree, RunBranch     string
		Diff                                              string
		Round, Rounds                                     int
		FocusChecks, Related, Checked                     []string
	}{Round: it.Attempts, Rounds: maxReviewRounds, Stage: stage, ID: it.ID, Kind: it.Kind, Title: it.Title, Detail: it.Detail, Focus: b.Focus,
		Worktree: it.Worktree, Agree: verdictAgree, Disagree: verdictDisagree, Diff: diff,
		RunBranch: b.RunBranch, Related: related(b, it, items)}
	if stage == "result" {
		d.FocusChecks = checks(b)
		// they passed: a piece reaches its result review only then (ADR-131)
		d.Checked = verifyCommands(b, it.Worktree)
		if it.ReviewNote != "" { // whole: the next round is judged against what it asked
			d.ReviewNote = clip(strings.TrimSpace(it.ReviewNote), reviewNoteMax)
		}
		d.Summary = oneLine(it.Summary, 800)
	}
	return prompts.Render("burn/review", d)
}

// reviewNoteMax bounds the earlier review's notes handed to the next one.
const reviewNoteMax = 6000

// clip cuts s to n runes, keeping its lines.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// The first line of a reviewer's answer (verdictOf reads it).
const (
	verdictAgree    = verdict.Agree
	verdictDisagree = verdict.Disagree
)

// verdictOf reads the reviewer's conclusion (unclear counts as no, as a
// workflow's vote); the goal judge of automations reads the same (ADR-132).
func verdictOf(text string) string { return verdict.Of(text) }
