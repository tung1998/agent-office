package api_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
)

func TestChatAPIWithSSE(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	bin := filepath.Join(t.TempDir(), "claude")
	result := `{"type":"result","subtype":"success","is_error":false,"result":"Đề xuất:\n` + "```" + `diff\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-one\n+ONE\n` + "```" + `","session_id":"s1","total_cost_usd":0.01,"usage":{"input_tokens":3,"output_tokens":4}}`
	fakebin.Write(bin, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'JSON'\n"+
		`{"type":"system","subtype":"init","session_id":"s1"}`+"\n"+
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Đề xuất:"}}}`+"\n"+
		result+"\nJSON\n"))
	do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "CC", "kind": "claude_cli", "base_url": bin}, nil)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/conversations", map[string]any{}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create conversation = %d %v", resp.StatusCode, body)
	}
	cid := body["conversation"].(map[string]any)["id"].(string)
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/conversations/"+cid+"/messages", map[string]any{"text": "đổi one thành ONE"}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("send = %d %v", resp.StatusCode, body)
	}
	turn := body["turn_id"].(string)

	req, _ := http.NewRequest("GET", e.srv.URL+"/api/chat/turns/"+turn+"/stream", nil)
	sresp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	if ct := sresp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	var types []string
	var patchID string
	sc := bufio.NewScanner(sresp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev)
		types = append(types, ev["type"].(string))
		if ev["type"] == "patch" {
			patchID = ev["patch"].(map[string]any)["id"].(string)
		}
		if ev["type"] == "done" || ev["type"] == "error" {
			break
		}
	}
	if got := strings.Join(types, ","); !strings.Contains(got, "text") || !strings.Contains(got, "patch") || !strings.HasSuffix(got, "done") {
		t.Fatalf("stream events = %s", got)
	}

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/patches/"+patchID+"/approve", map[string]any{}, nil); resp.StatusCode != 403 {
		t.Fatalf("member approve = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/patches/"+patchID+"/approve", map[string]any{}, nil)
	if resp.StatusCode != 200 || body["patch"].(map[string]any)["status"] != "applied" {
		t.Fatalf("approve = %d %v", resp.StatusCode, body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "ONE\n" {
		t.Fatalf("file = %q", b)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/conversations/"+cid, nil, nil)
	msgs := body["messages"].([]any)
	if len(msgs) != 2 || body["conversation"].(map[string]any)["title"] != "đổi one thành ONE" {
		t.Fatalf("conversation = %v", body)
	}
	p := msgs[1].(map[string]any)["patches"].([]any)[0].(map[string]any)
	if p["status"] != "applied" || p["decided_by"] != "human:admin@x.io" {
		t.Fatalf("patch in history = %v", p)
	}

	// a member deletes only the chats they started
	if resp, _ := do(t, member, "DELETE", e.srv.URL+"/api/conversations/"+cid, nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member deletes admin's chat = %d", resp.StatusCode)
	}
	_, body = do(t, member, "POST", e.srv.URL+"/api/projects/"+pid+"/conversations", map[string]any{}, nil)
	own := body["conversation"].(map[string]any)["id"].(string)
	if resp, _ := do(t, member, "DELETE", e.srv.URL+"/api/conversations/"+own, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("member deletes own chat = %d", resp.StatusCode)
	}
}

// "New chat" clicked again while the last one is still empty brings that one back.
func TestNewChatReusesEmpty(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir()}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	create := func() string {
		resp, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/conversations", map[string]any{}, nil)
		if resp.StatusCode != 201 {
			t.Fatalf("create conversation = %d %v", resp.StatusCode, b)
		}
		return b["conversation"].(map[string]any)["id"].(string)
	}
	first := create()
	if again := create(); again != first {
		t.Fatalf("empty chat not reused: %s then %s", first, again)
	}
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/conversations/"+first+"/messages", map[string]any{"text": "hi"}, nil); resp.StatusCode != 202 {
		t.Fatalf("send = %d %v", resp.StatusCode, b)
	}
	if next := create(); next == first {
		t.Fatal("a chat with a message was reused")
	}
}
