package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestSkillifyPromptRecordsStepsAndAsks(t *testing.T) {
	msgs := []storage.Message{
		{Role: "user", Content: "Deploy bản mới lên staging\nchi tiết thêm"},
		{Role: "assistant", Tools: []storage.ToolCall{
			{Name: "Bash", Summary: "go build ./..."},
			{Name: "Bash", Summary: "rm -rf /oops", Error: true},
			{Name: "Bash", Summary: "go build ./..."}, // repeated: once
		}},
		{Role: "user", Content: "/skillify deploy-staging"},
	}
	p := skillifyPrompt(msgs, "deploy-staging")
	for _, want := range []string{"- Bash: go build ./...", "- Deploy bản mới lên staging", "```skill", "note for the skill: deploy-staging"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "rm -rf") {
		t.Error("a failed step is in the skill")
	}
	if strings.Count(p, "go build") != 1 {
		t.Error("a repeated step is listed twice")
	}
	if strings.Contains(p, "- /skillify") {
		t.Error("the /skillify itself is listed as an ask")
	}
}

func TestSkillifyPromptKeepsLatestSteps(t *testing.T) {
	var tools []storage.ToolCall
	for i := 0; i < skillifyMaxSteps+5; i++ {
		tools = append(tools, storage.ToolCall{Name: "Read", Summary: strings.Repeat("x", i+1)})
	}
	p := skillifyPrompt([]storage.Message{{Role: "assistant", Tools: tools}}, "")
	if strings.Contains(p, "- Read: x\n") || !strings.Contains(p, strings.Repeat("x", skillifyMaxSteps+5)) {
		t.Error("not the latest steps")
	}
	if !strings.Contains(p, "added no note") {
		t.Error("no note line")
	}
}

func TestSkillifyOnlyInPersonChatsWithoutSuchSkill(t *testing.T) {
	e := &Engine{}
	dir := t.TempDir()
	if note, ok := e.skillify(context.Background(), storage.Conversation{}, dir, "/skillify  tra đơn "); !ok || note != "tra đơn" {
		t.Fatalf("got %q %v", note, ok)
	}
	if _, ok := e.skillify(context.Background(), storage.Conversation{Purpose: "channel"}, dir, "/skillify"); ok {
		t.Error("a bot's chat runs /skillify")
	}
	if _, ok := e.skillify(context.Background(), storage.Conversation{}, dir, "skillify this"); ok {
		t.Error("plain text taken as /skillify")
	}
	// the project's own skill of that name wins
	sk := filepath.Join(dir, ".claude", "skills", SkillifyCommand)
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("---\nname: skillify\ndescription: own\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.skillify(context.Background(), storage.Conversation{}, dir, "/skillify"); ok {
		t.Error("office's /skillify hides the project's skill")
	}
}
