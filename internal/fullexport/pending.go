package fullexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A project whose repo this machine could not clone (no access yet, no
// network, a repo moved) still comes in, waiting for its code (ADR-279):
// what no remote has (unpushed commits, uncommitted changes, untracked
// files) is kept in <home>/pending-code/<project id>/ until a clone or a
// folder the person points to takes it.

// PendingCodeDir is where the code of projects waiting for it is kept.
const PendingCodeDir = "pending-code"

// Why a clone failed.
const (
	ReasonAuth    = "auth"    // no access: SSH key, gh login, or rights on the repo
	ReasonNetwork = "network" // host unreachable or unknown: try again later
	ReasonMissing = "missing" // the repo is not there (or private and hidden)
	ReasonOther   = "other"
)

// PendingCode is a project waiting for its code.
type PendingCode struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Project   Project   `json:"project"` // remote, branch, commit, and what was kept
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}

// restored is what restoreProject did.
type restored struct {
	OK      bool
	Note    string
	NoCode  bool   // the clone failed: nothing was put in place
	Reason  string // why (Reason*), with NoCode
	Message string // git's own words, with NoCode
}

// ClassifyCloneError reads git's output for why a clone or ls-remote failed.
// A private repo one may not see often answers "not found": that counts as
// access, the likelier cause and the one a person can fix.
func ClassifyCloneError(out string) string {
	o := strings.ToLower(out)
	switch {
	case strings.Contains(o, "permission denied"), strings.Contains(o, "authentication failed"),
		strings.Contains(o, "could not read username"), strings.Contains(o, "403"), strings.Contains(o, "401"),
		strings.Contains(o, "access denied"), strings.Contains(o, "host key verification failed"),
		strings.Contains(o, "repository not found"), strings.Contains(o, "does not appear to be a git repository"):
		return ReasonAuth
	case strings.Contains(o, "could not resolve host"), strings.Contains(o, "connection timed out"),
		strings.Contains(o, "connection refused"), strings.Contains(o, "network is unreachable"),
		strings.Contains(o, "operation timed out"), strings.Contains(o, "could not resolve hostname"):
		return ReasonNetwork
	case strings.Contains(o, "not found"), strings.Contains(o, "404"):
		return ReasonMissing
	}
	return ReasonOther
}

func pendingDir(home, projectID string) string {
	return filepath.Join(home, PendingCodeDir, filepath.Base(projectID))
}

// KeepPending moves src (a project's staged code: bundle, patch, untracked
// files) to the project's pending folder and records why it waits.
func KeepPending(home, projectID string, p Project, src string, r restored) error {
	dir := pendingDir(home, projectID)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(src); err == nil {
		if err := os.Rename(src, filepath.Join(dir, "code")); err != nil {
			if err := copyTree(src, filepath.Join(dir, "code")); err != nil {
				return err
			}
		}
	}
	return savePending(dir, PendingCode{ProjectID: projectID, Name: p.Name, Project: p, Reason: r.Reason, Message: r.Message, At: time.Now().UTC()})
}

func savePending(dir string, pc PendingCode) error {
	raw, _ := json.MarshalIndent(pc, "", "  ")
	return os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o600)
}

