package trigger_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
)

func openStore(t *testing.T) (storage.Store, storage.Repo) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	return st, p
}

type fakeExec struct {
	mu     sync.Mutex
	chats  []string
	tasks  []string // "agent|goal"
	tiers  []string // model tier each run was asked to use
	busy   int      // return ErrBusy this many times first
	fail   bool
	taskID string     // what RunTask returns ("" = tsk_x)
	actors []string   // who each task was asked by
	goals  []string   // each task's goal
	tags   [][]string // the chat tags each run was given
}

func (f *fakeExec) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy > 0 {
		f.busy--
		return "", "", trigger.ErrBusy
	}
	f.chats = append(f.chats, prompt)
	f.tiers = append(f.tiers, trigger.ModelTierOf(ctx))
	f.tags = append(f.tags, trigger.TagsOf(ctx))
	f.actors = append(f.actors, actor.From(ctx))
	if f.fail {
		return "cnv_x", "", errors.New("HTTP 529")
	}
	return "cnv_x", "", nil
}

func TestScheduleRunsOnceAndNoOverlap(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	past := time.Now().UTC().Add(-time.Hour) // missed many runs: catch up once
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "check", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "kiểm tra {{today}}", Config: storage.AutomationConfig{EveryMinutes: 5}, NextRunAt: &past})
	now := time.Now().UTC()
	r.Tick(ctx, now)
	r.Wait()
	r.Tick(ctx, now) // not due again
	r.Wait()
	if len(ex.chats) != 1 || !strings.HasPrefix(ex.chats[0], "kiểm tra ") {
		t.Fatalf("chats = %v", ex.chats)
	}
	a, _ = st.Automations().Get(ctx, a.ID)
	if a.NextRunAt == nil || !a.NextRunAt.After(now) || a.LastRunAt == nil {
		t.Fatalf("next = %v last = %v", a.NextRunAt, a.LastRunAt)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
	if len(jobs) != 1 || jobs[0].Status != "done" || jobs[0].Trigger != "schedule" || jobs[0].Origin != "automation" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

// A schedule with a stop time: its next run is at most then; then it turns
// itself off (no run), as a Burn stops.
func TestScheduleStops(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	now := time.Now().UTC()
	end := now.Add(10 * time.Minute)
	past := now.Add(-time.Minute)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "loop", Source: "schedule", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{EveryMinutes: 60, EndsAt: &end}, NextRunAt: &past})
	r.Tick(ctx, now)
	r.Wait()
	a, _ = st.Automations().Get(ctx, a.ID)
	if len(ex.chats) != 1 || a.NextRunAt == nil || !a.NextRunAt.Equal(end) || !a.Enabled {
		t.Fatalf("before its stop: runs %d, next %v (want %v), on %v", len(ex.chats), a.NextRunAt, end, a.Enabled)
	}
	r.Tick(ctx, end)
	r.Wait()
	a, _ = st.Automations().Get(ctx, a.ID)
	if len(ex.chats) != 1 || a.Enabled || a.DisabledCode != "ended" || a.NextRunAt != nil {
		t.Fatalf("at its stop: runs %d, %+v", len(ex.chats), a)
	}
}

// A run hands the automation's tags to the chat it talks in.
func TestRunCarriesTags(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{Tags: []string{"nightly", "ops"}}})
	if _, _, err := r.Enqueue(ctx, a, "webhook", `{}`, "", ""); err != nil {
		t.Fatal(err)
	}
	r.StartReady(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.tags) != 1 || strings.Join(ex.tags[0], ",") != "nightly,ops" {
		t.Fatalf("tags = %v", ex.tags)
	}
}

