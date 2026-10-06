package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Tags on chats: set (cleaned, "Bug" = "bug"), offered back by the project,
// and the list keeps the chats that have every tag asked for.
func TestChatTags(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	bin := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "CC", "kind": "claude_cli", "base_url": bin}, nil)
	_, tpls := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	var soloID string
	for _, x := range tpls["templates"].([]any) {
		if x.(map[string]any)["key"] == "solo" {
			soloID = x.(map[string]any)["id"].(string)
		}
	}
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "template_id": soloID}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	newChat := func() string {
		_, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/conversations", map[string]any{}, nil)
		return b["conversation"].(map[string]any)["id"].(string)
	}
	a, b, c := newChat(), newChat(), newChat()
	setTags := func(id string, tags ...string) (int, map[string]any) {
		resp, out := do(t, admin, "PUT", e.srv.URL+"/api/conversations/"+id+"/tags", map[string]any{"tags": tags}, nil)
		return resp.StatusCode, out
	}
	code, out := setTags(a, " Bug ", "bug", "ui  fix")
	got := fmt.Sprint(out["conversation"].(map[string]any)["tags"])
	if code != 200 || got != "[Bug ui fix]" {
		t.Fatalf("set a = %d %s", code, got)
	}
	setTags(b, "bug")
	setTags(c, "ui fix")

	ids := func(q string) map[string]bool {
		_, l := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations?source=all"+q, nil, nil)
		m := map[string]bool{}
		for _, x := range l["conversations"].([]any) {
			m[x.(map[string]any)["id"].(string)] = true
		}
		return m
	}
	if m := ids("&tag=BUG"); !m[a] || !m[b] || m[c] {
		t.Fatalf("tag=BUG = %v", m)
	}
	if m := ids("&tag=bug&tag=ui+fix"); !m[a] || m[b] || m[c] || len(m) != 1 {
		t.Fatalf("both tags = %v", m)
	}
	if m := ids(""); len(m) != 3 {
		t.Fatalf("no filter = %v", m)
	}

	_, tg := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat-tags", nil, nil)
	counts := map[string]float64{}
	for _, x := range tg["tags"].([]any) {
		counts[x.(map[string]any)["tag"].(string)] = x.(map[string]any)["count"].(float64)
	}
	if len(counts) != 2 || counts["ui fix"] != 2 || (counts["Bug"]+counts["bug"]) != 2 {
		t.Fatalf("project tags = %v", counts)
	}

	// removing one keeps the other; too long is refused
	setTags(a, "ui fix")
	if m := ids("&tag=bug"); m[a] || !m[b] {
		t.Fatalf("after removing bug from a = %v", m)
	}
	if code, _ := setTags(a, "x234567890123456789012345678901"); code != 400 {
		t.Fatalf("31-char tag = %d", code)
	}

	// a member tags a chat too
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "PUT", e.srv.URL+"/api/conversations/"+c+"/tags", map[string]any{"tags": []string{"later"}}, nil); resp.StatusCode != 200 {
		t.Fatalf("member tags = %d", resp.StatusCode)
	}
}
