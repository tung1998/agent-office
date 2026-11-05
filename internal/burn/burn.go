// Package burn runs a project's Burn (spec 2026-10-01-burn-design): its main
// agent, on its own and with full access, finds what is unfinished, what
// could be better and what is broken, and does it — a few pieces at a time
// (ADR-117), each in its own worktree and chat — until it is stopped or its time is up. Stopped,
// the piece in progress waits (its worktree kept); started again, it goes on.
// Each run gives one result to review (ADR-123): a worktree on a branch of its own.
package burn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

// Kinds of work a Burn looks for.
var Kinds = map[string]bool{"unfinished": true, "upgrade": true, "bug": true, "idea": true}

// KindQuest is a piece the person gives (not a worker's find): it waits in
// found, a free slot takes it before a new worker looks for work, and it is
// done as given, with no scan.
const KindQuest = "quest"

// maxQuestTitle, maxQuestDetail cap what the person writes for a quest.
const maxQuestTitle, maxQuestDetail = 160, 4000

// AddQuest gives the project's Burn a piece of work to do as given; a
// running Burn takes it at the next free slot, the oldest first.
func (s *Service) AddQuest(ctx context.Context, b storage.BurnSession, title, detail string) (storage.BurnItem, error) {
	return s.addQuest(ctx, b, Quest{Title: title, Detail: detail})
}

// addQuest is AddQuest with who follows it up (ADR-197).
func (s *Service) addQuest(ctx context.Context, b storage.BurnSession, q Quest) (storage.BurnItem, error) {
	title, detail := strings.Join(strings.Fields(q.Title), " "), strings.TrimSpace(q.Detail)
	switch {
	case title == "":
		return storage.BurnItem{}, errors.New("quest cần tên")
	case len([]rune(title)) > maxQuestTitle:
		return storage.BurnItem{}, fmt.Errorf("tên quest dài quá %d ký tự", maxQuestTitle)
	case len([]rune(detail)) > maxQuestDetail:
		return storage.BurnItem{}, fmt.Errorf("mô tả quest dài quá %d ký tự", maxQuestDetail)
	}
	it, err := s.store.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: title, Kind: KindQuest, Detail: detail, Status: "found", Owner: q.Owner})
	if err == nil {
		s.wake(b.ProjectID)
	}
	return it, err
}

// SessionOr is the project's Burn, or the defaults for a first one (not
// saved); agent gives the project's default agent (nil: none picked).
func SessionOr(ctx context.Context, st storage.Store, projectID string, agent func(context.Context, string) (storage.Agent, error)) (storage.BurnSession, error) {
	b, err := st.Burn().Session(ctx, projectID)
	if errors.Is(err, storage.ErrNotFound) {
		b = storage.BurnSession{ProjectID: projectID, ModelTier: storage.TierBalanced, MaxParallel: 1, Order: "roadmap", State: "stopped"}
		if agent != nil {
			if a, err := agent(ctx, projectID); err == nil && !a.Disabled {
				b.AgentID = a.ID
			}
		}
		return b, nil
	}
	return b, err
}

// Session is the project's Burn, or the defaults for a first one (not saved).
func (s *Service) Session(ctx context.Context, projectID string) (storage.BurnSession, error) {
	var agent func(context.Context, string) (storage.Agent, error)
	if s.chat != nil {
		agent = s.chat.DefaultAgent
	}
	return SessionOr(ctx, s.store, projectID, agent)
}

// DefaultEnd is when a Burn started with no stop time stops: the agent's
// weekly limit reset, else 8 hours from now.
func (s *Service) DefaultEnd(ctx context.Context, agentID string) time.Time {
	t, ok := s.WeeklyReset(ctx, agentID)
	if !ok {
		t = time.Now().Add(8 * time.Hour)
	}
	return t.UTC()
}

// Quest is a piece given in a batch (an automation's @@quest lines, ADR-132);
// Owner is who follows it up (an agent key, "" = the default agent; ADR-197).
type Quest struct{ Title, Detail, Owner string }

// openStatus: a piece not finished yet (a quest with its title is not given twice).
var openStatus = map[string]bool{"found": true, "queued": true, "doing": true, "paused": true, "review": true, StatusHanded: true}

// StatusHanded: a task an agent was handed with delegate (ADR-210): in
// progress outside the Burn, which never takes it.
const StatusHanded = "handed"

// questKey is a title as dedupe compares it: case and spaces aside.
func questKey(title string) string { return strings.ToLower(strings.Join(strings.Fields(title), " ")) }

// AddQuests gives the project's Burn the quests an automation found (ADR-132),
// leaving out those an open piece already has (same title, case and spaces
// aside). With start, a stopped Burn starts on its saved settings (no stop
// time: its plan's) once any was added; who names the starter. A quest that
// cannot be added is skipped; err says why the last one failed, or why the
// Burn did not start.
func (s *Service) AddQuests(ctx context.Context, projectID string, qs []Quest, start bool, who string) (added int, started bool, err error) {
	if len(qs) == 0 {
		return 0, false, nil
	}
	b, err := s.Session(ctx, projectID)
	if err != nil {
		return 0, false, err
	}
	if b.ID == "" { // no Burn saved yet: the defaults, to hold them
		if b, err = s.store.Burn().SaveSession(ctx, b); err != nil {
			return 0, false, err
		}
	}
	open := map[string]bool{}
	items, err := s.store.Burn().Items(ctx, b.ID)
	if err != nil {
		return 0, false, err
	}
	for _, it := range items {
		if openStatus[it.Status] {
			open[questKey(it.Title)] = true
		}
	}
	var addErr error
	for _, q := range qs {
		k := questKey(q.Title)
		if k == "" || open[k] {
			continue
		}
		if _, e := s.addQuest(ctx, b, q); e != nil {
			addErr = e
			continue
		}
		open[k] = true
		added++
	}
	if added == 0 || !start || b.Active() {
		return added, false, addErr
	}
	if _, err := s.TurnOn(ctx, projectID, who, time.Now()); err != nil { // as turned on on the dashboard: stops by its plan
		return added, false, err
	}
	return added, true, addErr
}