// LoadPending is project id's wait for code; false when it has its code.
func LoadPending(home, projectID string) (PendingCode, bool, error) {
	var pc PendingCode
	raw, err := os.ReadFile(filepath.Join(pendingDir(home, projectID), "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return pc, false, nil
	}
	if err != nil {
		return pc, false, err
	}
	return pc, true, json.Unmarshal(raw, &pc)
}

// DropPending gives up waiting: what was kept is deleted.
func DropPending(home, projectID string) error {
	return os.RemoveAll(pendingDir(home, projectID))
}

// RetryPending clones the project's repo (url, when set, instead of the one
// it had) into to and puts back what was kept. On success the wait ends;
// on failure it records the new reason.
func RetryPending(ctx context.Context, home, projectID, to, url string) (bool, string, error) {
	pc, ok, err := LoadPending(home, projectID)
	if err != nil || !ok {
		return false, "", errors.Join(err, errors.New("project này không chờ code"))
	}
	p := pc.Project
	if url = strings.TrimSpace(url); url != "" {
		p.Remote = url
	}
	dir := pendingDir(home, projectID)
	r := restoreProject(ctx, p, filepath.Join(dir, "code"), to)
	if r.NoCode {
		pc.Reason, pc.Message, pc.At, pc.Project.Remote = r.Reason, r.Message, time.Now().UTC(), p.Remote
		_ = savePending(dir, pc)
		return false, r.Note, nil
	}
	if !r.OK && !strings.HasPrefix(r.Note, "repo đã có") {
		return false, r.Note, nil
	}
	return true, r.Note, DropPending(home, projectID)
}

// AttachPending takes a folder the person cloned themselves: it must be a
// git repo of the same remote. What was kept goes in where it can without
// touching their work: unpushed branches as branches, the uncommitted
// changes only when they apply cleanly, untracked files only where none is.
func AttachPending(ctx context.Context, home, projectID, folder string) (string, error) {
	pc, ok, err := LoadPending(home, projectID)
	if err != nil || !ok {
		return "", errors.Join(err, errors.New("project này không chờ code"))
	}
	if out, err := git(ctx, folder, "rev-parse", "--show-toplevel"); err != nil {
		return "", fmt.Errorf("%s không phải repo git: %s", folder, lastLine(out))
	}
	remotes, _ := git(ctx, folder, "remote", "-v")
	if pc.Project.Remote != "" && !anyRemote(remotes, pc.Project.Remote) {
		return "", fmt.Errorf("repo ở %s không trỏ tới %s", folder, pc.Project.Remote)
	}
	code := filepath.Join(pendingDir(home, projectID), "code")
	var notes []string
	if pc.Project.Bundle {
		if out, err := git(ctx, folder, "fetch", filepath.Join(code, "repo.bundle"), "+refs/heads/*:refs/remotes/office-export/*"); err != nil {
			notes = append(notes, "không lấy được commit chưa push: "+lastLine(out))
		} else {
			notes = append(notes, "commit chưa push ở nhánh office-export/*")
		}
	}
	if pc.Project.Patch {
		patch := filepath.Join(code, "changes.patch")
		if _, err := git(ctx, folder, "apply", "--check", "--binary", patch); err != nil {
			notes = append(notes, "thay đổi chưa commit không áp sạch được lên thư mục này, giữ lại file patch")
			_ = os.MkdirAll(filepath.Join(folder, ".office-import"), 0o755)
			_ = copyFile(patch, filepath.Join(folder, ".office-import", "changes.patch"))
		} else if _, err := git(ctx, folder, "apply", "--binary", "--whitespace=nowarn", patch); err == nil {
			notes = append(notes, "đã áp thay đổi chưa commit")
		}
	}
	if pc.Project.Untracked > 0 {
		n := 0
		_ = filepath.Walk(filepath.Join(code, "untracked"), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(filepath.Join(code, "untracked"), p)
			t := filepath.Join(folder, rel)
			if _, err := os.Lstat(t); err == nil {
				return nil // theirs stays
			}
			_ = os.MkdirAll(filepath.Dir(t), 0o755)
			if moveFile(p, t, info) == nil {
				n++
			}
			return nil
		})
		notes = append(notes, fmt.Sprintf("%d file chưa track", n))
	}
	return strings.Join(notes, "; "), DropPending(home, projectID)
}

func anyRemote(remotesV, want string) bool {
	for _, line := range strings.Split(remotesV, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && SameRemote(f[1], want) {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o600)
}

// SameRemote compares git remotes without scheme, user, ".git" and case
// (git@host:a/b.git is https://host/a/b).
func SameRemote(a, b string) bool {
	norm := func(u string) string {
		u = strings.ToLower(strings.TrimSpace(u))
		u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
		if i := strings.Index(u, "://"); i >= 0 {
			u = u[i+3:]
		}
		if i := strings.Index(u, "@"); i >= 0 {
			u = u[i+1:]
		}
		return strings.Replace(u, ":", "/", 1)
	}
	return a != "" && norm(a) == norm(b)
}

// ProbeRemote says whether this machine may read url: "" when it may, else
// a Reason* and git's words. It never prompts (no terminal, SSH in batch mode).
func ProbeRemote(ctx context.Context, url string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := git(ctx, os.TempDir(), "ls-remote", "--heads", url)
	if err == nil {
		return "", ""
	}
	if ctx.Err() != nil {
		return ReasonNetwork, "quá 20 giây không trả lời"
	}
	return ClassifyCloneError(out), lastLine(out)
}
