package burn_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A piece cut off by office restarting is not a try of its own (ADR-257):
// its try is given back, so the try it goes on with is still its first, and
// failing then still gets a second one. One cut too many times in a row
// counts (it may be what brings office down).
func TestRestartIsNotATry(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	past := time.Now().Add(-time.Minute) // its loop stops at once: only what Start does is looked at
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ResultMode: "branch", State: "running", RunBranch: "burn/2026-11-06-0800", EndsAt: &past})
	cut, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "đang làm", Kind: "bug", Status: "doing", Attempts: 1, RunBranch: b.RunBranch})
	again, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "cứ bị cắt", Kind: "bug", Status: "doing", Attempts: 1, Restarts: storage.MaxJobResumes, RunBranch: b.RunBranch})
	f.svc.Start(ctx)
	if it, _ := f.st.Burn().Item(ctx, cut.ID); it.Status != "paused" || it.Attempts != 0 || it.Restarts != 1 {
		t.Fatalf("cut once: %s attempts=%d restarts=%d", it.Status, it.Attempts, it.Restarts)
	}
	if it, _ := f.st.Burn().Item(ctx, again.ID); it.Status != "paused" || it.Attempts != 1 || it.Restarts != storage.MaxJobResumes+1 {
		t.Fatalf("cut again and again: %s attempts=%d restarts=%d", it.Status, it.Attempts, it.Restarts)
	}
}

// A piece cut off and gone on with, whose turn then ends without a report,
// gets its second try (before ADR-257 the restart was its first, and it
// failed); its turn having run, its restarts in a row are over.
func TestCutPieceKeepsItsSecondTry(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ends := time.Now().Add(time.Hour)
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxParallel: 1, ResultMode: "branch", State: "running",
		RunBranch: "burn/2026-11-06-0800", EndsAt: &ends, StartedBy: "admin@x.io"})
	it, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "sửa lỗi", Kind: "bug", Status: "doing", Attempts: 1, RunBranch: b.RunBranch})
	f.svc.Start(ctx)
	// its first try (gone on with) ends without burn_done: tried again, not failed
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		cur, _ := f.st.Burn().Item(ctx, it.ID)
		if cur.Status == "failed" {
			// each try: its turn, then the one asking it to report
			jobs, _ := f.st.Jobs().List(ctx, storage.JobFilter{ConversationID: cur.WorkConversationID, Kind: "chat_turn"})
			if cur.Attempts != 2 || cur.Restarts != 0 || len(jobs) != 4 {
				t.Fatalf("failed after: attempts=%d restarts=%d turns=%d (%s)", cur.Attempts, cur.Restarts, len(jobs), cur.Summary)
			}
			return
		}
	}
	cur, _ := f.st.Burn().Item(ctx, it.ID)
	t.Fatalf("not over: %+v", cur)
}
