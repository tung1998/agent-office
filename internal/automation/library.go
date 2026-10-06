package automation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Library is the office's own collection of skills, agents and MCP templates,
// kept as plain files under <office home>/library so it can be versioned,
// copied or exported:
//
//	library/skills/<name>/SKILL.md (+ files)
//	library/agents/<name>.md
//	library/mcp/<name>.json   (an MCPTemplate)
type Library struct {
	Dir   string
	Trash string
}

// LibraryItem is an entry in the library.
type LibraryItem struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Files       map[string]string `json:"files,omitempty"`    // skill files / agent: {"<name>.md": content}
	Template    *MCPTemplate      `json:"template,omitempty"` // mcp
}

var ErrNotFound = errors.New("không tìm thấy")

func (l Library) sub(kind string) string {
	return filepath.Join(l.Dir, map[string]string{"skill": "skills", "agent": "agents", "mcp": "mcp"}[kind])
}

func (l Library) path(kind, name string) string {
	switch kind {
	case "skill":
		return filepath.Join(l.sub(kind), name)
	case "agent":
		return filepath.Join(l.sub(kind), name+".md")
	}
	return filepath.Join(l.sub(kind), name+".json")
}

// List returns entries of a kind (without file contents).
func (l Library) List(kind string) ([]LibraryItem, error) {
	entries, err := os.ReadDir(l.sub(kind))
	if errors.Is(err, os.ErrNotExist) {
		return []LibraryItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []LibraryItem{}
	for _, e := range entries {
		name := e.Name()
		switch kind {
		case "skill":
			if !e.IsDir() {
				continue
			}
		case "agent":
			if e.IsDir() || !strings.HasSuffix(name, ".md") {
				continue
			}
			name = strings.TrimSuffix(name, ".md")
		case "mcp":
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			name = strings.TrimSuffix(name, ".json")
		}
		it, err := l.Get(kind, name)
		if err != nil {
			continue
		}
		if kind != "mcp" {
			it.Files = nil
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get loads one entry with its contents.
func (l Library) Get(kind, name string) (LibraryItem, error) {
	if !nameRe.MatchString(name) {
		return LibraryItem{}, ErrBadName
	}
	p := l.path(kind, name)
	st, err := os.Stat(p)
	if err != nil {
		return LibraryItem{}, ErrNotFound
	}
	it := LibraryItem{Kind: kind, Name: name, UpdatedAt: st.ModTime().UTC()}
	switch kind {
	case "skill":
		files, err := ReadSkill(p)
		if err != nil {
			return it, err
		}
		fm, _ := Frontmatter(files["SKILL.md"])
		it.Description, it.Files = fm["description"], files
	case "agent":
		raw, err := os.ReadFile(p)
		if err != nil {
			return it, err
		}
		fm, _ := Frontmatter(string(raw))
		it.Description, it.Files = fm["description"], map[string]string{name + ".md": string(raw)}
	case "mcp":
		raw, err := os.ReadFile(p)
		if err != nil {
			return it, err
		}
		var t MCPTemplate
		if err := json.Unmarshal(raw, &t); err != nil {
			return it, err
		}
		t.Source = "library"
		if t.Inputs == nil {
			t.Inputs = []Input{}
		}
		it.Description, it.Template = t.Description, &t
	}
	return it, nil
}

// SaveSkill replaces a skill folder.
func (l Library) SaveSkill(name string, files map[string]string) error {
	if !nameRe.MatchString(name) {
		return ErrBadName
	}
	in := Installer{Trash: l.Trash}
	dir := l.path("skill", name)
	if _, err := os.Stat(dir); err == nil {
		if _, err := in.toTrash(dir, "library-skill-"+name); err != nil {
			return err
		}
	}
	// Reuse the installer's file writer with a synthetic target.
	if err := os.MkdirAll(l.sub("skill"), 0o755); err != nil {
		return err
	}
	return writeFiles(dir, files)
}

// SaveAgent writes an agent file.
func (l Library) SaveAgent(name, content string) error {
	if !nameRe.MatchString(name) {
		return ErrBadName
	}
	if err := os.MkdirAll(l.sub("agent"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.path("agent", name), []byte(content), 0o644)
}

// SaveMCP writes an MCP template. Stored values must be placeholders, not
// secrets: the caller strips secret inputs before saving.
func (l Library) SaveMCP(t MCPTemplate) error {
	if !nameRe.MatchString(t.Name) {
		return ErrBadName
	}
	if t.Config == nil {
		return errors.New("thiếu cấu hình MCP")
	}
	t.Source = ""
	if err := os.MkdirAll(l.sub("mcp"), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.path("mcp", t.Name), append(raw, '\n'), 0o644)
}

// Delete moves an entry to trash.
func (l Library) Delete(kind, name string) error {
	if !nameRe.MatchString(name) {
		return ErrBadName
	}
	p := l.path(kind, name)
	if _, err := os.Stat(p); err != nil {
		return ErrNotFound
	}
	_, err := Installer{Trash: l.Trash}.toTrash(p, "library-"+kind+"-"+name)
	return err
}