// Service runs the projects' Burns.
type Service struct {
	store storage.Store
	chat  *chat.Engine
	trees *worktree.Manager

	mu     sync.Mutex
	root   context.Context
	loops  map[string]context.CancelFunc // by project
	wakes  map[string]chan struct{}      // by project: its loop looks again now
	nones  map[string]int                // scans in a row that moved nothing on, by run (maxNones)
	found  map[string]int                // pieces a scan recorded, by scan (ADR-130)
	marked map[string]int                // coverage items checked when a scan started, by scan (ADR-141)
	full   map[string]bool               // coverage all checked and a scan found nothing more, by run (ADR-141)
	held   map[string]bool               // waiting for the person's review, by run (ADR-135)
	capped map[string]bool               // StopAfter reached once, by run (ADR-135)
	tried  map[string]time.Time          // the schedule's slot last looked at, by project
	find   bool                          // a free slot gets a worker finding its own piece (off: only the queued ones)
	runMu  sync.Mutex                    // a run's worktree: made, and pieces merged into it, one at a time

	notify Notify      // a run's summary to a bot's chat (ADR-120); nil: none
	hold   func() bool // true: no new piece or scan starts (an update waits for office to be idle, ADR-255)
}

// SetHold sets what holds the Burns' new work: while it holds, a Burn only
// goes on with what it has in progress, as when it waits for a review.
func (s *Service) SetHold(fn func() bool) { s.hold = fn }

func (s *Service) holding() bool { return s.hold != nil && s.hold() }

// Notify posts text to a bot's chat (the channels manager).
type Notify func(ctx context.Context, channelID, chatID, text string) error

// SetNotify sends the runs' summaries to the bots' chats the Burns name.
func (s *Service) SetNotify(n Notify) { s.notify = n }

// New builds a Service; Start runs the Burns that were running.
func New(st storage.Store, engine *chat.Engine, trees *worktree.Manager) *Service {
	return &Service{store: st, chat: engine, trees: trees, loops: map[string]context.CancelFunc{}, wakes: map[string]chan struct{}{}, nones: map[string]int{}, found: map[string]int{}, marked: map[string]int{}, full: map[string]bool{}, held: map[string]bool{}, capped: map[string]bool{}, tried: map[string]time.Time{}, find: true}
}

// SetFindWork off: free slots take only the pieces queued, no worker finds
// its own (tests that drive the pieces themselves).
func (s *Service) SetFindWork(on bool) { s.find = on }

// Start goes on with the Burns that ran when office stopped: their piece in
// progress waits, then goes on first.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.root = ctx
	s.mu.Unlock()
	go s.schedule(ctx) // the Burns that turn on by themselves
	running, err := s.store.Burn().Running(ctx)
	if err != nil {
		return
	}
	for _, b := range running {
		if b.RunBranch == "" { // running since before ADR-123
			b.RunBranch = newRunBranch(time.Now())
			_, _ = s.store.Burn().SaveSession(ctx, b)
		}
		s.pauseDoing(ctx, b.ID)
		s.say(ctx, b, "Office khởi động lại: Burn chạy tiếp.")
		s.spawn(b.ProjectID)
	}
}

// Begin starts a project's Burn (settings as saved); who is the admin who
// confirmed it.
func (s *Service) Begin(ctx context.Context, projectID, who string) (storage.BurnSession, error) {
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil {
		return b, err
	}
	if b.EndsAt != nil && !b.EndsAt.After(time.Now()) {
		return b, errors.New("giờ tắt đã qua: hãy đặt giờ tắt mới")
	}
	// each piece runs in its own worktree: without git it would edit the folder itself
	if p, err := s.store.Repos().Get(ctx, projectID); err != nil || p.Path == "" || !worktree.IsRepo(ctx, p.Path) {
		return b, errors.New("Burn cần project là một git repo (mỗi việc chạy trong worktree riêng)")
	}
	if err := s.CheckAgent(ctx, b.AgentID); err != nil {
		return b, err
	}
	if templateOf(b) == "custom" && hunt(b) == "" {
		return b, errors.New("mẫu Tùy chỉnh cần prompt tìm việc: hãy viết trong Cài đặt Burn")
	}
	if err := s.CheckReviewers(ctx, b.ReviewProfileID); err != nil {
		return b, err
	}
	if !b.Active() || b.RunBranch == "" {
		// each start, a chat and a branch (or worktree) of its own: the earlier ones stay as they were
		b.ConversationID, b.RunBranch = "", newRunBranch(time.Now())
		if b.Coverage != "" { // a new run looks at everything again (ADR-141)
			b.Coverage = uncheck(b.Coverage)
			if planTemplate(b.Coverage) != templateOf(b) { // written for another template: the first scan writes one for this
				b.Coverage = ""
			}
			_ = s.store.Burn().SetCoverage(ctx, b.ID, b.Coverage)
		}
	}
	if err := s.ensureConversation(ctx, &b); err != nil {
		return b, err
	}
	now := time.Now().UTC()
	b.State, b.StartedBy, b.StartedAt, b.WaitingUntil = "running", who, &now, nil
	if b, err = s.store.Burn().SaveSession(ctx, b); err != nil {
		return b, err
	}
	s.sayStart(ctx, b)
	s.spawn(projectID)
	return b, nil
}

