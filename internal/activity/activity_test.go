package activity_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/activity"
	"github.com/tung1998/agent-office/internal/errdigest"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-210: a project and those it links to, as one: their work boards,
// agents' runs and cost; a project it does not link to is left out.
func TestActivity(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	mk := func(name string) storage.Repo {
		r, _ := st.Repos().Create(ctx, storage.Repo{Name: name, Path: t.TempDir()})
		return r
	}
	hq, unit, other := mk("HQ"), mk("Theme Studio"), mk("Khác")
	perm.UpdatePolicy(ctx, st, hq.ID, func(p *perm.Policy) (bool, error) {
		p.LinkedProjects = append(p.LinkedProjects, unit.ID)
		return true, nil
	})
	if ps := activity.Linked(ctx, st, hq.ID); len(ps) != 2 || ps[1].ID != unit.ID {
		t.Fatalf("linked: %+v", ps)
	}
	dev, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: unit.ID, Key: "thuc-thi", Name: "Thực thi", ModelTier: storage.TierBalanced})
	b, _ := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: unit.ID})
	st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "a", Kind: "quest", Status: "handed"})
	st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "b", Kind: "quest", Status: "done"})
	cost := 0.5
	st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: unit.ID, AgentID: dev.ID, Status: "ok", CostUSD: &cost})
	st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: other.ID, Status: "ok", CostUSD: &cost})
	ov, err := activity.Load(ctx, st, activity.Linked(ctx, st, hq.ID), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ov.Runs != 1 || ov.CostUSD != 0.5 || len(ov.Projects) != 2 || ov.Projects[1].ByStatus["handed"] != 1 || ov.Projects[1].Done != 1 {
		t.Fatalf("overview: %+v", ov)
	}
	if r := ov.Report(); !strings.Contains(r, "Theme Studio / Thực thi: 1 runs") || strings.Contains(r, "Khác") {
		t.Fatalf("report:\n%s", r)
	}
	tools := activity.NewTools(st)
	if len(tools.Tools(officetools.Scope{ProjectID: hq.ID})) != 3 || len(tools.Tools(officetools.Scope{ProjectID: hq.ID, Office: true})) != 0 {
		t.Fatal("project_report/failure_digest/success_rate missing, or given to the office assistant")
	}
	if out, isErr := tools.Call(ctx, officetools.Scope{ProjectID: hq.ID}, "project_report", nil); isErr || !strings.Contains(out, "Theme Studio") {
		t.Fatalf("tool: %s", out)
	}
}

// ADR-253: failure_digest groups one project's failed jobs and model calls by
// cause; another project's failures are not in it; agent and category narrow it.
func TestFailureDigest(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "P", Path: t.TempDir()})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "Other", Path: t.TempDir()})
	ai, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "openai", Name: "openAI", ModelTier: storage.TierBalanced})
	dev, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "dev", Name: "Dev", ModelTier: storage.TierBalanced})
	fail := func(proj, agent, msg string) {
		j, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: proj, AgentID: agent, Kind: "chat_turn", Origin: "user", Status: "running"})
		st.Jobs().Finish(ctx, j.ID, "failed", "agent_error", msg, time.Now().UTC())
	}
	for _, u := range []string{"a", "b", "c"} {
		st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: p.ID, AgentID: ai.ID, ProviderName: "Codex", Status: "error",
			Error: "codex: Error: spawn /Users/" + u + "/codex ENOENT"})
	}
	fail(p.ID, dev.ID, "claude: exit status 1")
	fail(other.ID, "", "secret fault of another project")

	tools := activity.NewTools(st)
	call := func(args string) string {
		t.Helper()
		out, isErr := tools.Call(ctx, officetools.Scope{ProjectID: p.ID}, "failure_digest", []byte(args))
		if isErr {
			t.Fatalf("failure_digest %s: %s", args, out)
		}
		return out
	}
	out := call(`{}`)
	if !strings.Contains(out, "Failures in the last 7 days: 4") || !strings.Contains(out, "3× [cli_broken] codex: Error: spawn <path> ENOENT") ||
		!strings.Contains(out, "agents: openAI") || !strings.Contains(out, "connections: Codex") || !strings.Contains(out, "1× [other] claude: exit status 1") {
		t.Fatalf("digest:\n%s", out)
	}
	if strings.Index(out, "cli_broken") > strings.Index(out, "[other]") {
		t.Fatalf("biggest cause not first:\n%s", out)
	}
	if strings.Contains(out, "another project") {
		t.Fatalf("another project's failure leaked:\n%s", out)
	}
	if out := call(`{"agent":"dev"}`); strings.Contains(out, "cli_broken") || !strings.Contains(out, "exit status 1") {
		t.Fatalf("agent filter:\n%s", out)
	}
	if out := call(`{"category":"cli_broken"}`); strings.Contains(out, "exit status 1") || !strings.Contains(out, ": 3,") {
		t.Fatalf("category filter:\n%s", out)
	}
	if strings.Contains(out, "lower than the truth") {
		t.Fatalf("a whole window said to be cut:\n%s", out)
	}
	// more failures than are read: the tool says its totals are a floor (ADR-254)
	func() {
		defer func(n int) { errdigest.MaxRows = n }(errdigest.MaxRows)
		errdigest.MaxRows = 2
		if out := call(`{}`); !strings.Contains(out, "only the latest 2 failed jobs") || !strings.Contains(out, "lower than the truth") {
			t.Fatalf("cut window:\n%s", out)
		}
	}()
	if out, isErr := tools.Call(ctx, officetools.Scope{ProjectID: p.ID}, "failure_digest", []byte(`{"agent":"nobody"}`)); !isErr {
		t.Fatalf("unknown agent accepted: %s", out)
	}
	if out := call(`{"category":"quota"}`); !strings.Contains(out, "No failures") {
		t.Fatalf("empty digest: %s", out)
	}
}

