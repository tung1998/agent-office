package cleanup

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Files: what the work keeps on disk — worktrees (dependency folders are
// links to the project's, not counted) and attachments — and the junk among
// them: worktrees nothing uses (a deleted or cleaned chat, a finished Burn
// piece) and attachments no message or task points to.

// ProjectFiles is one project's files.
type ProjectFiles struct {
	Worktrees   int64 `json:"worktrees"`
	Attachments int64 `json:"attachments"`
	Junk        int64 `json:"junk"`
	JunkItems   int   `json:"junk_items"`
}

// Files is every project's, plus the database file.
type Files struct {
	Projects  map[string]*ProjectFiles `json:"projects"`
	DBBytes   int64                    `json:"db_bytes"`
	Junk      int64                    `json:"junk"`
	ScannedAt time.Time                `json:"scanned_at"`
	junk      []junkItem
}

type junkItem struct {
	projectID, repo, tree, attachment string
	bytes                             int64
}

type filesCache struct {
	mu  sync.Mutex
	got *Files
}

func (c *filesCache) reset() { c.mu.Lock(); c.got = nil; c.mu.Unlock() }

// filesTTL: a scan walks the folders; it is kept this long.
const filesTTL = 2 * time.Minute

// Files measures the files (from the last scan if it is recent).
func (s *Service) Files(ctx context.Context) (Files, error) {
	s.files.mu.Lock()
	defer s.files.mu.Unlock()
	if g := s.files.got; g != nil && s.now().Sub(g.ScannedAt) < filesTTL {
		return *g, nil
	}
	f, err := s.scan(ctx)
	if err == nil {
		s.files.got = &f
	}
	return f, err
}

func (s *Service) scan(ctx context.Context) (Files, error) {
	f := Files{Projects: map[string]*ProjectFiles{}, ScannedAt: s.now()}
	of := func(id string) *ProjectFiles {
		if f.Projects[id] == nil {
			f.Projects[id] = &ProjectFiles{}
		}
		return f.Projects[id]
	}
	projects, err := s.store.Repos().List(ctx)
	if err != nil {
		return f, err
	}
	for _, p := range projects {
		if s.trees == nil {
			break
		}
		entries, err := os.ReadDir(s.trees.Path(p.ID, ""))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || ctx.Err() != nil {
				continue
			}
			size := dirSize(filepath.Join(s.trees.Path(p.ID, ""), e.Name()))
			pf := of(p.ID)
			pf.Worktrees += size
			if s.junkTree(ctx, e.Name()) {
				pf.Junk += size
				pf.JunkItems++
				f.Junk += size
				f.junk = append(f.junk, junkItem{projectID: p.ID, repo: p.Path, tree: e.Name(), bytes: size})
			}
		}
	}
	used := s.usedAttachments(ctx)
	if entries, err := os.ReadDir(s.attachDir); err == nil && s.attachDir != "" {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(s.attachDir, e.Name())
			var meta struct {
				ProjectID string    `json:"project_id"`
				CreatedAt time.Time `json:"created_at"`
			}
			raw, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
			_ = json.Unmarshal(raw, &meta)
			size := dirSize(dir)
			pf := of(meta.ProjectID)
			pf.Attachments += size
			// a day to be sent: a file just added waits for its message
			if used != nil && !used[e.Name()] && s.now().Sub(meta.CreatedAt) > 24*time.Hour {
				pf.Junk += size
				pf.JunkItems++
				f.Junk += size
				f.junk = append(f.junk, junkItem{projectID: meta.ProjectID, attachment: dir, bytes: size})
			}
		}
	}
	for _, p := range []string{s.dbPath, s.dbPath + "-wal", s.dbPath + "-shm"} {
		if info, err := os.Stat(p); err == nil && s.dbPath != "" {
			f.DBBytes += info.Size()
		}
	}
	return f, nil
}

// junkTree: nothing uses the worktree any more (a Burn piece that is over
// keeps its branch; its folder is of no more use).
func (s *Service) junkTree(ctx context.Context, name string) bool {
	if id, ok := strings.CutPrefix(name, "burn-"); ok && !strings.HasPrefix(name, "burn-scan-") {
		it, err := s.store.Burn().Item(ctx, id)
		return err != nil || (it.Status != "doing" && it.Status != "paused" && it.Status != "queued")
	}
	if id, ok := strings.CutPrefix(name, "task-"); ok { // a task's, while it runs: kept
		t, err := s.store.Tasks().Get(ctx, id)
		return err != nil || t.Status != "running"
	}
	if !strings.HasPrefix(name, "chat-") && !strings.HasPrefix(name, "burn-scan-") {
		return false // not office's own: left alone
	}
	keep, _ := s.chat.TreeWanted(ctx, name)
	return !keep
}

// usedAttachments are the attachments a message or a task points to.
func (s *Service) usedAttachments(ctx context.Context) map[string]bool {
	used := map[string]bool{}
	ids, err := s.store.Data().AttachmentIDs(ctx)
	if err != nil {
		return nil // unknown: nothing is junk
	}
	for _, id := range ids {
		used[id] = true
	}
	return used
}

// SweepFiles removes the junk (worktrees nothing uses, attachments nothing
// points to); it returns the bytes freed.
func (s *Service) SweepFiles(ctx context.Context) (int64, error) {
	s.files.reset()
	f, err := s.Files(ctx)
	if err != nil {
		return 0, err
	}
	var freed int64
	used := s.usedAttachments(ctx) // again: a message may have taken one since the scan
	for _, j := range f.junk {
		var err error
		if j.attachment != "" {
			if used == nil || used[filepath.Base(j.attachment)] {
				continue
			}
			err = os.RemoveAll(j.attachment)
		} else {
			err = s.trees.Remove(ctx, j.repo, j.projectID, j.tree)
		}
		if err == nil {
			freed += j.bytes
		}
	}
	s.files.reset()
	return freed, nil
}

// dirSize adds up the files under dir, links not followed.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
