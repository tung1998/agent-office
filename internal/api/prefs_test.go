package api_test

import "testing"

// A dashboard setting is kept per account: another account never sees it,
// and only known settings are accepted.
func TestPrefsPerAccount(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	url := e.srv.URL + "/api/me/prefs/watch"

	if _, got := do(t, admin, "GET", url, nil, nil); got["value"] != nil {
		t.Fatalf("unset = %v", got)
	}
	if resp, _ := do(t, admin, "PUT", url, map[string]any{"value": map[string]any{"active": "b1"}}, nil); resp.StatusCode != 204 {
		t.Fatalf("put = %d", resp.StatusCode)
	}
	if _, got := do(t, admin, "GET", url, nil, nil); got["value"].(map[string]any)["active"] != "b1" {
		t.Fatalf("admin's = %v", got)
	}
	if _, got := do(t, member, "GET", url, nil, nil); got["value"] != nil {
		t.Fatalf("member sees admin's: %v", got)
	}
	// the sidebar's projects: order and hidden ones
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/me/prefs/projects", map[string]any{"value": map[string]any{"order": []string{"a", "b"}, "hidden": []string{"b"}}}, nil); resp.StatusCode != 204 {
		t.Fatalf("put projects = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/me/prefs/other", map[string]any{"value": 1}, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown key = %d", resp.StatusCode)
	}
}
