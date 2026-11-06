package api_test

import "testing"

// TestPutPolicyConflict: two saves started from the same read version must
// not both land. The first PUT with a given version succeeds; a second PUT
// that still claims the same (now stale) version must be rejected with 409,
// and the stored policy must keep the first save, not the second one
// silently overwriting it (the bug: putPolicy's version check was only
// advisory, and the storage write below it was an unconditional overwrite).
func TestPutPolicyConflict(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	_, got := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/policy", nil, nil)
	version := got["version"].(string)

	first := map[string]any{"version": version, "packs": []map[string]any{{"label": "A", "commands": []string{"go test *"}}}}
	resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/policy", first, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("first save with a fresh version must succeed: %d", resp.StatusCode)
	}

	second := map[string]any{"version": version, "packs": []map[string]any{{"label": "B", "commands": []string{"go vet *"}}}}
	resp, b := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/policy", second, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("second save with the now-stale version must be rejected: %d %v", resp.StatusCode, b)
	}

	_, final := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/policy", nil, nil)
	packs := final["policy"].(map[string]any)["packs"].([]any)
	if len(packs) != 1 || packs[0].(map[string]any)["label"] != "A" {
		t.Fatalf("the rejected second save must not have landed: %v", packs)
	}
}
