package burn

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// One run, one result (ADR-123): the run's own worktree, on the run's own
// branch, a commit per finished piece; the person reviews it there and
// merges the branch. Pieces still run side by side, each in a worktree
// started from the run as it is then, and are merged into it one at a time.

// baseRef is where a piece's worktree started, per worktree: its diff is
// taken from there, whatever the agent committed.
const baseRef = "refs/worktree/burn-base"

// errConflict: a piece's changes no longer apply to the run (another piece
// changed the same lines meanwhile).
var errConflict = errors.New("xung đột với việc khác đã gộp")

// newRunBranch names a new run's branch: burn/<when it started>.
func newRunBranch(now time.Time) string {
	return "burn/" + now.Local().Format("2006-01-02-150405")
}

// RunStarted reads when a run started from its branch name, written in this
// machine's time zone: the dashboard may be in another.
func RunStarted(branch string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02-150405", strings.TrimPrefix(branch, "burn/"), time.Local)
	return t, err == nil
}

// RunChats pairs each run with its chat: a run starts with a chat of its own
// (Start), saved only as the Burn's current one, so an earlier run's is the
// latest Burn chat made between its start and the next run's. A chat made
// mid-run (its agent was changed) is the later one, the one it went on in.
func RunChats(runs map[string]time.Time, current, currentChat string, chats []storage.Conversation) map[string]string {
	type run struct {
		branch string
		at     time.Time
	}
	order := make([]run, 0, len(runs))
	for b, at := range runs {
		order = append(order, run{b, at})
	}
	slices.SortFunc(order, func(a, b run) int { return a.at.Compare(b.at) })
	out := map[string]string{}
	for i, r := range order {
		if r.branch == current && currentChat != "" {
			out[r.branch] = currentChat
			continue
		}
		var best storage.Conversation
		for _, c := range chats {
			if c.Purpose != "burn" || c.CreatedAt.Before(r.at.Add(-5*time.Second)) {
				continue
			}
			if i+1 < len(order) && !c.CreatedAt.Before(order[i+1].at.Add(-5*time.Second)) {
				continue
			}
			if best.ID == "" || c.CreatedAt.After(best.CreatedAt) {
				best = c
			}
		}
		if best.ID != "" {
			out[r.branch] = best.ID
		}
	}
	return out
}

// runTree is the name of the run's worktree.
func runTree(b storage.BurnSession) string {
	return "burn-run-" + strings.TrimPrefix(b.RunBranch, "burn/")
}

// ensureRun gives the run its worktree, made once, on the run's branch (from
// the project's HEAD, without its uncommitted changes). Called with runMu held.
func (s *Service) ensureRun(ctx context.Context, b storage.BurnSession, repo string) (string, error) {
	if b.RunBranch == "" {
		return "", errors.New("Burn chưa có nhánh của lần chạy")
	}
	name := runTree(b)
	fresh := !s.trees.Exists(b.ProjectID, name)
	dir, err := s.trees.Ensure(ctx, repo, b.ProjectID, name, nil)
	if err != nil || !fresh {
		return dir, err
	}
	if _, err := gitIn(ctx, dir, "", "rev-parse", "--verify", "-q", "refs/heads/"+b.RunBranch); err == nil {
		_, err = gitIn(ctx, dir, "", "switch", "--discard-changes", b.RunBranch) // made again: the branch goes on
		return dir, err
	}
	head, err := gitIn(ctx, repo, "", "rev-parse", "HEAD")
	if err != nil {
		return dir, err
	}
	if _, err := gitIn(ctx, dir, "", "reset", "--hard", head); err != nil {
		return dir, err
	}
	_, err = gitIn(ctx, dir, "", "switch", "-c", b.RunBranch)
	return dir, err
}

