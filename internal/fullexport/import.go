package fullexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Import modes (ADR-274).
const (
	// ModeMove: this machine takes over; the old office must stop (its bots,
	// automations and peers come here as they were).
	ModeMove = "move"
	// ModeClone: both offices go on; here bots and automations come in off
	// and the peer pairings are dropped (two offices must not answer the same
	// bot or speak as the same peer).
	ModeClone = "clone"
)

// ImportOptions says where an archive goes.
type ImportOptions struct {
	HomeDir, ProjectRoot string // this office's data folder and the project owning it
	KeyPath              string // where the encryption key goes
	ProjectsDir          string // where the projects' folders go (default: next to ProjectRoot)
	Passphrase           string
	Mode                 string // ModeMove (default) or ModeClone
	Force                bool   // replace a database already here (kept aside, not deleted)
	Defer                bool   // the office here is running: stage the database, ApplyPending puts it in place at its next start
	Log                  func(format string, a ...any)
}

// ProjectResult is what became of one project.
type ProjectResult struct {
	Name, From, To, Kind string
	OK                   bool
	Pending              bool // the clone failed: the project waits for its code (ADR-279)
	Note                 string
}

// Report is what an import did.
type Report struct {
	Manifest Manifest
	Projects []ProjectResult
	Files    int
	Remapped int64 // database values whose paths were moved
	Pending  bool  // the database waits for the office's next start (Defer)
	Warnings []string
}

// ErrExists: this machine already has an office database (Force replaces it).
var ErrExists = errors.New("fullexport: máy này đã có database office; dừng office rồi chạy lại với --force (bản cũ được giữ lại bên cạnh)")

