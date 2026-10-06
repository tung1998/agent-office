package transfer

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Directory layout (git-friendly: one file per template and project, stable
// ordering, no timestamps):
//
//	office.json            {"version": 1}
//	providers.json         connections, no secrets
//	templates/<key>.json   library templates
//	projects/<slug>.json   projects and their models
const readme = `# agent-office config

Export bởi ` + "`office export`" + `. Không chứa API key, tài khoản hay phiên đăng nhập.

- providers.json: kết nối AI (key phải nhập lại, hoặc dùng api_key_env)
- templates/: mô hình mẫu
- projects/: project và mô hình của từng project

Nhập lại: ` + "`office import <thư-mục> --dry-run`" + ` để xem trước, bỏ --dry-run để áp dụng.
`

// WriteDir writes b into dir, removing template/project files that are no longer in b.
func WriteDir(dir string, b Bundle) error {
	for _, sub := range []string{"templates", "projects"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(dir, "office.json"), map[string]int{"version": b.Version}); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "providers.json"), b.Providers); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, t := range b.Templates {
		name := filepath.Join("templates", t.Template.Key+".json")
		keep[name] = true
		if err := writeJSON(filepath.Join(dir, name), t); err != nil {
			return err
		}
	}
	for _, p := range b.Projects {
		name := filepath.Join("projects", projectSlug(p)+".json")
		keep[name] = true
		if err := writeJSON(filepath.Join(dir, name), p); err != nil {
			return err
		}
	}
	for _, sub := range []string{"templates", "projects"} {
		files, _ := filepath.Glob(filepath.Join(dir, sub, "*.json"))
		for _, f := range files {
			rel, _ := filepath.Rel(dir, f)
			if !keep[rel] {
				if err := os.Remove(f); err != nil {
					return err
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644)
	}
	return nil
}

// ReadDir reads a directory written by WriteDir.
func ReadDir(dir string) (Bundle, error) {
	var b Bundle
	var meta struct {
		Version int `json:"version"`
	}
	if err := readJSON(filepath.Join(dir, "office.json"), &meta); err != nil {
		return b, fmt.Errorf("không phải thư mục export của office: %w", err)
	}
	b.Version = meta.Version
	if err := readJSON(filepath.Join(dir, "providers.json"), &b.Providers); err != nil && !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	for _, sub := range []string{"templates", "projects"} {
		files, _ := filepath.Glob(filepath.Join(dir, sub, "*.json"))
		sort.Strings(files)
		for _, f := range files {
			var err error
			if sub == "templates" {
				var t TemplateEntry
				if err = readJSON(f, &t); err == nil {
					b.Templates = append(b.Templates, t)
				}
			} else {
				var p ProjectSpec
				if err = readJSON(f, &p); err == nil {
					b.Projects = append(b.Projects, p)
				}
			}
			if err != nil {
				return b, fmt.Errorf("%s: %w", f, err)
			}
		}
	}
	return b, nil
}

// ReadAny reads a directory export or a single bundle file.
func ReadAny(path string) (Bundle, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Bundle{}, err
	}
	if st.IsDir() {
		return ReadDir(path)
	}
	var b Bundle
	err = readJSON(path, &b)
	return b, err
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// projectSlug is stable for a project: its name plus a short hash of its path
// (two projects may share a name).
func projectSlug(p ProjectSpec) string {
	s := strings.Trim(slugRe.ReplaceAllString(asciiFold(strings.ToLower(p.Name)), "-"), "-")
	if s == "" {
		s = "project"
	}
	sum := sha1.Sum([]byte(p.Path + "\x00" + p.Name))
	return s + "-" + hex.EncodeToString(sum[:])[:6]
}

// asciiFold drops diacritics so "Trợ lý máy" becomes "tro ly may".
func asciiFold(s string) string {
	s = strings.NewReplacer("đ", "d", "Đ", "d").Replace(s)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
