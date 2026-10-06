package agentinfo_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/agentinfo"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func setup(t *testing.T) (storage.Store, *orgmodel.Service, storage.Repo, storage.Agent) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Migrate(ctx)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "demo", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	m, _ := st.OrgModels().GetForRepo(ctx, project.ID)
	agents, _ := st.Agents().List(ctx, m.ID)
	return st, org, project, agents[0]
}

func TestHistoryAndRestore(t *testing.T) {
	ctx := context.Background()
	st, org, _, a := setup(t)
	svc := agentinfo.New(st, org, time.UTC)
	original := a.Instructions

	a.Instructions = "Viết ngắn."
	a, _ = org.SaveAgent(ctx, a)
	a.Role, a.Key = "Người review", "reviewer"
	a, err := org.SaveAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}

	h, err := svc.History(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) < 2 {
		t.Fatalf("history = %+v", h)
	}
	fields := func(e agentinfo.Entry) map[string]bool {
		m := map[string]bool{}
		for _, c := range e.Changes {
			m[c.Field] = true
		}
		return m
	}
	// newest: role and key (renamed), then the instructions
	if f := fields(h[0]); !f["role"] || !f["key"] || f["instructions"] {
		t.Fatalf("newest change = %+v", h[0])
	}
	if f := fields(h[1]); !f["instructions"] || h[1].Changes[0].Before != original {
		t.Fatalf("older change = %+v", h[1])
	}

	// back to before the instructions changed: old text, old role; key and id stay
	back, err := svc.Restore(ctx, a, h[1].RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Instructions != original || back.Role == "Người review" || back.ID != a.ID || back.Key != "reviewer" {
		t.Fatalf("restored = %+v", back)
	}
	// the restore is itself in the history
	if h2, _ := svc.History(ctx, back); len(h2) != len(h)+1 {
		t.Fatalf("history after restore = %d entries, want %d", len(h2), len(h)+1)
	}
}

// ADR-074 security: restoring an agent to an earlier revision never turns
// full access back on, even when the snapshot being restored to had it.
func TestRestoreNeverTurnsOnFullAccess(t *testing.T) {
	ctx := context.Background()
	st, org, _, a := setup(t)
	svc := agentinfo.New(st, org, time.UTC)

	a.Permissions.FullAccess, a.Permissions.FullAccessBy, a.Permissions.ExtraDirs = true, "admin@x.io", []string{"/tmp/x"}
	a, _ = org.SaveAgent(ctx, a) // live agent now has full access
	a.Role = "Người review"      // a trivial change: its snapshot captures the full-access state above
	a, err := org.SaveAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	// permView (what History shows) leaves full access out of the visible
	// diff, so only the role change shows up — but its snapshot (taken right
	// before it) still captures the agent's full state, full access included.
	h, err := svc.History(ctx, a)
	if err != nil || len(h) < 1 {
		t.Fatalf("history = %+v, %v", h, err)
	}
	back, err := svc.Restore(ctx, a, h[0].RevisionID) // the snapshot with full access on
	if err != nil {
		t.Fatal(err)
	}
	if back.Permissions.FullAccess || back.Permissions.FullAccessBy != "" || len(back.Permissions.ExtraDirs) != 0 {
		t.Fatalf("restore turned full access back on: %+v", back.Permissions)
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	st, org, project, a := setup(t)
	svc := agentinfo.New(st, org, time.UTC)
	cost := 0.5
	for i, d := range []int64{1000, 2000, 3000, 40000} {
		r := storage.Run{Kind: "chat", ProjectID: project.ID, AgentID: a.ID, Model: "m1", Status: "ok", CostUSD: &cost, DurationMS: d, InputTokens: 10, OutputTokens: 5}
		if i == 3 {
			r.Status, r.Error, r.CostUSD = "error", "HTTP 529: overloaded\ndetails", nil
		}
		st.Runs().Create(ctx, r)
	}
	st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: project.ID, AgentID: "other", Status: "ok"})
	s, err := svc.Stats(ctx, a, project.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.Runs != 4 || s.OK != 3 || s.Errors != 1 || s.CostUSD != 1.5 || s.UnknownCost != 1 || s.InputTokens != 40 {
		t.Fatalf("stats = %+v", s)
	}
	if s.P95MS != 40000 || s.P50MS != 3000 || len(s.PerDay) != 7 || s.PerDay[6].OK != 3 {
		t.Fatalf("durations/days = %+v", s)
	}
	if len(s.TopErrors) != 1 || s.TopErrors[0].Error != "HTTP 529: overloaded" || len(s.ByModel) != 1 {
		t.Fatalf("errors/models = %+v %+v", s.TopErrors, s.ByModel)
	}
}

// An agent's activity has its chats from bots (Discord/Telegram) too.
func TestActivityHasBotChats(t *testing.T) {
	ctx := context.Background()
	st, org, project, a := setup(t)
	svc := agentinfo.New(st, org, time.UTC)
	st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, AgentID: a.ID, Title: "web"})
	st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, AgentID: a.ID, Title: "discord", Purpose: "channel", CreatedBy: "discord:an"})
	items, err := svc.Activity(ctx, a, project.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	if len(items) != 2 {
		t.Fatalf("activity = %v", titles)
	}
}

// One row per place the agent took part in, however often it answered there.
func TestActivityByPlace(t *testing.T) {
	ctx := context.Background()
	st, org, project, a := setup(t)
	svc := agentinfo.New(st, org, time.UTC)
	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, AgentID: a.ID, Title: "của mình"})
	for _, s := range []string{"trả lời 1", "trả lời 2", "trả lời 3"} {
		st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "user", Content: "hỏi"})
		st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "assistant", Author: a.Name, Content: s})
	}
	// someone else's chat it was pulled into
	other, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, Title: "của người khác"})
	st.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: other.ID, AgentID: a.ID, AgentName: a.Name})
	st.Chat().AddMessage(ctx, storage.Message{ConversationID: other.ID, Role: "assistant", Author: a.Name, Content: "góp ý"})
	items, err := svc.Activity(ctx, a, project.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	answers := map[string]int{}
	for _, it := range items {
		answers[it.Title] = it.Answers
	}
	if len(items) != 2 || answers["của mình"] != 3 || answers["của người khác"] != 1 {
		t.Fatalf("items = %+v", items)
	}
}
