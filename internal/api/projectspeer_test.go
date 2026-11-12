package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-278: projects go through a paired office. Sync applies at once, only
// when the receiving office gave the sender Admin: a clash updates the
// project there, the rest are made new. An offer is fetched once and
// previewed there.
func TestProjectsThroughPairedOffice(t *testing.T) {
	a, b := setup(t), setup(t)
	ca, cb := a.client(t), b.client(t)
	login(t, a, ca, "admin@x.io", "admin-password")
	login(t, b, cb, "admin@x.io", "admin-password")
	ctx := context.Background()

	_, out := do(t, cb, "POST", b.srv.URL+"/api/peers/invite", map[string]any{"name": "office-b", "url": b.srv.URL}, nil)
	code, _ := out["code"].(string)
	resp, out := do(t, ca, "POST", a.srv.URL+"/api/peers/join", map[string]any{"code": code, "name": "office-a", "url": a.srv.URL}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("join = %d %v", resp.StatusCode, out)
	}
	toB, _ := out["id"].(string)
	_, list := do(t, cb, "GET", b.srv.URL+"/api/peers", nil, nil)
	toA, _ := list["peers"].([]any)[0].(map[string]any)["id"].(string)

	mk := func(e *env, name, content string) storage.Repo {
		dir := filepath.Join(t.TempDir(), strings.ToLower(name))
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o644)
		r, err := e.st.Repos().Create(ctx, storage.Repo{Name: name, Path: dir})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	shopA, docsA := mk(a, "Shop", "from A"), mk(a, "Docs", "docs from A")
	shopB := mk(b, "Shop", "B's own")

	sync := map[string]any{"project_ids": []string{shopA.ID, docsA.ID}}
	if resp, out := do(t, ca, "POST", a.srv.URL+"/api/peers/"+toB+"/projects-sync", sync, nil); resp.StatusCode != http.StatusBadGateway || !strings.Contains(toJSON(out), "Admin") {
		t.Fatalf("sync without Admin there = %d %v", resp.StatusCode, out)
	}
	if resp, _ := do(t, cb, "PUT", b.srv.URL+"/api/peers/"+toA+"/admin", map[string]any{"admin": true}, nil); resp.StatusCode/100 != 2 {
		t.Fatalf("grant admin = %d", resp.StatusCode)
	}
	resp, out = do(t, ca, "POST", a.srv.URL+"/api/peers/"+toB+"/projects-sync", sync, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("sync = %d %v", resp.StatusCode, out)
	}
	for _, x := range out["results"].([]any) {
		m := x.(map[string]any)
		want := map[string]string{"Shop": "overwrite", "Docs": "new"}[m["name"].(string)]
		if m["action"] != want || m["ok"] != true {
			t.Errorf("%v: %v", m["name"], m)
		}
	}
	repos, _ := b.st.Repos().List(ctx)
	names := map[string]storage.Repo{}
	for _, r := range repos {
		names[r.Name] = r
	}
	if _, ok := names["Docs"]; !ok || len(repos) != 2 {
		t.Fatalf("B's projects after sync: %v", names)
	}
	if got, _ := os.ReadFile(filepath.Join(shopB.Path, "a.txt")); string(got) != "B's own" {
		t.Errorf("sync overwrote B's code: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(names["Docs"].Path, "a.txt")); string(got) != "docs from A" {
		t.Errorf("Docs' code on B = %q", got)
	}

	// offer → fetch once → preview there
	const pass = "a long enough passphrase"
	if resp, out := do(t, ca, "POST", a.srv.URL+"/api/peers/"+toB+"/projects-offer", map[string]any{"project_ids": []string{docsA.ID}, "passphrase": pass}, nil); resp.StatusCode != 200 {
		t.Fatalf("offer = %d %v", resp.StatusCode, out)
	}
	resp, out = do(t, cb, "POST", b.srv.URL+"/api/peers/"+toA+"/projects-fetch", map[string]any{"passphrase": pass}, nil)
	if resp.StatusCode != 200 || out["token"] == nil || !strings.Contains(toJSON(out["projects"]), `"conflict"`) {
		t.Fatalf("fetch = %d %v", resp.StatusCode, out)
	}
	if resp, _ := do(t, cb, "POST", b.srv.URL+"/api/peers/"+toA+"/projects-fetch", map[string]any{"passphrase": pass}, nil); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("second fetch = %d (an offer is fetched once)", resp.StatusCode)
	}
}
