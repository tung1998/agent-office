package chat_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

const twoSteps = `---
key: hai-buoc
name: Hai bước
roles:
  - { key: a, name: A }
  - { key: b, name: B }
steps:
  - id: a
    type: agent
    role: a
    prompt: "Bước một"
    next: b
  - id: b
    type: agent
    role: b
    prompt: "Bước hai"
    next: xong
  - id: xong
    type: end
    summary: "{{steps.b.output}}"
---
`

// callFromAutomation runs #hai-buoc from a job of automation a, as its
// runner does; the run once it ended.
func callFromAutomation(t *testing.T, g wfGroup, a storage.Automation) (storage.Job, storage.WorkflowRun) {
	t.Helper()
	if _, err := g.svc.Create(g.context, g.f.project.ID, twoSteps, map[string]string{"a": g.qa.ID, "b": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job, err := g.f.st.Jobs().Create(g.context, storage.Job{ProjectID: g.f.project.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID,
		Trigger: "schedule", Status: "running", StartedAt: &now, AgentID: g.lead})
	if err != nil {
		t.Fatal(err)
	}
	call, _, err := g.engine.Send(usage.WithJob(actor.With(g.context, "auto:"+a.Name), job.ID), g.conv.ID, "#hai-buoc", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, call)
	return job, g.run(t)
}

func newCappedAutomation(t *testing.T, g wfGroup, cap float64) storage.Automation {
	t.Helper()
	a, err := g.f.st.Automations().Create(g.context, storage.Automation{ProjectID: g.f.project.ID, Name: "Nâng cấp", Source: "event", Action: "workflow",
		Enabled: true, AgentID: g.lead, Limits: storage.AutomationLimits{DailyCostUSD: cap}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The jobs of a workflow's steps an automation started keep that automation
// as their root: its daily cap, its reports and failure digest count them.
// They used to be new jobs of nobody's ("user", "ui"), so a workflow
// automation spent $300 a day under a $15 cap (ADR-263).
func TestWorkflowRunJobsKeepAutomationOrigin(t *testing.T) {
	t.Parallel()
	g := newWFGroup(t)
	a := newCappedAutomation(t, g, 0)
	job, r := callFromAutomation(t, g, a)
	if r.Status != storage.RunDone {
		t.Fatalf("run = %+v", r)
	}
	jobs, _ := g.f.st.Jobs().List(g.context, storage.JobFilter{ConversationID: r.ConversationID})
	if len(jobs) < 2 {
		t.Fatalf("step jobs = %+v", jobs)
	}
	for _, j := range jobs {
		if j.RootJobID != job.ID || j.RootAutomationID != a.ID || j.Automation() != a.ID {
			t.Fatalf("step job %s has root %q/%q, want %s/%s", j.Title, j.RootJobID, j.RootAutomationID, job.ID, a.ID)
		}
		if j.Origin != "user" {
			t.Fatalf("step job origin = %q: the step's own rights and resume rules read it", j.Origin)
		}
	}
	// what a step's call costs is the automation's
	cost := 1.5
	if _, err := g.f.st.Runs().Create(g.context, storage.Run{Kind: "chat", JobID: jobs[0].ID, ProjectID: g.f.project.ID, Status: "ok", CostUSD: &cost, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if spent := a.SpentToday(g.context, g.f.st.Jobs(), time.Now()); spent < 1.5 {
		t.Fatalf("spent today = %v, want the step's 1.5 counted", spent)
	}
}

// Once the automation reached its daily cap, a workflow it runs opens no
// further step: the run ends with the cap said, not after spending on
// (ADR-263).
func TestDailyCapStopsRunMidway(t *testing.T) {
	t.Parallel()
	g := newWFGroup(t)
	a := newCappedAutomation(t, g, 1)
	os.WriteFile(filepath.Join(g.dir, "sleep-qa"), []byte("1"), 0o644)
	go func() { // while step a runs, the automation's tree passes its cap
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, g.conv.ID, 1)
			if len(runs) > 0 && runs[0].ConversationID != g.conv.ID {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		now := time.Now().UTC()
		_, _ = g.f.st.Jobs().Create(g.context, storage.Job{ProjectID: g.f.project.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", Status: "done",
			StartedAt: &now, FinishedAt: &now, CostUSD: 2, RootJobID: "job_x", RootAutomationID: a.ID})
	}()
	_, r := callFromAutomation(t, g, a)
	if r.Status != storage.RunFailed {
		t.Fatalf("run = %s, want failed at the cap", r.Status)
	}
	jobs, _ := g.f.st.Jobs().List(g.context, storage.JobFilter{ConversationID: r.ConversationID})
	for _, j := range jobs {
		if j.AgentID == g.dev.ID {
			t.Fatalf("step b started past the cap: %+v", j)
		}
	}
	msgs, _ := g.f.st.Chat().ListMessages(g.context, r.ConversationID)
	said := false
	for _, m := range msgs {
		said = said || strings.Contains(m.Content, "ngân sách ngày của tự động hóa Nâng cấp")
	}
	if !said && !strings.Contains(r.Result+r.Error, "ngân sách") {
		t.Fatalf("the cap was not said: run %+v", r)
	}
}
