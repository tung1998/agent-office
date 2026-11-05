package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/storage"
)

// A graph of steps survives a restart (ADR-256): it saves where it is as it
// goes (storage.RunCheckpoint) and the next office goes on from there.

// MaxResumes is how often a run goes on after a restart that cut one of its
// steps in the middle: an office that keeps going down on it ends it.
const MaxResumes = 3

// SafeStep: a step of this type that is running loses nothing when office
// goes down (a wait, a card waiting on a person): the next office waits on.
func SafeStep(kind string) bool {
	return kind == StepWait || kind == StepApprove || kind == StepCheck
}

// RunHash is the hash a run keeps of the workflow it ran (the chat's).
func RunHash(source string) string {
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:8])
}

// CanResume says whether the next office may go on with run r and returns
// its workflow; the error says why not (a run before checkpoints, a
// coordinator's, a workflow changed since…).
func CanResume(ctx context.Context, st storage.Store, r storage.WorkflowRun) (storage.Workflow, Def, error) {
	cp := r.Checkpoint
	if cp == nil {
		return storage.Workflow{}, Def{}, errors.New("không có điểm lưu (quy trình có điều phối hoặc chạy từ trước)")
	}
	if cp.Resumes >= MaxResumes {
		return storage.Workflow{}, Def{}, fmt.Errorf("đã chạy tiếp %d lần sau khi office khởi động lại", cp.Resumes)
	}
	w, err := st.Workflows().Get(ctx, r.WorkflowID)
	if err != nil {
		return storage.Workflow{}, Def{}, errors.New("quy trình không còn")
	}
	if RunHash(w.Source) != r.BodyHash {
		return storage.Workflow{}, Def{}, errors.New("quy trình đã được sửa từ lúc chạy")
	}
	def, err := Parse(w.Source)
	if err != nil {
		return storage.Workflow{}, Def{}, err
	}
	if !def.StepMode() {
		return storage.Workflow{}, Def{}, errors.New("quy trình có điều phối")
	}
	if _, err := st.Chat().GetConversation(ctx, r.ConversationID); err != nil {
		return storage.Workflow{}, Def{}, errors.New("chat của lần chạy không còn")
	}
	return w, def, nil
}

// Cuts is whether a restart now would cut one of the run's steps in the
// middle (what counts toward MaxResumes): a step running that is not safe,
// or a sub-workflow whose run a restart would cut (a coordinator's is).
func Cuts(ctx context.Context, st storage.Store, cp *storage.RunCheckpoint) bool {
	if cp == nil {
		return true
	}
	for id, kind := range cp.Running {
		if SafeStep(kind) {
			continue
		}
		if kind == StepWorkflow || kind == StepCoordinate {
			if m := cp.Marks[id]; m.ChildRunID != "" {
				if c, err := st.WorkflowRuns().Get(ctx, m.ChildRunID); err == nil && c.Status == storage.RunRunning && c.Checkpoint != nil && !Cuts(ctx, st, c.Checkpoint) {
					continue
				}
			}
		}
		return true
	}
	return false
}

// Parked is a run a restart would not hurt (ADR-256): office may go down
// under it (an update waiting for idle does not wait for it) and the next
// one goes on with it. Its running steps only wait (until a time, on a card
// a person has not decided yet); a step about to start or running in the
// middle is work.
func Parked(ctx context.Context, st storage.Store, r storage.WorkflowRun) bool {
	cp := r.Checkpoint
	if r.Status != storage.RunRunning || cp == nil || len(cp.Running) == 0 || Cuts(ctx, st, cp) {
		return false
	}
	if _, _, err := CanResume(ctx, st, r); err != nil {
		return false
	}
	for id, kind := range cp.Running {
		m := cp.Marks[id]
		switch kind {
		case StepWait:
			if m.WaitUntil == nil {
				return false
			}
		case StepWorkflow, StepCoordinate: // its sub-run only waits too
			c, err := st.WorkflowRuns().Get(ctx, m.ChildRunID)
			if err != nil || c.ParentRunID != r.ID || !Parked(ctx, st, c) {
				return false
			}
		default: // an approval, a check: its card, still undecided (a check's command may be running)
			if m.ActionID == "" {
				return false
			}
			a, err := st.Actions().Get(ctx, m.ActionID)
			if err != nil || a.Status != "pending" && a.Status != "queued" {
				return false
			}
			if _, busy := deciding.Is(m.ActionID); busy { // approved, its command runs now
				return false
			}
		}
	}
	return true
}
