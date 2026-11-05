package chat

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// A chat's proposals (actions and diffs) run in the order its agents made
// them: one approved while an earlier one still waits is queued ("queued")
// and runs once those before it are decided (Drain), never out of order (a
// "git add" before the edit it stages). Rejecting or skipping is never held
// back. Its agent goes on only once nothing of the chat waits any more.

// OrderError: an earlier proposal of the chat still waits for a person (or
// still runs).
type OrderError struct {
	Earlier string // what it is
	ID      string
	Seq     int // its number in the chat (0: not shown yet)
}

func (e *OrderError) Error() string {
	what := e.Earlier
	if e.Seq > 0 {
		what = fmt.Sprintf("#%d %s", e.Seq, e.Earlier)
	}
	return fmt.Sprintf("còn đề xuất tạo trước chưa xử lý hoặc đang chạy: %s. Duyệt hoặc từ chối nó trước (đề xuất chạy theo thứ tự agent đưa ra)", what)
}

type waitingCard struct {
	id, label string
	kind      string // action | patch
	queued    bool   // approved, waits for those before it
	by        string // queued: who approved it
	seq       int
	at        time.Time
}

// cardSeqs numbers a chat's proposals as numberCards does for its history,
// from the store: answer by answer, an answer's actions before its diff.
func (e *Engine) cardSeqs(ctx context.Context, conversationID string, acts []storage.Action, ps []storage.Patch) (map[string]int, error) {
	msgs, err := e.store.Chat().ListMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	byMsg := map[string][]string{}
	for _, a := range acts {
		if !a.Args.AfterDiff && !followUp(a) {
			byMsg[a.MessageID] = append(byMsg[a.MessageID], a.ID)
		}
	}
	for _, p := range ps {
		byMsg[p.MessageID] = append(byMsg[p.MessageID], p.ID)
	}
	for _, a := range acts { // a commit waiting for its answer's diff comes after it (ADR-189)
		if a.Args.AfterDiff {
			byMsg[a.MessageID] = append(byMsg[a.MessageID], a.ID)
		}
	}
	for _, a := range acts { // then what its merge asks (ADR-251)
		if followUp(a) {
			byMsg[a.MessageID] = append(byMsg[a.MessageID], a.ID)
		}
	}
	seq := map[string]int{}
	for _, m := range msgs {
		for _, id := range byMsg[m.ID] {
			seq[id] = len(seq) + 1
		}
	}
	return seq, nil
}