// CheckAgent refuses an agent that is paused: a Burn with it would only get
// the "agent is off" notice.
func (s *Service) CheckAgent(ctx context.Context, agentID string) error {
	if agentID == "" {
		return nil // the first lead that is on
	}
	ag, err := s.store.Agents().Get(ctx, agentID)
	if err != nil {
		return errors.New("không tìm thấy agent của Burn: hãy chọn agent khác")
	}
	if ag.Disabled {
		return fmt.Errorf("agent %s đang tắt: chọn agent khác cho Burn hoặc bật lại agent này", ag.Name)
	}
	return nil
}

// ensureConversation gives the Burn a chat with its agent: its own while the
// agent is the same, a new one once the agent was changed (the old one stays
// with the agent it had, which may be paused now) or the Burn started again.
func (s *Service) ensureConversation(ctx context.Context, b *storage.BurnSession) error {
	if b.ConversationID != "" {
		c, err := s.store.Chat().GetConversation(ctx, b.ConversationID)
		if err == nil && (b.AgentID == "" || c.AgentID == b.AgentID) && c.Cleaned == "" { // cleaned: it takes no more
			return nil
		}
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
	}
	conv, err := s.chat.StartConversationPurpose(ctx, b.ProjectID, b.AgentID, "burn")
	if err != nil {
		return err
	}
	conv.Title = "Burn " + time.Now().Format("02/01 15:04")
	_ = s.store.Chat().UpdateConversation(ctx, conv)
	// saved before SetMode: a failure there must not lose the conversation just made
	b.ConversationID = conv.ID
	if saved, err := s.store.Burn().SaveSession(ctx, *b); err != nil {
		return err
	} else {
		*b = saved
	}
	return s.chat.SetMode(ctx, conv.ID, "operate")
}

// Stop stops a project's Burn: the answer running now is cancelled, its
// piece of work waits.
func (s *Service) Stop(ctx context.Context, projectID string) error {
	return s.stop(ctx, projectID, "dừng hẳn")
}

// stop is Stop saying why, in the run's summary (ADR-120).
func (s *Service) stop(ctx context.Context, projectID, why string) error {
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil {
		return err
	}
	ran := b.Active()
	b.State, b.WaitingUntil = "stopped", nil
	if _, err := s.store.Burn().SaveSession(ctx, b); err != nil {
		return err
	}
	s.mu.Lock()
	if cancel, ok := s.loops[projectID]; ok {
		cancel()
		delete(s.loops, projectID)
	}
	s.mu.Unlock()
	if b.ConversationID != "" {
		s.chat.StopAll(b.ConversationID)
	}
	if items, err := s.store.Burn().Items(ctx, b.ID); err == nil {
		for _, it := range items {
			if it.Status == "doing" && it.WorkConversationID != "" {
				s.chat.StopAll(it.WorkConversationID)
			}
		}
	}
	s.pauseDoing(ctx, b.ID)
	if ran {
		s.report(context.WithoutCancel(ctx), b, why)
	}
	return nil
}

// Drain lets a project's Burn finish what it has in progress (the pieces
// being done, paused or waiting for their result's review), then it stops: no
// scan, no new piece. The scan running now is cancelled.
func (s *Service) Drain(ctx context.Context, projectID string) error {
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil {
		return err
	}
	if !b.Active() {
		return errors.New("Burn không chạy")
	}
	if b.State == "draining" {
		return nil
	}
	b.State = "draining" // a limit's wait (WaitingUntil) still holds
	if _, err := s.store.Burn().SaveSession(ctx, b); err != nil {
		return err
	}
	s.say(ctx, b, "Làm nốt việc đang dở rồi dừng (không nhận việc mới).")
	if b.ConversationID != "" {
		s.chat.StopAll(b.ConversationID)
	}
	s.spawn(projectID) // its loop ends it once nothing is left
	s.wake(projectID)
	return nil
}

// Resume takes a draining Burn back to work as before, or a blocked one
// (ADR-152) to try again now.
func (s *Service) Resume(ctx context.Context, projectID string) error {
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil {
		return err
	}
	if b.State != "draining" && b.State != "blocked" {
		return errors.New("Burn không ở trạng thái đang hoàn thành nốt hay tạm dừng vì lỗi")
	}
	if b.State == "blocked" { // tried again now, not after its wait (ADR-152)
		b.WaitingUntil = nil
	}
	b.State = "running"
	if b.WaitingUntil != nil && b.WaitingUntil.After(time.Now()) {
		b.State = "waiting_limit"
	}
	if _, err := s.store.Burn().SaveSession(ctx, b); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.nones, b.RunBranch) // resumed by hand: it scans again
	delete(s.full, b.RunBranch)
	s.mu.Unlock()
	s.say(ctx, b, "Chạy tiếp như cũ.")
	s.spawn(projectID)
	s.wake(projectID)
	return nil
}

// wake has a project's loop look again now (it may be waiting a long while).
// Wake has a project's loop look again now: a piece queued (Làm trước).
func (s *Service) Wake(projectID string) { s.wake(projectID) }

func (s *Service) wake(projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case s.wakes[projectID] <- struct{}{}:
	default:
	}
}

func (s *Service) pauseDoing(ctx context.Context, sessionID string) {
	items, _ := s.store.Burn().Items(ctx, sessionID)
	for _, it := range items {
		if it.Status == "doing" {
			it.Status = "paused"
			_ = s.store.Burn().UpdateItem(ctx, it)
		}
	}
}

