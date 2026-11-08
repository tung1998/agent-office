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

	// No handle() ever ran for this job (m.enqueuing is empty): a follow-up or a
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
// for this job's own chat (m.enqueuing[key] > 0), must still record the race
// against this job's own ID so that handle() can clean up its own waiter —
// stoppedEnqueuing's sweep must not throw that away before handle() reads it.
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
	key := ch.ID + "/1"
	m.mu.Lock()
	m.enqueuing[key]++ // simulates a handle() currently between starting Enqueue and registering its waiter, for this same chat
	m.mu.Unlock()

	m.Reply(ctx, job, "xong", nil, true)

	m.mu.Lock()
	early, ok := m.early[job.ID]
	m.mu.Unlock()
	if !ok || early != key {
		t.Fatal("Reply did not record the race against this job's own ID while its chat's handle() was mid-Enqueue")
	}
}

// A final Reply for an unrelated job (a different channel/chat) that lands
// while some other chat's handle() is mid-Enqueue must not be mistaken for
// that chat's own race: m.early must stay gated by the specific chat that is
// mid-Enqueue, not by "some handle() somewhere is mid-Enqueue" (manager.go
// previously used a bare counter, m.enqueuing int, shared across every
// channel/bot — see bit_01m6tp7f6x2zkwgjbdk4v1ay7x). With several channels
// running concurrently, that let an unrelated final Reply leak a permanent
// m.early entry that nothing (no handle() for that job) would ever clear.
func TestReplyDoesNotMarkEarlyForUnrelatedChatMidEnqueue(t *testing.T) {
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
	// The job finishing now belongs to a different chat ("2") than the one
	// some other handle() is mid-Enqueue for ("1") — e.g. a resumed job after
	// restart, or approvals.go's own Enqueue, racing against a busy bot.
	raw, _ := json.Marshal(trigger.ChannelPayload{Message: "hi", ChatID: "2", ChannelID: ch.ID})
	job, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, nil, nil, func(storage.Channel) (Adapter, error) { return nil, nil })
	midEnqueueKey := ch.ID + "/1" // a different chat's handle() is mid-Enqueue
	m.mu.Lock()
	m.enqueuing[midEnqueueKey]++
	m.mu.Unlock()

	m.Reply(ctx, job, "xong", nil, true)

	m.mu.Lock()
	n := len(m.early)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("m.early leaked %d entries for a job from an unrelated chat (never going through that chat's handle())", n)
	}
}

// Two different jobs on the SAME chat racing concurrently (a second handle()
// also mid-Enqueue for this chat, a follow-up turn, a resumed job, or
// approvals.go's own Enqueue) must not cross-contaminate: a final Reply for
// job X landing early must only ever make a handle() skip its waiter for
// job X itself — never for some other job Y on the very same chat key. Keying
// m.early purely by chat key (without the job.ID) would let X's early entry
// be misread as "early" for Y too, silently dropping Y's waiter: /stop would
// stop working for it, the slash command's "thinking…" message would never
// get edited, and the verbose step list would be lost (reviewer finding on
// bit_01m6tp7f6x2zkwgjbdk4v1ay7x).
func TestReplyEarlyForOneJobDoesNotAffectAnotherJobSameChat(t *testing.T) {
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
	jobX, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	jobY, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, nil, nil, func(storage.Channel) (Adapter, error) { return nil, nil })
	key := ch.ID + "/1"
	m.mu.Lock()
	m.enqueuing[key] = 2 // two concurrent handle()s mid-Enqueue for this same chat: one for jobX, one about to register jobY
	m.mu.Unlock()

	// jobX's run raced ahead and answered final before its own handle()
	// registered a waiter.
	m.Reply(ctx, jobX, "xong X", nil, true)

	m.mu.Lock()
	_, xEarly := m.early[jobX.ID]
	_, yEarly := m.early[jobY.ID]
	m.mu.Unlock()
	if !xEarly {
		t.Fatal("jobX's own early race was not recorded")
	}
	if yEarly {
		t.Fatal("jobX's early entry leaked onto jobY: a handle() for jobY would wrongly skip registering its waiter")
	}

	// jobY's own handle() now reaches the point of checking m.early: it must
	// see nothing for its own ID and register its waiter normally.
	m.mu.Lock()
	_, wasEarlyY := m.early[jobY.ID]
	delete(m.early, jobY.ID)
	if !wasEarlyY {
		m.waiting[jobY.ID] = waiter{key: key}
	}
	m.stoppedEnqueuing(key)
	m.mu.Unlock()

	m.mu.Lock()
	_, stillWaiting := m.waiting[jobY.ID]
	m.mu.Unlock()
	if !stillWaiting {
		t.Fatal("jobY's waiter should have been registered: it was never early")
	}

	// jobX's own handle() now also finishes its Enqueue window.
	m.mu.Lock()
	m.stoppedEnqueuing(key)
	n := len(m.early)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("m.early leaked %d entries once this chat's enqueuing count dropped to 0", n)
	}
}

// A job that never goes through handle() at all (a follow-up turn, a resumed
// job, approvals.go's own Enqueue) finishing on the SAME chat a handle() is
// mid-Enqueue for must not stop that handle() from registering its own
// waiter: m.early is keyed by job.ID, so an unrelated job's final Reply on
// the same chat key must leave the in-flight job's own entry untouched.
func TestHandleStillRegistersWaiterWhenUnrelatedJobFinishesSameChat(t *testing.T) {
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
	unrelated, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	ownJob, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "task", Origin: "automation", Status: "pending", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, nil, nil, func(storage.Channel) (Adapter, error) { return nil, nil })
	key := ch.ID + "/1"
	m.mu.Lock()
	m.enqueuing[key]++ // this chat's handle() is mid-Enqueue for ownJob, whose ID is not known yet
	m.mu.Unlock()

	// A completely unrelated job on the same chat (never went through this
	// handle()) answers final right now.
	m.Reply(ctx, unrelated, "xong", nil, true)

	// handle() now learns its real job.ID (ownJob) and checks m.early for it.
	m.mu.Lock()
	_, wasEarly := m.early[ownJob.ID]
	delete(m.early, ownJob.ID)
	if !wasEarly {
		m.waiting[ownJob.ID] = waiter{key: key}
	}
	m.stoppedEnqueuing(key)
	_, stillWaiting := m.waiting[ownJob.ID]
	m.mu.Unlock()

	if wasEarly {
		t.Fatal("ownJob was wrongly marked early by an unrelated job's Reply on the same chat")
	}
	if !stillWaiting {
		t.Fatal("handle() should have registered its own waiter for ownJob")
	}
}
