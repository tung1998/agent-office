package api_test

import (
	"strings"
	"testing"
)

// An agent's avatar: a color and an icon, or a small uploaded image.
func TestAgentAvatar(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	tpl := ""
	for _, x := range body["templates"].([]any) {
		if m := x.(map[string]any); m["key"] == "solo" {
			tpl = m["id"].(string)
		}
	}
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop", "template_id": tpl}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat/agents", nil, nil)
	agents, _ := body["agents"].([]any)
	if len(agents) == 0 {
		t.Fatalf("project has no agents: %v", body)
	}
	a := agents[0].(map[string]any)
	id := a["id"].(string)
	put := func(avatar map[string]any) int {
		in := map[string]any{"avatar": avatar}
		for _, k := range []string{"key", "name", "tier", "role", "description", "reports_to", "provider_id", "model_tier", "llm_model", "instructions", "permissions"} {
			in[k] = a[k]
		}
		resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id, in, nil)
		if resp.StatusCode != 200 {
			t.Logf("PATCH = %d %v", resp.StatusCode, b)
		}
		return resp.StatusCode
	}
	if code := put(map[string]any{"color": "violet", "icon": "i-lucide-rocket"}); code != 200 {
		t.Fatalf("color+icon = %d", code)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat/agents", nil, nil)
	got := body["agents"].([]any)[0].(map[string]any)["avatar"].(map[string]any)
	if got["color"] != "violet" || got["icon"] != "i-lucide-rocket" {
		t.Fatalf("avatar = %v", got)
	}
	png := "data:image/png;base64,iVBORw0KGgo="
	if code := put(map[string]any{"image": png}); code != 200 {
		t.Fatalf("image = %d", code)
	}
	for name, bad := range map[string]map[string]any{
		"icon":    {"icon": "javascript:alert(1)"},
		"color":   {"color": "url(x)"},
		"not img": {"image": "data:text/html;base64,PGgxPg=="},
		"too big": {"image": "data:image/png;base64," + strings.Repeat("A", 300<<10)},
		"remote":  {"image": "https://evil.example/x.png"},
	} {
		if code := put(bad); code != 400 {
			t.Errorf("%s accepted (%d)", name, code)
		}
	}
}
