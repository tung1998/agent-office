package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-047: a personal token lets the person's own Claude Code CLI use the
// office tools in office scope; proposals wait in the approval inbox.
func TestPersonalTokenForCLI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	if _, err := assistant.Ensure(ctx, e.st, orgmodel.NewService(e.st), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/me/tokens", map[string]any{"name": "laptop"}, nil)
	tok, _ := body["token"].(string)
	if resp.StatusCode != 201 || !strings.HasPrefix(tok, "ofc_") {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/me/tokens", nil, nil)
	list := body["tokens"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "laptop" || strings.Contains(mustJSON(list), tok) {
		t.Fatalf("list = %v", list)
	}
	id := list[0].(map[string]any)["id"].(string)

	mcp := func(token, method string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	code, out := mcp(tok, "tools/list")
	if code != 200 || !strings.Contains(mustJSON(out), `"projects"`) {
		t.Fatalf("tools/list = %d %v", code, out)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects","arguments":{}}}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	var called map[string]any
	json.NewDecoder(res.Body).Decode(&called)
	res.Body.Close()
	if called["error"] != nil {
		t.Fatalf("tools/call projects = %v", called)
	}
	if code, _ := mcp("ofc_wrong", "tools/list"); code != 401 {
		t.Fatalf("a wrong token = %d", code)
	}
	do(t, admin, "DELETE", e.srv.URL+"/api/me/tokens/"+id, nil, nil)
	if code, _ := mcp(tok, "tools/list"); code != 401 {
		t.Fatalf("a revoked token = %d", code)
	}

	// proposals without a chat (the CLI) wait in the inbox
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, Kind: "run_command", Target: "go test ./...", ProposedBy: "Claude Code CLI (admin@x.io)"})
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/actions/pending", nil, nil)
	if resp.StatusCode != 200 || len(body["actions"].([]any)) != 1 {
		t.Fatalf("pending = %d %v", resp.StatusCode, body)
	}
}

// A token past its expiry is refused on /mcp and /mcp/s/*; one made before
// expiry existed (expires_at NULL) still works; new ones last 90 days.
func TestPersonalTokenExpiry(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	if _, err := assistant.Ensure(ctx, e.st, orgmodel.NewService(e.st), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	u, err := e.st.Users().GetByEmail(ctx, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	put := func(tok string, exp *time.Time) {
		sum := sha256.Sum256([]byte(tok))
		if _, err := e.st.Tokens().Create(ctx, storage.UserToken{UserID: u.ID, Name: tok, TokenHash: hex.EncodeToString(sum[:]), ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
	}
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	put("ofc_old", nil)
	put("ofc_expired", &past)
	put("ofc_fresh", &future)
	call := func(path, tok string) int {
		req, _ := http.NewRequest("POST", e.srv.URL+path, bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for tok, want := range map[string]int{"ofc_old": 200, "ofc_fresh": 200, "ofc_expired": 401} {
		if code := call("/mcp", tok); code != want {
			t.Errorf("/mcp %s = %d, want %d", tok, code, want)
		}
	}
	if code := call("/mcp/s/any", "ofc_expired"); code != 401 {
		t.Errorf("/mcp/s expired = %d", code)
	}

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/me/tokens", map[string]any{"name": "laptop"}, nil)
	exp, _ := time.Parse(time.RFC3339, fmt.Sprint(body["expires_at"]))
	if resp.StatusCode != 201 || exp.Sub(time.Now()) < 89*24*time.Hour || exp.Sub(time.Now()) > 91*24*time.Hour {
		t.Fatalf("default expiry = %d %v", resp.StatusCode, body)
	}
	if resp, body = do(t, admin, "POST", e.srv.URL+"/api/me/tokens", map[string]any{"days": 0}, nil); resp.StatusCode != 201 || body["expires_at"] != nil {
		t.Fatalf("no expiry = %d %v", resp.StatusCode, body)
	}
	if resp, _ = do(t, admin, "POST", e.srv.URL+"/api/me/tokens", map[string]any{"days": 7}, nil); resp.StatusCode != 400 {
		t.Fatalf("days 7 = %d", resp.StatusCode)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/me/tokens", nil, nil)
	for _, x := range body["tokens"].([]any) {
		m := x.(map[string]any)
		if (m["name"] == "ofc_expired") != (m["expired"] == true) {
			t.Errorf("list expired flag: %v", m)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
