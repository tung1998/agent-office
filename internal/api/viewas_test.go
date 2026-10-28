package api_test

import "testing"

func TestAdminViewsAsMember(t *testing.T) {
	e := setup(t)
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	_, body := do(t, member, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	memberID := body["user"].(map[string]any)["id"].(string)
	// a member may not view as anyone
	resp, _ := do(t, member, "POST", e.srv.URL+"/api/users/"+memberID+"/view-as", nil, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("member view-as = %d", resp.StatusCode)
	}

	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body = do(t, admin, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	adminID := body["user"].(map[string]any)["id"].(string)
	resp, _ = do(t, admin, "POST", e.srv.URL+"/api/users/"+adminID+"/view-as", nil, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("view as self = %d", resp.StatusCode)
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/users/"+memberID+"/view-as", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("view-as = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 200 || body["user"].(map[string]any)["email"] != "member@x.io" || body["viewed_by"].(map[string]any)["email"] != "admin@x.io" {
		t.Fatalf("me while viewing = %d %v", resp.StatusCode, body)
	}
	// it only reads
	resp, _ = do(t, admin, "PUT", e.srv.URL+"/api/me/prefs/projects", map[string]any{"value": map[string]any{}}, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("write while viewing = %d", resp.StatusCode)
	}

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/auth/view-as/end", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("end = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 200 || body["user"].(map[string]any)["email"] != "admin@x.io" || body["viewed_by"] != nil {
		t.Fatalf("me after end = %d %v", resp.StatusCode, body)
	}
	// a normal login has no view to end
	resp, _ = do(t, member, "POST", e.srv.URL+"/api/auth/view-as/end", nil, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("member end = %d", resp.StatusCode)
	}
	resp, _ = do(t, member, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("member still logged in = %d", resp.StatusCode)
	}
}
