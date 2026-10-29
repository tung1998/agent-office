package officetools

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/transfer"
	"github.com/tung1998/agent-office/internal/workflow"
)

// ADR-191: an administrator's run makes a project under its own (or a
// linked one), the two link to each other and each default agent learns it;
// any other run cannot, nor reach a project it does not link to.
func TestAdminCreatesSubProject(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(dir, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	lib := workflow.Library{Dir: filepath.Join(dir, "wf")}
	tm := team.NewService(st, &workflow.Service{Store: st, Lib: lib})
	tr := transfer.New(st, provider.NewService(st, box, llm.Options{}), tm, lib)
	tr.SetCloneRoot(t.TempDir())
	acts := actions.New(st, nil)
	acts.SetProjects(tr)
	tb := New(st, nil, acts)
	tb.SetProjects(tr, dir)

	hq, _ := st.Repos().Create(ctx, storage.Repo{Name: "hq", Path: t.TempDir()})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "other", Path: t.TempDir()})
	tm.AddFromLibrary(ctx, hq.ID, team.StarterAgent)
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: hq.ID, Title: "x"})
	plain := Scope{ProjectID: hq.ID, Agent: "ceo", Level: perm.Operate, Access: perm.Admin(perm.Access{}), ConversationID: conv.ID}
	admin := plain
	admin.Admin = true
	has := func(sc Scope, name string) bool {
		return slices.ContainsFunc(tb.ToolsFor(sc), func(x Tool) bool { return x.Name == name })
	}
	if has(plain, "propose_create_project") {
		t.Fatal("a run that is not administrator may create projects")
	}
	if _, isErr := tb.Call(ctx, plain, "propose_create_project", []byte(`{"name":"sales","reason":"r"}`)); !isErr {
		t.Fatal("a run that is not administrator created a project")
	}
	if !has(admin, "propose_create_project") {
		t.Fatal("administrator has no propose_create_project")
	}
	for _, x := range tb.ToolsFor(admin) {
		if x.Name == "propose_change" {
			if _, ok := x.Schema["properties"].(map[string]any)["project"]; !ok {
				t.Fatal("propose_change takes no project for an administrator")
			}
		}
	}
	if _, isErr := tb.Call(ctx, admin, "propose_create_project", []byte(`{"name":"sales","parent":"other","reason":"r"}`)); !isErr {
		t.Fatal("made a project under one it does not link to")
	}
	out, isErr := tb.Call(ctx, admin, "propose_create_project", []byte(`{"name":"Sales","description":"bán hàng","reason":"mở phòng"}`))
	if isErr {
		t.Fatal(out)
	}
	var sales storage.Repo
	list, _ := st.Repos().List(ctx)
	for _, r := range list {
		if r.Name == "Sales" {
			sales = r
		}
	}
	if sales.ID == "" {
		t.Fatalf("no project made: %s", out)
	}
	if !slices.Contains(perm.LoadPolicy(ctx, st, hq.ID).LinkedProjects, sales.ID) || !slices.Contains(perm.LoadPolicy(ctx, st, sales.ID).LinkedProjects, hq.ID) {
		t.Fatal("parent and child do not link to each other")
	}
	notes := func(r storage.Repo) string {
		r, _ = st.Repos().Get(ctx, r.ID)
		agents, _ := st.Agents().List(ctx, r.ID)
		a, _ := storage.DefaultAgent(r, agents)
		ms, _ := st.Memories().List(ctx, r.ID, a.ID)
		var b strings.Builder
		for _, m := range ms {
			b.WriteString(m.Text + "\n")
		}
		return b.String()
	}
	if !strings.Contains(notes(sales), "Cấp trên: project hq") || !strings.Contains(notes(hq), "Đơn vị cấp dưới: project Sales") {
		t.Fatalf("notes: %q / %q", notes(sales), notes(hq))
	}
	// under the child, now linked: allowed; it is reached as a linked project
	if out, isErr := tb.Call(ctx, admin, "propose_create_project", []byte(`{"name":"Sales VN","parent":"Sales","reason":"r"}`)); isErr {
		t.Fatal(out)
	}
	if out, isErr := tb.Call(ctx, admin, "list", []byte(`{"resource":"agent","project":"other"}`)); !isErr || !strings.Contains(out, "liên kết") {
		t.Fatalf("reached a project it does not link to: %s", out)
	}
	// ADR-207: a unit an administrator's run makes works on its own: its
	// agents at Vận hành run as administrator in the name of the same admin
	boss := admin
	boss.AdminBy = "boss@x.io"
	if out, isErr := tb.Call(ctx, boss, "propose_create_project", []byte(`{"name":"Ops","pack":"@agentoffice/startup/unit","reason":"r"}`)); isErr {
		t.Fatal(out)
	}
	list, _ = st.Repos().List(ctx)
	admins := 0
	for _, r := range list {
		if r.Name != "Ops" {
			continue
		}
		agents, _ := st.Agents().List(ctx, r.ID)
		for _, x := range agents {
			if x.Permissions.FullAccess {
				admins++
			}
			if want := perm.Agent(x) == perm.Operate; x.Permissions.FullAccess != want || want && x.Permissions.FullAccessBy != "boss@x.io" {
				t.Fatalf("%s: full access %v by %q", x.Key, x.Permissions.FullAccess, x.Permissions.FullAccessBy)
			}
		}
	}
	if admins == 0 {
		t.Fatal("no agent of the unit runs as administrator")
	}
	if agents, _ := st.Agents().List(ctx, sales.ID); agents[0].Permissions.FullAccess {
		t.Fatal("a project made with no admin to act for got administrator agents")
	}
	// ADR-196: a unit's work board, from an administrator's run only
	var board string
	tb.SetBurn(func(_ context.Context, sc Scope, name string, _ BurnInput) (string, error) {
		board = sc.ProjectID
		return "ok", nil
	})
	if out, isErr := tb.Call(ctx, admin, "burn_quest", []byte(`{"title":"t","detail":"d","project":"Sales"}`)); isErr || board != sales.ID {
		t.Fatalf("quest on the unit's board: %s → %s", out, board)
	}
	if _, isErr := tb.Call(ctx, plain, "burn_quests", []byte(`{"project":"Sales"}`)); !isErr {
		t.Fatal("a run that is not administrator reached a unit's board")
	}
	if _, isErr := tb.Call(ctx, admin, "burn_quest", []byte(`{"title":"t","detail":"d","project":"other"}`)); !isErr {
		t.Fatal("put a quest on a board it does not link to")
	}
	// from a pack in this project's folder (relative path): its agent leads it
	os.MkdirAll(filepath.Join(hq.Path, "packs"), 0o755)
	os.WriteFile(filepath.Join(hq.Path, "packs", "dept.json"), []byte(`{"version":2,"include":["config"],"project":{"name":"x",
	  "agents":{"default":"truong-phong","agents":[{"key":"truong-phong","name":"Trưởng phòng","model_tier":"strong"}]}}}`), 0o644)
	if out, isErr := tb.Call(ctx, admin, "propose_create_project", []byte(`{"name":"Kho","pack":"packs/dept.json","reason":"r"}`)); isErr {
		t.Fatal(out)
	}
	list, _ = st.Repos().List(ctx)
	for _, r := range list {
		if r.Name == "Kho" {
			if n := notes(r); !strings.Contains(n, "Gói mẫu của tổ chức ở "+filepath.Join(hq.Path, "packs")) {
				t.Fatalf("kho notes: %q", n)
			}
			agents, _ := st.Agents().List(ctx, r.ID)
			if len(agents) != 1 || agents[0].Key != "truong-phong" {
				t.Fatalf("kho agents: %+v", agents)
			}
			return
		}
	}
	t.Fatal("no Kho")
	_ = other
}
