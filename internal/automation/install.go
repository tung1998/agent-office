package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Target is where to install.
type Target struct {
	Scope       string `json:"scope"`        // user | project | local
	ProjectPath string `json:"project_path"` // for project / local
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var (
	ErrBadName      = errors.New("tên chỉ gồm chữ, số, dấu chấm, gạch ngang, gạch dưới (tối đa 64 ký tự)")
	ErrBadTarget    = errors.New("nơi cài không hợp lệ")
	ErrExists       = errors.New("đã có mục cùng tên ở nơi này")
	ErrNotAllowed   = errors.New("không được xóa mục ở vị trí này")
	ErrNeedsClaude  = errors.New("cần Claude Code CLI để cài MCP cho toàn máy hoặc riêng máy")
	ErrUnsafeSkill  = errors.New("nội dung có mẫu nguy hiểm nên không được cài")
	ErrNeedsConsent = errors.New("nội dung có cảnh báo, cần xác nhận trước khi cài")
)

// Installer writes to the machine. Home is the user's home; Trash is where
// removed items go (inside the office data folder) so they can be restored.
type Installer struct {
	Home   string
	Trash  string
	Claude func() string // path of the claude binary, "" if missing
}

func (in Installer) claudeDir() string { return filepath.Join(in.Home, ".claude") }

// dirFor returns the skills/agents folder of a target.
func (in Installer) dirFor(kind string, t Target) (string, error) {
	sub := map[string]string{"skill": "skills", "agent": "agents"}[kind]
	switch t.Scope {
	case "user":
		return filepath.Join(in.claudeDir(), sub), nil
	case "project":
		if !filepath.IsAbs(t.ProjectPath) {
			return "", ErrBadTarget
		}
		if st, err := os.Stat(t.ProjectPath); err != nil || !st.IsDir() {
			return "", ErrBadTarget
		}
		return filepath.Join(t.ProjectPath, ".claude", sub), nil
	}
	return "", ErrBadTarget
}

// InstallSkill writes a skill folder. files maps relative paths to content and
// must contain SKILL.md.
func (in Installer) InstallSkill(t Target, name string, files map[string]string, overwrite bool) (string, error) {
	if !nameRe.MatchString(name) {
		return "", ErrBadName
	}
	if _, ok := files["SKILL.md"]; !ok {
		return "", errors.New("skill phải có SKILL.md")
	}
	base, err := in.dirFor("skill", t)
	if err != nil {
		return "", err
	}
	if err := validateFilePaths(files); err != nil {
		return "", err
	}
	dir := filepath.Join(base, name)
	if _, err := os.Stat(dir); err == nil {
		if !overwrite {
			return "", ErrExists
		}
		dest, err := in.toTrash(dir, "skill-"+name)
		if err != nil {
			return "", err
		}
		if err := writeFiles(dir, files); err != nil {
			_ = os.RemoveAll(dir)
			if rerr := os.Rename(dest, dir); rerr != nil {
				return "", fmt.Errorf("cài lỗi (%w) và khôi phục bản cũ cũng lỗi (%v); bản sao lưu ở %s", err, rerr, dest)
			}
			return "", err
		}
		return dir, nil
	}
	if err := writeFiles(dir, files); err != nil {
		return "", err
	}
	return dir, nil
}

// validateFilePaths checks every relative path in files is safe to write
// before any destructive step (like trashing an existing skill) happens.
func validateFilePaths(files map[string]string) error {
	for rel := range files {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("đường dẫn file không hợp lệ: %s", rel)
		}
	}
	return nil
}

func writeFiles(dir string, files map[string]string) error {
	for rel, content := range files {
		clean := filepath.Clean(rel)
		p := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(content, "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			return err
		}
	}
	return nil
}

// InstallAgent writes <dir>/<name>.md.
func (in Installer) InstallAgent(t Target, name, content string, overwrite bool) (string, error) {
	if !nameRe.MatchString(name) {
		return "", ErrBadName
	}
	base, err := in.dirFor("agent", t)
	if err != nil {
		return "", err
	}
	p := filepath.Join(base, name+".md")
	if _, err := os.Stat(p); err == nil {
		if !overwrite {
			return "", ErrExists
		}
		dest, err := in.toTrash(p, "agent-"+name)
		if err != nil {
			return "", err
		}
		if werr := writeAgentFile(base, p, content); werr != nil {
			if rerr := os.Rename(dest, p); rerr != nil {
				return "", fmt.Errorf("cài lỗi (%w) và khôi phục bản cũ cũng lỗi (%v); bản sao lưu ở %s", werr, rerr, dest)
			}
			return "", werr
		}
		return p, nil
	}
	if err := writeAgentFile(base, p, content); err != nil {
		return "", err
	}
	return p, nil
}