// Import restores the archive in r. The office must not be running.
func Import(ctx context.Context, r io.Reader, o ImportOptions) (Report, error) {
	var rep Report
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if o.Mode == "" {
		o.Mode = ModeMove
	}
	if o.Mode != ModeMove && o.Mode != ModeClone {
		return rep, fmt.Errorf("fullexport: chế độ %q không có (move hoặc clone)", o.Mode)
	}
	dbPath := filepath.Join(o.HomeDir, "office.db")
	if _, err := os.Stat(dbPath); err == nil && !o.Force && !o.Defer {
		return rep, ErrExists
	}
	tr, done, err := newArchiveReader(r, o.Passphrase)
	if err != nil {
		return rep, err
	}
	defer done()
	h, err := tr.Next()
	if err != nil || h.Name != manifestName {
		return rep, ErrPassphrase
	}
	if err := json.NewDecoder(tr).Decode(&rep.Manifest); err != nil {
		return rep, fmt.Errorf("fullexport: manifest: %w", err)
	}
	m := rep.Manifest
	if m.Kind == KindProjects {
		return rep, errors.New("fullexport: đây là gói project, không phải bản export toàn bộ office (nhập ở mục Project)")
	}
	if m.Version > Version {
		return rep, fmt.Errorf("fullexport: bản export dùng định dạng %d, office này chỉ đọc tới %d: cập nhật office trước", m.Version, Version)
	}
	if err := os.MkdirAll(o.HomeDir, 0o700); err != nil {
		return rep, err
	}
	stage, err := os.MkdirTemp(o.HomeDir, "import-")
	if err != nil {
		return rep, err
	}
	defer os.RemoveAll(stage)

	targets := projectTargets(m, o)
	slugs := map[string]Project{}
	for _, p := range m.Projects {
		if p.Slug != "" {
			slugs[p.Slug] = p
		}
	}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rep, fmt.Errorf("fullexport: đọc file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		var dst, root string
		switch {
		case h.Name == dbName:
			dst, root = filepath.Join(stage, "office.db"), stage
		case h.Name == keyName:
			dst, root = filepath.Join(stage, "key"), stage
		case strings.HasPrefix(h.Name, homePrefix):
			dst, root = safeJoin(o.HomeDir, strings.TrimPrefix(h.Name, homePrefix)), o.HomeDir
			rep.Files++
		case strings.HasPrefix(h.Name, codePrefix):
			dst, root = safeJoin(filepath.Join(stage, "code"), strings.TrimPrefix(h.Name, codePrefix)), filepath.Join(stage, "code")
		}
		if dst == "" {
			continue
		}
		if err := extract(h, tr, dst, root); err != nil {
			return rep, fmt.Errorf("fullexport: %s: %w", h.Name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stage, "office.db")); err != nil {
		return rep, errors.New("fullexport: bản export không có database")
	}

	// the code, project by project: one failing (a clone refused) does not stop the rest
	for _, p := range m.Projects {
		if p.Kind == KindHelper {
			continue
		}
		res := ProjectResult{Name: p.Name, From: p.Path, To: targets[p.Path], Kind: p.Kind}
		logf("• %s → %s", p.Name, res.To)
		r := restoreProject(ctx, p, filepath.Join(stage, "code", p.Slug), res.To)
		res.OK, res.Note = r.OK, r.Note
		if r.NoCode { // its data comes in all the same; its code waits for access (ADR-279)
			_ = os.MkdirAll(res.To, 0o755)
			if err := KeepPending(o.HomeDir, p.ID, p, filepath.Join(stage, "code", p.Slug), r); err == nil {
				res.Pending, res.Note = true, "chờ code: "+r.Note
			}
		}
		if !res.OK {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: %s", p.Name, res.Note))
		}
		rep.Projects = append(rep.Projects, res)
	}

	// the database and key last: until here this machine's office is untouched
	moves := map[string]string{}
	for from, to := range targets {
		moves[from] = to
	}
	for _, h := range append([]string{m.HomeDir}, m.HomeAliases...) {
		if h != "" {
			moves[h] = o.HomeDir
		}
	}
	if o.Defer { // the office runs on this database: it takes the new one when it starts again
		pend := filepath.Join(o.HomeDir, PendingDir)
		_ = os.RemoveAll(pend)
		if err := os.MkdirAll(pend, 0o700); err != nil {
			return rep, err
		}
		for _, f := range []string{"office.db", "key"} {
			if _, err := os.Stat(filepath.Join(stage, f)); err == nil {
				if err := os.Rename(filepath.Join(stage, f), filepath.Join(pend, f)); err != nil {
					return rep, err
				}
			}
		}
		raw, _ := json.Marshal(pendingPlan{Moves: moves, Mode: o.Mode})
		if err := os.WriteFile(filepath.Join(pend, "plan.json"), raw, 0o600); err != nil {
			return rep, err
		}
		rep.Pending = true
		rep.Warnings = append(rep.Warnings, "database mới được áp khi office khởi động lại; những gì đổi trên office này từ giờ tới lúc đó sẽ mất")
	} else {
		w, err := placeDatabase(ctx, filepath.Join(stage, "office.db"), filepath.Join(stage, "key"), dbPath, o.KeyPath, moves, o.Mode, &rep.Remapped)
		if err != nil {
			return rep, err
		}
		rep.Warnings = append(rep.Warnings, w...)
	}
	if o.Mode == ModeMove {
		rep.Warnings = append(rep.Warnings, "chế độ chuyển máy: dừng office ở máy cũ trước khi chạy office ở đây (bot, tự động hóa và peer chạy y như bên đó)")
	} else {
		rep.Warnings = append(rep.Warnings, "chế độ nhân bản: bot và tự động hóa đã tắt, ghép đôi peer đã bỏ; bật lại những gì máy này cần (dùng bot token khác với máy cũ)")
	}
	rep.Warnings = append(rep.Warnings, "tài khoản AI chạy bằng CLI (Claude, Codex, Gemini) phải đăng nhập lại trên máy này")
	return rep, nil
}

// projectTargets: where each project's folder goes here. The project owning
// the office's data is this office's own; the rest go into ProjectsDir under
// their folder's name (a second one of the same name gets -2).
func projectTargets(m Manifest, o ImportOptions) map[string]string {
	dir := o.ProjectsDir
	if dir == "" && o.ProjectRoot != "" {
		dir = filepath.Dir(o.ProjectRoot)
	}
	if dir == "" {
		dir = o.HomeDir
	}
	out, used := map[string]string{}, map[string]bool{}
	if o.ProjectRoot != "" {
		if m.ProjectRoot != "" {
			out[m.ProjectRoot] = o.ProjectRoot
		}
		for _, p := range m.Projects {
			if p.Own {
				out[p.Path] = o.ProjectRoot
			}
		}
		used[o.ProjectRoot] = true
	}
	for _, p := range m.Projects {
		if p.Path == "" || out[p.Path] != "" {
			continue
		}
		base := filepath.Base(p.Path)
		t := filepath.Join(dir, base)
		for i := 2; used[t]; i++ {
			t = filepath.Join(dir, fmt.Sprintf("%s-%d", base, i))
		}
		used[t] = true
		out[p.Path] = t
	}
	return out
}

// restoreProject puts p's code at to from its staged files in src.
func restoreProject(ctx context.Context, p Project, src, to string) restored {
	// .office alone (this office's data, just restored) does not make the folder taken
	exists := false
	if entries, err := os.ReadDir(to); err == nil {
		for _, e := range entries {
			if e.Name() != ".office" {
				exists = true
			}
		}
	}
	switch p.Kind {
	case KindMissing:
		return restored{OK: false, Note: "thư mục đã mất trên máy cũ, không có code để mang"}
	case KindPlain:
		if exists {
			return restored{OK: false, Note: "thư mục đích đã có file, giữ nguyên (file trong bản export không được chép)"}
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return restored{OK: false, Note: err.Error()}
		}
		files := filepath.Join(src, "files")
		if _, err := os.Stat(files); err != nil {
			return restored{OK: true, Note: "thư mục trống"}
		}
		if err := os.Rename(files, to); err != nil { // another disk: file by file
			if err := copyTree(files, to); err != nil {
				return restored{OK: false, Note: err.Error()}
			}
		}
		return restored{OK: true, Note: fmt.Sprintf("%d file", p.Files)}
	}
	bundle := filepath.Join(src, "repo.bundle")
	if exists {
		if _, err := git(ctx, to, "rev-parse", "--git-dir"); err != nil {
			return restored{OK: false, Note: "thư mục đích đã có file nhưng không phải repo git, giữ nguyên"}
		}
		return restored{OK: true, Note: "repo đã có ở đây, giữ nguyên (không áp phần chưa commit)"}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return restored{OK: false, Note: err.Error()}
	}
	// git clones only into an empty folder: clone beside it, then move in
	final := to
	to = to + ".import-tmp"
	_ = os.RemoveAll(to)
	defer os.RemoveAll(to)
	var notes []string
	switch p.Kind {
	case KindRemote:
		if out, err := git(ctx, filepath.Dir(to), "clone", p.Remote, to); err != nil {
			return restored{NoCode: true, Reason: ClassifyCloneError(out), Message: lastLine(out), Note: "clone " + p.Remote + " lỗi: " + lastLine(out)}
		}
		if p.Bundle {
			if out, err := git(ctx, to, "fetch", bundle, "+refs/heads/*:refs/remotes/office-export/*"); err != nil {
				notes = append(notes, "không lấy được commit chưa push: "+lastLine(out))
			} else {
				notes = append(notes, "đã lấy commit chưa push")
			}
		}
	case KindGit:
		if !p.Bundle {
			if out, err := git(ctx, filepath.Dir(to), "init", to); err != nil {
				return restored{OK: false, Note: "git init lỗi: " + lastLine(out)}
			}
		} else {
			if out, err := git(ctx, filepath.Dir(to), "clone", bundle, to); err != nil {
				return restored{OK: false, Note: "clone từ bundle lỗi: " + lastLine(out)}
			}
			_, _ = git(ctx, to, "remote", "remove", "origin") // the bundle was a file here, not a remote
		}
	}
	if p.Head != "" {
		args := []string{"checkout", "-q", p.Head}
		if p.Branch != "" {
			args = []string{"checkout", "-q", "-B", p.Branch, p.Head}
		}
		if out, err := git(ctx, to, args...); err != nil {
			notes = append(notes, "không về được commit "+short(p.Head)+": "+lastLine(out))
		}
		if p.Kind == KindRemote && p.Bundle { // the other unpushed branches, as local branches
			if out, err := git(ctx, to, "for-each-ref", "--format=%(refname:strip=3)", "refs/remotes/office-export/"); err == nil {
				for _, b := range strings.Fields(out) {
					if b != p.Branch {
						_, _ = git(ctx, to, "branch", b, "refs/remotes/office-export/"+b)
					}
				}
			}
		}
	}
	if p.Patch {
		if out, err := git(ctx, to, "apply", "--binary", "--whitespace=nowarn", filepath.Join(src, "changes.patch")); err != nil {
			notes = append(notes, "không áp được phần chưa commit: "+lastLine(out))
		} else {
			notes = append(notes, "đã áp phần chưa commit")
		}
	}
	if err := moveInto(to, final); err != nil {
		return restored{OK: false, Note: err.Error()}
	}
	to = final
	if p.Untracked > 0 {
		if err := copyTree(filepath.Join(src, "untracked"), to); err != nil {
			notes = append(notes, "file chưa track: "+err.Error())
		} else {
			notes = append(notes, fmt.Sprintf("%d file chưa track", p.Untracked))
		}
	}
	ok := true
	for _, n := range notes {
		if strings.HasPrefix(n, "không") {
			ok = false
		}
	}
	return restored{OK: ok, Note: strings.Join(notes, "; ")}
}

// moveInto moves each entry of src into dst (made if missing).
func moveInto(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyTree moves src's files into dst (renaming where it can).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
			return err
		}
		return moveFile(p, t, info)
	})
}