func (s *Service) spawn(projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.loops[projectID]; ok || s.root == nil {
		return
	}
	ctx, cancel := context.WithCancel(s.root)
	wake := make(chan struct{}, 1)
	s.loops[projectID], s.wakes[projectID] = cancel, wake
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("burn: loop panic", "project", projectID, "panic", r, "stack", string(debug.Stack()))
				_ = s.stop(context.WithoutCancel(ctx), projectID, fmt.Sprintf("lỗi hệ thống: %v", r))
			}
			s.mu.Lock()
			delete(s.loops, projectID)
			if s.wakes[projectID] == wake {
				delete(s.wakes, projectID)
			}
			s.mu.Unlock()
			cancel()
		}()
		s.loop(ctx, projectID, wake)
	}()
}

// loop keeps its slots full (ADR-117, ADR-126): the pieces in progress and
// the ones the person queued first, else a new worker that finds one piece
// and does it from A to Z, each in its own worktree and chat. No coordinator,
// no waiting while idle: a slot freed is filled at once. Out of quota, it
// waits for the reset; draining, it only finishes what is in progress.
// wake: a worker is done, or the Burn was drained or resumed.
func (s *Service) loop(ctx context.Context, projectID string, wake chan struct{}) {
	var (
		mu   sync.Mutex
		busy = map[string]bool{} // the pieces a worker has
		wg   sync.WaitGroup
	)
	defer wg.Wait()
	for ctx.Err() == nil {
		b, err := s.store.Burn().Session(ctx, projectID)
		if err != nil || !b.Active() {
			return
		}
		now := time.Now()
		if b.EndsAt != nil && !b.EndsAt.After(now) {
			_ = s.stop(context.WithoutCancel(ctx), projectID, "tới giờ tắt") // its time is up
			return
		}
		if b.WaitingUntil != nil || b.State == "waiting_limit" {
			if b.WaitingUntil != nil && b.WaitingUntil.After(now) {
				wait(ctx, wake, min(time.Until(*b.WaitingUntil), time.Minute))
				continue
			}
			msg := "Quota AI đã reset: chạy tiếp."
			if b.State == "waiting_limit" {
				b.State = "running"
			}
			if b.State == "blocked" { // stays blocked until a turn goes through (unblock)
				msg = "Thử chạy lại sau lỗi kỹ thuật."
			}
			b.WaitingUntil = nil
			b, _ = s.store.Burn().SaveSession(ctx, b)
			s.say(ctx, b, msg)
		}
		draining := b.State == "draining"
		if conv := b.ConversationID; s.ensureConversation(ctx, &b) == nil && b.ConversationID != conv { // its agent was changed while it ran
			b, _ = s.store.Burn().SaveSession(ctx, b)
		}
		items, _ := s.store.Burn().Items(ctx, b.ID)
		if !draining && s.stopAfterReached(b, items) { // its count of pieces is done (ADR-135)
			s.say(ctx, b, fmt.Sprintf("Đã xong %d việc trong lần chạy này (giới hạn %d).", doneInRun(b, items), b.StopAfter))
			_ = s.Drain(context.WithoutCancel(ctx), projectID)
			continue
		}
		// waiting for the person's review (ADR-135): only what is in progress
		// or an update waiting for office to be idle (ADR-255)
		held := !draining && (s.holding() || s.heldForReview(ctx, b))
		mu.Lock()
		started := 0
		run := func(it storage.BurnItem) {
			busy[it.ID] = true
			wg.Add(1)
			gap := time.Duration(started) * startGap
			started++
			go func() {
				defer wg.Done()
				sleep(ctx, gap) // workers start one after another, not all at once
				func() {
					defer func() {
						if r := recover(); r != nil {
							slog.Error("burn: step panic", "project", projectID, "item", it.ID, "panic", r, "stack", string(debug.Stack()))
							cur, cerr := s.store.Burn().Item(context.WithoutCancel(ctx), it.ID)
							if cerr != nil {
								cur = it
							}
							cur.Status, cur.Summary = "failed", fmt.Sprintf("lỗi trong lúc chạy: %v", r)
							_ = s.store.Burn().UpdateItem(context.WithoutCancel(ctx), cur)
						}
					}()
					if ctx.Err() == nil { // stopped while it waited its turn
						s.step(ctx, b, it)
					}
				}()
				mu.Lock()
				delete(busy, it.ID)
				mu.Unlock()
				select {
				case wake <- struct{}{}:
				default:
				}
			}()
		}
		// a scan (ADR-130): one at a time, beside the work (not in its
		// slots), each recording up to scanBatch pieces; no cap on the pieces
		// waiting (ADR-141): it scans until its coverage plan is done. One
		// under way goes on (draining too); a new one not draining, not out
		// of quota (it would only fail), not once scans are over.
		if sc, ok := scanOf(items); ok {
			if !busy[sc.ID] {
				run(sc)
			}
		} else if !draining && !held && s.find && !builderBusy(b, items) && !s.scansOver(b) && !s.waitLimit(ctx, b) {
			if sc, err := s.store.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: huntTitle, Status: "queued", RunBranch: b.RunBranch}); err == nil {
				items = append(items, sc)
				s.startScan(b, sc.ID)
				run(sc)
			}
		}
		// the work: MaxParallel pieces at a time
		free := parallel(b) - pieces(items, busy)
		for ; free > 0; free-- {
			it, ok := next(items, busy, draining || held)
			if !ok {
				break
			}
			if it.Status == "found" { // a quest or a piece a scan found: taken; the person may have moved it meanwhile
				it.Status = "queued"
				if err := s.store.Burn().UpdateItemFrom(ctx, it, "found"); err != nil {
					break // looked at again on the next round
				}
			}
			run(it)
		}
		working := len(busy)
		mu.Unlock()
		if !draining && working == 0 && s.scansOver(b) { // all covered, or scans move nothing on: it finishes
			if _, more := next(items, busy, false); !more {
				_ = s.Drain(context.WithoutCancel(ctx), projectID)
				continue
			}
		}
		if draining {
			if working == 0 && s.drained(ctx, b.ID) {
				return // all it had is finished
			}
			wait(ctx, wake, time.Minute)
			continue
		}
		wait(ctx, wake, time.Minute) // a worker done wakes it
	}
}