func TestBusyRetriesAndFailuresDisable(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{busy: 1, fail: true}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true,
		Limits: storage.AutomationLimits{DisableAfterFailures: 2}})
	for i := 0; i < 2; i++ {
		if _, status, err := r.Enqueue(ctx, a, "webhook", `{"n":1}`, fmt.Sprint(i), ""); err != nil || status != "queued" {
			t.Fatalf("enqueue = %s %v", status, err)
		}
	}
	now := time.Now().UTC()
	r.Tick(ctx, now) // first is busy → retry in 60s; second runs and fails
	r.Wait()
	r.Tick(ctx, now.Add(61*time.Second))
	r.Wait()
	a, _ = st.Automations().Get(ctx, a.ID)
	if a.Enabled || a.DisabledCode != "failures" || a.Failures != 2 {
		t.Fatalf("automation = %+v", a)
	}
	failed, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID, Status: "failed"})
	if len(failed) != 2 || failed[0].ErrorCode != "agent_error" {
		t.Fatalf("failed = %+v", failed)
	}
}

func TestRestartedJobDoesNotRunAgain(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true})
	now := time.Now().UTC()
	st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Status: "running", StartedAt: &now})
	st.Jobs().FailRunning(ctx, "restart", "restart", now)
	r := trigger.New(st, ex)
	r.Tick(ctx, now.Add(time.Minute))
	r.Wait()
	if len(ex.chats) != 0 {
		t.Fatalf("re-ran a restarted job: %v", ex.chats)
	}
}

// An automation can ask for a cheaper model tier for its runs.
func TestAutomationModelTier(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	past := time.Now().UTC().Add(-time.Minute)
	st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "daily", Source: "schedule", Action: "chat", Enabled: true, ModelTier: "fast",
		Prompt: "báo cáo", Config: storage.AutomationConfig{EveryMinutes: 60}, NextRunAt: &past})
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if len(ex.tiers) != 1 || ex.tiers[0] != "fast" {
		t.Fatalf("tiers = %v", ex.tiers)
	}
	got, _ := st.Automations().List(ctx, p.ID)
	if got[0].ModelTier != "fast" {
		t.Fatalf("stored tier = %q", got[0].ModelTier)
	}
}

// fullAccessAgent creates an agent whose FullAccess an admin turned on.
func fullAccessAgent(t *testing.T, ctx context.Context, st storage.Store, p storage.Repo) storage.Agent {
	t.Helper()
	st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	m, _ := st.OrgModels().Create(ctx, storage.OrgModel{RepoID: p.ID, Key: "m", Name: "m", Kind: "solo"})
	ag, _ := st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "a", Name: "A", Tier: storage.TierLead, ModelTier: "fast",
		Permissions: storage.Permissions{Level: perm.Operate, FullAccess: true, FullAccessBy: "admin@x.io"}})
	return ag
}

// ADR-074 security fix: a webhook never gets the agent's own full access,
// however it is configured — a webhook's caller is whoever has the URL.
func TestWebhookNeverGetsAgentFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true, AgentID: ag.ID})
	job, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, "webhook", "{}", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if job.FullAccess || job.FullAccessBy != "" {
		t.Fatalf("webhook got full access: %+v", job)
	}
}

// ADR-074 security fix: a channel message never gets the agent's own full
// access either, even from someone who may approve there (MayDecide) — that
// is a separate, already-safe mechanism (ADR-071's bot admin mode); an
// agent's own FullAccess must never leak through a channel message, which
// anyone addressed to the bot can send.
func TestChannelMessageNeverGetsAgentFullAccessEvenWithMayDecide(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "bot", Source: "discord", Action: "chat", Enabled: true, AgentID: ag.ID})
	for _, trig := range []string{"discord", "telegram"} {
		job, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, trig, "{}", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if job.FullAccess || job.FullAccessBy != "" {
			t.Fatalf("%s message got full access: %+v", trig, job)
		}
	}
}

// A scheduled automation (nobody outside can drive it) with its agent's own
// full access gets it; a manual run by hand does too (ADR-074).
func TestScheduleAndManualTrustAgentFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "daily", Source: "schedule", Action: "chat", Enabled: true, AgentID: ag.ID,
		Limits: storage.AutomationLimits{DailyCostUSD: 5, DisableAfterFailures: 3}})
	for _, trig := range []string{"schedule", "manual"} {
		job, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, trig, "", fmt.Sprint(trig), "")
		if err != nil {
			t.Fatal(err)
		}
		if !job.FullAccess || job.FullAccessBy != "admin@x.io" {
			t.Fatalf("%s did not trust the agent's own full access: %+v", trig, job)
		}
	}
}

