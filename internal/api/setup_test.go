package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeClaude(t *testing.T, answer string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"claude-opus-5-5"}]}`))
			return
		}
		out, _ := json.Marshal(map[string]any{"model": "claude-opus-5-5", "content": []map[string]string{{"type": "text", "text": answer}},
			"usage": map[string]int{"input_tokens": 100, "output_tokens": 50}})
		w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupFlowAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"shop","dependencies":{"nuxt":"4"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Run pnpm test."), 0o644)
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir}, nil)
	id := body["project"].(map[string]any)["id"].(string)

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/scan", map[string]any{}, nil)
	sum := body["summary"].(map[string]any)
	if resp.StatusCode != 200 || sum["name"] != "shop" || len(sum["agent_docs"].([]any)) != 1 {
		t.Fatalf("scan = %d %v", resp.StatusCode, body)
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/propose", map[string]any{}, nil)
	if resp.StatusCode != 412 || body["code"] != "no_provider" {
		t.Fatalf("propose without provider = %d %v", resp.StatusCode, body)
	}

	answer := "```json\n" + `{"description":"Shop Nuxt.","template_key":"solo","reason":"Nhỏ","confidence":0.7,
	  "agent_changes":[{"action":"update","key":"assistant","instructions":"Chạy pnpm test.","reason":"CLAUDE.md"}],"notes":[]}` + "\n```"
	srv := fakeClaude(t, answer)
	do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "Claude", "kind": "anthropic", "base_url": srv.URL, "api_key": "sk-ant-test-0000-key"}, nil)

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/propose", map[string]any{"goal": "sửa bug"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("propose = %d %v", resp.StatusCode, body)
	}
	result := body["result"].(map[string]any)
	prop := result["proposal"].(map[string]any)
	if prop["template_key"] != "solo" || len(result["problems"].([]any)) != 0 {
		t.Fatalf("result = %v", result)
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/build", map[string]any{
		"template_key": "team", "changes": []map[string]any{{"action": "add", "key": "orphan", "tier": "worker", "reason": "x"}}}, nil)
	if resp.StatusCode != 200 || len(body["problems"].([]any)) == 0 {
		t.Fatalf("build with bad change = %d %v", resp.StatusCode, body)
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/apply", map[string]any{
		"template_key": "solo", "changes": prop["agent_changes"], "description": "Shop Nuxt."}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("apply = %d %v", resp.StatusCode, body)
	}
	p := body["project"].(map[string]any)
	if p["description"] != "Shop Nuxt." || p["model"].(map[string]any)["kind"] != "solo" {
		t.Fatalf("project = %v", p)
	}
	agent := p["model"].(map[string]any)["agents"].([]any)[0].(map[string]any)
	if s, _ := agent["instructions"].(string); len(s) == 0 || !strings.Contains(s, "Chạy pnpm test.") {
		t.Fatalf("agent instructions not tailored: %v", agent["instructions"])
	}

	// machine-wide project needs a goal
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"name": "Trợ lý"}, nil)
	hid := body["project"].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+hid+"/setup/propose", map[string]any{}, nil); resp.StatusCode != 400 {
		t.Fatalf("helper without goal = %d", resp.StatusCode)
	}
}

func TestUsageAPIAndBudget(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	srv := fakeClaude(t, "```json\n"+`{"description":"x","template_key":"solo","reason":"r","confidence":0.5,"agent_changes":[],"notes":[]}`+"\n```")
	do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "Claude", "kind": "anthropic", "base_url": srv.URL, "api_key": "sk-ant-test-0000-key"}, nil)
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"name": "Trợ lý"}, nil)
	id := body["project"].(map[string]any)["id"].(string)

	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/propose", map[string]any{"goal": "x"}, nil); resp.StatusCode != 200 {
		t.Fatalf("propose = %d", resp.StatusCode)
	}
	resp, sum := do(t, admin, "GET", e.srv.URL+"/api/usage/summary?days=7", nil, nil)
	day := sum["by_day"].([]any)[6].(map[string]any)
	if resp.StatusCode != 200 || len(sum["by_day"].([]any)) != 7 || sum["today"].(float64) <= 0 || day["cost_usd"].(float64) <= 0 || day["runs"].(float64) != 1 {
		t.Fatalf("summary = %d %v", resp.StatusCode, sum)
	}
	resp, runs := do(t, admin, "GET", e.srv.URL+"/api/usage/runs", nil, nil)
	r0 := runs["runs"].([]any)[0].(map[string]any)
	if resp.StatusCode != 200 || r0["kind"] != "setup_propose" || r0["project_name"] != "Trợ lý" || r0["cost_source"] != "estimate" {
		t.Fatalf("runs = %v", runs)
	}

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "PUT", e.srv.URL+"/api/usage/settings", map[string]any{"daily_limit_usd": 1}, nil); resp.StatusCode != 403 {
		t.Fatalf("member settings = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/usage/settings", map[string]any{"daily_limit_usd": 0.0001}, nil); resp.StatusCode != 200 {
		t.Fatalf("settings = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/propose", map[string]any{"goal": "x"}, nil)
	if resp.StatusCode != 429 || body["code"] != "budget" {
		t.Fatalf("over budget = %d %v", resp.StatusCode, body)
	}
}
