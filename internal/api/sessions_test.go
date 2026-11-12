package api_test

import (
	"net/http"
	"testing"
)

// ADR-272: an account sees where it is signed in and signs a device out; the
// device's next request is 401, and another account's session is out of reach.
func TestSessionsListAndRevoke(t *testing.T) {
	t.Parallel()
	e := setup(t)
	laptop, phone, member := e.client(t), e.client(t), e.client(t)
	login(t, e, laptop, "admin@x.io", "admin-password")
	login(t, e, phone, "admin@x.io", "admin-password")
	login(t, e, member, "member@x.io", "member-password")

	resp, body := do(t, laptop, "GET", e.srv.URL+"/api/me/sessions", nil, nil)
	list, _ := body["sessions"].([]any)
	if resp.StatusCode != 200 || len(list) != 2 {
		t.Fatalf("list = %d %v", resp.StatusCode, body)
	}
	var phoneID string
	currents := 0
	for _, x := range list {
		m := x.(map[string]any)
		if m["current"] == true {
			currents++
		} else {
			phoneID, _ = m["id"].(string)
		}
		if _, ok := m["token_hash"]; ok {
			t.Fatal("token hash sent to the dashboard")
		}
	}
	if currents != 1 || phoneID == "" {
		t.Fatalf("current marks = %d, other = %q", currents, phoneID)
	}

	// another account cannot revoke it (nor tell it exists)
	if resp, _ := do(t, member, "DELETE", e.srv.URL+"/api/me/sessions/"+phoneID, nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("member revoking admin's session = %d", resp.StatusCode)
	}
	if resp, _ := do(t, phone, "GET", e.srv.URL+"/api/auth/me", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("phone signed out by another account: %d", resp.StatusCode)
	}

	if resp, _ := do(t, laptop, "DELETE", e.srv.URL+"/api/me/sessions/"+phoneID, nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d", resp.StatusCode)
	}
	if resp, _ := do(t, phone, "GET", e.srv.URL+"/api/auth/me", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked phone still signed in: %d", resp.StatusCode)
	}
	if resp, _ := do(t, laptop, "GET", e.srv.URL+"/api/auth/me", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("laptop signed out too: %d", resp.StatusCode)
	}
}
