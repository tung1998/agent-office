package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Fingerprint is the source fingerprint of the running build, stamped by
// "Cập nhật office" (-ldflags -X); "" for a build made by hand.
var Fingerprint string

const fingerprintVar = "github.com/tung1998/agent-office/internal/selfupdate.Fingerprint"

// gitTimeout bounds each git call of a source check.
const gitTimeout = 10 * time.Second

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
}

// SourceFingerprint hashes what a build of root would contain: HEAD, the
// uncommitted changes and the files git does not know yet (ignored ones
// left out). Two equal fingerprints mean the same code.
func SourceFingerprint(ctx context.Context, root string) (string, error) {
	head, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", errors.New("không đọc được git của mã nguồn")
	}
	diff, err := git(ctx, root, "diff", "HEAD", "--binary")
	if err != nil {
		return "", err
	}
	others, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(bytes.TrimSpace(head))
	h.Write([]byte{0})
	h.Write(diff)
	for _, name := range strings.Split(string(others), "\x00") {
		if name == "" {
			continue
		}
		h.Write([]byte{0})
		h.Write([]byte(name))
		h.Write([]byte{0})
		if raw, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			h.Write(raw)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Commit is one commit the running build does not have.
type Commit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	At      string `json:"at"`
}

// Changes says whether the source differs from the running build.
type Changes struct {
	Changed bool `json:"changed"`
	// fingerprints of the source now and of the running build ("" built by hand)
	Fingerprint string `json:"fingerprint"`
	Running     string `json:"running,omitempty"`
	Head        string `json:"head"`
	// commits since the running build, newest first, at most maxCommits
	Commits []Commit `json:"commits"`
	More    bool     `json:"more"`
	// files changed or new, not committed
	Uncommitted int `json:"uncommitted"`
	// the running revision is not in the source
	Unknown bool `json:"unknown,omitempty"`
}

const maxCommits = 20

// Changes compares the source with the running build (revision: what the Go
// toolchain stamped in it). With a stamped fingerprint the answer is
// exact; a build made by hand is compared by commit, and counts as changed
// while the source has uncommitted files.
func (s Source) Changes(ctx context.Context, revision string) (Changes, error) {
	fp, err := SourceFingerprint(ctx, s.Root)
	if err != nil {
		return Changes{}, err
	}
	head, _ := git(ctx, s.Root, "rev-parse", "HEAD")
	c := Changes{Fingerprint: fp, Running: Fingerprint, Head: strings.TrimSpace(string(head)), Commits: []Commit{}}
	if st, err := git(ctx, s.Root, "status", "--porcelain"); err == nil {
		for _, l := range strings.Split(string(st), "\n") {
			if strings.TrimSpace(l) != "" {
				c.Uncommitted++
			}
		}
	}
	if revision != "" && revision != c.Head {
		out, err := git(ctx, s.Root, "log", "--format=%h%x00%s%x00%cI", "-n", "21", revision+"..HEAD", "--")
		if err != nil {
			c.Unknown = true
		}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f := strings.SplitN(l, "\x00", 3); len(f) == 3 {
				c.Commits = append(c.Commits, Commit{Hash: f[0], Subject: f[1], At: f[2]})
			}
		}
		if len(c.Commits) > maxCommits {
			c.Commits, c.More = c.Commits[:maxCommits], true
		}
	}
	if Fingerprint != "" {
		c.Changed = fp != Fingerprint
	} else {
		c.Changed = revision == "" || revision != c.Head || c.Uncommitted > 0
	}
	return c, nil
}
