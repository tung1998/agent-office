package api_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// failSettingsGet wraps a Store so Settings().Get fails (a transient error,
// not "not found") for one key while armed, simulating a DB hiccup while
// reading a member's stored office grants (officesKey).
type failSettingsGet struct {
	storage.Store
	key   *string // the one settings key to fail on, set once known
	armed *atomic.Bool
}

type failSettingsGetRepo struct {
	storage.SettingRepo
	key   *string
	armed *atomic.Bool
}

func (s failSettingsGet) Settings() storage.SettingRepo {
	return failSettingsGetRepo{s.Store.Settings(), s.key, s.armed}
}

func (r failSettingsGetRepo) Get(ctx context.Context, key string, dst any) (bool, error) {
	if key == *r.key && r.armed.Load() {
		return false, errors.New("transient db error")
	}
	return r.SettingRepo.Get(ctx, key, dst)
}

// Bug: officesOf used to discard any error from Settings().Get (not just
// "not found"), leaving the office-access accumulator at its zero value.
// mergeOffices then treated that empty value as "the member has nothing
// else to keep", so a manager's update silently wiped the member's existing
// office grants from other managers/admins instead of failing closed.
func TestUpdateUserFailsClosedWhenOfficeGrantsUnreadable(t *testing.T) {
	var armed atomic.Bool
	failKey := new(string)
	e := setupWithStore(t, nil, func(st storage.Store) storage.Store {
		return failSettingsGet{Store: st, key: failKey, armed: &armed}
	})

	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/users", map[string]any{"email": "mem@x.io", "role": "member", "password": "member-password"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create member = %d %v", resp.StatusCode, body)
	}
	memberID := body["user"].(map[string]any)["id"].(string)

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/users", map[string]any{"email": "pm@x.io", "role": "manager", "password": "manager-password"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create manager = %d %v", resp.StatusCode, body)
	}
	pmID := body["user"].(map[string]any)["id"].(string)
	pm := e.client(t)
	login(t, e, pm, "pm@x.io", "manager-password")
	_ = pmID

	// grant the member a whole-office office access directly (bypassing the
	// admin-only /api/peers pairing flow, which this test doesn't need):
	// an admin or another manager gave the member reach into "peer-x" that
	// this manager cannot itself reach.
	const peerID = "peer-x"
	*failKey = "user_offices/" + memberID
	if err := e.st.Settings().Set(context.Background(), *failKey, map[string][]string{peerID: {"*"}}); err != nil {
		t.Fatalf("seed office grant: %v", err)
	}

	armed.Store(true)

	resp, body = do(t, pm, "PATCH", e.srv.URL+"/api/users/"+memberID, map[string]any{"offices": map[string]any{}}, nil)
	if resp.StatusCode == 200 {
		t.Fatalf("update succeeded despite unreadable existing office grants: %v", body)
	}

	armed.Store(false)

	resp, body = do(t, admin, "GET", e.srv.URL+"/api/users", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list users = %d %v", resp.StatusCode, body)
	}
	for _, u := range body["users"].([]any) {
		m := u.(map[string]any)
		if m["id"] == memberID {
			offices, _ := m["offices"].(map[string]any)
			list, _ := offices[peerID].([]any)
			if len(list) == 0 || !strings.Contains(list[0].(string), "*") {
				t.Fatalf("member's office grant was wiped: %v", m["offices"])
			}
			return
		}
	}
	t.Fatal("member not found")
}
