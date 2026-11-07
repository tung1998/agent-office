package workflow_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/workflow"
)

const updateTestSource = `---
key: thu
name: Thử
description: d
roles:
  - key: a
    access: analyze
---
Làm đi.
`

// Update's version check (ADR-072) at the API layer is decorative unless the
// write itself is conditioned on the row the caller read: two saves racing
// on the same Source/Bindings must not let the second silently clobber the
// first (regression for the TOCTOU between the API's Get and this Update).
func TestServiceUpdateExpectIsCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	svc := &workflow.Service{Store: st}
	w, err := svc.Create(ctx, p.ID, updateTestSource, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.Workflows().Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}

	// tab A toggling Enabled must not clobber tab B's Source change read
	// from the same "before" row.
	if _, err := svc.Update(ctx, w.ID, workflow.Change{Enabled: boolPtr(false)}); err != nil {
		t.Fatalf("enable toggle: %v", err)
	}
	other := strings.Replace(updateTestSource, "Thử", "Thử 2", 1)
	if _, err := svc.Update(ctx, w.ID, workflow.Change{Source: &other, Expect: &before}); err != nil {
		t.Fatalf("Update with current Expect: %v", err)
	}

	// a second save against the same stale "before" must 409, not overwrite.
	stale := strings.Replace(updateTestSource, "Thử", "Thử 3", 1)
	if _, err := svc.Update(ctx, w.ID, workflow.Change{Source: &stale, Expect: &before}); err != workflow.ErrConflict {
		t.Fatalf("Update with stale Expect = %v, want ErrConflict", err)
	}
	cur, err := st.Workflows().Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cur.Source, "Thử 2") || strings.Contains(cur.Source, "Thử 3") {
		t.Fatalf("a conflicted Update changed the row: %q", cur.Source)
	}
	if cur.Enabled {
		t.Fatal("the conflicted Update's loss should not also erase the earlier Enabled toggle")
	}
}

func boolPtr(b bool) *bool { return &b }

// raceRepo runs onGet (once) right after Get returns, letting a test splice
// an interleaved write between the caller's Get and its own later write —
// simulating two requests racing inside the gap Service.Update's single Get
// leaves open.
type raceRepo struct {
	storage.WorkflowRepo
	onGet func()
}

func (r *raceRepo) Get(ctx context.Context, id string) (storage.Workflow, error) {
	w, err := r.WorkflowRepo.Get(ctx, id)
	if f := r.onGet; f != nil {
		r.onGet = nil // once, so the interleaved call's own Get doesn't recurse
		f()
	}
	return w, err
}

type raceStore struct {
	storage.Store
	wf storage.WorkflowRepo
}

func (s raceStore) Workflows() storage.WorkflowRepo { return s.wf }

// contentWriteRaceRepo runs onContentWrite (once) right after UpdateContent
// returns, letting a test splice a write between FromLibrary's content write
// (via Service.Update) and its own final source_key/source_hash write.
type contentWriteRaceRepo struct {
	storage.WorkflowRepo
	onContentWrite func()
}

func (r *contentWriteRaceRepo) UpdateContent(ctx context.Context, w storage.Workflow) error {
	err := r.WorkflowRepo.UpdateContent(ctx, w)
	if f := r.onContentWrite; f != nil {
		r.onContentWrite = nil
		f()
	}
	return err
}

// FromLibrary's own write (content + source_key/source_hash) must be one
// compare-and-swap, not two separate statements with a gap between them: a
// WorkflowEditor save landing in that gap must not leave source_hash
// pointing at a source the row no longer has (regression for the TOCTOU
// between FromLibrary's Get and its final write).
func TestServiceFromLibraryContentAndSourceAreOneCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	plain := &workflow.Service{Store: st, Lib: workflow.Library{Dir: t.TempDir()}}
	w, err := plain.Install(ctx, p.ID, "handoff", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.Workflows().Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}

	race := &raceRepo{WorkflowRepo: st.Workflows()}
	svc := &workflow.Service{Store: raceStore{Store: st, wf: race}, Lib: plain.Lib}
	race.onGet = func() {
		// a WorkflowEditor save (CAS'd via Change.Expect) landing right
		// after FromLibrary's own Get, before FromLibrary's final write.
		edited := strings.Replace(before.Source, "handoff", "handoff", 1) + "\n# edited by the user\n"
		if _, err := plain.Update(ctx, w.ID, workflow.Change{Source: &edited, Expect: &before}); err != nil {
			t.Errorf("interleaved edit: %v", err)
		}
	}

	_, err = svc.FromLibrary(ctx, w.ID)
	if err != workflow.ErrConflict {
		t.Fatalf("FromLibrary racing an interleaved edit = %v, want ErrConflict", err)
	}

	cur, err := st.Workflows().Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cur.Source, "edited by the user") {
		t.Fatalf("the interleaved edit was lost: %q", cur.Source)
	}
	if cur.SourceHash != before.SourceHash {
		t.Fatalf("SourceHash = %q, want it untouched (%q): FromLibrary must not write a hash for a source the row no longer has", cur.SourceHash, before.SourceHash)
	}
}

// A council's planner is the default agent: it still takes the seat named
// as it is, and every seat goes to the agent named for it (ADR-166).
func TestSuggestNamedSeats(t *testing.T) {
	def, err := workflow.Parse(workflow.BuiltinSource("council-3"))
	if err != nil {
		t.Fatal(err)
	}
	agents := []storage.Agent{
		{ID: "p", Name: "Lập kế hoạch", Role: "Lập pháp: mục tiêu, quy tắc, kế hoạch", ModelTier: "strong", Permissions: storage.Permissions{Level: "read", ReadOnly: true}},
		{ID: "e", Name: "Thực thi", Role: "Hành pháp: điều phối worker thực hiện", ModelTier: "strong", Permissions: storage.Permissions{Level: "operate"}},
		{ID: "a", Name: "Giám sát", Role: "Tư pháp: kiểm tra, đánh giá, phủ quyết", ModelTier: "strong", Permissions: storage.Permissions{Level: "read", ReadOnly: true}},
		{ID: "d", Name: "DevOps", Role: "Build, CI/CD", ModelTier: "balanced", Permissions: storage.Permissions{Level: "operate"}},
	}
	got := workflow.Suggest(def, agents, "p", nil)
	if got["lap-ke-hoach"] != "p" || got["thuc-thi"] != "e" || got["giam-sat"] != "a" {
		t.Fatalf("bindings = %v", got)
	}
	// a default agent not named for a seat still only coordinates
	def2, _ := workflow.Parse(workflow.BuiltinSource("handoff"))
	for role, id := range workflow.Suggest(def2, agents, "p", nil) {
		if id == "p" {
			t.Fatalf("%s given to the default agent", role)
		}
	}
}