// drained ends a draining Burn with nothing left in progress; false when it
// was resumed meanwhile.
func (s *Service) drained(ctx context.Context, sessionID string) bool {
	b, err := s.store.Burn().SessionByID(ctx, sessionID)
	if err != nil || b.State != "draining" {
		return err != nil
	}
	b.State, b.WaitingUntil = "stopped", nil
	_, _ = s.store.Burn().SaveSession(ctx, b)
	s.report(context.WithoutCancel(ctx), b, "đã làm nốt việc dở")
	return true
}

// step takes a piece one step on: its result's review once done, the
// reviews before it is done, then its work — whichever the review profile
// has (ADR-112); a worker not claimed yet goes straight to finding one.
func (s *Service) step(ctx context.Context, b storage.BurnSession, it storage.BurnItem) {
	if it.Status == "review" { // done: its result waits for the reviewer (ADR-112)
		if s.finish(ctx, b, it) && !s.waitLimit(ctx, b) && !s.blocked(ctx, b) {
			sleep(ctx, time.Minute) // the review could not run: ask again in a while
		}
		return
	}
	if !hunting(it) {
		if proceed, failed := s.gate(ctx, b, it); !proceed { // reviewed before it is done
			if failed && !s.waitLimit(ctx, b) && !s.blocked(ctx, b) {
				sleep(ctx, time.Minute)
			}
			return
		}
		// gate saves what it found (stages passed, its error count) straight to
		// the store, not back into it: reread, so work does not clobber that.
		if cur, err := s.store.Burn().Item(ctx, it.ID); err == nil {
			it = cur
		}
	}
	if failed := s.work(ctx, b, it); failed {
		s.waitLimit(ctx, b) // the connection's limit: it waits for the reset
	}
}

// next: a piece done waiting for its review, a paused one (it goes on),
// else the one chosen first, else a quest the person gave, else a piece a
// scan found (highest priority first) — none a worker has already;
// draining, only the pieces in progress. Quests go one at a time, oldest
// first: the person often gives a plan whose steps build on each other, and
// one started before the last is merged finds nothing to build on.
func next(items []storage.BurnItem, busy map[string]bool, draining bool) (storage.BurnItem, bool) {
	order := []string{"review", "paused", "doing", "queued", KindQuest, "found"}
	if draining {
		order = order[:3]
	}
	questOn := slices.ContainsFunc(items, func(it storage.BurnItem) bool {
		return it.Kind == KindQuest && it.Status != "found" && openStatus[it.Status]
	})
	for _, st := range order {
		for _, it := range items {
			if busy[it.ID] || hunting(it) { // a scan runs beside the work
				continue
			}
			if (it.Status == "queued" || it.Status == "found") && clashes(it, items, busy) { // not begun: after the one on its files (ADR-144)
				continue
			}
			if it.Status == "found" && it.Kind == KindQuest {
				if st == KindQuest && !questOn {
					return it, true
				}
				continue
			}
			if st != KindQuest && it.Status == st {
				return it, true
			}
		}
	}
	return storage.BurnItem{}, false
}

// scanOf: the scan under way (or waiting to go on), if any.
func scanOf(items []storage.BurnItem) (storage.BurnItem, bool) {
	for _, it := range items {
		if hunting(it) && (it.Status == "queued" || it.Status == "doing" || it.Status == "paused") {
			return it, true
		}
	}
	return storage.BurnItem{}, false
}

// pieces: how many of the busy ones are pieces of work (a scan is not).
func pieces(items []storage.BurnItem, busy map[string]bool) int {
	n := 0
	for _, it := range items {
		if busy[it.ID] && !hunting(it) {
			n++
		}
	}
	return n
}

// wait is sleep that ends early when woken.
func wait(ctx context.Context, wake <-chan struct{}, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-wake:
	case <-time.After(d):
	}
}

// startGap spaces out the workers a Burn starts together: each is an AI CLI
// with its own MCP servers, and starting several at once stalls the machine.
const startGap = 2 * time.Second

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// runCtx is how a Burn's turns run: the machine, its model, no time limit.
func runCtx(ctx context.Context, b storage.BurnSession, tree string, noPatch bool) context.Context {
	ctx = actor.With(ctx, "burn:"+b.StartedBy)
	ctx = chat.WithFullAccess(chat.WithModelTier(chat.WithTurnTimeout(ctx, 0), b.ModelTier))
	return chat.WithTree(ctx, tree, noPatch)
}

// turnResult is what a turn did.
type turnResult struct {
	subagents int
	cost      float64
	failed    string
	text      string // the answer
}

// run sends text in one of the Burn's conversations and waits for the answer
// (a person chatting there now: it waits its turn).
func (s *Service) run(ctx context.Context, conversationID, text string) (turnResult, error) {
	var res turnResult
	var turn *chat.Turn
	for {
		t, _, err := s.chat.Send(ctx, conversationID, text, nil)
		if errors.Is(err, chat.ErrBusy) {
			sleep(ctx, 10*time.Second)
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			continue
		}
		if err != nil {
			return res, err
		}
		turn = t
		break
	}
	for seq := 0; ; {
		evs, done, wake := turn.Since(seq)
		seq += len(evs)
		for _, e := range evs {
			switch {
			case e.Type == "tool" && e.Tool != nil && (e.Tool.Name == "Task" || e.Tool.Name == "Agent"):
				res.subagents++
			case e.Type == "error":
				res.failed = e.Text
			case e.Type == "done" && e.Message != nil:
				res.text = e.Message.Content
				if e.Message.CostUSD != nil {
					res.cost += *e.Message.CostUSD
				}
			}
		}
		if done {
			return res, nil
		}
		select {
		case <-wake:
		case <-ctx.Done():
			turn.Cancel()
			return res, ctx.Err()
		}
	}
}

