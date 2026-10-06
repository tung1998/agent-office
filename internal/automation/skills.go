package automation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SkillRef is a skill a project's chat can call with "/name".
type SkillRef struct {
	Name        string `json:"name"` // plugin skills are "<plugin>:<skill>", like Claude Code
	Description string `json:"description"`
	Source      string `json:"source"` // project | user | plugin
	Path        string `json:"-"`
}

// ProjectSkills lists skills usable in a project: the project's own, then
// machine-wide, then plugin skills. On a name clash the first wins, as in
// Claude Code. projectPath "" (machine-wide helper) skips project skills.
func ProjectSkills(home, projectPath string) []SkillRef {
	seen := map[string]bool{}
	var out []SkillRef
	add := func(items []Item, source, prefix string) {
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		for _, it := range items {
			name := prefix + filepath.Base(it.Location.Path)
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, SkillRef{Name: name, Description: it.Description, Source: source, Path: it.Location.Path})
		}
	}
	if projectPath != "" {
		add(scanSkills(filepath.Join(projectPath, ".claude", "skills"), Location{}), "project", "")
	}
	add(scanSkills(filepath.Join(home, ".claude", "skills"), Location{}), "user", "")
	dirs, _ := filepath.Glob(filepath.Join(home, ".claude", "plugins", "cache", "*", "*", "*", "skills"))
	sort.Strings(dirs)
	for _, d := range dirs {
		add(scanSkills(d, Location{}), "plugin", pluginName(d)+":")
	}
	if out == nil {
		out = []SkillRef{}
	}
	return out
}

var (
	skillCallRe     = regexp.MustCompile(`^/([A-Za-z0-9][A-Za-z0-9._:-]*)(?:\s+|$)`)
	ErrUnknownSkill = errors.New("không có skill này trong project")
)

// ParseSkillCall splits "/name rest" into name and rest. ok is false when the
// text is not a skill call ("/Users/x" is a path, not a call).
func ParseSkillCall(text string) (name, rest string, ok bool) {
	m := skillCallRe.FindStringSubmatchIndex(text)
	if m == nil {
		return "", text, false
	}
	return text[m[2]:m[3]], strings.TrimSpace(text[m[1]:]), true
}

// ExpandSkillCall turns "/name rest" into a prompt carrying the skill's
// instructions, so every runtime (Claude Code, API, Codex) can follow it.
func ExpandSkillCall(home, projectPath, text string) (string, *SkillRef, error) {
	name, rest, ok := ParseSkillCall(text)
	if !ok {
		return text, nil, nil
	}
	var skill *SkillRef
	for _, s := range ProjectSkills(home, projectPath) {
		if s.Name == name {
			s := s
			skill = &s
			break
		}
	}
	if skill == nil {
		return "", nil, fmt.Errorf("%w: /%s", ErrUnknownSkill, name)
	}
	body := readText(filepath.Join(skill.Path, "SKILL.md"), 32<<10)
	var others []string
	_ = filepath.WalkDir(skill.Path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || len(others) >= 30 {
			return nil
		}
		if rel, _ := filepath.Rel(skill.Path, p); rel != "SKILL.md" {
			others = append(others, filepath.ToSlash(rel))
		}
		return nil
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Người dùng gọi skill /%s. Làm theo hướng dẫn của skill dưới đây cho yêu cầu của họ.\n", name)
	b.WriteString("Bạn chỉ có công cụ đọc: nếu skill bảo chạy lệnh hay sửa file, hãy nêu lệnh cần chạy hoặc đề xuất diff thay vì tự làm.\n\n")
	fmt.Fprintf(&b, "<skill name=%q dir=%q>\n%s\n</skill>\n", name, skill.Path, strings.TrimSpace(body))
	if len(others) > 0 {
		fmt.Fprintf(&b, "File khác trong thư mục skill (đọc khi cần): %s\n", strings.Join(others, ", "))
	}
	if rest == "" {
		rest = "(không có thêm yêu cầu, thực hiện theo skill)"
	}
	fmt.Fprintf(&b, "\nYêu cầu: %s", rest)
	return b.String(), skill, nil
}