// ADR-074 security fix: retrying a failed job must never be more trusted than
// the job being retried was — a webhook's failed run stays untrusted even
// though a retry is always enqueued as "manual" (normally trusted).
func TestRetryOfWebhookJobNeverGetsFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true, AgentID: ag.ID})
	job, _, err := trigger.New(st, &fakeExec{}).Retry(ctx, a, "webhook", false, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if job.FullAccess || job.FullAccessBy != "" {
		t.Fatalf("retry of a webhook job got full access: %+v", job)
	}
}

// ADR-074 security fix: a SECOND retry (retrying the job the first retry made,
// itself stored with trigger "manual") must still never gain full access —
// the original webhook never had it, and "manual" alone must not launder that.
func TestRetryOfRetryOfWebhookJobNeverGetsFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true, AgentID: ag.ID})
	r := trigger.New(st, &fakeExec{})
	job1, _, err := r.Retry(ctx, a, "webhook", false, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if job1.FullAccess {
		t.Fatalf("first retry got full access: %+v", job1)
	}
	// job1 is stored with Trigger "manual" and FullAccess false; retry it again
	job2, _, err := r.Retry(ctx, a, job1.Trigger, job1.FullAccess, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if job2.FullAccess || job2.FullAccessBy != "" {
		t.Fatalf("second retry (of a 'manual'-trigger job) got full access: %+v", job2)
	}
}

// A retry of a scheduled job's run (already trusted) keeps the agent's full
// access, same as a fresh manual run would.
func TestRetryOfScheduleJobKeepsFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "daily", Source: "schedule", Action: "chat", Enabled: true, AgentID: ag.ID,
		Limits: storage.AutomationLimits{DailyCostUSD: 5, DisableAfterFailures: 3}})
	job, _, err := trigger.New(st, &fakeExec{}).Retry(ctx, a, "schedule", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !job.FullAccess || job.FullAccessBy != "admin@x.io" {
		t.Fatalf("retry of a schedule job lost full access: %+v", job)
	}
}

// ADR-074 TOCTOU fix: an extra dir saved as a symlink to a harmless folder is
// valid at the time, but if the symlink is later repointed at ~/.ssh, a run
// started after that must not use it — effectivePermissions re-checks right
// before building the job, it does not trust the path as saved.
func TestExtraDirSymlinkRepointedAfterSaveIsDropped(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, p := openStore(t)
	m, _ := st.OrgModels().Create(ctx, storage.OrgModel{RepoID: p.ID, Key: "m", Name: "m", Kind: "solo"})
	other := t.TempDir() // the symlink's original, harmless target
	link := filepath.Join(t.TempDir(), "extra-dir-link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	ag, _ := st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "a", Name: "A", Tier: storage.TierLead, ModelTier: "fast",
		Permissions: storage.Permissions{Level: perm.Operate, ExtraDirs: []string{link}}})
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "daily", Source: "schedule", Action: "chat", Enabled: true, AgentID: ag.ID})

	job1, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(job1.ExtraDirs[0], "extra-dir-link") && len(job1.ExtraDirs) != 1 {
		t.Fatalf("first run did not use the (still-valid) symlink: %+v", job1.ExtraDirs)
	}

	// the attack: repoint the same, already-saved path at ~/.ssh
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Remove(link)
	if err := os.Symlink(sshDir, link); err != nil {
		t.Fatal(err)
	}

	job2, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(job2.ExtraDirs) != 0 {
		t.Fatalf("a run after the symlink was repointed at ~/.ssh still got it: %+v", job2.ExtraDirs)
	}
}

// An automation's override full access, with a proper budget, gets full
// access at run time.
func TestOverrideFullAccessWithBudgetGetsFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "daily", Source: "schedule", Action: "chat", Enabled: true,
		AgentID: ag.ID, PermissionMode: "override", OverrideFullAccess: true, OverrideAdminBy: "admin@x.io",
		Limits: storage.AutomationLimits{DailyCostUSD: 5, DisableAfterFailures: 3}})
	job, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !job.FullAccess || job.FullAccessBy != "admin@x.io" {
		t.Fatalf("override with a budget did not get full access: %+v", job)
	}
}

