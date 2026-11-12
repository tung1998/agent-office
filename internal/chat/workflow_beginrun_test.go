package chat_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// failingRunsStore wraps a real Store but fails WorkflowRuns().Create, to
// reproduce a run that never makes it to the database.
type failingRunsStore struct {
	storage.Store
}

func (s failingRunsStore) WorkflowRuns() storage.WorkflowRunRepo {
	return failingRunRepo{s.Store.WorkflowRuns()}
}

type failingRunRepo struct {
	storage.WorkflowRunRepo
}

var errRunCreate = errors.New("boom: run create")

func (r failingRunRepo) Create(ctx context.Context, rec storage.WorkflowRun) (storage.WorkflowRun, error) {
	return storage.WorkflowRun{}, errRunCreate
}

// TestWorkflowBeginRunFailsCleanlyWhenCreateErrors covers the beginRun bug
// fix: if WorkflowRuns().Create fails, the run must not register itself in
// the engine's running-workflow map, SendWithContext must return the error
// instead of swallowing it, and the job it started must end up failed, not
// stuck running.
func TestWorkflowBeginRunFailsCleanlyWhenCreateErrors(t *testing.T) {
	t.Parallel()
	bin, dir := fakeGroupClaude(t)
	ctx := context.Background()
	tmp := t.TempDir()
	real, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { real.Close() })
	real.Migrate(ctx)
	st := failingRunsStore{real}
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	org := team.NewService(st, nil)
	p, _ := provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	_ = p
	project, _ := real.Repos().Create(ctx, storage.Repo{Name: "demo", Path: dir})
	org.AddFromLibrary(ctx, project.ID, team.StarterAgent)
	engine := chat.NewEngine(st, provs, u)

	dev, err := st.Agents().Create(ctx, storage.Agent{ProjectID: project.ID, Key: "dev", Name: "Dev", ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	qa, err := st.Agents().Create(ctx, storage.Agent{ProjectID: project.ID, Key: "qa", Name: "QA", ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	lib := workflow.Library{Dir: filepath.Join(t.TempDir(), "workflows")}
	svc := &workflow.Service{Store: st, Lib: lib}
	conv, _ := engine.StartConversation(ctx, project.ID, "")
	if _, err := svc.Install(ctx, project.ID, "council", map[string]string{"a": dev.ID, "b": qa.ID}); err != nil {
		t.Fatal(err)
	}

	_, _, err = engine.Send(ctx, conv.ID, "/council vì sao test chập chờn", nil)
	if err == nil || !errors.Is(err, errRunCreate) {
		t.Fatalf("Send err = %v, want errRunCreate", err)
	}

	if engine.RunningWorkflow(conv.ID) != "" {
		t.Fatal("a run with a failed Create must not be registered as running")
	}
	if runs, _ := real.WorkflowRuns().List(ctx, project.ID, "", 10); len(runs) != 0 {
		t.Fatalf("no run should have been persisted: %+v", runs)
	}
	jobs, err := real.Jobs().List(ctx, storage.JobFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected a job for the failed run")
	}
	for _, j := range jobs {
		if j.Status == "running" {
			t.Fatalf("job left running: %+v", j)
		}
	}
}
