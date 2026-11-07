package channels

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
)

// Reply(final=true) for a job that never went through handle() at all — a
// follow-up turn (trigger.Runner's WithFollowUp), a job resumed after a
// restart, or approvals.go's own Enqueue — must never write to m.early:
// nothing outside handle()'s own Enqueue window (tracked by m.enqueuing) is
// ever going to clear that entry, so with no guard it would grow forever
// over a long-running office.
func TestReplyDoesNotLeakEarlyOutsideEnqueueWindow(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "A", Enabled: true, Allow: []string{"*"}})
	raw, _ := json.Marshal(trigger.ChannelPayload{Message: "hi", ChatID: "1", ChannelID: ch.ID})
	job, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, nil, nil, func(storage.Channel) (Adapter, error) { return nil, nil })

	// No handle() ever ran for this job (m.enqueuing is 0): a follow-up or a
	// resumed job's Reply arrives completely outside the race window.
	m.Reply(ctx, job, "xong", nil, true)

	m.mu.Lock()
	n := len(m.early)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("m.early leaked %d entries for a job that never went through handle()'s Enqueue window", n)
	}
}

// The same Reply, but while a handle() elsewhere is genuinely mid-Enqueue
// (m.enqueuing > 0), must still record the race so that handle() can clean up
// its own waiter — stoppedEnqueuing's reset must not throw that away.
func TestReplyStillRecordsEarlyInsideEnqueueWindow(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "A", Enabled: true, Allow: []string{"*"}})
	raw, _ := json.Marshal(trigger.ChannelPayload{Message: "hi", ChatID: "1", ChannelID: ch.ID})
	job, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, nil, nil, func(storage.Channel) (Adapter, error) { return nil, nil })
	m.mu.Lock()
	m.enqueuing++ // simulates a handle() currently between starting Enqueue and registering its waiter
	m.mu.Unlock()

	m.Reply(ctx, job, "xong", nil, true)

	m.mu.Lock()
	early := m.early[job.ID]
	m.mu.Unlock()
	if !early {
		t.Fatal("Reply did not record the race while a handle() was mid-Enqueue")
	}
}
