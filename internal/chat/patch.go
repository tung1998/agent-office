package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var diffBlockRe = regexp.MustCompile("(?s)```(?:diff|patch)[ \t]*\n(.*?)```")

// ExtractPatches finds unified diffs in an answer (```diff blocks with ---/+++ headers).
func ExtractPatches(text string) []string {
	var out []string
	for _, m := range diffBlockRe.FindAllStringSubmatch(text, -1) {
		d := strings.TrimSpace(m[1]) + "\n"
		if strings.Contains(d, "\n+++ ") || strings.HasPrefix(d, "--- ") {
			out = append(out, d)
		}
	}
	return out
}

var fileHeaderRe = regexp.MustCompile(`(?m)^(?:---|\+\+\+) (?:[ab]/)?([^\t\n]+)`)

// PatchFiles lists the files a diff touches and rejects unsafe paths.
func PatchFiles(diff string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, m := range fileHeaderRe.FindAllStringSubmatch(diff, -1) {
		p := strings.TrimSpace(m[1])
		if p == "/dev/null" {
			continue
		}
		if filepath.IsAbs(p) || strings.HasPrefix(filepath.Clean(p), "..") || strings.Contains(p, "/../") {
			return nil, fmt.Errorf("diff chứa đường dẫn không an toàn: %s", p)
		}
		if strings.HasPrefix(p, ".git/") || p == ".git" || strings.HasPrefix(p, ".office/") {
			return nil, fmt.Errorf("diff không được sửa %s", p)
		}
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("không thấy file nào trong diff")
	}
	return files, nil
}

// CheckPatch verifies a diff applies cleanly without touching files.
func CheckPatch(ctx context.Context, root, diff string) error {
	return gitApply(ctx, root, diff, true)
}

// ApplyPatch writes a diff to the project (after a person approved it).
func ApplyPatch(ctx context.Context, root, diff string) error {
	if _, err := PatchFiles(diff); err != nil {
		return err
	}
	if err := gitApply(ctx, root, diff, true); err != nil {
		return err
	}
	return gitApply(ctx, root, diff, false)
}

// withGitHeaders adds a "diff --git" line before each file section that has
// none. With --recount, git reads a hunk until the next header; without one,
// the next file's "--- a/x" line is taken as a removed line of the previous
// hunk, so multi-file diffs from agents (and batches) would not apply.
func withGitHeaders(diff string) string {
	lines := strings.Split(diff, "\n")
	var b strings.Builder
	for i, l := range lines {
		if strings.HasPrefix(l, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") &&
			(i == 0 || !strings.HasPrefix(lines[i-1], "diff --git") && !strings.HasPrefix(lines[i-1], "index ") &&
				!strings.HasPrefix(lines[i-1], "new file mode") && !strings.HasPrefix(lines[i-1], "deleted file mode")) {
			oldName := strings.Fields(strings.TrimPrefix(l, "--- "))[0]
			name := strings.TrimPrefix(strings.Fields(strings.TrimPrefix(lines[i+1], "+++ "))[0], "b/")
			mode := ""
			switch {
			case name == "/dev/null": // deleted file
				name, mode = strings.TrimPrefix(oldName, "a/"), "deleted file mode 100644\n"
			case oldName == "/dev/null": // new file
				mode = "new file mode 100644\n"
			}
			fmt.Fprintf(&b, "diff --git a/%s b/%s\n%s", name, name, mode)
		}
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func gitApply(ctx context.Context, root, diff string, check bool, extra ...string) error {
	diff = withGitHeaders(diff)
	args := append([]string{"apply", "--recount", "--whitespace=nowarn"}, extra...)
	if check {
		args = append(args, "--check")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(diff)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("không áp được diff: %s", msg)
	}
	return nil
}

// joinDiffs concatenates diffs into one patch (each on its own lines).
func joinDiffs(diffs []string) string {
	var b strings.Builder
	for _, d := range diffs {
		b.WriteString(d)
		if !strings.HasSuffix(d, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ApplyBatch applies several diffs as one: either all of them apply or none.
func ApplyBatch(ctx context.Context, root string, diffs []string) error {
	all := joinDiffs(diffs)
	if err := gitApply(ctx, root, all, true); err != nil {
		return err
	}
	return gitApply(ctx, root, all, false)
}

// RevertBatch takes back diffs applied earlier, all or none.
func RevertBatch(ctx context.Context, root string, diffs []string) error {
	rev := make([]string, len(diffs))
	for i, d := range diffs {
		rev[len(diffs)-1-i] = d
	}
	all := joinDiffs(rev)
	if err := gitApply(ctx, root, all, true, "-R"); err != nil {
		return fmt.Errorf("không hoàn tác được (file đã đổi sau khi áp?): %w", err)
	}
	return gitApply(ctx, root, all, false, "-R")
}

// CheckBatch reports whether diffs would apply together (nothing is changed).
func CheckBatch(ctx context.Context, root string, diffs []string) error {
	return gitApply(ctx, root, joinDiffs(diffs), true)
}
