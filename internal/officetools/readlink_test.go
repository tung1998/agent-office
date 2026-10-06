package officetools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// read_link: an agent reads a chat, a message or a task the person linked,
// only of its own project.
func TestReadLink(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "other"})
	c, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "Graylog"})
	st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: "kiểm tra graylog"})
	m2, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "có 3 lỗi 500", Author: "Lead"})
	task, _ := st.Tasks().Create(ctx, storage.Task{ProjectID: p.ID, Title: "Sửa lỗi 500", Goal: "sửa lỗi 500", Status: "done", Result: "đã sửa ở api.go"})
	oc, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: other.ID})
	box := New(st, nil, nil)
	call := func(url string) (string, bool) {
		raw, _ := json.Marshal(map[string]string{"url": url})
		return box.Call(ctx, Scope{ProjectID: p.ID}, "read_link", raw)
	}
	base := "http://localhost:2704/projects/" + p.ID
	if out, isErr := call(base + "?tab=chat&c=" + c.ID); isErr || !strings.Contains(out, "kiểm tra graylog") || !strings.Contains(out, "[Lead]") {
		t.Fatalf("chat = %v %s", isErr, out)
	}
	if out, isErr := call(base + "?tab=chat&c=" + c.ID + "&m=" + m2.ID); isErr || !strings.Contains(out, "có 3 lỗi 500") || !strings.Contains(out, "tin được dẫn") {
		t.Fatalf("message = %v %s", isErr, out)
	}
	if out, isErr := call(base + "?tab=tasks&task=" + task.ID); isErr || !strings.Contains(out, "đã sửa ở api.go") {
		t.Fatalf("task = %v %s", isErr, out)
	}
	if _, isErr := call("http://localhost:2704/projects/" + other.ID + "?tab=chat&c=" + oc.ID); !isErr {
		t.Fatal("another project's chat was readable")
	}
	if _, isErr := call("https://evil.example/x"); !isErr {
		t.Fatal("a foreign link was read")
	}
}