// waiting lists a chat's proposals waiting for a person in the order they
// are shown (and decided): by number, then those of an answer not over yet.
func (e *Engine) waiting(ctx context.Context, conversationID string) ([]waitingCard, error) {
	acts, err := e.store.Actions().List(ctx, conversationID, "", "")
	if err != nil {
		return nil, err
	}
	ps, err := e.store.Chat().PatchRefs(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	seq, err := e.cardSeqs(ctx, conversationID, acts, ps)
	if err != nil {
		return nil, err
	}
	var out []waitingCard
	for _, a := range acts {
		if followUp(a) {
			continue // a merged diff's install: decided whenever, nothing waits on it (ADR-251)
		}
		if a.Status == "pending" || a.Status == "queued" {
			out = append(out, waitingCard{id: a.ID, label: cmp.Or(actions.Kinds[a.Kind], a.Kind) + ": " + truncate(a.Target, 80), kind: "action",
				queued: a.Status == "queued", by: a.DecidedBy, seq: seq[a.ID], at: a.CreatedAt})
		}
	}
	for _, p := range ps {
		if p.Status == "pending" || p.Status == "queued" {
			out = append(out, waitingCard{id: p.ID, label: fmt.Sprintf("diff %d file", len(p.Files)), kind: "patch",
				queued: p.Status == "queued", by: p.DecidedBy, seq: seq[p.ID], at: p.CreatedAt})
		}
	}
	unseen := func(n int) int { // not numbered yet: after every numbered one
		if n == 0 {
			return int(^uint(0) >> 1)
		}
		return n
	}
	slices.SortStableFunc(out, func(a, b waitingCard) int {
		return cmp.Or(cmp.Compare(unseen(a.seq), unseen(b.seq)), a.at.Compare(b.at), cmp.Compare(a.id, b.id))
	})
	return out, nil
}

// CheckOrder: the proposal id of a chat can't run now (OrderError) while one
// before it still waits or is queued: approved, it is queued instead.
func (e *Engine) CheckOrder(ctx context.Context, conversationID, id string) error {
	if conversationID == "" {
		return nil
	}
	list, err := e.waiting(ctx, conversationID)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(list, func(c waitingCard) bool { return c.id == id }); i > 0 {
		return &OrderError{Earlier: list[0].label, ID: list[0].id, Seq: list[0].seq}
	}
	return nil
}

// Waiting: something of the chat still waits for a person or in the queue;
// when that can't be told, it is taken as waiting (the next decision looks
// again).
func (e *Engine) Waiting(ctx context.Context, conversationID string) bool {
	list, err := e.waiting(ctx, conversationID)
	return err != nil || len(list) > 0
}

// numberCards numbers a chat's proposals in the order they were made, as the
// chat shows them: answer by answer, an answer's actions before its diff.
func numberCards(msgs []MessageDTO) {
	n := 0
	for _, m := range msgs {
		for i := range m.Actions {
			if !m.Actions[i].AfterDiff && m.Actions[i].Patch == "" {
				n++
				m.Actions[i].Seq = n
			}
		}
		for i := range m.Patches {
			n++
			m.Patches[i].Seq = n
		}
		for i := range m.Actions { // after the answer's diff (ADR-189)
			if m.Actions[i].AfterDiff {
				n++
				m.Actions[i].Seq = n
			}
		}
		for i := range m.Actions { // then its merge's install (ADR-251)
			if m.Actions[i].Patch != "" {
				n++
				m.Actions[i].Seq = n
			}
		}
	}
}

// cardsBefore: how many proposals of a chat belong to answers other than
// msgID (a new answer's are numbered after them).
func (e *Engine) cardsBefore(ctx context.Context, conversationID, msgID string) int {
	n := 0
	if acts, err := e.store.Actions().List(ctx, conversationID, "", ""); err == nil {
		for _, a := range acts {
			if a.MessageID != "" && a.MessageID != msgID {
				n++
			}
		}
	}
	if ps, err := e.store.Chat().PatchRefs(ctx, conversationID); err == nil {
		for _, p := range ps {
			if p.MessageID != "" && p.MessageID != msgID {
				n++
			}
		}
	}
	return n
}

// numberPending gives a chat's waiting proposals (shown elsewhere, as a
// linked chat's) the numbers they have in that chat.
func (e *Engine) numberPending(ctx context.Context, conversationID string, acts []ActionDTO, ps []PatchDTO) {
	list, err := e.waiting(ctx, conversationID)
	if err != nil {
		return
	}
	seq := map[string]int{}
	for _, c := range list {
		seq[c.id] = c.seq
	}
	for i := range acts {
		acts[i].Seq = seq[acts[i].ID]
	}
	for i := range ps {
		ps[i].Seq = seq[ps[i].ID]
	}
}

// WaitingOrder lists the ids of a chat's waiting proposals in the order they
// must be decided, with the number each has in the chat (0: not shown yet).
func (e *Engine) WaitingOrder(ctx context.Context, conversationID string) ([]string, map[string]int) {
	list, _ := e.waiting(ctx, conversationID)
	ids := make([]string, len(list))
	seq := map[string]int{}
	for i, c := range list {
		ids[i] = c.id
		seq[c.id] = c.seq
	}
	return ids, seq
}

// QueueRunner carries out a queued proposal (kind action | patch) approved by
// by, as an approval from the dashboard does (audited); the line its agent
// gets about it ("" = none).
type QueueRunner func(ctx context.Context, kind, id, by string) string

// SetQueueRunner sets what carries out queued proposals.
func (e *Engine) SetQueueRunner(fn QueueRunner) { e.queueRun = fn }

// QueuePatch approves a pending diff that waits behind an earlier proposal of
// its chat: it is applied once those are decided (Drain).
func (e *Engine) QueuePatch(ctx context.Context, patchID string) (PatchDTO, error) {
	if !deciding.Begin("patches", patchID, actor.From(ctx)) {
		p, err := e.store.Chat().GetPatch(ctx, patchID)
		if err != nil {
			return PatchDTO{}, err
		}
		return e.patchDTO(ctx, p), ErrDecided
	}
	defer deciding.End("patches", patchID)
	p, err := e.store.Chat().GetPatch(ctx, patchID)
	if err != nil {
		return PatchDTO{}, err
	}
	if p.Status != "pending" {
		return e.patchDTO(ctx, p), ErrDecided
	}
	who := actor.From(ctx)
	if err := e.store.Chat().QueuePatch(ctx, p.ID, who); err != nil {
		return PatchDTO{}, err
	}
	p.Status, p.DecidedBy = "queued", who
	return e.patchDTO(ctx, p), nil
}

// Drain runs a chat's queued proposals that nothing waits before any more,
// in order, until one waits for a person (or none is left). Each one's line
// goes to its agent as a decision (Decided). Safe to call after any decision.
func (e *Engine) Drain(conversationID string) {
	if e.queueRun == nil || conversationID == "" {
		return
	}
	defer e.holdDrain(conversationID)()
	ctx := context.Background()
	for range 100 {
		list, err := e.waiting(ctx, conversationID)
		if err != nil || len(list) == 0 || !list[0].queued {
			return
		}
		c := list[0]
		if line := e.queueRun(ctx, c.kind, c.id, c.by); line != "" {
			e.Decided(conversationID, strings.TrimPrefix(c.by, "human:"), line)
		}
		if again, err := e.waiting(ctx, conversationID); err != nil || len(again) > 0 && again[0].id == c.id {
			return // it could not be carried out: never loop on it
		}
	}
}

// holdDrain keeps Drain off a chat until the returned func is called: while
// a turn replaces its diff (the old one rejected, the commits held for it
// carried, the new one saved), a Drain in between would see a queued commit
// with nothing before it and run it ahead of its diff (ADR-189). Never call
// Drain while holding it.
func (e *Engine) holdDrain(conversationID string) func() {
	mu, _ := e.draining.LoadOrStore(conversationID, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// beforePatchSave (tests only) runs once a turn's new diff is made, before
// it is saved: where a Drain from elsewhere could slip in.
var beforePatchSave func(conversationID string)

// GetPatchDTO is a diff as a card shows it.
func (e *Engine) GetPatchDTO(ctx context.Context, id string) (PatchDTO, error) {
	p, err := e.store.Chat().GetPatch(ctx, id)
	if err != nil {
		return PatchDTO{}, err
	}
	return e.patchDTO(ctx, p), nil
}

// autoApply applies a diff its agent may apply on its own (CapApply): now
// when nothing of the chat waits before it, else queued in its turn (Drain).
func (e *Engine) autoApply(conv storage.Conversation, jobID, agentName, level string, saved storage.Patch) storage.Patch {
	ctx := actor.With(context.Background(), "auto:"+agentName+" ("+perm.Label(level)+")")
	if e.CheckOrder(ctx, conv.ID, saved.ID) != nil {
		if q, err := e.QueuePatch(ctx, saved.ID); err == nil {
			saved.Status, saved.DecidedBy = q.Status, q.DecidedBy
		}
		return saved
	}
	if d, err := e.DecidePatch(ctx, saved.ID, true); err == nil {
		saved.Status, saved.Detail, saved.DecidedBy, saved.DecidedAt = d.Status, d.Detail, d.DecidedBy, d.DecidedAt
		e.auditAutoPatch(agentName, conv, jobID, saved)
	}
	return saved
}
