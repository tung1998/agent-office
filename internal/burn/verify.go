package burn

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
	verifier "github.com/tung1998/agent-office/internal/verify"
)

// A Burn's checks (ADR-131): a worker reporting done is not taken at its
// word. Office runs the checks itself in the piece's worktree, one command a
// line (the Burn's, else guessed from the project); one failing, the piece
// goes back to the worker with the output. No AI: it costs nothing. Running
// them is package verify's, shared with a chat's diff (ADR-222).

// maxVerify caps the checks the person writes.
const maxVerify = 2000

// unverified marks a piece no check ran on (the Burn has none, none guessed).
const unverified = "(chưa kiểm chứng tự động: Burn chưa có lệnh kiểm chứng)"

// verifyCommands are the checks of b in dir: the Burn's own lines, else
// what the project's files suggest (none known: no check).
func verifyCommands(b storage.BurnSession, dir string) []string {
	var out []string
	for _, l := range strings.Split(b.Verify, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	if dir == "" {
		return out
	}
	if len(out) == 0 {
		out = verifier.Guess(dir)
	}
	if templateOf(b) == "builder" { // what the features done say they pass (ADR-139)
		out = append(out, acceptance(dir)...)
	}
	return out
}

// ValidVerify checks what the person wrote as the Burn's checks.
func ValidVerify(v string) error {
	if len([]rune(v)) > maxVerify {
		return fmt.Errorf("lệnh kiểm chứng dài quá %d ký tự", maxVerify)
	}
	return nil
}

// verify runs the checks of b on a piece in its worktree: "" when they pass
// (or there are none), else which failed and its output's end.
func verify(ctx context.Context, b storage.BurnSession, dir string) string {
	return verifier.Run(ctx, verifyCommands(b, dir), dir)
}

// checked runs the checks on a piece its worker reported done (before its
// result's review and before it is merged): false when one failed, the
// piece then going back to its worker with the output (ADR-131).
func (s *Service) checked(ctx context.Context, b storage.BurnSession, it *storage.BurnItem) bool {
	failed := verify(ctx, b, it.Worktree)
	if ctx.Err() != nil { // stopped meanwhile: checked again when it goes on
		it.Status = "paused"
		return false
	}
	if failed == "" {
		if it.Worktree != "" && !strings.Contains(it.Summary, unverified) && len(verifyCommands(b, it.Worktree)) == 0 { // nothing ran: the person reads it with care
			it.Summary = strings.TrimSpace(it.Summary + " " + unverified)
		}
		return true
	}
	it.ReviewNote = "Kiểm chứng tự động chưa qua, sửa cho qua rồi báo burn_done lại:\n" + failed
	it.Status, it.Summary = s.failedOrAgain(*it, "Kiểm chứng tự động chưa qua: "+oneLine(failed, 300))
	return false
}

// maxLessons caps the lessons kept (ADR-131).
const maxLessons = 3000

// maxSetbacks caps the pieces turned down or failed a scan is shown.
const maxSetbacks = 15

// setbacks are the latest pieces turned down (skipped with a reason) or
// failed, newest first: what a scan learns lessons from.
func setbacks(items []storage.BurnItem) []string {
	var list []storage.BurnItem
	for _, it := range items {
		// a quest turned down teaches nothing about what to look for: the person chose it
		if !hunting(it) && it.Kind != KindQuest && it.Summary != "" && (it.Status == "failed" || it.Status == "skipped") {
			list = append(list, it)
		}
	}
	slices.SortStableFunc(list, func(a, b storage.BurnItem) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	var out []string
	for _, g := range groupByPiece(list) { // the same piece tried again: once, with its count
		if len(out) == maxSetbacks {
			break
		}
		out = append(out, fmt.Sprintf("[%s%s] %s: %s", g.it.Status, times(g.n), oneLine(g.it.Title, 100), oneLine(g.it.Summary, 240)))
	}
	return out
}

// pieceKey is what tells one piece from another across its tries: its
// title without the parts in brackets, and for "<id>: <title>" its id.
func pieceKey(title string) string {
	var sb strings.Builder
	depth := 0
	for _, r := range title {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0:
			sb.WriteRune(r)
		}
	}
	key := strings.ToLower(strings.Join(strings.Fields(sb.String()), " "))
	if id, _, ok := strings.Cut(key, ":"); ok {
		if id = strings.TrimSpace(id); id != "" && !strings.Contains(id, " ") {
			return id
		}
	}
	return key
}

// tries is one piece across its tries: the first (latest) of them and how
// many there are.
type tries struct {
	it storage.BurnItem
	n  int
}

// groupByPiece folds the tries of a piece into one, keeping the order.
func groupByPiece(items []storage.BurnItem) []tries {
	var out []tries
	at := map[string]int{}
	for _, it := range items {
		k := pieceKey(it.Title)
		if i, ok := at[k]; ok {
			out[i].n++
			continue
		}
		at[k] = len(out)
		out = append(out, tries{it, 1})
	}
	return out
}

// times is " ×n" past one.
func times(n int) string {
	if n < 2 {
		return ""
	}
	return fmt.Sprintf(" ×%d", n)
}

// keepLessons saves what a scan wrote as the Burn's lessons.
func (s *Service) keepLessons(ctx context.Context, b storage.BurnSession, lessons string) {
	lessons = strings.TrimSpace(lessons)
	if lessons == "" {
		return
	}
	if r := []rune(lessons); len(r) > maxLessons {
		lessons = string(r[:maxLessons])
	}
	_ = s.store.Burn().SetLessons(context.WithoutCancel(ctx), b.ID, lessons)
}
