// Package gitops runs the few git operations office allows: reading status,
// diffs and history, committing chosen files, creating a branch and pushing
// the current branch (never forced).
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Change is one changed file from `git status`.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"` // M, A, D, R, ?? (untracked)…
}

// Status describes the working tree.
type Status struct {
	Branch   string   `json:"branch"`
	Upstream string   `json:"upstream,omitempty"`
	Ahead    int      `json:"ahead"`
	Behind   int      `json:"behind"`
	Changes  []Change `json:"changes"`
}

var ErrNotRepo = errors.New("thư mục project không phải git repo")

// commitLocks serializes Commit per repo root: two approvals of the same
// project committing at once would otherwise race on git's own
// .git/index.lock and one fails with a confusing git error instead of
// just waiting its turn.
var (
	commitLocksMu sync.Mutex
	commitLocks   = map[string]*sync.Mutex{}
)

func commitLock(root string) func() {
	key := root
	if abs, err := filepath.Abs(root); err == nil {
		key = abs
	}
	commitLocksMu.Lock()
	l := commitLocks[key]
	if l == nil {
		l = &sync.Mutex{}
		commitLocks[key] = l
	}
	commitLocksMu.Unlock()
	l.Lock()
	return l.Unlock
}

func git(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // never wait for a password prompt (fetch/push)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "not a git repository") {
			return "", ErrNotRepo
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

// ReadStatus parses `git status --porcelain=v1 -b`.
// CurrentBranch is the branch checked out (a repo with no commit yet too);
// detached, the short commit; "" when root is not a git repo.
func CurrentBranch(ctx context.Context, root string) string {
	if b, err := git(ctx, root, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && strings.TrimSpace(b) != "" {
		return strings.TrimSpace(b)
	}
	if h, err := git(ctx, root, "rev-parse", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(h)
	}
	return ""
}

func ReadStatus(ctx context.Context, root string) (Status, error) {
	out, err := git(ctx, root, "status", "--porcelain=v1", "-b", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	st := Status{Changes: []Change{}}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			// "main...origin/main [ahead 1, behind 2]"
			head, info, _ := strings.Cut(rest, " [")
			st.Branch, st.Upstream, _ = strings.Cut(head, "...")
			for _, part := range strings.Split(strings.TrimSuffix(info, "]"), ", ") {
				var n int
				if _, err := fmt.Sscanf(part, "ahead %d", &n); err == nil {
					st.Ahead = n
				} else if _, err := fmt.Sscanf(part, "behind %d", &n); err == nil {
					st.Behind = n
				}
			}
			continue
		}
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if _, to, ok := strings.Cut(path, " -> "); ok {
			path = to
		}
		st.Changes = append(st.Changes, Change{Path: strings.Trim(path, `"`), Status: strings.TrimSpace(line[:2])})
	}
	return st, nil
}

// Diff returns the working-tree diff (tracked files), optionally for some files.
func Diff(ctx context.Context, root string, files []string, maxBytes int) (string, error) {
	args := append([]string{"diff", "HEAD", "--"}, files...)
	out, err := git(ctx, root, args...)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes] + "\n… (cắt bớt)"
	}
	return out, nil
}

// Ignored returns which of paths (relative, folders ending in "/") git ignores.
// Not a git repo, or git missing: none.
func Ignored(ctx context.Context, root string, paths []string) map[string]bool {
	out := map[string]bool{}
	if len(paths) == 0 {
		return out
	}
	// exit 1 means "none ignored": the output is what counts
	res, _ := git(ctx, root, append([]string{"-c", "core.quotePath=false", "check-ignore", "--"}, paths...)...)
	for _, l := range strings.Split(res, "\n") {
		if l != "" {
			out[strings.Trim(l, `"`)] = true
		}
	}
	return out
}

// Log returns the last n commits, one per line.
func Log(ctx context.Context, root string, n int) (string, error) {
	return git(ctx, root, "log", fmt.Sprintf("-%d", n), "--pretty=format:%h %ad %an: %s", "--date=short")
}

// Commit stages exactly files (new, changed or deleted) and commits them.
func Commit(ctx context.Context, root, message string, files []string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("commit message trống")
	}
	if len(files) == 0 {
		return "", errors.New("chưa chọn file để commit")
	}
	defer commitLock(root)()
	if _, err := git(ctx, root, append([]string{"add", "-A", "--"}, files...)...); err != nil {
		return "", err
	}
	if _, err := git(ctx, root, append([]string{"commit", "-m", message, "--"}, files...)...); err != nil {
		return "", err
	}
	hash, err := git(ctx, root, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(hash), err
}

// CreateBranch creates and switches to a new branch (local changes follow).
func CreateBranch(ctx context.Context, root, name string) error {
	if err := checkBranch(ctx, root, name); err != nil {
		return err
	}
	_, err := git(ctx, root, "switch", "-c", name)
	return err
}

