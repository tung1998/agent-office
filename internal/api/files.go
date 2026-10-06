package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/gitops"
	"github.com/tung1998/agent-office/internal/perm"
)

// The dashboard's file editor: an admin browses the project folder and edits
// a file right there, like an IDE (no worktree, no review). Ignored, secret
// and protected files are hidden until asked for; writing a secret or
// protected file needs an explicit confirm. A write carries the sha the
// person loaded, so it never overwrites what an agent changed meanwhile.

const (
	maxEditBytes = 1 << 20 // larger files are shown as too large, not opened
	maxDirItems  = 1000
)

func (s *server) fileRoutes(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/projects/{id}/files", admin(s.listFiles))
	mux.Handle("GET /api/projects/{id}/file", admin(s.readFile))
	mux.Handle("PUT /api/projects/{id}/file", admin(s.writeFile))
	mux.Handle("GET /api/projects/{id}/file/diff", admin(s.fileDiff))
}

type fileFlags struct {
	Ignored   bool `json:"ignored"`
	Secret    bool `json:"secret"`
	Protected bool `json:"protected"`
}

type fileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
	fileFlags
}

// fileTarget is a project's folder and one cleaned relative path in it.
type fileTarget struct {
	root, rel, full string
	policy          perm.Policy
}

func (s *server) fileTarget(w http.ResponseWriter, r *http.Request, rel string) (fileTarget, bool) {
	p, ok, err := s.projectRoot(r, r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return fileTarget{}, false
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "project chưa có thư mục")
		return fileTarget{}, false
	}
	rel = path.Clean("/" + filepath.ToSlash(strings.TrimSpace(rel)))[1:] // "" is the root
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		writeError(w, http.StatusForbidden, "không mở thư mục .git")
		return fileTarget{}, false
	}
	full, err := chat.Workspace{Root: p.Path}.Resolve(cmpRel(rel))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return fileTarget{}, false
	}
	return fileTarget{root: p.Path, rel: rel, full: full, policy: perm.LoadPolicy(r.Context(), s.cfg.Store, r.PathValue("id"))}, true
}