// moveFile renames, or copies when src and dst are on different disks.
func moveFile(src, dst string, info os.FileInfo) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// PendingDir holds a database an import staged while the office ran.
const PendingDir = "pending-import"

type pendingPlan struct {
	Moves map[string]string `json:"moves"`
	Mode  string            `json:"mode"`
}

// placeDatabase puts the staged database and key in place (the ones there
// kept aside) and moves their paths.
func placeDatabase(ctx context.Context, stageDB, stageKey, dbPath, keyPath string, moves map[string]string, mode string, remapped *int64) ([]string, error) {
	var warnings []string
	stamp := time.Now().Format("20060102-150405")
	if _, err := os.Stat(dbPath); err == nil {
		aside := dbPath + ".before-import-" + stamp
		if err := os.Rename(dbPath, aside); err != nil {
			return nil, err
		}
		for _, sfx := range []string{"-wal", "-shm"} {
			_ = os.Rename(dbPath+sfx, aside+sfx)
		}
		warnings = append(warnings, "database cũ được giữ tại "+aside)
	}
	if err := os.Rename(stageDB, dbPath); err != nil {
		return nil, err
	}
	if keyPath != "" {
		if _, err := os.Stat(stageKey); err == nil {
			if _, err := os.Stat(keyPath); err == nil {
				_ = os.Rename(keyPath, keyPath+".before-import-"+stamp)
			}
			if err := os.Rename(stageKey, keyPath); err != nil {
				return nil, err
			}
			_ = os.Chmod(keyPath, 0o600)
		}
	}
	n, err := fixDatabase(ctx, dbPath, moves, mode)
	if err != nil {
		return nil, fmt.Errorf("fullexport: sửa database: %w", err)
	}
	*remapped = n
	return warnings, nil
}

// ApplyPending puts in place the database an import staged while the office
// ran (Defer); call it before the office opens its database. false: none.
func ApplyPending(ctx context.Context, homeDir, dbPath, keyPath string) (bool, error) {
	pend := filepath.Join(homeDir, PendingDir)
	raw, err := os.ReadFile(filepath.Join(pend, "plan.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var plan pendingPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return false, err
	}
	var n int64
	if _, err := placeDatabase(ctx, filepath.Join(pend, "office.db"), filepath.Join(pend, "key"), dbPath, keyPath, plan.Moves, plan.Mode, &n); err != nil {
		return false, err
	}
	return true, os.RemoveAll(pend)
}
