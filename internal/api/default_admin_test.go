package api_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
)

// addDefaultAdmin puts the first run's admin / admin in (setup already made other accounts).
func addDefaultAdmin(t *testing.T, e *env) {
	t.Helper()
	hash, _ := auth.FastHasherForTests().Hash(auth.DefaultAdminPassword)
	if _, err := e.st.Users().Create(context.Background(), storage.User{Email: auth.DefaultAdminEmail, Name: "Admin", Role: storage.RoleAdmin,
		PasswordHash: hash, MustChange: true}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultAdminSetsUpBeforeAnythingElse(t *testing.T) {
	e := setup(t)
	addDefaultAdmin(t, e)
	c := e.client(t)

	_, status := do(t, c, "GET", e.srv.URL+"/api/auth/status", nil, nil)
	if status["default_admin"] != true {
		t.Fatalf("status = %v", status)
	}
	login(t, e, c, "admin", "admin")
	if _, me := do(t, c, "GET", e.srv.URL+"/api/auth/me", nil, nil); me["user"].(map[string]any)["must_change"] != true {
		t.Fatalf("me = %v", me)
	}
	// nothing but the setup until it is done
	for _, p := range []string{"/api/projects", "/api/users"} {
		if resp, _ := do(t, c, "GET", e.srv.URL+p, nil, nil); resp.StatusCode != 403 {
			t.Fatalf("%s before setup = %d", p, resp.StatusCode)
		}
	}
	if resp, body := do(t, c, "POST", e.srv.URL+"/api/auth/setup", map[string]string{"email": "admin@x.io", "password": "a-long-password-1"}, nil); resp.StatusCode != 409 {
		t.Fatalf("taken email = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, c, "POST", e.srv.URL+"/api/auth/setup", map[string]string{"email": "boss@x.io", "password": "short"}, nil); resp.StatusCode != 400 {
		t.Fatalf("weak password = %d %v", resp.StatusCode, body)
	}
	resp, body := do(t, c, "POST", e.srv.URL+"/api/auth/setup", map[string]string{"email": "boss@x.io", "name": "Boss", "password": "a-long-password-1"}, nil)
	if resp.StatusCode != 200 || body["user"].(map[string]any)["must_change"] != false {
		t.Fatalf("setup = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, c, "GET", e.srv.URL+"/api/projects", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("projects after setup = %d", resp.StatusCode)
	}
	if _, status := do(t, c, "GET", e.srv.URL+"/api/auth/status", nil, nil); status["default_admin"] != false {
		t.Fatalf("status after setup = %v", status)
	}
}