func cmpRel(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func (t fileTarget) flags(rel string, dir bool, ignored map[string]bool) fileFlags {
	key := rel
	if dir {
		key += "/"
	}
	return fileFlags{
		Ignored:   ignored[key],
		Secret:    !dir && chat.IsSecret(path.Base(rel)),
		Protected: len(t.policy.Denied([]string{key})) > 0,
	}
}

// listFiles lists one folder level; hidden=1 also shows ignored and secret entries.
func (s *server) listFiles(w http.ResponseWriter, r *http.Request) {
	t, ok := s.fileTarget(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	hidden := r.URL.Query().Get("hidden") == "1"
	items, err := os.ReadDir(t.full)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	out := make([]fileEntry, 0, len(items))
	var check []string
	for _, e := range items {
		if t.rel == "" && e.Name() == ".git" {
			continue
		}
		rel := path.Join(t.rel, e.Name())
		dir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 { // a link to a folder opens like one
			if st, err := os.Stat(filepath.Join(t.full, e.Name())); err == nil {
				dir = st.IsDir()
			}
		}
		var size int64
		if info, err := e.Info(); err == nil && !dir {
			size = info.Size()
		}
		out = append(out, fileEntry{Name: e.Name(), Path: rel, Dir: dir, Size: size})
		if dir {
			check = append(check, rel+"/")
		} else {
			check = append(check, rel)
		}
	}
	ignored := gitops.Ignored(r.Context(), t.root, check)
	shown := out[:0]
	hiddenCount := 0
	for _, e := range out {
		e.fileFlags = t.flags(e.Path, e.Dir, ignored)
		if !hidden && (e.Ignored || e.Secret) {
			hiddenCount++
			continue
		}
		shown = append(shown, e)
	}
	sort.SliceStable(shown, func(i, j int) bool {
		if shown[i].Dir != shown[j].Dir {
			return shown[i].Dir
		}
		return strings.ToLower(shown[i].Name) < strings.ToLower(shown[j].Name)
	})
	more := 0
	if len(shown) > maxDirItems {
		more = len(shown) - maxDirItems
		shown = shown[:maxDirItems]
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": t.rel, "entries": shown, "hidden": hiddenCount, "more": more})
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func isBinary(b []byte) bool {
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(b)
}

// readFile returns a text file's content and its sha (the version a save is based on).
func (s *server) readFile(w http.ResponseWriter, r *http.Request) {
	t, ok := s.fileTarget(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	st, err := os.Stat(t.full)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	if st.IsDir() {
		writeError(w, http.StatusBadRequest, "đây là thư mục")
		return
	}
	res := map[string]any{"path": t.rel, "size": st.Size(), "flags": t.flags(t.rel, false, gitops.Ignored(r.Context(), t.root, []string{t.rel}))}
	if st.Size() > maxEditBytes {
		res["too_large"] = true
		writeJSON(w, http.StatusOK, res)
		return
	}
	b, err := os.ReadFile(t.full)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	res["sha"] = sha(b)
	if isBinary(b) {
		res["binary"] = true
	} else {
		res["content"] = string(b)
	}
	writeJSON(w, http.StatusOK, res)
}

// writeFile saves (or creates, with sha "") a text file. A sha that no longer
// matches the file is a conflict: someone changed it after it was loaded.
func (s *server) writeFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*maxEditBytes)
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Sha     string `json:"sha"`
		Confirm bool   `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	t, ok := s.fileTarget(w, r, in.Path)
	if !ok {
		return
	}
	if t.rel == "" {
		writeError(w, http.StatusBadRequest, "thiếu đường dẫn file")
		return
	}
	if len(in.Content) > maxEditBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file quá lớn để sửa trên dashboard")
		return
	}
	f := t.flags(t.rel, false, nil)
	if (f.Secret || f.Protected) && !in.Confirm {
		writeError(w, http.StatusPreconditionRequired, "file bí mật hoặc được bảo vệ: cần xác nhận trước khi lưu")
		return
	}
	// a new file: its folder must resolve inside the project too (no symlink out)
	if _, err := (chat.Workspace{Root: t.root}).Resolve(path.Dir(t.rel)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := fs.FileMode(0o644)
	cur, err := os.ReadFile(t.full)
	created := errors.Is(err, os.ErrNotExist)
	switch {
	case err == nil:
		if st, err := os.Stat(t.full); err == nil {
			mode = st.Mode().Perm()
		}
		if sha(cur) != in.Sha {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "file đã bị thay đổi sau khi mở; tải lại rồi sửa tiếp", "sha": sha(cur)})
			return
		}
	case created:
		if in.Sha != "" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "file đã bị xóa sau khi mở", "sha": ""})
			return
		}
		if err := os.MkdirAll(filepath.Dir(t.full), 0o755); err != nil {
			s.fileError(w, r, err)
			return
		}
	default:
		s.fileError(w, r, err)
		return
	}
	if err := os.WriteFile(t.full, []byte(in.Content), mode); err != nil {
		s.fileError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "file.write", ResourceID: t.rel, ProjectID: r.PathValue("id"),
		Detail: map[string]any{"bytes": len(in.Content), "created": created, "secret": f.Secret, "protected": f.Protected}})
	writeJSON(w, http.StatusOK, map[string]any{"path": t.rel, "sha": sha([]byte(in.Content))})
}

// fileDiff is the file's uncommitted change against HEAD.
func (s *server) fileDiff(w http.ResponseWriter, r *http.Request) {
	t, ok := s.fileTarget(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	d, err := gitops.Diff(r.Context(), t.root, []string{cmpRel(t.rel)}, 400_000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if d == "" && t.rel != "" && !gitops.Tracked(r.Context(), t.root, t.rel) {
		d = newFileDiff(t.rel, t.full, 400_000) // a new file: all of it added
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": d})
}

// newFileDiff is an untracked file as a diff that adds it ("" when it cannot
// be read as text).
func newFileDiff(rel, full string, max int) string {
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return ""
	}
	f, err := os.Open(full)
	if err != nil {
		return ""
	}
	defer f.Close()
	data := make([]byte, min(info.Size(), int64(max)))
	n, _ := io.ReadFull(f, data)
	data = data[:n]
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "--- /dev/null\n+++ b/" + rel + "\n(file nhị phân)"
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", rel, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	if info.Size() > int64(max) {
		b.WriteString("… (cắt bớt)\n")
	}
	return b.String()
}

func (s *server) fileError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "không tìm thấy file")
	case errors.Is(err, os.ErrPermission):
		writeError(w, http.StatusForbidden, "không có quyền với file này")
	default:
		s.internal(w, r, err)
	}
}
