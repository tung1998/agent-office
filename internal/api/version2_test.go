package api_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// Bot settings, a model, a skill and a note: a save from what is no longer
// there answers 409, nothing overwritten.
func TestMoreSaveConflicts(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	solo := ""
	for _, x := range body["templates"].([]any) {
		if m := x.(map[string]any); m["key"] == "solo" {
			solo = m["id"].(string)
		}
	}
	dir := t.TempDir()
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "shop", "template_id": solo}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	dir = body["project"].(map[string]any)["path"].(string) // as office keeps it

	// bot
	ch, _ := e.st.Channels().Create(ctx, storage.Channel{ProjectID: pid, Kind: "discord", Name: "Dev", TokenEnc: "x"})
	_, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/channels", nil, nil)
	cv := b["channels"].([]any)[0].(map[string]any)["version"].(string)
	if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/channels/"+ch.ID, map[string]any{"refusal": "một", "version": cv}, nil); resp.StatusCode != 200 {
		t.Fatalf("bot save = %d %v", resp.StatusCode, b)
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/channels/"+ch.ID, map[string]any{"refusal": "hai", "version": cv}, nil); resp.StatusCode != 409 {
		t.Fatalf("bot stale save = %d", resp.StatusCode)
	}

	// model (the project's)
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid, nil, nil)
	mid := b["project"].(map[string]any)["model"].(map[string]any)["id"].(string)
	_, b = do(t, admin, "GET", e.srv.URL+"/api/org-models/"+mid, nil, nil)
	mv := b["model"].(map[string]any)["version"].(string)
	if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/org-models/"+mid, map[string]any{"description": "một", "version": mv}, nil); resp.StatusCode != 200 {
		t.Fatalf("model save = %d %v", resp.StatusCode, b)
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/org-models/"+mid, map[string]any{"description": "hai", "version": mv}, nil); resp.StatusCode != 409 {
		t.Fatalf("model stale save = %d", resp.StatusCode)
	}

	// skill
	os.MkdirAll(filepath.Join(dir, ".claude", "skills", "tra-don"), 0o755)
	os.WriteFile(filepath.Join(dir, ".claude", "skills", "tra-don", "SKILL.md"), []byte("---\nname: tra-don\ndescription: Tra đơn\n---\nmột\n"), 0o644)
	_, inv := do(t, admin, "GET", e.srv.URL+"/api/automation/scan", nil, nil)
	var ref map[string]any // as the editor refers to it (refOf)
	for _, x := range inv["items"].([]any) {
		it := x.(map[string]any)
		loc := it["location"].(map[string]any)
		if it["kind"] == "skill" && it["name"] == "tra-don" && loc["type"] == "project" {
			ref = map[string]any{"kind": "skill", "name": "tra-don", "type": "project", "path": loc["path"], "project_path": loc["project_path"]}
		}
	}
	if ref == nil {
		t.Fatalf("scan = %v", inv)
	}
	_, b = do(t, admin, "POST", e.srv.URL+"/api/automation/content", ref, nil)
	sv, _ := b["version"].(string)
	if sv == "" {
		t.Fatalf("content = %v", b)
	}
	install := func(body string) int {
		resp, _ := do(t, admin, "POST", e.srv.URL+"/api/automation/install", map[string]any{"kind": "skill", "name": "tra-don", "overwrite": true, "accept": true,
			"target": map[string]any{"scope": "project", "project_path": dir}, "edited": ref, "version": sv,
			"files": map[string]string{"SKILL.md": "---\nname: tra-don\ndescription: Tra đơn\n---\n" + body + "\n"}}, nil)
		return resp.StatusCode
	}
	if code := install("hai"); code != 200 {
		t.Fatalf("skill save = %d", code)
	}
	if code := install("ba"); code != 409 {
		t.Fatalf("skill stale save = %d", code)
	}

	// note
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat/agents", nil, nil)
	aid := b["agents"].([]any)[0].(map[string]any)["id"].(string)
	base := e.srv.URL + "/api/projects/" + pid + "/agents/" + aid + "/memories"
	do(t, admin, "POST", base, map[string]any{"text": "Repo dùng pnpm"}, nil)
	_, b = do(t, admin, "GET", base, nil, nil)
	n := b["items"].([]any)[0].(map[string]any)
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/memories/"+n["id"].(string), map[string]any{"text": "pnpm 9", "version": n["version"]}, nil); resp.StatusCode != 200 {
		t.Fatalf("note save = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/memories/"+n["id"].(string), map[string]any{"text": "pnpm 10", "version": n["version"]}, nil); resp.StatusCode != 409 {
		t.Fatalf("note stale save = %d", resp.StatusCode)
	}
}