// pieceTree gives a piece its worktree, started from the run as it is now (a
// retry goes on from what is there).
func (s *Service) pieceTree(ctx context.Context, b storage.BurnSession, repo, name string) (string, error) {
	if s.trees.Exists(b.ProjectID, name) {
		return s.trees.Path(b.ProjectID, name), nil
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	run, err := s.ensureRun(ctx, b, repo)
	if err != nil {
		return "", err
	}
	base, err := gitIn(ctx, run, "", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	dir, err := s.trees.Ensure(ctx, repo, b.ProjectID, name, nil)
	if err != nil {
		return "", err
	}
	for _, args := range [][]string{{"reset", "--hard", base}, {"update-ref", baseRef, base}} {
		if _, err := gitIn(ctx, dir, "", args...); err != nil {
			_ = s.trees.Remove(ctx, repo, b.ProjectID, name)
			return "", err
		}
	}
	return dir, nil
}

// integrate merges a finished piece into the run (a commit on its branch) and
// drops the piece's worktree.
func (s *Service) integrate(ctx context.Context, b storage.BurnSession, it *storage.BurnItem) error {
	if it.Worktree == "" {
		return errors.New("không có worktree")
	}
	p, err := s.store.Repos().Get(ctx, b.ProjectID)
	if err != nil {
		return err
	}
	if _, err := gitIn(ctx, it.Worktree, "", "add", "-A"); err != nil {
		return err
	}
	base, err := gitIn(ctx, it.Worktree, "", "rev-parse", "--verify", "-q", baseRef)
	if err != nil {
		base = "HEAD" // started before ADR-123
	}
	diff, err := gitDiff(ctx, it.Worktree, base)
	if err != nil {
		return err
	}
	if diff != "" {
		if names, err := gitIn(ctx, it.Worktree, "", "diff", "--cached", "--name-only", "--no-renames", base); err == nil {
			it.Files = strings.Fields(names) // what it touched, for the run's summary (ADR-131)
		}
		s.runMu.Lock()
		err = s.mergeInto(ctx, b, p.Path, *it, diff)
		s.runMu.Unlock()
		if err != nil {
			return err
		}
		it.Branch = b.RunBranch
	}
	if err := s.trees.Remove(ctx, p.Path, b.ProjectID, "burn-"+it.ID); err == nil {
		it.Worktree = ""
	}
	return nil
}

// mergeInto applies a piece's diff to the run. Called with runMu held.
func (s *Service) mergeInto(ctx context.Context, b storage.BurnSession, repo string, it storage.BurnItem, diff string) error {
	run, err := s.ensureRun(ctx, b, repo)
	if err != nil {
		return err
	}
	// 3-way: lines another piece moved do not stop it, only the same lines changed
	if out, err := gitIn(ctx, run, diff, "apply", "--binary", "--3way", "--whitespace=nowarn", "-"); err != nil {
		_, _ = gitIn(ctx, run, "", "reset", "-q", "--hard", "HEAD") // a clash leaves markers: the run stays as it was
		_, _ = gitIn(ctx, run, "", "clean", "-fdq")
		return fmt.Errorf("%w: %s", errConflict, oneLine(out, 300))
	}
	if err := commit(ctx, run, b.RunBranch, it.Title, it.Summary); err != nil {
		_, _ = gitIn(ctx, run, "", "reset", "-q", "--hard", "HEAD") // a failed commit must not leave this piece's changes for the next commit to absorb
		_, _ = gitIn(ctx, run, "", "clean", "-fdq")
		return err
	}
	return nil
}

// RunTree is where a project's latest Burn run is, to review: its worktree
// ("" when there is none).
func (s *Service) RunTree(b storage.BurnSession) string {
	if b.RunBranch == "" || s.trees == nil || !s.trees.Exists(b.ProjectID, runTree(b)) {
		return ""
	}
	return s.trees.Path(b.ProjectID, runTree(b))
}

// replayItem is replay for a piece of the project's Burn.
func (s *Service) replayItem(ctx context.Context, b storage.BurnSession, it storage.BurnItem) ([]string, error) {
	p, err := s.store.Repos().Get(ctx, b.ProjectID)
	if err != nil {
		return nil, err
	}
	return s.replay(ctx, b, p.Path, it)
}

// replay puts a piece's changes on top of the run as it is now: a 3-way
// merge in the piece's own worktree, so a clash with a piece merged meanwhile
// costs a fix, not the whole piece. It returns the files left with conflict
// markers (none: it merges cleanly now). Failing, the worktree is as it was.
func (s *Service) replay(ctx context.Context, b storage.BurnSession, repo string, it storage.BurnItem) ([]string, error) {
	dir := it.Worktree
	if dir == "" {
		return nil, errors.New("không có worktree")
	}
	if _, err := gitIn(ctx, dir, "", "add", "-A"); err != nil {
		return nil, err
	}
	base, err := gitIn(ctx, dir, "", "rev-parse", "--verify", "-q", baseRef)
	if err != nil {
		return nil, errors.New("worktree không có mốc bắt đầu")
	}
	diff, err := gitDiff(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	s.runMu.Lock()
	run, err := s.ensureRun(ctx, b, repo)
	var head string
	if err == nil {
		head, err = gitIn(ctx, run, "", "rev-parse", "HEAD")
	}
	s.runMu.Unlock()
	if err != nil {
		return nil, err
	}
	for _, args := range [][]string{{"reset", "-q", "--hard", head}, {"clean", "-fdq"}, {"update-ref", baseRef, head}} {
		if _, err := gitIn(ctx, dir, "", args...); err != nil {
			return nil, err
		}
	}
	if diff == "" {
		return nil, nil
	}
	_, aerr := gitIn(ctx, dir, diff, "apply", "--binary", "--3way", "--whitespace=nowarn", "-")
	if aerr == nil {
		return nil, nil
	}
	names, _ := gitIn(ctx, dir, "", "diff", "--name-only", "--diff-filter=U")
	if files := strings.Fields(names); len(files) > 0 {
		return files, nil
	}
	// not even a 3-way merge: back to where it was
	for _, args := range [][]string{{"reset", "-q", "--hard", base}, {"clean", "-fdq"}, {"update-ref", baseRef, base}} {
		_, _ = gitIn(ctx, dir, "", args...)
	}
	_, _ = gitIn(ctx, dir, diff, "apply", "--binary", "--index", "--whitespace=nowarn", "-")
	return nil, aerr
}

// gitDiff is what dir's index changed since base, as a patch, untrimmed:
// gitIn trims, and a hunk ending on a blank context line (a lone " ") then
// loses it, so the patch no longer applies ("corrupt patch") on every try.
func gitDiff(ctx context.Context, dir, base string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotepath=false", "diff", "--cached", "--binary", "--no-renames", base)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return string(out), nil
}

func gitIn(ctx context.Context, dir, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return strings.TrimSpace(stderr.String() + "\n" + string(out)), err
	}
	return strings.TrimSpace(string(out)), nil
}
