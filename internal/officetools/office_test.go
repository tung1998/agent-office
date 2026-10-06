package officetools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// The office assistant's scope (ADR-046): tools across projects, a project
// named by id or name, figures, and hand-offs to a project's chat.
func TestOfficeScopeTools(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	office, _ := st.Repos().Create(ctx, storage.Repo{Name: "Office"})
	shop, _ := st.Repos().Create(ctx, storage.Repo{Name: "Storefront", Description: "cửa hàng"})
	st.Jobs().Create(ctx, storage.Job{ProjectID: shop.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", Title: "hỏi graylog", Status: "done"})
	acts := actions.New(st, nil)
	acts.SetRunner(fakeRunner{})
	box := New(st, nil, acts)
	box.SetOffice(func(ctx context.Context) string { return office.ID })
	sc := Scope{ProjectID: office.ID, Office: true, Agent: "Trợ lý office"}
	call := func(name string, args map[string]any) (string, bool) {
		raw, _ := json.Marshal(args)
		return box.Call(ctx, sc, name, raw)
	}
	names := map[string]bool{}
	for _, tl := range box.ToolsFor(sc) {
		names[tl.Name] = true
	}
	for _, n := range []string{"projects", "jobs_query", "usage_summary", "handoff", "run_automation"} {
		if !names[n] {
			t.Errorf("office scope lacks %s", n)
		}
	}
	if project := box.ToolsFor(Scope{ProjectID: shop.ID}); len(project) > 0 {
		for _, tl := range project {
			if tl.Name == "projects" || tl.Name == "handoff" {
				t.Errorf("a project's agent got %s", tl.Name)
			}
		}
	}
	if out, isErr := call("projects", nil); isErr || !strings.Contains(out, "Storefront") || strings.Contains(out, `"Office"`) {
		t.Fatalf("projects = %v %s", isErr, out)
	}
	if out, isErr := call("jobs_query", map[string]any{"project": "storefront"}); isErr || !strings.Contains(out, "hỏi graylog") {
		t.Fatalf("jobs_query = %v %s", isErr, out)
	}
	if out, isErr := call("handoff", map[string]any{"project": "Storefront", "message": "sửa lỗi 500"}); isErr || !strings.Contains(out, "/projects/"+shop.ID+"?tab=chat&draft=") {
		t.Fatalf("handoff = %v %s", isErr, out)
	}
	if _, isErr := call("jobs_query", map[string]any{"project": "không có"}); !isErr {
		t.Fatal("an unknown project was accepted")
	}
	// review I1: the assistant's own chats (each person's) never show through tools
	private, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: office.ID, Title: "riêng", CreatedBy: "human:b@x.io"})
	st.Chat().AddMessage(ctx, storage.Message{ConversationID: private.ID, Role: "user", Content: "lương của tôi"})
	st.Jobs().Create(ctx, storage.Job{ProjectID: office.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", Title: "lương của tôi", Status: "done", ConversationID: private.ID})
	if out, _ := call("jobs_query", nil); strings.Contains(out, "lương của tôi") {
		t.Fatalf("jobs_query shows an assistant chat: %s", out)
	}
	if _, isErr := call("read_link", map[string]any{"url": "http://x/projects/" + office.ID + "?tab=chat&c=" + private.ID}); !isErr {
		t.Fatal("read_link read another person's assistant chat")
	}
}

type fakeRunner struct{}

func (fakeRunner) RunAutomation(context.Context, string) (string, error) { return "job_x", nil }

type fakeConfig struct{}

func (fakeConfig) DescribeConfig(string) (string, error)                             { return "", nil }
func (fakeConfig) ListConfig(context.Context, string, string) (string, error)        { return "", nil }
func (fakeConfig) GetConfig(context.Context, string, string, string) (string, error) { return "", nil }

// A read-only agent of a project may look at settings but not propose a
// change (the office assistant may: its cards are what it is for).
func TestProposeChangeNeedsProposeLevel(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	shop, _ := st.Repos().Create(ctx, storage.Repo{Name: "Storefront"})
	box := New(st, nil, actions.New(st, nil))
	box.SetConfig(fakeConfig{})
	raw, _ := json.Marshal(map[string]any{"resource": "automation", "op": "create", "patch": map[string]any{"name": "x"}, "reason": "r"})
	out, isErr := box.Call(ctx, Scope{ProjectID: shop.ID, Level: "read"}, "propose_change", raw)
	if !isErr || !strings.Contains(out, "quyền") {
		t.Fatalf("a read-only agent proposed a change: %v %s", isErr, out)
	}
}

// Answer only: the assistant reads and answers, it proposes nothing.
func TestAnswerOnlyHasNoProposals(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	office, _ := st.Repos().Create(ctx, storage.Repo{Name: "Office"})
	acts := actions.New(st, nil)
	acts.SetRunner(fakeRunner{})
	box := New(st, nil, acts)
	box.SetOffice(func(ctx context.Context) string { return office.ID })
	sc := Scope{ProjectID: office.ID, Office: true, Agent: "Trợ lý office", AnswerOnly: true}
	for _, tl := range box.ToolsFor(sc) {
		if strings.HasPrefix(tl.Name, "propose") || tl.Name == "run_automation" {
			t.Fatalf("answer only offers %s", tl.Name)
		}
	}
	if !box.Has(sc, "jobs_query") {
		t.Fatal("answer only still reads the office")
	}
}

// ADR-086: search_history finds the project's chats by their words, with a
// link to read them.
func TestSearchHistory(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	tb := New(st, nil, nil)
	c, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "Thanh toán", CreatedBy: "human:a@x.io"})
	m, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Author: "human:a@x.io", Content: "sửa lỗi thanh toán PayPal"})
	out, isErr := tb.Call(ctx, Scope{ProjectID: p.ID, ConversationID: c.ID}, "search_history", []byte(`{"query":"thanh toan paypal"}`))
	if isErr || !strings.Contains(out, "Thanh toán") || !strings.Contains(out, "&m="+m.ID) {
		t.Fatalf("search = %v %s", isErr, out)
	}
}