// work runs one piece in its own worktree; reported done, it is merged into
// the run (ADR-123).
func (s *Service) work(ctx context.Context, b storage.BurnSession, it storage.BurnItem) (failed bool) {
	// read before it moves to "doing": the person may skip it meanwhile, and theirs wins
	from := it.Status
	again := it.Status == "paused" || it.Status == "doing" || it.Worktree != "" // a retry goes on from what is there
	tree := "burn-" + it.ID
	it.Status, it.Attempts = "doing", it.Attempts+1
	it.RunBranch = b.RunBranch // the run it is done in
	// its worktree first: a run without one would write in the project's own folder
	p, perr := s.store.Repos().Get(ctx, b.ProjectID)
	// tried before, yet its worktree is gone: its chat remembers work that is not there
	fresh := it.Attempts > 1 && s.trees != nil && !s.trees.Exists(b.ProjectID, tree)
	if perr == nil && s.trees != nil {
		it.Worktree, perr = s.pieceTree(ctx, b, p.Path, tree)
	} else if perr == nil {
		perr = errors.New("office không có worktree")
	}
	if perr != nil {
		it.Status, it.Summary = "failed", "không tạo được worktree: "+perr.Error()
		if s.store.Burn().UpdateItemFrom(ctx, it, from) != nil {
			return false // the person moved it meanwhile: theirs wins
		}
		s.sayItem(ctx, b, it)
		return false
	}
	// its own chat (ADR-116): what other pieces did does not crowd its context
	if perr = s.ensureWorkConversation(ctx, b, &it); perr != nil {
		it.Status, it.Summary = s.failedOrAgain(it, "không tạo được chat làm việc: "+perr.Error())
		if s.store.Burn().UpdateItemFrom(ctx, it, from) != nil {
			return false
		}
		s.sayItem(ctx, b, it)
		return false
	}
	if s.store.Burn().UpdateItemFrom(ctx, it, from) != nil {
		return false // skipped (or otherwise moved) while its worktree/chat were set up: do not start the agent
	}
	// held back for its review (ADR-112): no diff until the reviewer agrees
	held := s.reviews(ctx, b, "result")
	prompt := workPrompt(b, it, again && !fresh, held, fresh)
	worker := hunting(it)
	switch {
	case worker:
		s.say(ctx, b, "**Quét** tìm việc mới")
	case again:
		s.say(ctx, b, "**Làm tiếp** "+oneLine(it.Title, 120))
	default:
		s.say(ctx, b, "**Bắt đầu** "+oneLine(it.Title, 120))
	}
	if worker { // a scan: it only records pieces (ADR-130)
		items, _ := s.store.Burn().Items(ctx, b.ID)
		prompt = scanPrompt(b, it, items, again)
	}
	turnCtx := chat.WithPinnedTree(runCtx(ctx, b, tree, true), tree)
	res, err := s.run(turnCtx, it.WorkConversationID, prompt)
	if err == nil && res.failed == "" {
		s.unblock(ctx, b.ProjectID) // a turn went through
	}
	if !worker && err == nil && res.failed == "" && ctx.Err() == nil {
		// it ended its turn without burn_done/burn_fail (often: waiting on a
		// background command, which never wakes it): asked once to report
		if c, e := s.store.Burn().Item(ctx, it.ID); e == nil && c.Status == "doing" {
			r2, err2 := s.run(turnCtx, it.WorkConversationID, prompts.Render("burn/report", struct{ ID string }{it.ID}))
			res.subagents, res.cost, res.failed, err = res.subagents+r2.subagents, res.cost+r2.cost, r2.failed, err2
		}
	}
	cur, gerr := s.store.Burn().Item(context.WithoutCancel(ctx), it.ID)
	if errors.Is(gerr, storage.ErrNotFound) { // burn_scan_done: the scan is over
		_ = s.trees.Remove(context.WithoutCancel(ctx), p.Path, b.ProjectID, tree)
		return false
	}
	if gerr != nil {
		return false
	}
	cur.Subagents += res.subagents
	cur.CostUSD += res.cost
	fromCur := cur.Status // read before checked()'s build/test (which can take a while): the person may skip it meanwhile, and theirs wins
	switch {
	case ctx.Err() != nil: // stopped: it waits
		if cur.Status == "doing" {
			cur.Status = "paused"
		}
	case err != nil || res.failed != "":
		failed = true
		if _, hit := s.limitHit(ctx, b.AgentID); hit { // not its fault: it goes on after the reset
			cur.Status, cur.Attempts = "paused", max(cur.Attempts-1, 0)
			s.say(ctx, b, "**Tạm dừng** "+oneLine(cur.Title, 120)+": hết quota AI, làm tiếp sau khi reset")
			break
		}
		if why := firstNonEmpty(res.failed, errText(err)); cur.Status == "doing" && (technical(err) || technicalText(res.failed)) {
			// the machine's, not the piece's: it waits, untried (ADR-152)
			cur.Status, cur.Attempts = "paused", max(cur.Attempts-1, 0)
			s.block(ctx, b.ProjectID, why)
			break
		}
		cur.Status, cur.Summary = s.failedOrAgain(cur, firstNonEmpty(res.failed, errText(err)))
	case worker: // a scan that did not say it was over: it is, with what it recorded
		_ = s.store.Burn().DeleteItem(context.WithoutCancel(ctx), cur.ID)
		_ = s.trees.Remove(context.WithoutCancel(ctx), p.Path, b.ProjectID, tree)
		s.endScan(ctx, b, cur.ID)
		return false
	case (cur.Status == "done" || cur.Status == "review") && !s.checked(ctx, b, &cur): // its checks failed: back to its worker (ADR-131)
	case cur.Status == "review": // the reviewer next, in the loop
	case cur.Status == "done" && held: // review turned on meanwhile
		cur.Status = "review"
	case cur.Status == "done":
		if latest, lerr := s.store.Burn().Item(context.WithoutCancel(ctx), cur.ID); lerr != nil || latest.Status != fromCur {
			return false // skipped (or otherwise moved) while checked() ran: theirs wins, no merge
		}
		s.deliver(context.WithoutCancel(ctx), b, &cur)
	case cur.Status == "doing": // it said nothing of how it went
		cur.Status, cur.Summary = s.failedOrAgain(cur, "agent không báo kết quả (burn_done/burn_fail)")
	}
	if s.store.Burn().UpdateItemFrom(context.WithoutCancel(ctx), cur, fromCur) != nil {
		return failed // the person moved it meanwhile (e.g. skip): theirs wins, don't re-announce or re-watch
	}
	if ctx.Err() == nil {
		s.sayItem(ctx, b, cur)
		if cur.Status == "failed" {
			s.watch(ctx, b) // the same reason again and again: the process, not the piece (ADR-143)
		}
	}
	return failed
}

