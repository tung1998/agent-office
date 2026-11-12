package fullexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// KindProjects marks an archive of chosen projects (ADR-276): each one's
// pack (agents, workflows, automations, chats, notes: never a path or a
// secret) and its code, imported into a running office without replacing
// its database.
const KindProjects = "projects"

const packPrefix = "packs/"

// ProjectsExportOptions says which projects go.
type ProjectsExportOptions struct {
	HomeDir    string                          // the office's data folder: never goes as code
	Projects   []ProjectRef                    // the chosen projects
	Pack       func(id string) ([]byte, error) // a project's pack as JSON
	Passphrase string
}

// ExportProjects writes an archive of the chosen projects to w.
func ExportProjects(ctx context.Context, w io.Writer, o ProjectsExportOptions) (Manifest, error) {
	m := Manifest{Version: Version, Kind: KindProjects, ExportedAt: time.Now().UTC()}
	if len(o.Projects) == 0 {
		return m, errors.New("fullexport: chưa chọn project nào")
	}
	tmp, err := os.MkdirTemp("", "office-projects-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	used := map[string]bool{}
	packs := map[string][]byte{}
	for _, ref := range o.Projects {
		if err := ctx.Err(); err != nil {
			return m, err
		}
		p := Project{ID: ref.ID, Name: ref.Name, Path: ref.Path, Slug: uniqueSlug(ref.Name, used)}
		raw, err := o.Pack(ref.ID)
		if err != nil {
			return m, fmt.Errorf("%s: %w", ref.Name, err)
		}
		packs[p.Slug] = raw
		if ref.Path == "" {
			p.Kind = KindHelper
		} else {
			dir := filepath.Join(tmp, p.Slug)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return m, err
			}
			if err := describeProject(ctx, &p, dir, o.HomeDir); err != nil {
				return m, fmt.Errorf("%s: %w", ref.Name, err)
			}
		}
		m.Projects = append(m.Projects, p)
	}
	aw, err := newArchiveWriter(w, o.Passphrase)
	if err != nil {
		return m, err
	}
	man, _ := json.MarshalIndent(m, "", "  ")
	if err := aw.addBytes(manifestName, man); err != nil {
		return m, err
	}
	for _, p := range m.Projects {
		if err := aw.addBytes(packPrefix+p.Slug+".json", packs[p.Slug]); err != nil {
			return m, err
		}
		if err := writeProjectCode(ctx, aw, p, filepath.Join(tmp, p.Slug), o.HomeDir); err != nil {
			return m, err
		}
	}
	return m, aw.Close()
}

// writeProjectCode adds p's bundle, patch, untracked files or plain files.
func writeProjectCode(ctx context.Context, aw *archiveWriter, p Project, dir, home string) error {
	if p.Bundle {
		if err := aw.addFile(codePrefix+p.Slug+"/repo.bundle", filepath.Join(dir, "repo.bundle")); err != nil {
			return err
		}
	}
	if p.Patch {
		if err := aw.addFile(codePrefix+p.Slug+"/changes.patch", filepath.Join(dir, "changes.patch")); err != nil {
			return err
		}
	}
	switch {
	case p.Kind == KindPlain:
		root := p.Path
		_, err := aw.addTree(root, codePrefix+p.Slug+"/files", true, func(rel string, d fs.DirEntry) bool {
			return slices.Contains(plainSkip, d.Name()) || inside(filepath.Join(root, filepath.FromSlash(rel)), home)
		})
		return err
	case p.Untracked > 0:
		for _, rel := range untrackedFiles(ctx, p.Path, home) {
			if err := aw.addFile(codePrefix+p.Slug+"/untracked/"+rel, filepath.Join(p.Path, filepath.FromSlash(rel))); err != nil {
				return err
			}
		}
	}
	return nil
}

// Staged is an archive of projects unpacked in Dir, waiting for a person to
// say what each project becomes.
type Staged struct {
	Dir      string
	Manifest Manifest
}

// StageProjects unpacks an archive of projects into dir (made fresh).
func StageProjects(ctx context.Context, r io.Reader, passphrase, dir string) (Staged, error) {
	st := Staged{Dir: dir}
	tr, done, err := newArchiveReader(r, passphrase)
	if err != nil {
		return st, err
	}
	defer done()
	h, err := tr.Next()
	if err != nil || h.Name != manifestName {
		return st, ErrPassphrase
	}
	if err := json.NewDecoder(tr).Decode(&st.Manifest); err != nil {
		return st, fmt.Errorf("fullexport: manifest: %w", err)
	}
	if st.Manifest.Kind != KindProjects {
		return st, errors.New("fullexport: đây là bản export toàn bộ office, không phải gói project (nhập ở mục Toàn bộ office)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return st, err
	}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return st, fmt.Errorf("fullexport: đọc file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return st, err
		}
		if !strings.HasPrefix(h.Name, packPrefix) && !strings.HasPrefix(h.Name, codePrefix) {
			continue
		}
		dst := safeJoin(dir, h.Name)
		if dst == "" {
			continue
		}
		if err := extract(h, tr, dst, dir); err != nil {
			return st, fmt.Errorf("fullexport: %s: %w", h.Name, err)
		}
	}
	raw, _ := json.Marshal(st.Manifest)
	return st, os.WriteFile(filepath.Join(dir, manifestName), raw, 0o600)
}

// OpenStaged reads back what StageProjects left in dir.
func OpenStaged(dir string) (Staged, error) {
	st := Staged{Dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(raw, &st.Manifest)
}

// Pack is the staged pack of project slug (JSON).
func (st Staged) Pack(slug string) ([]byte, error) {
	return os.ReadFile(filepath.Join(st.Dir, "packs", slug+".json"))
}

// RestoreCode puts staged project p's code at to (a folder that does not
// exist or is empty). A failed clone leaves to made but empty, and says so
// (NoCode): the caller keeps the code with KeepCode once the project exists.
func (st Staged) RestoreCode(ctx context.Context, p Project, to string) restored {
	if p.Kind == KindMissing {
		if err := os.MkdirAll(to, 0o755); err != nil {
			return restored{Note: err.Error()}
		}
		return restored{OK: true, Note: "thư mục đã mất ở office cũ: tạo thư mục trống"}
	}
	r := restoreProject(ctx, p, filepath.Join(st.Dir, "code", p.Slug), to)
	if r.NoCode {
		_ = os.MkdirAll(to, 0o755)
	}
	return r
}

// KeepCode keeps staged project p's code for project id, waiting for access.
func (st Staged) KeepCode(home, projectID string, p Project, r restored) error {
	return KeepPending(home, projectID, p, filepath.Join(st.Dir, "code", p.Slug), r)
}

// Restored is RestoreCode's answer, exported for callers outside.
type Restored = restored
