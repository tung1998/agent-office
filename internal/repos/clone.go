package repos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// cloneLocks serializes the check-then-clone-then-cleanup-on-fail sequence
// per destination path: two concurrent clones into the same dest must not
// let one's cleanup (on failure) delete the other's successful clone.
var (
	cloneLocksMu sync.Mutex
	cloneLocks   = map[string]*sync.Mutex{}
)

func cloneLock(dest string) func() {
	cloneLocksMu.Lock()
	lk := cloneLocks[dest]
	if lk == nil {
		lk = &sync.Mutex{}
		cloneLocks[dest] = lk
	}
	cloneLocksMu.Unlock()
	lk.Lock()
	return lk.Unlock
}

var (
	// ErrCloneURL: not a git URL office will clone (https, ssh, git or user@host:path).
	ErrCloneURL = errors.New("link repo không hợp lệ")
	// ErrCloneDir: the folder name is not a plain name.
	ErrCloneDir = errors.New("tên thư mục không hợp lệ")
	// ErrCloneExists: the target folder is already there.
	ErrCloneExists = errors.New("thư mục đích đã tồn tại")
)

// CloneError is git's own reason, credentials stripped.
type CloneError struct{ Output string }

func (e *CloneError) Error() string { return "git clone lỗi: " + e.Output }

var (
	urlRe  = regexp.MustCompile(`^(https?|ssh|git)://[^\s]+$`)
	scpRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^\s]+$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// CloneURL checks a pasted repo link: remote transports only (no local paths,
// file:// or ext:: helpers) and never something git could read as an option.
func CloneURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" || strings.HasPrefix(u, "-") || (!urlRe.MatchString(u) && !scpRe.MatchString(u)) {
		return "", ErrCloneURL
	}
	return u, nil
}

// CloneDirName is the folder a link clones into: its last segment without .git.
func CloneDirName(url string) string {
	u := strings.TrimRight(url, "/")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(u, ".git")
}

// StripCredentials drops user:token@ from every URL in s.
func StripCredentials(s string) string {
	return credRe.ReplaceAllString(s, "$1")
}

var credRe = regexp.MustCompile(`([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

// Clone runs git clone of url into parent/name and returns the new folder.
// git never prompts (no terminal): a private repo needs credentials the
// machine already has (ssh key, credential helper). A failed clone leaves nothing.
func Clone(ctx context.Context, url, parent, name string) (string, error) {
	u, err := CloneURL(url)
	if err != nil {
		return "", err
	}
	return cloneInto(ctx, u, parent, name)
}

// cloneInto is Clone after the URL check (tests clone a local repo with it).
func cloneInto(ctx context.Context, u, parent, name string) (string, error) {
	var err error
	if name == "" {
		name = CloneDirName(u)
	}
	if !nameRe.MatchString(name) || name == "." || name == ".." {
		return "", ErrCloneDir
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(parent); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", ErrNotDir
	}
	dest := filepath.Join(parent, name)
	defer cloneLock(dest)()
	if _, err := os.Lstat(dest); err == nil {
		return "", ErrCloneExists
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--", u, dest)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") // no passphrase prompt
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dest)
		msg := strings.TrimSpace(StripCredentials(string(out)))
		if lines := strings.Split(msg, "\n"); len(lines) > 4 {
			msg = strings.Join(lines[len(lines)-4:], "\n")
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", &CloneError{Output: msg}
	}
	return dest, nil
}