func writeAgentFile(base, p, content string) error {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// ReadSkill loads a skill folder (text files up to 4 MB each, 16 MB total):
// skills ship bundled scripts and data (Impeccable's live mode is a 550 KB
// script and a 1 MB font index).
func ReadSkill(dir string) (map[string]string, error) {
	files := map[string]string{}
	total := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(raw, 0) >= 0 {
			return nil
		}
		total += len(raw)
		if total > 16<<20 {
			return errors.New("skill quá lớn")
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	return files, err
}

// Remove deletes an installed skill/agent (to trash) or MCP server.
func (in Installer) Remove(ctx context.Context, item Item) (string, error) {
	loc := item.Location
	switch item.Kind {
	case "skill", "agent":
		if !in.removable(item.Kind, loc) {
			return "", ErrNotAllowed
		}
		return in.toTrash(loc.Path, item.Kind+"-"+item.Name)
	case "mcp":
		switch loc.Type {
		case "user":
			bk, err := in.backup(in.claudeJSONPath(), "claude.json")
			if err != nil {
				return "", err
			}
			return bk, in.claudeMCP(ctx, "", "remove", "-s", "user", item.Name)
		case "local":
			if loc.ProjectPath == "" {
				return "", ErrNotAllowed
			}
			bk, err := in.backup(in.claudeJSONPath(), "claude.json")
			if err != nil {
				return "", err
			}
			return bk, in.claudeMCP(ctx, loc.ProjectPath, "remove", "-s", "local", item.Name)
		case "project":
			return in.editMCPJSON(loc.ProjectPath, func(servers map[string]any) error {
				if _, ok := servers[item.Name]; !ok {
					return errors.New("không thấy MCP này trong .mcp.json")
				}
				delete(servers, item.Name)
				return nil
			})
		}
	}
	return "", ErrNotAllowed
}

// removable checks the path is exactly a skill/agent inside a .claude folder.
func (in Installer) removable(kind string, loc Location) bool {
	if loc.Type != "user" && loc.Type != "project" {
		return false
	}
	sub := map[string]string{"skill": "skills", "agent": "agents"}[kind]
	var base string
	if loc.Type == "user" {
		base = filepath.Join(in.claudeDir(), sub)
	} else {
		if loc.ProjectPath == "" {
			return false
		}
		base = filepath.Join(loc.ProjectPath, ".claude", sub)
	}
	rel, err := filepath.Rel(base, filepath.Clean(loc.Path))
	return err == nil && rel != "." && !strings.Contains(rel, string(filepath.Separator)) && !strings.HasPrefix(rel, "..")
}

func (in Installer) toTrash(p, label string) (string, error) {
	dest := filepath.Join(in.Trash, time.Now().Format("20060102-150405")+"-"+label)
	if err := os.MkdirAll(in.Trash, 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(p, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// InstallMCP adds a server. user/local go through `claude mcp add-json`;
// project writes the shared .mcp.json.
func (in Installer) InstallMCP(ctx context.Context, t Target, name string, config map[string]any, overwrite bool) error {
	if !nameRe.MatchString(name) {
		return ErrBadName
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	switch t.Scope {
	case "user":
		return in.claudeMCPReplace(ctx, "", "user", name, string(raw), overwrite, in.existingUserMCP(name))
	case "local":
		if !filepath.IsAbs(t.ProjectPath) {
			return ErrBadTarget
		}
		return in.claudeMCPReplace(ctx, t.ProjectPath, "local", name, string(raw), overwrite, in.existingLocalMCP(t.ProjectPath, name))
	case "project":
		if !filepath.IsAbs(t.ProjectPath) {
			return ErrBadTarget
		}
		_, err := in.editMCPJSON(t.ProjectPath, func(servers map[string]any) error {
			if _, ok := servers[name]; ok && !overwrite {
				return ErrExists
			}
			servers[name] = config
			return nil
		})
		return err
	}
	return ErrBadTarget
}

// claudeJSONPath is where Claude Code keeps user/local MCP servers (and
// projects), e.g. ~/.claude.json.
func (in Installer) claudeJSONPath() string { return filepath.Join(in.Home, ".claude.json") }

// existingUserMCP returns name's raw config under top-level mcpServers in
// ~/.claude.json, "" if it is not there.
func (in Installer) existingUserMCP(name string) func() string {
	return func() string {
		cfg := readJSON(in.claudeJSONPath())
		servers, _ := cfg["mcpServers"].(map[string]any)
		return rawMCPEntry(servers, name)
	}
}

// existingLocalMCP returns name's raw config under
// projects[projectPath].mcpServers in ~/.claude.json, "" if it is not there.
func (in Installer) existingLocalMCP(projectPath, name string) func() string {
	return func() string {
		cfg := readJSON(in.claudeJSONPath())
		projects, _ := cfg["projects"].(map[string]any)
		pc, _ := projects[projectPath].(map[string]any)
		servers, _ := pc["mcpServers"].(map[string]any)
		return rawMCPEntry(servers, name)
	}
}

func rawMCPEntry(servers map[string]any, name string) string {
	c, ok := servers[name]
	if !ok {
		return ""
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(raw)
}

// claudeMCPReplace installs name via `claude mcp add-json`, overwriting an
// existing server only by removing it first (the CLI has no in-place
// update); existing, when non-empty, is name's current raw config, read and
// backed up (~/.claude.json goes to trash) before it is removed, so a
// failing add-json can be rolled back instead of losing the server.
func (in Installer) claudeMCPReplace(ctx context.Context, dir, scope, name, raw string, overwrite bool, existing func() string) error {
	if !overwrite {
		return in.claudeMCP(ctx, dir, "add-json", "-s", scope, name, raw)
	}
	prev := existing()
	if prev == "" {
		// nothing to remove: add-json would fail on "already exists" only if
		// the CLI's own view disagrees with ~/.claude.json, which it should not
		return in.claudeMCP(ctx, dir, "add-json", "-s", scope, name, raw)
	}
	if _, err := in.backup(in.claudeJSONPath(), "claude.json"); err != nil {
		return err
	}
	if err := in.claudeMCP(ctx, dir, "remove", "-s", scope, name); err != nil {
		return fmt.Errorf("gỡ cấu hình cũ để cài lại: %w", err)
	}
	if err := in.claudeMCP(ctx, dir, "add-json", "-s", scope, name, raw); err != nil {
		rctx := context.WithoutCancel(ctx)
		if rerr := in.claudeMCP(rctx, dir, "add-json", "-s", scope, name, prev); rerr != nil {
			return fmt.Errorf("cài lỗi (%w) và khôi phục cấu hình cũ cũng lỗi (%v); bản sao lưu ở %s", err, rerr, in.Trash)
		}
		return fmt.Errorf("cài lỗi, đã khôi phục cấu hình cũ: %w", err)
	}
	return nil
}

func (in Installer) claudeMCP(ctx context.Context, dir string, args ...string) error {
	bin := ""
	if in.Claude != nil {
		bin = in.Claude()
	}
	if bin == "" {
		return ErrNeedsClaude
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"mcp"}, args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("claude mcp: %s", msg)
	}
	return nil
}

// editMCPJSON read-modify-writes <project>/.mcp.json, keeping a backup in trash.
func (in Installer) editMCPJSON(projectPath string, fn func(map[string]any) error) (string, error) {
	p := filepath.Join(projectPath, ".mcp.json")
	doc := map[string]any{}
	backup := ""
	if raw, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf(".mcp.json không phải JSON hợp lệ: %w", err)
		}
		_ = os.MkdirAll(in.Trash, 0o700)
		backup = filepath.Join(in.Trash, time.Now().Format("20060102-150405")+"-mcp.json")
		_ = os.WriteFile(backup, raw, 0o600)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if err := fn(servers); err != nil {
		return "", err
	}
	doc["mcpServers"] = servers
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return backup, os.WriteFile(p, append(out, '\n'), 0o644)
}
