package chat

import (
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// What every chat of a project shares opens the system prompt; the chat's
// own folder comes after it, so a prompt cache keeps the shared start.
func TestSystemPromptSharedPartFirst(t *testing.T) {
	project := storage.Repo{Name: "p", Path: "/p", Description: "d"}
	agent := storage.Agent{Name: "a", Role: "r", Instructions: "do x"}
	acc := perm.Access{Level: perm.Operate}
	sys := func(dir string, power string) string {
		return systemPrompt(project, agent, false, nil, acc, place{dir: dir, write: true, tree: "chat-" + dir, mode: perm.EditWorktree}, Language{System: "vi"}, power)
	}
	a, b := sys("/w/one", ""), sys("/w/two", "")
	i := strings.Index(a, "Working directory: /w/one")
	if i < 0 || !strings.Contains(a, "do x") || i < strings.Index(a, "Rules:") || i < strings.Index(a, "Your permission this turn") {
		t.Fatalf("the chat's folder comes after the shared part:\n%s", a)
	}
	if a[:i] != b[:i] {
		t.Fatal("two chats of the project share all before the folder")
	}
	if s := sys("/w/one", "answer"); !strings.Contains(s, "Working directory: /w/one") {
		t.Fatalf("the assistant's prompt keeps the folder:\n%s", s)
	}
}
