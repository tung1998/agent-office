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
	t.Parallel()
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

	answer := "```json\n" + `{"description":"Shop Nuxt.","reason":"Nhỏ","confidence":0.7,
	  "agents":[{"id":"office/frontend-dev","instructions":"Chạy pnpm test.","reason":"CLAUDE.md"}],"notes":[],
	  "workflows":[{"name":"fix-tests","reason":"Có test"}],
	  "skills":[{"name":"made-up","reason":"x"}],"mcp":[{"name":"context7","reason":"docs"},{"name":"github","reason":"PRs"}],
	  "quick_checks":".ts .vue: npx eslint {file}"}` + "\n```"
	srv := fakeClaude(t, answer)
	do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "Claude", "kind": "anthropic", "base_url": srv.URL, "api_key": "sk-ant-test-0000-key"}, nil)

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/propose", map[string]any{"goal": "sửa bug"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("propose = %d %v", resp.StatusCode, body)
	}
	result := body["result"].(map[string]any)
	prop := result["proposal"].(map[string]any)
	if len(prop["agents"].([]any)) != 1 || len(prop["workflows"].([]any)) != 1 || len(result["problems"].([]any)) != 0 {
		t.Fatalf("result = %v", result)
	}
	// a skill not in the library is dropped with a warning; MCP picks say what they need
	if len(prop["skills"].([]any)) != 0 || !strings.Contains(strings.Join(anyStrings(result["warnings"]), ";"), "made-up") {
		t.Fatalf("skills = %v, warnings = %v", prop["skills"], result["warnings"])
	}
	if mcp := prop["mcp"].([]any); len(mcp) != 2 || mcp[1].(map[string]any)["needs"] != "key" || prop["quick_checks"] == "" {
		t.Fatalf("mcp = %v, quick_checks = %v", prop["mcp"], prop["quick_checks"])
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/build", map[string]any{
		"agents": []map[string]any{{"id": "office/devops", "key": "Bad Key", "reason": "x"}}}, nil)
	if resp.StatusCode != 200 || len(body["problems"].([]any)) == 0 {
		t.Fatalf("build with bad change = %d %v", resp.StatusCode, body)
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/setup/apply", map[string]any{
		"agents": prop["agents"], "workflows": []string{"fix-tests"}, "description": "Shop Nuxt.",
		"mcp": []string{"context7", "github"}, "skills": []string{"missing-skill"}, "quick_checks": prop["quick_checks"]}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("apply = %d %v", resp.StatusCode, body)
	}
	ready := map[string]string{}
	for _, x := range body["ready"].([]any) {
		it := x.(map[string]any)
		ready[it["kind"].(string)+":"+it["name"].(string)] = it["status"].(string)
	}
	if ready["mcp:context7"] != "ok" || ready["mcp:github"] != "todo" || ready["skill:missing-skill"] != "error" ||
		ready["quick_check:kiểm tra nhanh"] != "ok" || ready["agents:agent"] != "ok" {
		t.Fatalf("ready = %v", ready)
	}
	_, pol := do(t, admin, "GET", e.srv.URL+"/api/projects/"+id+"/policy", nil, nil)
	if pp, _ := pol["policy"].(map[string]any); pp == nil || pp["quick_check"] != true || pp["quick_checks"] != ".ts .vue: npx eslint {file}" {
		t.Fatalf("policy = %v", pol)
	}
	_, ms := do(t, admin, "GET", e.srv.URL+"/api/mcp/servers", nil, nil)
	var c7 map[string]any
	for _, x := range ms["servers"].([]any) {
		if m := x.(map[string]any); m["name"] == "context7" {
			c7 = m
		}
		if x.(map[string]any)["name"] == "github" {
			t.Fatal("an MCP that needs a key must not be added")
		}
	}
	if c7 == nil || c7["kind"] != "stdio" || len(c7["agents"].([]any)) != 2 {
		t.Fatalf("context7 = %v", c7)
	}
	p := body["project"].(map[string]any)
	// the starter agent stays the default; the pick is added, tailored
	if p["description"] != "Shop Nuxt." || p["agent_count"].(float64) != 2 || p["default_agent_id"] != p["agents"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("project = %v", p)
	}
	agent := p["agents"].([]any)[1].(map[string]any)
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
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	srv := fakeClaude(t, "```json\n"+`{"description":"x","reason":"r","confidence":0.5,"agents":[],"notes":[]}`+"\n```")
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

func anyStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		out = append(out, x.(string))
	}
	return out
}
