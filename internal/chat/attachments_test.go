package chat

import (
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/attach"
)

func TestAttachmentsPerRuntime(t *testing.T) {
	s := attach.Store{Dir: t.TempDir()}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02")
	img, _ := s.Save("p", "u", "shot.png", png)
	pdf, _ := s.Save("p", "u", "spec.pdf", []byte("%PDF-1.4\n"))
	doc, _ := s.Save("p", "u", "log.txt", []byte("ERROR x"))
	files, err := s.Resolve("p", []string{img.ID, pdf.ID, doc.ID})
	if err != nil {
		t.Fatal(err)
	}

	p, dirs := claudePrompt("Q", files)
	if len(dirs) != 2 || !strings.Contains(p, "ERROR x") || !strings.Contains(p, files[0].Path) {
		t.Fatalf("claude: %s %v", p, dirs)
	}
	c, err := anthropicContent("Q", files)
	blocks, _ := c.([]any)
	if err != nil || len(blocks) != 3 || blocks[0].(map[string]any)["type"] != "image" || blocks[1].(map[string]any)["type"] != "document" {
		t.Fatalf("anthropic: %v %v", c, err)
	}
	o, _ := openAIContent("Q", files)
	parts, _ := o.([]any)
	if len(parts) != 3 || parts[1].(map[string]any)["type"] != "image_url" || parts[2].(map[string]any)["type"] != "file" {
		t.Fatalf("openai: %v", o)
	}
	cp, images := codexPrompt("Q", files)
	if len(images) != 1 || !strings.Contains(cp, "spec.pdf") || !strings.Contains(cp, "ERROR x") {
		t.Fatalf("codex: %s %v", cp, images)
	}
	if c, _ := anthropicContent("Q", nil); c != "Q" {
		t.Fatal("no attachments must stay a plain string")
	}
}