// ensureWorkConversation gives a piece a hidden chat of its own with the
// Burn's agent (ADR-116), kept after it is done (a retry goes on there, the
// person reads it from the Burn); a new one once the agent was changed.
func (s *Service) ensureWorkConversation(ctx context.Context, b storage.BurnSession, it *storage.BurnItem) error {
	if id := it.WorkConversationID; id != "" {
		c, err := s.store.Chat().GetConversation(ctx, id)
		if err == nil && (b.AgentID == "" || c.AgentID == b.AgentID) && c.Cleaned == "" {
			return nil
		}
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
	}
	conv, err := s.chat.StartConversationPurpose(ctx, b.ProjectID, b.AgentID, chat.BurnWorkPurpose)
	if err != nil {
		return err
	}
	conv.Title = oneLine("Burn: "+it.Title, 80)
	_ = s.store.Chat().UpdateConversation(ctx, conv)
	it.WorkConversationID = conv.ID
	return s.chat.SetMode(ctx, conv.ID, "operate")
}

// failedOrAgain: a second try, then failed.
func (s *Service) failedOrAgain(it storage.BurnItem, why string) (string, string) {
	if it.Attempts < 2 {
		return "queued", why
	}
	return "failed", why
}

// maxReviewRounds: how many times a piece is done before its result review
// turning it down fails it. Each round goes back with the reviewer's notes;
// the review of the next one judges first whether they were followed.
const maxReviewRounds = 3

