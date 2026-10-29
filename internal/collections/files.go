package collections

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Files (ADR-233): every file sent with a message is copied into the
// project's folder, .office-files/tam (temporary), and listed in the
// project's collection "files"; one the person wants kept is moved to
// .office-files/giu. Temporary files are never deleted on their own: the
// admin cleans them on the data page. The folder is kept out of git by the
// repo's .git/info/exclude, not its .gitignore.
const (
	FilesKey  = "files"
	FilesDir  = ".office-files"
	FileTemp  = "tam"
	FileKept  = "giu"
	fileQuery = 200
)

var (
	ErrNoFolder   = invalid("project chưa có thư mục trên máy")
	ErrNoFile     = invalid("không tìm thấy tệp trong bảng files hay trong thư mục project")
	errOutsideDir = invalid("tệp nằm ngoài thư mục .office-files của project")
	unsafeNameRe  = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)
	FileStates    = []string{FileTemp, FileKept}
)

func filesCollection() storage.Collection {
	return storage.Collection{Key: FilesKey, Name: "Tệp",
		Description: "Tệp gửi kèm tin nhắn (tự thêm, trạng thái tam) và tệp người dùng muốn giữ (giu), nằm trong thư mục .office-files của project. Giữ lại bằng file_keep.",
		Fields: []storage.Field{
			{Name: "name", Label: "Tên", Type: storage.FieldText, Required: true},
			{Name: "path", Label: "Đường dẫn", Type: storage.FieldText, Required: true, Unique: true}, // relative to the project folder
			{Name: "status", Label: "Trạng thái", Type: storage.FieldEnum, Options: FileStates, Indexed: true},
			{Name: "size", Label: "Dung lượng", Type: storage.FieldNumber},
			{Name: "chat", Label: "Chat", Type: storage.FieldText},
			{Name: "note", Label: "Ghi chú", Type: storage.FieldText},
		}}
}

// ensureFiles makes the project's files collection when it has none.
func (s *Service) ensureFiles(ctx context.Context, projectID string) error {
	if projectID == "" {
		return nil
	}
	if _, err := s.store.Collections().Get(ctx, projectID, FilesKey); !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	_, err := s.Define(ctx, projectID, filesCollection())
	return err
}

// FilesRoot is the project's folder (where .office-files lives).
func (s *Service) FilesRoot(ctx context.Context, projectID string) (string, error) {
	p, err := s.store.Repos().Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	if p.Path == "" {
		return "", ErrNoFolder
	}
	return p.Path, nil
}

// prepare makes .office-files/<state> and keeps the folder out of git.
func prepare(root, state string) (string, error) {
	dir := filepath.Join(root, FilesDir, state)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	excludeFromGit(root)
	return dir, nil
}

// excludeFromGit lists .office-files in the repo's local exclude file (a
// worktree's .git is a file: it shares the main repo's, nothing to do).
func excludeFromGit(root string) {
	info, err := os.Stat(filepath.Join(root, ".git"))
	if err != nil || !info.IsDir() {
		return
	}
	path := filepath.Join(root, ".git", "info", "exclude")
	raw, _ := os.ReadFile(path)
	for _, l := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(l); t == FilesDir || t == FilesDir+"/" || t == "/"+FilesDir+"/" {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	prefix := ""
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(prefix + "/" + FilesDir + "/\n")
}

func safeName(name string) string {
	name = strings.TrimSpace(unsafeNameRe.ReplaceAllString(filepath.Base(name), "_"))
	if name == "" || name == "." || name == ".." {
		return "tep"
	}
	return name
}

// free: dir/name, or dir/name-2… when taken.
func free(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	p := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
	}
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return n, err
}

// SaveFile copies src (a file sent with a message) into the project's
// temporary folder and lists it; it returns the path in the project.
func (s *Service) SaveFile(ctx context.Context, projectID, src, name, chat, by string) (string, error) {
	root, err := s.FilesRoot(ctx, projectID)
	if err != nil {
		return "", err
	}
	if err := s.ensureFiles(ctx, projectID); err != nil {
		return "", err
	}
	dir, err := prepare(root, FileTemp)
	if err != nil {
		return "", err
	}
	name = safeName(name)
	dst := free(dir, time.Now().Format("20060102-150405")+"-"+name)
	n, err := copyFile(src, dst)
	if err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(root, dst)
	rel = filepath.ToSlash(rel)
	data := map[string]any{"name": name, "path": rel, "status": FileTemp, "size": float64(n)}
	if chat != "" {
		data["chat"] = chat
	}
	if _, _, err := s.Upsert(ctx, projectID, FilesKey, "", data, by); err != nil {
		os.Remove(dst)
		return "", err
	}
	return rel, nil
}

