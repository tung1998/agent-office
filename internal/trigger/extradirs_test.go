package trigger_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/usage"
)

// realExec runs automation chats on a real chat.Engine: job.FullAccess and
// job.ExtraDirs are computed once by internal/trigger.Runner and read
// straight off the job by internal/chat.Engine (ADR-074 security fix), so
// unlike cmd/office's executor this needs no ctx plumbing for either.
type realExec struct{ engine *chat.Engine }

func (x realExec) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	if conv == "" {
		c, err := x.engine.StartConversationPurpose(ctx, projectID, agentID, "")
		if err != nil {
			return "", "", err
		}
		conv = c.ID
		if err := x.engine.SetMode(ctx, conv, perm.Operate); err != nil {
			return conv, "", err
		}
	}
	turn, _, err := x.engine.Send(ctx, conv, prompt, nil)
	if err != nil {
		return conv, "", err
	}
	for seq := 0; ; {
		evs, done, wake := turn.Since(seq)
		seq += len(evs)
		for _, e := range evs {
			if e.Type == "error" {
				return conv, "", nil
			}
		}
		if done {
			return conv, "", nil
		}
		<-wake
	}
}

// extraDirsFixture builds a project+agent+schedule setup with a real
// chat.Engine wired to a fake "claude" binary that just logs its argv.
func extraDirsFixture(t *testing.T) (st storage.Store, project storage.Repo, agent storage.Agent, runner *trigger.Runner, argsLog string) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	var err error
	st, err = sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	bin, log := filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$*" >> `+log+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, err = st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	agents, _ := engine.Agents(ctx, project.ID)
	agent, err = st.Agents().Get(ctx, agents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	runner = trigger.New(st, realExec{engine})
	return st, project, agent, runner, log
}

// ADR-074 security fix: an automation's override replaces the agent's own
// full access entirely, including to turn it OFF — it never falls back to
// the agent's own when OverrideFullAccess is false.
func TestOverrideOffWinsOverAgentFullAccess(t *testing.T) {
	ctx := context.Background()
	st, project, agent, runner, _ := extraDirsFixture(t)
	st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	agent.Permissions.Level, agent.Permissions.FullAccess, agent.Permissions.FullAccessBy = perm.Operate, true, "admin@x.io"
	st.Agents().Update(ctx, agent)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Sync", Source: "schedule", Action: "chat", Enabled: true,
		AgentID: agent.ID, PermissionMode: "override", OverrideFullAccess: false, OverrideAdminBy: "admin@x.io"})
	job, _, err := runner.Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if job.FullAccess {
		t.Fatalf("override off did not win over the agent's own full access: %+v", job)
	}
}

// An automation's override of extra dirs replaces the agent's own entirely:
// the CLI run sees only the override's dirs, never the agent's.
func TestOverrideExtraDirsReplacesAgentDirs(t *testing.T) {
	ctx := context.Background()
	st, project, agent, runner, argsLog := extraDirsFixture(t)
	st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	agentDir, overrideDir := t.TempDir(), t.TempDir()
	agent.Permissions.Level, agent.Permissions.ExtraDirs = perm.Operate, []string{agentDir}
	st.Agents().Update(ctx, agent)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Sync", Source: "schedule", Action: "chat", Enabled: true,
		AgentID: agent.ID, PermissionMode: "override", OverrideAdminBy: "admin@x.io", OverrideExtraDirs: []string{overrideDir}})
	job, _, err := runner.Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(job.ExtraDirs, []string{overrideDir}) {
		t.Fatalf("job.ExtraDirs = %v, want only the override's", job.ExtraDirs)
	}
	runner.StartReady(ctx, time.Now().UTC())
	runner.Wait()
	b, _ := os.ReadFile(argsLog)
	args := string(b)
	if !strings.Contains(args, "--add-dir "+overrideDir) {
		t.Fatalf("argv missing the override's dir: %s", args)
	}
	if strings.Contains(args, "--add-dir "+agentDir) {
		t.Fatalf("argv leaked the agent's own dir under an override: %s", args)
	}
}

// Mode "agent" (no override, the default): the CLI run sees the agent's own
// extra dirs.
func TestAgentModeUsesAgentExtraDirs(t *testing.T) {
	ctx := context.Background()
	st, project, agent, runner, argsLog := extraDirsFixture(t)
	agentDir := t.TempDir()
	agent.Permissions.Level, agent.Permissions.ExtraDirs = perm.Operate, []string{agentDir}
	st.Agents().Update(ctx, agent)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Sync", Source: "schedule", Action: "chat", Enabled: true, AgentID: agent.ID})
	job, _, err := runner.Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(job.ExtraDirs, []string{agentDir}) {
		t.Fatalf("job.ExtraDirs = %v, want the agent's own", job.ExtraDirs)
	}
	runner.StartReady(ctx, time.Now().UTC())
	runner.Wait()
	b, _ := os.ReadFile(argsLog)
	if args := string(b); !strings.Contains(args, "--add-dir "+agentDir) {
		t.Fatalf("argv missing the agent's own dir: %s", args)
	}
}