// reviewAgain is failedOrAgain for a result the reviewer turned down: what
// to fix is known, so it gets more rounds than a technical failure.
func reviewAgain(it storage.BurnItem, why string) (string, string) {
	if it.Attempts < maxReviewRounds {
		return "queued", why
	}
	return "failed", why
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// maxScanned caps what is kept of the areas scanned (the latest stay).
const maxScanned = 2000

// addScanned adds a scan's areas to what was kept, dropping the oldest lines
// past maxScanned.
func addScanned(kept, area string, at time.Time) string {
	lines := append(strings.Split(strings.TrimSpace(kept), "\n"), at.Local().Format("02/01 15:04")+" "+area)
	for len(lines) > 1 && (lines[0] == "" || len([]rune(strings.Join(lines, "\n"))) > maxScanned) {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

// workPrompt is a piece's work turn (burn/work.md).
// fresh: tried before, but its worktree starts over (its changes are gone).
func workPrompt(b storage.BurnSession, it storage.BurnItem, again, reviewed, fresh bool) string {
	return prompts.Render("burn/work", struct {
		ID, Kind, Title, Detail, Focus, ReviewNote, Branch, Map, Lessons string
		FocusChecks, Verify                                              []string
		Again, Reviewed, Fresh                                           bool
	}{it.ID, it.Kind, it.Title, it.Detail, b.Focus, it.ReviewNote, b.RunBranch, b.CodeMap, b.Lessons, checks(b), verifyCommands(b, it.Worktree), again, reviewed, fresh})
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// commit puts a piece's changes on the run's branch, in the run's worktree
// (a branch of the project's repository: the person merges it, office never).
func commit(ctx context.Context, dir, branch, title, summary string) error {
	if dir == "" {
		return errors.New("không có worktree")
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if cur, _ := git("rev-parse", "--abbrev-ref", "HEAD"); cur != branch {
		if _, err := git("switch", branch); err != nil {
			if out, err := git("switch", "-c", branch); err != nil {
				return fmt.Errorf("tạo nhánh %s: %s", branch, out)
			}
		}
	}
	if _, err := git("add", "-A"); err != nil {
		return err
	}
	if out, _ := git("status", "--porcelain"); out == "" {
		return nil // nothing (still) uncommitted: the agent may have committed itself
	}
	msg := "burn: " + title
	if summary != "" {
		msg += "\n\n" + summary
	}
	if _, err := git("commit", "-m", msg); err != nil {
		if out, err := git("-c", "user.name=Agent Office", "-c", "user.email=office@localhost", "commit", "-m", msg); err != nil {
			return fmt.Errorf("commit: %s", out)
		}
	}
	return nil
}

// provider is the AI connection the agent answers with: its own, else the
// office's default.
func (s *Service) provider(ctx context.Context, agentID string) string {
	if ag, err := s.store.Agents().Get(ctx, agentID); err == nil && ag.ProviderID != "" {
		return ag.ProviderID
	}
	if ps, err := s.store.Providers().List(ctx); err == nil {
		for _, p := range ps {
			if p.IsDefault {
				return p.ID
			}
		}
	}
	return ""
}

func (s *Service) limits(ctx context.Context, agentID string) (chat.Limits, bool) {
	var l chat.Limits
	id := s.provider(ctx, agentID)
	if id == "" {
		return l, false
	}
	ok, _ := s.store.Settings().Get(ctx, chat.LimitsKey(id), &l)
	return l, ok
}

// WeeklyReset is when the agent's AI connection's weekly limit resets next
// (the suggested time a Burn stops), if it said.
func (s *Service) WeeklyReset(ctx context.Context, agentID string) (time.Time, bool) {
	l, ok := s.limits(ctx, agentID)
	if !ok {
		return time.Time{}, false
	}
	w, ok := l.Windows["seven_day"]
	if !ok || !w.ResetsAt.After(time.Now()) {
		return time.Time{}, false
	}
	return w.ResetsAt, true
}

// limitHit: the AI connection said no more for now, or every connection
// the agent may run on passed its stop threshold (ADR-136) — until when.
func (s *Service) limitHit(ctx context.Context, agentID string) (time.Time, bool) {
	if s.chat != nil {
		if ag, err := s.store.Agents().Get(ctx, agentID); err == nil {
			if until, hit := s.chat.CapStop(ctx, ag); hit {
				return until, true
			}
		}
	}
	l, ok := s.limits(ctx, agentID)
	if !ok || l.Status != "rejected" {
		return time.Time{}, false
	}
	var until time.Time
	for _, w := range l.Windows {
		if w.Utilization >= 1 && w.ResetsAt.After(until) {
			until = w.ResetsAt
		}
	}
	if !until.IsZero() && !until.After(time.Now()) {
		return time.Time{}, false // its reset has passed: a stale report
	}
	if until.IsZero() {
		// No window carried a reset time (e.g. a hard "rejected" with no window
		// detail at all) — fall back to a short cooldown timed from the report
		// itself, not from now, so a stale record expires instead of pushing
		// the wait out further on every check (matches chat.RejectedCooldown).
		until = l.UpdatedAt.Add(chat.RejectedCooldown)
		if !until.After(time.Now()) {
			return time.Time{}, false
		}
	}
	return until, true
}

// limitsHit: some of agentIDs' AI connections said no more for now — until
// the last of their resets.
func (s *Service) limitsHit(ctx context.Context, agentIDs ...string) (time.Time, bool) {
	var until time.Time
	hit := false
	for _, id := range agentIDs {
		if t, ok := s.limitHit(ctx, id); ok {
			hit = true
			if t.After(until) {
				until = t
			}
		}
	}
	return until, hit
}

// agents are who a Burn runs on: its own agent, and every reviewer of its
// review profile with the agents their workflows' roles are bound to. One
// of them out of quota stalls every piece.
func (s *Service) agents(ctx context.Context, b storage.BurnSession) []string {
	ids := []string{b.AgentID}
	for _, stage := range ReviewStages {
		if r, ok := s.reviewer(ctx, b, stage); ok {
			ids = append(ids, s.stageAgents(ctx, b, r)...)
		}
	}
	return ids
}

// stageAgents: a review stage's reviewer and its workflow's bound agents.
func (s *Service) stageAgents(ctx context.Context, b storage.BurnSession, r storage.BurnReviewStage) []string {
	ids := []string{r.AgentID}
	if r.Workflow == "" {
		return ids
	}
	if w, err := s.store.Workflows().GetByKey(ctx, b.ProjectID, r.Workflow); err == nil {
		for _, id := range w.Bindings {
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// waitLimit puts the Burn to wait for its connections' reset (past its stop
// time: it stops): its own agent's, or a reviewer's (ADR-124).
func (s *Service) waitLimit(ctx context.Context, b storage.BurnSession) bool {
	until, hit := s.limitsHit(ctx, s.agents(ctx, b)...)
	if !hit {
		return false
	}
	// as it is now: pieces run side by side, b may be a while old
	if cur, err := s.store.Burn().SessionByID(ctx, b.ID); err == nil {
		if !cur.Active() {
			return true
		}
		b = cur
	}
	if b.EndsAt != nil && !until.Before(*b.EndsAt) {
		_ = s.stop(context.WithoutCancel(ctx), b.ProjectID, "chạm giới hạn kết nối AI, reset sau giờ tắt")
		return true
	}
	if b.State != "draining" { // draining: it still finishes, after the reset
		b.State = "waiting_limit"
	}
	first := b.WaitingUntil == nil // said once, not on every check
	b.WaitingUntil = &until
	_, _ = s.store.Burn().SaveSession(ctx, b)
	if first {
		s.say(ctx, b, "Hết quota AI: chờ tới "+until.Local().Format("02/01 15:04")+" rồi chạy tiếp.")
	}
	return true
}

// DropWorktree removes a piece's worktree (its branch stays: merged, or not).
func (s *Service) DropWorktree(ctx context.Context, it storage.BurnItem) error {
	b, err := s.store.Burn().SessionByID(ctx, it.SessionID)
	if err != nil {
		return err
	}
	p, err := s.store.Repos().Get(ctx, b.ProjectID)
	if err != nil {
		return err
	}
	if s.trees == nil {
		return errors.New("không có worktree")
	}
	return s.trees.Remove(ctx, p.Path, b.ProjectID, "burn-"+it.ID)
}
