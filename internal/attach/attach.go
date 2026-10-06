// Package attach stores files people attach to a chat message or a task
// (images, PDFs, text documents) and prepares them for each runtime.
//
// Layout: <office>/attachments/<id>/meta.json and <id>/<file name>.
package attach

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

const (
	MaxSize     = 10 << 20  // per file
	MaxTextSize = 200 << 10 // text files inlined into the prompt
	MaxPerSend  = 10
)

var (
	ErrTooBig      = fmt.Errorf("file quá lớn (tối đa %d MB)", MaxSize>>20)
	ErrUnsupported = errors.New("chỉ hỗ trợ ảnh (png, jpg, gif, webp), PDF và file chữ (md, txt, csv, json, code, log…)")
	ErrNotFound    = errors.New("không tìm thấy file đính kèm")
	ErrTooMany     = fmt.Errorf("tối đa %d file mỗi lần gửi", MaxPerSend)
)

// Meta is stored next to the file.
type Meta struct {
	storage.Attachment
	ProjectID string    `json:"project_id"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// File is an attachment ready for a runtime.
type File struct {
	storage.Attachment
	Path string // absolute path on disk
	Text string // content, for kind text
}

// Store keeps attachments in a folder.
type Store struct{ Dir string }

var (
	idRe     = regexp.MustCompile(`^att_[0-9a-z]+$`)
	unsafeRe = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)
	images   = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}
)

// Kind classifies content: image, pdf or text; "" if unsupported.
func Kind(name string, data []byte) (kind, mime string) {
	sniff := http.DetectContentType(data)
	switch {
	case images[sniff]:
		return "image", sniff
	case sniff == "application/pdf":
		return "pdf", sniff
	}
	// any UTF-8 text (html/svg included) is kept as plain text, never rendered
	if bytes.IndexByte(data, 0) < 0 && utf8.Valid(data) {
		return "text", "text/plain; charset=utf-8"
	}
	return "", sniff
}

// SafeName keeps a readable file name without path tricks.
func SafeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(unsafeRe.ReplaceAllString(name, "_"))
	name = strings.TrimLeft(name, ".")
	if len(name) > 120 {
		ext := filepath.Ext(name)
		name = name[:120-len(ext)] + ext
	}
	if name == "" {
		name = "file"
	}
	return name
}

// Save stores a file for a project.
func (s Store) Save(projectID, createdBy, name string, data []byte) (Meta, error) {
	if len(data) == 0 {
		return Meta{}, errors.New("file trống")
	}
	if len(data) > MaxSize {
		return Meta{}, ErrTooBig
	}
	kind, mime := Kind(name, data)
	if kind == "" {
		return Meta{}, ErrUnsupported
	}
	if kind == "text" && len(data) > MaxTextSize {
		return Meta{}, fmt.Errorf("file chữ tối đa %d KB", MaxTextSize>>10)
	}
	m := Meta{
		Attachment: storage.Attachment{ID: ids.New("att"), Name: SafeName(name), Kind: kind, Mime: mime, Size: int64(len(data))},
		ProjectID:  projectID, CreatedBy: createdBy, CreatedAt: time.Now().UTC(),
	}
	dir := filepath.Join(s.Dir, m.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Meta{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, m.Name), data, 0o600); err != nil {
		return Meta{}, err
	}
	raw, _ := json.Marshal(m)
	return m, os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600)
}

// Get loads an attachment's metadata and path.
func (s Store) Get(id string) (Meta, string, error) {
	if !idRe.MatchString(id) {
		return Meta{}, "", ErrNotFound
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, id, "meta.json"))
	if err != nil {
		return Meta{}, "", ErrNotFound
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Meta{}, "", err
	}
	return m, filepath.Join(s.Dir, id, m.Name), nil
}

// Resolve checks ids belong to the project and loads them for a runtime.
func (s Store) Resolve(projectID string, idList []string) ([]File, error) {
	if len(idList) > MaxPerSend {
		return nil, ErrTooMany
	}
	out := []File{}
	for _, id := range idList {
		m, p, err := s.Get(id)
		if err != nil || m.ProjectID != projectID {
			return nil, ErrNotFound
		}
		f := File{Attachment: m.Attachment, Path: p}
		if m.Kind == "text" {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			f.Text = string(raw)
		}
		out = append(out, f)
	}
	return out, nil
}

// Load loads stored references (already checked when they were sent).
func (s Store) Load(refs []storage.Attachment) []File {
	out := []File{}
	for _, r := range refs {
		m, p, err := s.Get(r.ID)
		if err != nil {
			continue
		}
		f := File{Attachment: m.Attachment, Path: p}
		if m.Kind == "text" {
			if raw, err := os.ReadFile(p); err == nil {
				f.Text = string(raw)
			}
		}
		out = append(out, f)
	}
	return out
}

// Refs returns the storage references of files.
func Refs(files []File) []storage.Attachment {
	out := make([]storage.Attachment, 0, len(files))
	for _, f := range files {
		out = append(out, f.Attachment)
	}
	return out
}

// InlineText appends text attachments to a prompt; every runtime can read
// them. note lists the other files the runtime cannot take (e.g. a PDF on
// Codex) so the agent knows they exist.
func InlineText(prompt string, files []File, unsupported func(File) bool) string {
	var b strings.Builder
	b.WriteString(prompt)
	for _, f := range files {
		switch {
		case f.Kind == "text":
			fmt.Fprintf(&b, "\n\n<attachment name=%q>\n%s\n</attachment>", f.Name, f.Text)
		case unsupported != nil && unsupported(f):
			fmt.Fprintf(&b, "\n\n(Người dùng có đính kèm %q nhưng kết nối AI này không đọc được loại file đó.)", f.Name)
		}
	}
	return b.String()
}