// ADR-268: an AI connection out of quota is the service's quota, not the
// office's budget (it was listed as [budget]); a scheduled run skipped while
// every connection is out of quota is not a failure at all.
func TestFailureDigestIgnoresQuotaSkips(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "P", Path: t.TempDir()})
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "t", Name: "Thực thi", ModelTier: storage.TierBalanced})
	end := func(status, code, msg string) {
		j, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, AgentID: ag.ID, Kind: "chat_turn", Origin: "automation", Status: "running"})
		st.Jobs().Finish(ctx, j.ID, status, code, msg, time.Now().UTC())
	}
	end("failed", "out_of_tokens", "codex: You’ve hit your usage limit. try again at Nov 13th, 2026 10:18 AM.")
	end("skipped", "quota", "mọi kết nối AI của Thực thi đang hết hạn mức, chờ tới 13/11 10:18")
	end("skipped", "quota", "mọi kết nối AI của Thực thi đang hết hạn mức, chờ tới 13/11 10:18")
	out, isErr := activity.NewTools(st).Call(ctx, officetools.Scope{ProjectID: p.ID}, "failure_digest", []byte(`{}`))
	if isErr || !strings.Contains(out, "Failures in the last 7 days: 1") || !strings.Contains(out, "1× [quota]") || strings.Contains(out, "[budget]") {
		t.Fatalf("digest:\n%s", out)
	}
}

// success_rate: goal completion by Burn piece owner — done without the
// result reviewer sending it back (Attempts>1) over closed (skipped
// excluded), not lumped together across owners.
func TestSuccessRate(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "P", Path: t.TempDir()})
	b, _ := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID})
	add := func(owner, status string, attempts int) {
		st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "x", Kind: "bug", Owner: owner, Status: status, Attempts: attempts})
	}
	// agent A: 8 done cleanly, 2 failed -> 8/10
	for i := 0; i < 8; i++ {
		add("A", "done", 1)
	}
	for i := 0; i < 2; i++ {
		add("A", "failed", 3)
	}
	// agent B: 3 done cleanly, 5 done after reviewAgain (Attempts>1, not clean) -> 3/8; 2 skipped don't count (never attempted)
	for i := 0; i < 3; i++ {
		add("B", "done", 1)
	}
	for i := 0; i < 5; i++ {
		add("B", "done", 2)
	}
	for i := 0; i < 2; i++ {
		add("B", "skipped", 1)
	}

	tools := activity.NewTools(st)
	out, isErr := tools.Call(ctx, officetools.Scope{ProjectID: p.ID}, "success_rate", []byte(`{}`))
	if isErr {
		t.Fatalf("success_rate: %s", out)
	}
	if !strings.Contains(out, "A: 8/10 (80%)") || !strings.Contains(out, "B: 3/8 (38%)") {
		t.Fatalf("success_rate:\n%s", out)
	}
	if out, isErr := tools.Call(ctx, officetools.Scope{ProjectID: p.ID}, "success_rate", []byte(`{"agent":"b"}`)); isErr ||
		strings.Contains(out, "A:") || !strings.Contains(out, "B: 3/8") {
		t.Fatalf("agent filter:\n%s", out)
	}
}