// Branch is a local branch.
type Branch struct {
	Name     string `json:"name"`
	Current  bool   `json:"current"`
	Upstream string `json:"upstream,omitempty"`
	Track    string `json:"track,omitempty"` // "[ahead 1, behind 2]", "[gone]"…
	Date     string `json:"date"`            // of its last commit (ISO)
	Subject  string `json:"subject"`         // its last commit's
}

// Branches lists the local branches, the latest commit first.
func Branches(ctx context.Context, root string) ([]Branch, error) {
	out, err := git(ctx, root, "for-each-ref", "--sort=-committerdate",
		"--format=%(HEAD)%00%(refname:short)%00%(upstream:short)%00%(upstream:track)%00%(committerdate:iso-strict)%00%(contents:subject)", "refs/heads")
	if err != nil {
		return nil, err
	}
	list := []Branch{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) < 6 {
			continue
		}
		list = append(list, Branch{Current: f[0] == "*", Name: f[1], Upstream: f[2], Track: f[3], Date: f[4], Subject: f[5]})
	}
	return list, nil
}

func checkBranch(ctx context.Context, root, name string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("tên nhánh không hợp lệ: %s", name)
	}
	if _, err := git(ctx, root, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("tên nhánh không hợp lệ: %s", name)
	}
	return nil
}

// SwitchBranch checks out an existing local branch; git refuses when local
// changes would be lost (they are carried over otherwise).
func SwitchBranch(ctx context.Context, root, name string) error {
	if err := checkBranch(ctx, root, name); err != nil {
		return err
	}
	_, err := git(ctx, root, "switch", "--no-guess", name)
	return err
}

// DeleteBranch deletes a local branch that is merged (never forced, never the current one).
func DeleteBranch(ctx context.Context, root, name string) error {
	if err := checkBranch(ctx, root, name); err != nil {
		return err
	}
	if CurrentBranch(ctx, root) == name {
		return errors.New("không xóa được nhánh đang dùng")
	}
	_, err := git(ctx, root, "branch", "-d", "--", name)
	return err
}

// Tracked: path is known to git (an untracked file has no diff against HEAD).
func Tracked(ctx context.Context, root, path string) bool {
	out, err := git(ctx, root, "ls-files", "--", path)
	return err == nil && strings.TrimSpace(out) != ""
}

// Push pushes the current branch to its remote (setting the upstream the
// first time). Never forced.
func Push(ctx context.Context, root string) (string, error) {
	st, err := ReadStatus(ctx, root)
	if err != nil {
		return "", err
	}
	if st.Branch == "" || strings.HasPrefix(st.Branch, "HEAD") {
		return "", errors.New("không ở trên nhánh nào (detached HEAD)")
	}
	args := []string{"push"}
	if st.Upstream == "" {
		args = append(args, "-u", "origin", st.Branch)
	}
	out, err := git(ctx, root, args...)
	return strings.TrimSpace(out), err
}

// Fetch updates the remote-tracking branches (read-only for the working
// tree), so ahead/behind in ReadStatus is current.
func Fetch(ctx context.Context, root string) error {
	_, err := git(ctx, root, "fetch", "--quiet", "--prune")
	return err
}

// BranchDiff fetches base and the first of heads origin has (a PR's own ref,
// then its branch: a PR from a fork has only the ref) and returns what head
// adds to base, with a summary on top.
func BranchDiff(ctx context.Context, root, base string, heads []string, max int) (string, error) {
	if root == "" {
		return "", errors.New("project chưa có thư mục")
	}
	if base == "" || strings.HasPrefix(base, "-") {
		return "", errors.New("thiếu nhánh")
	}
	if _, err := git(ctx, root, "fetch", "--no-tags", "origin", base); err != nil {
		return "", err
	}
	head, why := "", "thiếu nhánh"
	for _, h := range heads {
		if h == "" || strings.HasPrefix(h, "-") {
			continue
		}
		_, err := git(ctx, root, "fetch", "--no-tags", "origin", h)
		if err != nil {
			why = err.Error()
			continue
		}
		sha, err := git(ctx, root, "rev-parse", "FETCH_HEAD")
		if err == nil {
			head = strings.TrimSpace(sha)
			break
		}
	}
	if head == "" {
		return "", errors.New(why)
	}
	rng := "origin/" + base + "..." + head
	stat, err := git(ctx, root, "diff", "--stat", rng)
	if err != nil {
		return "", err
	}
	diff, err := git(ctx, root, "diff", rng)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(stat) + "\n\n" + diff
	if len(out) > max {
		out = out[:max] + "\n… (diff quá dài, đã cắt; xem thêm bằng git diff)"
	}
	return out, nil
}
