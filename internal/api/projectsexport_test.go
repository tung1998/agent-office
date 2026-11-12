package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-276: chosen projects go to another office (here the same one, so both
// clash by name); per project the person picks a new copy "<name> 2",
// overwrite, or nothing (a clash with no choice is skipped, never overwritten).
func TestProjectsExportImportWithClashes(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	mk := func(name string) storage.Repo {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := e.st.Repos().Create(ctx, storage.Repo{Name: name, Path: dir})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b, c := mk("Alpha"), mk("Beta"), mk("Gamma")
	const pass = "a long enough passphrase"

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/transfer/projects-export",
		map[string]any{"project_ids": []string{a.ID, b.ID, c.ID}, "include": []string{"chats", "memory"}, "passphrase": pass}, nil)
	name, _ := body["name"].(string)
	if resp.StatusCode != 200 || name == "" {
		t.Fatalf("export = %d %v", resp.StatusCode, body)
	}
	dl, _ := admin.Get(e.srv.URL + "/api/transfer/full-export/" + name)
	archive, _ := io.ReadAll(dl.Body)
	dl.Body.Close()

	// the whole-office import refuses a pack of projects
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/transfer/full-import", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Office-Passphrase", pass)
	if res, _ := admin.Do(req); res.StatusCode == 200 {
		t.Fatal("whole-office import took a pack of projects")
	}

	req, _ = http.NewRequest("POST", e.srv.URL+"/api/transfer/projects-import", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Office-Passphrase", pass)
	res, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var prev struct {
		Token    string `json:"token"`
		Projects []any  `json:"projects"`
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	_ = json.Unmarshal(raw, &prev)
	var full map[string]any
	_ = json.Unmarshal(raw, &full)
	if res.StatusCode != 200 || prev.Token == "" || len(prev.Projects) != 3 {
		t.Fatalf("preview = %d %s", res.StatusCode, raw)
	}
	list := full["projects"].([]any)
	slugs := map[string]string{}
	for _, x := range list {
		m := x.(map[string]any)
		if m["conflict"] == nil {
			t.Errorf("%v: no clash found with the same name here", m["name"])
		}
		slugs[m["name"].(string)] = m["slug"].(string)
		if m["name"] == "Alpha" && m["new_name"] != "Alpha 2" {
			t.Errorf("new name = %v", m["new_name"])
		}
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/transfer/projects-import/apply", map[string]any{
		"token": prev.Token, "decisions": map[string]string{slugs["Alpha"]: "new", slugs["Beta"]: "overwrite"},
	}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("apply = %d %v", resp.StatusCode, body)
	}
	for _, x := range body["results"].([]any) {
		m := x.(map[string]any)
		want := map[string]string{"Alpha": "new", "Beta": "overwrite", "Gamma": "skip"}[m["name"].(string)]
		if m["action"] != want || m["ok"] != true {
			t.Errorf("%v: %v", m["name"], m)
		}
	}
	repos, _ := e.st.Repos().List(ctx)
	var copy *storage.Repo
	for i := range repos {
		if repos[i].Name == "Alpha 2" {
			copy = &repos[i]
		}
		if repos[i].Name == "Gamma 2" || repos[i].Name == "Beta 2" {
			t.Errorf("%s made without being asked", repos[i].Name)
		}
	}
	if copy == nil {
		t.Fatal("Alpha 2 not made")
	}
	if got, _ := os.ReadFile(filepath.Join(copy.Path, "note.md")); string(got) != "Alpha" {
		t.Errorf("Alpha 2's code = %q in %s", got, copy.Path)
	}
	if copy.Path == a.Path {
		t.Error("the copy took the original's folder")
	}
	// the preview is used up
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/transfer/projects-import/apply", map[string]any{"token": prev.Token}, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("second apply = %d", resp.StatusCode)
	}
}
