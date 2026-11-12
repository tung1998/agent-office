package api_test

import (
	"testing"
)

// putPolicy saves a custom pack command that deletes or publishes: commands
// follow the permissions the person sets, risky or not.
func TestPutPolicyRiskyCommands(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	for _, pattern := range []string{"git push *", "rm *", "sudo *"} {
		policy := map[string]any{"packs": []map[string]any{{"label": "Custom", "commands": []string{pattern}}}}
		resp, b := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/policy", policy, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("pattern %q must save: %d %v", pattern, resp.StatusCode, b)
		}
	}

	// a plain pattern saves too
	policy := map[string]any{"packs": []map[string]any{{"label": "Custom", "commands": []string{"go test *"}}}}
	resp, b := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/policy", policy, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("a safe pattern must save: %d %v", resp.StatusCode, b)
	}
}