// find is the record ref names: its id, its path (in the project or
// absolute), or its name (the newest).
func (s *Service) find(ctx context.Context, projectID, root, ref string) (storage.Record, bool) {
	c, err := s.Collection(ctx, projectID, FilesKey)
	if err != nil {
		return storage.Record{}, false
	}
	if x, err := s.store.Collections().Record(ctx, c.ID, ref); err == nil {
		return x, true
	}
	rel := ref
	if filepath.IsAbs(ref) {
		if r, err := filepath.Rel(root, ref); err == nil {
			rel = r
		}
	}
	if x, err := s.store.Collections().FindBy(ctx, c.ID, "path", filepath.ToSlash(filepath.Clean(rel))); err == nil {
		return x, true
	}
	list, _, err := s.Query(ctx, projectID, FilesKey, Query{Where: []storage.Cond{{Field: "name", Op: "eq", Value: ref}}, Limit: 1})
	if err == nil && len(list) > 0 {
		return list[0], true
	}
	return storage.Record{}, false
}

// KeepFile keeps a file: one of the table (moved from tam to giu), or a file
// of the project folder or the machine an agent made (copied to giu and
// listed). note says why it is kept.
func (s *Service) KeepFile(ctx context.Context, projectID, ref, note, by string) (storage.Record, error) {
	ref = strings.TrimSpace(ref)
	root, err := s.FilesRoot(ctx, projectID)
	if err != nil {
		return storage.Record{}, err
	}
	if err := s.ensureFiles(ctx, projectID); err != nil {
		return storage.Record{}, err
	}
	dir, err := prepare(root, FileKept)
	if err != nil {
		return storage.Record{}, err
	}
	data := map[string]any{"status": FileKept}
	if note = strings.TrimSpace(note); note != "" {
		data["note"] = note
	}
	x, ok := s.find(ctx, projectID, root, ref)
	switch {
	case ok:
		cur := filepath.Join(root, filepath.FromSlash(fmt.Sprint(x.Data["path"])))
		if !inside(filepath.Join(root, FilesDir), cur) {
			return x, errOutsideDir
		}
		if x.Data["status"] != FileKept {
			dst := free(dir, filepath.Base(cur))
			if err := os.Rename(cur, dst); err != nil {
				return x, err
			}
			rel, _ := filepath.Rel(root, dst)
			data["path"] = filepath.ToSlash(rel)
		}
	default: // a file the agent made, or one of the project's
		src := ref
		if !filepath.IsAbs(src) {
			src = filepath.Join(root, src)
		}
		info, err := os.Stat(src)
		if err != nil || !info.Mode().IsRegular() {
			return storage.Record{}, ErrNoFile
		}
		dst := free(dir, safeName(info.Name()))
		n, err := copyFile(src, dst)
		if err != nil {
			return storage.Record{}, err
		}
		rel, _ := filepath.Rel(root, dst)
		data["name"], data["path"], data["size"] = safeName(info.Name()), filepath.ToSlash(rel), float64(n)
	}
	out, _, err := s.Upsert(ctx, projectID, FilesKey, x.ID, data, by)
	return out, err
}

func inside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TempFiles measures the project's temporary files (on disk).
func TempFiles(root string) (bytes int64, n int) {
	entries, err := os.ReadDir(filepath.Join(root, FilesDir, FileTemp))
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			bytes += info.Size()
			n++
		}
	}
	return bytes, n
}

// CleanTemp deletes the project's temporary files and their rows (the rows'
// history keeps them); kept files stay. It returns the bytes freed.
func (s *Service) CleanTemp(ctx context.Context, projectID, by string) (int64, int, error) {
	root, err := s.FilesRoot(ctx, projectID)
	if err != nil {
		return 0, 0, err
	}
	var freed int64
	n := 0
	if err := s.ensureFiles(ctx, projectID); err != nil {
		return 0, 0, err
	}
	for {
		list, _, err := s.Query(ctx, projectID, FilesKey, Query{Where: []storage.Cond{{Field: "status", Op: "eq", Value: FileTemp}}, Limit: fileQuery})
		if err != nil {
			return freed, n, err
		}
		if len(list) == 0 {
			break
		}
		for _, x := range list {
			if p := filepath.Join(root, filepath.FromSlash(fmt.Sprint(x.Data["path"]))); inside(filepath.Join(root, FilesDir, FileTemp), p) {
				if info, err := os.Stat(p); err == nil {
					freed += info.Size()
				}
				if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					return freed, n, err
				}
			}
			if err := s.Delete(ctx, projectID, FilesKey, x.ID, by); err != nil {
				return freed, n, err
			}
			n++
		}
	}
	// files left in tam with no row (copied by hand): gone too
	dir := filepath.Join(root, FilesDir, FileTemp)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() && os.Remove(filepath.Join(dir, e.Name())) == nil {
				freed += info.Size()
			}
		}
	}
	return freed, n, nil
}
