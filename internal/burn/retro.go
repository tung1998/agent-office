package burn

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// The Burn watches its own run (ADR-143): what goes wrong again and again is
// handled while it runs, not left to loop, and each run ends with a look
// back at what went wrong and what the process lacks, so the next run is
// set up better.
//
// While it runs (no AI): a piece that failed maxTries times is not recorded
// again; systemicFails pieces in a row failing for the same reason stop the
// Burn taking new work (it drains) and say why. When a run ends: its
// numbers (failures by reason, reviews turned down, tries, coverage) close
// the summary, and the Burn's agent reads them in its chat (read only) and
// says what went wrong, what is missing and what to change; the lessons it
// writes are kept for the next scans.

// maxTries: a piece (by pieceKey) failed this often is not recorded again.
const maxTries = 2

// systemicFails: pieces in a row failing for the same reason, past which
// the cause is the process, not the pieces.
const systemicFails = 3

// failClass is what kind of failure a piece's summary tells: its text
// without the parts in brackets, up to the first ":", short.
func failClass(summary string) string {
	k := pieceKey(summary)
	if head, _, ok := strings.Cut(k, ":"); ok && strings.TrimSpace(head) != "" {
		k = strings.TrimSpace(head)
	}
	if r := []rune(k); len(r) > 60 {
		k = string(r[:60])
	}
	return k
}

// failedBefore is how often a piece like title already failed in b, and
// the latest reason.
func failedBefore(items []storage.BurnItem, title string) (int, string) {
	key, n, why := pieceKey(title), 0, ""
	for _, it := range items {
		if it.Status == "failed" && !hunting(it) && it.Kind != KindQuest && pieceKey(it.Title) == key {
			n++
			why = it.Summary
		}
	}
	return n, why
}

// systemic is the reason the last systemicFails pieces finished in the run
// all failed for, if they did.
func systemic(items []storage.BurnItem, run string) (string, bool) {
	var closed []storage.BurnItem
	for _, it := range items {
		if !hunting(it) && it.RunBranch == run && (it.Status == "done" || it.Status == "failed" || it.Status == "skipped") {
			closed = append(closed, it)
		}
	}
	slices.SortStableFunc(closed, func(a, b storage.BurnItem) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if len(closed) < systemicFails {
		return "", false
	}
	class := ""
	for _, it := range closed[:systemicFails] {
		c := failClass(it.Summary)
		if it.Status != "failed" || c == "" || (class != "" && c != class) {
			return "", false
		}
		class = c
	}
	return closed[0].Summary, true
}

// watch runs after a piece fails: the same reason systemicFails times in a
// row, the Burn takes no new work and says why.
func (s *Service) watch(ctx context.Context, b storage.BurnSession) {
	items, err := s.store.Burn().Items(ctx, b.ID)
	if err != nil {
		return
	}
	why, ok := systemic(items, b.RunBranch)
	if !ok {
		return
	}
	s.say(ctx, b, fmt.Sprintf("**Burn ngưng nhận việc mới**: %d việc liền thất bại cùng một lý do, lỗi nằm ở quy trình chứ không ở từng việc: %s\nXem lại lý do này (cài đặt, lệnh kiểm chứng, môi trường) rồi bật lại.", systemicFails, oneLine(why, 300)))
	_ = s.Drain(context.WithoutCancel(ctx), b.ProjectID)
}

// runStats are a run's numbers, to look back at: what came of its pieces,
// why they failed or were turned down, what was tried again, how much of
// the coverage plan was looked at.
func runStats(b storage.BurnSession, items []storage.BurnItem, start time.Time) string {
	var done, failed, skipped, open int
	kinds := map[string]int{}
	fails := map[string]int{}
	turned := map[string]int{}
	cost := 0.0
	var run []storage.BurnItem
	for _, it := range items {
		if hunting(it) || it.UpdatedAt.Before(start) {
			continue
		}
		run = append(run, it)
		cost += it.CostUSD
		switch it.Status {
		case "done":
			done++
			kinds[it.Kind]++
		case "failed":
			failed++
			fails[failClass(it.Summary)]++
		case "skipped":
			skipped++
			turned[failClass(it.Summary)]++
		default:
			open++
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "- Pieces: %d done, %d failed, %d turned down, %d still open · cost $%.2f\n", done, failed, skipped, open, cost)
	if len(kinds) > 0 {
		fmt.Fprintf(&sb, "- Done by kind: %s\n", counts(kinds))
	}
	if len(fails) > 0 {
		fmt.Fprintf(&sb, "- Failed, by reason: %s\n", counts(fails))
	}
	if len(turned) > 0 {
		fmt.Fprintf(&sb, "- Turned down, by reason: %s\n", counts(turned))
	}
	var again []string
	for _, g := range groupByPiece(run) {
		if g.n > 1 {
			again = append(again, fmt.Sprintf("%s (×%d, %s)", oneLine(g.it.Title, 80), g.n, g.it.Status))
		}
	}
	if len(again) > 0 {
		fmt.Fprintf(&sb, "- Recorded again and again: %s\n", strings.Join(again, "; "))
	}
	if checked, total := coverageCount(b.Coverage); total > 0 {
		fmt.Fprintf(&sb, "- Coverage plan: %d/%d items looked at\n", checked, total)
	} else {
		sb.WriteString("- Coverage plan: none written\n")
	}
	return strings.TrimSpace(sb.String())
}

// counts is "a 3, b 1", the largest first.
func counts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if m[a] != m[b] {
			return m[b] - m[a]
		}
		return strings.Compare(a, b)
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%q %d", cmp.Or(k, "?"), m[k])
	}
	return strings.Join(parts, ", ")
}

// retroPrompt is the look back at a run (burn/retro.md).
func retroPrompt(b storage.BurnSession, items []storage.BurnItem, start time.Time) string {
	return prompts.Render("burn/retro", struct {
		Template, Focus, Stats, Lessons, PromptVersion string
		Setbacks                                       []string
	}{templateLabel[templateOf(b)], b.Focus, runStats(b, items, start), b.Lessons, prompts.Version("burn/retro"), setbacks(items)})
}

// lessonsBlock is the ```lessons block of the look back, the whole list.
var lessonsBlock = regexp.MustCompile("(?s)```lessons\\s*\\n(.*?)```")

// retro looks back at a run that did something, in the Burn's chat, read
// only; the lessons it writes replace the Burn's.
func (s *Service) retro(ctx context.Context, b storage.BurnSession) {
	if s.chat == nil || b.ConversationID == "" || b.StartedAt == nil {
		return
	}
	items, err := s.store.Burn().Items(ctx, b.ID)
	if err != nil {
		return
	}
	did := false
	for _, it := range items {
		if !hunting(it) && !it.UpdatedAt.Before(*b.StartedAt) && (it.Status == "done" || it.Status == "failed" || it.Status == "skipped") {
			did = true
			break
		}
	}
	if !did {
		return
	}
	rctx := chat.WithCeiling(chat.WithTurnTimeout(actor.With(ctx, "burn:"+b.StartedBy), 0), perm.Read)
	res, err := s.run(rctx, b.ConversationID, retroPrompt(b, items, *b.StartedAt))
	if err != nil || res.failed != "" {
		slog.Warn("burn: look back at the run", "project", b.ProjectID, "err", err, "failed", res.failed)
		return
	}
	if m := lessonsBlock.FindStringSubmatch(res.text); m != nil {
		s.keepLessons(ctx, b, m[1])
	}
}
