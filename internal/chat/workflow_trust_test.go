package chat_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// A workflow an automation started from a trusted job (its schedule, its
// chain, a hand run: the job got full access) keeps its agents' full access
// in its steps, though each step's job is a new one whose actor is
// "auto:<automation>", not a person. Without it a long run (daily-upgrade)
// stopped on approval cards for every command outside the list. A job that
// did not get full access (a webhook) gives its steps none (ADR-074).
func TestWorkflowStepsKeepTrustedAutomationAccess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		jobFull bool
	}{{"trusted", true}, {"webhook", false}} {
		t.Run(tc.name, func(t *testing.T) {
			g := newWFGroup(t)
			ctx := g.context
			if _, err := g.f.st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{g.lead, g.dev.ID} {
				a, _ := g.f.st.Agents().Get(ctx, id)
				a.Permissions.Level, a.Permissions.FullAccess, a.Permissions.FullAccessBy = perm.Operate, true, "admin@x.io"
				if err := g.f.st.Agents().Update(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			g.engine.SetMode(ctx, g.conv.ID, perm.Operate)
			os.WriteFile(filepath.Join(g.dir, "reply-dev"), []byte("xong"), 0o644)
			src := `---
key: lam-viec
name: Làm việc
roles:
  - { key: lam, name: Làm }
steps:
  - id: lam
    type: agent
    role: lam
    prompt: "Làm đi"
    next: xong
  - id: xong
    type: end
    summary: "{{steps.lam.output}}"
---
`
			if _, err := g.svc.Create(ctx, g.f.project.ID, src, map[string]string{"lam": g.dev.ID}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			job, err := g.f.st.Jobs().Create(ctx, storage.Job{ProjectID: g.f.project.ID, Kind: "chat_turn", Origin: "automation", OriginID: "aut_x", Trigger: "schedule",
				Status: "running", StartedAt: &now, FullAccess: tc.jobFull, FullAccessBy: map[bool]string{true: "admin@x.io"}[tc.jobFull]})
			if err != nil {
				t.Fatal(err)
			}
			actx := usage.WithJob(actor.With(ctx, "auto:Nâng cấp"), job.ID)
			call, _, err := g.engine.Send(actx, g.conv.ID, "#lam-viec", nil)
			if err != nil {
				t.Fatal(err)
			}
			collect(t, call)
			r := g.run(t)
			if r.Status != storage.RunDone {
				t.Fatalf("run = %+v", r)
			}
			jobs, err := g.f.st.Jobs().List(ctx, storage.JobFilter{ProjectID: g.f.project.ID})
			if err != nil {
				t.Fatal(err)
			}
			var step *storage.Job
			for i, j := range jobs {
				if j.ConversationID == r.ConversationID && j.AgentID == g.dev.ID {
					step = &jobs[i]
				}
			}
			if step == nil {
				t.Fatalf("no step job in %s: %+v", r.ConversationID, jobs)
			}
			if step.FullAccess != tc.jobFull {
				t.Fatalf("step full access = %v, want %v", step.FullAccess, tc.jobFull)
			}
		})
	}
}
