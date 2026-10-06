package chat

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/perm"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Workspace gives an agent access to one folder: read-only, or also writing
// when Write (its own worktree, or the project in direct mode). Paths are
// always relative to Root; anything resolving outside it, secret-looking
// files and Deny patterns (the project's protected files) are refused.
type Workspace struct {
	Root  string
	Write bool
	Deny  []string
}

var (
	errOutside = errors.New("đường dẫn nằm ngoài thư mục project")
	errSecret  = errors.New("file này có thể chứa bí mật nên không được đọc")
	errDenied  = errors.New("file này bị project cấm sửa")
	errNoWrite = errors.New("lượt này chỉ được đọc")
)

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".output": true,
	".nuxt": true, ".next": true, "target": true, "__pycache__": true, ".venv": true, ".office": true, ".cache": true, ".pnpm-store": true}

var secretName = regexp.MustCompile(`(?i)(^\.env($|\.)|\.pem$|\.key$|\.p12$|\.pfx$|^id_(rsa|ed25519|ecdsa)|^secret\.key$|credentials\.json$|\.keystore$)`)

// IsSecret reports a file name that may hold secrets (.env, keys…).
func IsSecret(name string) bool { return secretName.MatchString(name) }

// Resolve is resolve for callers outside the package (the dashboard's file editor).
func (w Workspace) Resolve(rel string) (string, error) { return w.resolve(rel) }

// resolve maps a relative path into Root, refusing escapes (including via symlinks).
func (w Workspace) resolve(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", errOutside
	}
	root, err := filepath.EvalSymlinks(w.Root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.Clean(rel))
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	}
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", errOutside
	}
	return full, nil
}

// ListDir lists a folder (dirs first), hiding build and dependency folders.
func (w Workspace) ListDir(rel string) (string, error) {
	full, err := w.resolve(rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	var dirs, files []string
	for _, e := range entries {
		if e.IsDir() {
			if !skipDirs[e.Name()] {
				dirs = append(dirs, e.Name()+"/")
			}
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(dirs)
	sort.Strings(files)
	out := append(dirs, files...)
	if len(out) > 300 {
		out = append(out[:300], fmt.Sprintf("… và %d mục khác", len(out)-300))
	}
	return strings.Join(out, "\n"), nil
}

// ReadFile returns numbered lines [start, end] (1-based, inclusive; 0 = whole file, capped).
func (w Workspace) ReadFile(rel string, start, end int) (string, error) {
	full, err := w.resolve(rel)
	if err != nil {
		return "", err
	}
	if secretName.MatchString(filepath.Base(full)) {
		return "", errSecret
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s là thư mục, dùng list_dir", rel)
	}
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	const maxLines, maxBytes = 800, 120_000
	if start <= 0 {
		start = 1
	}
	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n, shown := 0, 0
	for sc.Scan() {
		n++
		if n < start || (end > 0 && n > end) {
			continue
		}
		line := sc.Text()
		if strings.ContainsRune(line, 0) {
			return "", fmt.Errorf("%s là file nhị phân", rel)
		}
		if shown >= maxLines || b.Len() > maxBytes {
			fmt.Fprintf(&b, "… (đã cắt, đọc tiếp với start=%d)\n", n)
			break
		}
		fmt.Fprintf(&b, "%d\t%s\n", n, line)
		shown++
	}
	return b.String(), sc.Err()
}

// Search finds lines matching a regular expression (case-insensitive), optionally in files matching glob.
func (w Workspace) Search(pattern, glob string) (string, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return "", fmt.Errorf("biểu thức tìm kiếm không hợp lệ: %w", err)
	}
	root, err := w.resolve(".")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	hits, files := 0, 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if hits >= 200 || files > 20000 {
			return filepath.SkipAll
		}
		files++
		rel, _ := filepath.Rel(root, p)
		if glob != "" {
			if ok, _ := filepath.Match(glob, filepath.Base(p)); !ok {
				if ok2, _ := filepath.Match(glob, rel); !ok2 {
					return nil
				}
			}
		}
		if secretName.MatchString(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 2<<20 {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		ln := 0
		for sc.Scan() {
			ln++
			line := sc.Text()
			if strings.ContainsRune(line, 0) {
				return nil
			}
			if re.MatchString(line) {
				if len(line) > 240 {
					line = line[:240] + "…"
				}
				fmt.Fprintf(&b, "%s:%d: %s\n", rel, ln, strings.TrimSpace(line))
				hits++
				if hits >= 200 {
					break
				}
			}
		}
		return nil
	})
	if hits == 0 {
		return "Không tìm thấy kết quả.", nil
	}
	if hits >= 200 {
		b.WriteString("… (dừng ở 200 kết quả, hãy thu hẹp tìm kiếm)\n")
	}
	return b.String(), nil
}

// writable resolves rel for writing: inside Root, not in .git, not a secret
// and not protected by the project.
func (w Workspace) writable(rel string) (string, error) {
	if !w.Write {
		return "", errNoWrite
	}
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if clean == "." || clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", errOutside
	}
	if secretName.MatchString(filepath.Base(clean)) {
		return "", errSecret
	}
	if len(perm.Policy{DenyPaths: w.Deny}.Denied([]string{clean})) > 0 {
		return "", errDenied
	}
	full, err := w.resolve(clean)
	if err != nil {
		return "", err
	}
	// a new file: its folder must resolve inside Root too
	if _, err := w.resolve(filepath.Dir(clean)); err != nil {
		return "", err
	}
	return full, nil
}

// WriteFile creates or replaces a file.
func (w Workspace) WriteFile(rel, content string) (string, error) {
	full, err := w.writable(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(full); err == nil {
		if st.IsDir() {
			return "", fmt.Errorf("%s là thư mục", rel)
		}
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("Đã ghi %s (%d dòng)", rel, strings.Count(content, "\n")+1), nil
}

// EditFile replaces the one occurrence of old with new in a file.
func (w Workspace) EditFile(rel, old, new string) (string, error) {
	full, err := w.writable(rel)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	switch n := strings.Count(string(b), old); {
	case old == "":
		return "", errors.New("old trống; dùng write_file để tạo file")
	case n == 0:
		return "", errors.New("không tìm thấy đoạn old trong file; đọc lại file rồi thử lại")
	case n > 1:
		return "", fmt.Errorf("đoạn old xuất hiện %d lần; thêm ngữ cảnh để chỉ khớp một chỗ", n)
	}
	st, _ := os.Stat(full)
	if err := os.WriteFile(full, []byte(strings.Replace(string(b), old, new, 1)), st.Mode().Perm()); err != nil {
		return "", err
	}
	return "Đã sửa " + rel, nil
}