// Full access needs no budget (the admin sets limits if they want them): an
// automation with none still runs with it.
func TestFullAccessWithoutBudget(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "daily", Source: "schedule", Action: "chat", Enabled: true,
		AgentID: ag.ID, PermissionMode: "override", OverrideFullAccess: true, OverrideAdminBy: "admin@x.io"})
	job, _, err := trigger.New(st, &fakeExec{}).Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !job.FullAccess || job.FullAccessBy != "admin@x.io" {
		t.Fatalf("no budget took full access away: %+v", job)
	}
}

// ADR-081: a bot's message from its Admin list runs as the agent's own (its
// full access too); from Người dùng, never.
func TestChannelAdminGetsAgentFullAccess(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "@bot", Source: "discord", Action: "chat", Enabled: true, AgentID: ag.ID})
	r := trigger.New(st, &fakeExec{})
	admin, _ := json.Marshal(trigger.ChannelPayload{Message: "hi", UserID: "7", ChatID: "c1", ChannelID: "chn_1", Admin: true})
	user, _ := json.Marshal(trigger.ChannelPayload{Message: "hi", UserID: "8", ChatID: "c1", ChannelID: "chn_1"})
	ja, _, err := r.Enqueue(ctx, a, "discord", string(admin), "", "")
	if err != nil {
		t.Fatal(err)
	}
	ju, _, _ := r.Enqueue(ctx, a, "discord", string(user), "", "")
	if !ja.FullAccess || ju.FullAccess {
		t.Fatalf("admin full = %v, user full = %v", ja.FullAccess, ju.FullAccess)
	}
}

// ADR-082: at its time, with fewer runs going than it may have at once, a
// new one starts beside them; with as many, none — and none is stopped.
func TestScheduleRunsInParallelUpToItsLimit(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	r := trigger.New(st, &fakeExec{})
	past := time.Now().UTC().Add(-time.Minute)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "migrate", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "x", Config: storage.AutomationConfig{EveryMinutes: 20}, NextRunAt: &past, Limits: storage.AutomationLimits{MaxParallel: 2}})
	running := func() {
		st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: "schedule", Status: "running"})
	}
	count := func() int {
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
		return len(jobs)
	}
	running() // the last run is still going
	r.Tick(ctx, time.Now().UTC())
	if count() != 2 {
		t.Fatalf("one running of 2 allowed: jobs = %d, want a new one beside it", count())
	}
	a, _ = st.Automations().Get(ctx, a.ID)
	a.NextRunAt = &past
	st.Automations().Update(ctx, a)
	running() // now as many as it may have
	before := count()
	r.Tick(ctx, time.Now().UTC())
	if count() != before {
		t.Fatalf("at its limit a new run started: %d → %d", before, count())
	}
	if (storage.Automation{KeepContext: true, Limits: storage.AutomationLimits{MaxParallel: 3}}).Parallel() != 1 {
		t.Fatal("a kept conversation ran in parallel")
	}
}

// A bot rule that names no agent answers with the project's lead (or the
// conversation's agent): its full access counts for an admin's message.
func TestChannelAdminWithDefaultAgent(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag := fullAccessAgent(t, ctx, st, p) // the project's lead
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "@bot", Source: "discord", Action: "chat", Enabled: true})
	r := trigger.New(st, &fakeExec{})
	admin, _ := json.Marshal(trigger.ChannelPayload{Message: "hi", UserID: "7", ChatID: "c1", ChannelID: "chn_1", Admin: true})
	j, _, err := r.Enqueue(ctx, a, "discord", string(admin), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !j.FullAccess {
		t.Fatal("the lead's full access did not count for an admin, the rule naming no agent")
	}
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, AgentID: ag.ID, Purpose: "channel"})
	inConv, _ := json.Marshal(trigger.ChannelPayload{Message: "tiếp", UserID: "7", ChatID: "c1", ChannelID: "chn_1", Admin: true, ConversationID: conv.ID})
	if j, _, _ := r.Enqueue(ctx, a, "discord", string(inConv), "", ""); !j.FullAccess {
		t.Fatal("the conversation's agent's full access did not count")
	}
}
