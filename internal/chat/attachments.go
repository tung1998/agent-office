package chat

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tung1998/agent-office/internal/attach"
)

// Attachments reach each runtime its own way. Text files are always inlined
// into the prompt; images and PDFs go as:
//   - Claude Code: file paths the agent opens with Read (folder added via --add-dir)
//   - Codex: --image (PDFs are not supported)
//   - Anthropic API: image / document content blocks
//   - OpenAI API: image_url / file content parts

func onlyBinary(files []attach.File) []attach.File {
	var out []attach.File
	for _, f := range files {
		if f.Kind == "image" || f.Kind == "pdf" {
			out = append(out, f)
		}
	}
	return out
}

// claudePrompt lists binary attachments by path for the Read tool.
func claudePrompt(prompt string, files []attach.File) (string, []string) {
	prompt = attach.InlineText(prompt, files, nil)
	bin := onlyBinary(files)
	if len(bin) == 0 {
		return prompt, nil
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\nFile đính kèm (mở bằng công cụ Read trước khi trả lời):")
	dirs := []string{}
	for _, f := range bin {
		fmt.Fprintf(&b, "\n- %s: %s", f.Name, f.Path)
		dirs = append(dirs, filepath.Dir(f.Path))
	}
	return b.String(), dirs
}

func dataB64(f attach.File) (string, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// anthropicContent builds the user content with image/document blocks.
func anthropicContent(prompt string, files []attach.File) (any, error) {
	bin := onlyBinary(files)
	text := attach.InlineText(prompt, files, nil)
	if len(bin) == 0 {
		return text, nil
	}
	blocks := []any{}
	for _, f := range bin {
		data, err := dataB64(f)
		if err != nil {
			return nil, err
		}
		typ, mime := "image", f.Mime
		if f.Kind == "pdf" {
			typ, mime = "document", "application/pdf"
		}
		blocks = append(blocks, map[string]any{"type": typ, "source": map[string]any{"type": "base64", "media_type": mime, "data": data}})
	}
	return append(blocks, map[string]any{"type": "text", "text": text}), nil
}

// openAIContent builds the user content with image_url / file parts.
func openAIContent(prompt string, files []attach.File) (any, error) {
	bin := onlyBinary(files)
	text := attach.InlineText(prompt, files, nil)
	if len(bin) == 0 {
		return text, nil
	}
	parts := []any{map[string]any{"type": "text", "text": text}}
	for _, f := range bin {
		data, err := dataB64(f)
		if err != nil {
			return nil, err
		}
		if f.Kind == "pdf" {
			parts = append(parts, map[string]any{"type": "file", "file": map[string]any{"filename": f.Name, "file_data": "data:application/pdf;base64," + data}})
		} else {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + f.Mime + ";base64," + data}})
		}
	}
	return parts, nil
}

// codexPrompt inlines text, notes PDFs it cannot read, and returns image paths.
func codexPrompt(prompt string, files []attach.File) (string, []string) {
	prompt = attach.InlineText(prompt, files, func(f attach.File) bool { return f.Kind == "pdf" })
	var images []string
	for _, f := range files {
		if f.Kind == "image" {
			images = append(images, f.Path)
		}
	}
	return prompt, images
}
