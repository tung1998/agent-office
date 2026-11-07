package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Model/effort scoring over HTTP (ADR-267): 👍/👎 on answers, the project's
// table, a suggestion for a chat (never max, never a connection over its
// quota), the chat's own model per agent, labels and fallback order.
func TestScoresAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	prov, err := e.st.Providers().Create(ctx, storage.Provider{Name: "CC", Kind: storage.ProviderClaudeCLI, Enabled: true, IsDefault: true, Status: "ok",
		Models:     []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"},
		TierModels: map[string]string{"strong": "claude-opus-5-5", "balanced": "claude-sonnet-5-5", "fast": "claude-haiku-5-5"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.st.Providers().SetDefault(ctx, prov.ID)
	other, _ := e.st.Providers().Create(ctx, storage.Provider{Name: "Codex", Kind: storage.ProviderCodexCLI, Enabled: true, Status: "ok"})
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	agents, _ := e.st.Agents().List(ctx, pid)
	if len(agents) == 0 {
		t.Fatal("no agent")
	}
	agent := agents[0]
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "x", AgentID: agent.ID})
	user, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: "sửa lỗi"})
	ans, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "xong", AgentID: agent.ID})
	now := time.Now().UTC()
	j, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", Status: "done", ConversationID: c.ID, MessageID: ans.ID, AgentID: agent.ID, FinishedAt: &now})
	if err := e.st.Scores().Upsert(ctx, storage.JobScore{JobID: j.ID, ProjectID: pid, ConversationID: c.ID, MessageID: ans.ID, AgentID: agent.ID, ProviderID: prov.ID,
		Model: "claude-sonnet-5-5", Effort: "medium", TaskKind: "bugfix", Difficulty: "medium", Status: "done", FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	url := e.srv.URL

	// feedback
	if res, _ := do(t, admin, "PUT", url+"/api/conversations/"+c.ID+"/messages/"+user.ID+"/feedback", map[string]any{"value": 1}, nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("feedback on the person's message: %d", res.StatusCode)
	}
	if res, _ := do(t, admin, "PUT", url+"/api/conversations/"+c.ID+"/messages/"+ans.ID+"/feedback", map[string]any{"value": -1}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("feedback: %d", res.StatusCode)
	}
	if s, _ := e.st.Scores().Get(ctx, j.ID); s.Feedback != -1 || s.Q == nil || *s.Q != 0 {
		t.Fatalf("score after 👎 = %+v", s)
	}

	// stats
	res, b := do(t, admin, "GET", url+"/api/projects/"+pid+"/model-stats?days=7", nil, nil)
	table, _ := b["table"].(map[string]any)
	if res.StatusCode != http.StatusOK || table == nil || len(table["cells"].([]any)) != 1 || len(b["quota"].([]any)) != 2 {
		t.Fatalf("stats = %d %v", res.StatusCode, b)
	}

	// suggestion: never max
	res, b = do(t, admin, "GET", url+"/api/conversations/"+c.ID+"/suggest?text=ADR+l%C3%A0+g%C3%AC", nil, nil)
	sug, _ := b["suggestion"].(map[string]any)
	if res.StatusCode != http.StatusOK || sug == nil || sug["effort"] == "max" || b["kind"] != "ask" || b["difficulty"] != "light" {
		t.Fatalf("suggest = %d %v", res.StatusCode, b)
	}
	// every connection over its quota: no suggestion
	_ = e.st.Settings().Set(ctx, "provider_limits:"+prov.ID, map[string]any{"status": "rejected",
		"windows": map[string]any{"five_hour": map[string]any{"utilization": 1, "resets_at": now.Add(time.Hour)}}, "updated_at": now})
	if _, b = do(t, admin, "GET", url+"/api/conversations/"+c.ID+"/suggest?text=x", nil, nil); b["suggestion"] != nil || b["reason"] != "no_option" {
		t.Fatalf("suggested over quota: %v", b)
	}

	// the chat's own model for its agent
	if res, _ := do(t, admin, "PUT", url+"/api/conversations/"+c.ID+"/agents/"+agent.ID+"/model", map[string]any{"model": "nope"}, nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown model: %d", res.StatusCode)
	}
	if res, _ := do(t, admin, "PUT", url+"/api/conversations/"+c.ID+"/agents/"+agent.ID+"/model", map[string]any{"model": "claude-haiku-5-5"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("set model: %d", res.StatusCode)
	}
	if _, b = do(t, admin, "GET", url+"/api/conversations/"+c.ID+"/suggest?agent="+agent.ID, nil, nil); b["current_model"] != "claude-haiku-5-5" || len(b["models"].([]any)) != 3 {
		t.Fatalf("current model = %v", b)
	}

	// labels
	if res, _ := do(t, admin, "PUT", url+"/api/projects/"+pid+"/job-scores/"+j.ID+"/labels", map[string]any{"kind": "nope", "difficulty": "light"}, nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad label: %d", res.StatusCode)
	}
	if res, _ := do(t, admin, "PUT", url+"/api/projects/"+pid+"/job-scores/"+j.ID+"/labels", map[string]any{"kind": "feature", "difficulty": "heavy"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("labels: %d", res.StatusCode)
	}

	// fallback order: the other vendor
	_, b = do(t, admin, "GET", url+"/api/agents/"+agent.ID+"/fallback-suggest", nil, nil)
	if order, _ := b["order"].([]any); len(order) != 1 || order[0] != other.ID {
		t.Fatalf("fallback = %v", b)
	}

	// frozen: 409
	old := now.Add(-8 * 24 * time.Hour)
	s, _ := e.st.Scores().Get(ctx, j.ID)
	s.FinishedAt = old
	_ = e.st.Scores().Upsert(ctx, s)
	do(t, admin, "GET", url+"/api/projects/"+pid+"/model-stats", nil, nil) // refresh freezes it
	if res, _ := do(t, admin, "PUT", url+"/api/conversations/"+c.ID+"/messages/"+ans.ID+"/feedback", map[string]any{"value": 1}, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("feedback on frozen: %d", res.StatusCode)
	}
}

// Another project's member cannot read a project's model statistics.
func TestModelStatsOtherProjectForbidden(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	anon := e.client(t)
	if res, _ := do(t, anon, "GET", e.srv.URL+"/api/projects/"+pid+"/model-stats", nil, nil); res.StatusCode == http.StatusOK {
		t.Fatalf("anonymous read stats: %d", res.StatusCode)
	}
}
