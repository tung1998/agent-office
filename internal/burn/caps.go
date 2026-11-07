package burn

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// A Burn's caps (ADR-135): the person's review, not the slots, is the
// ceiling of what it can usefully do. Past ReviewCap pieces done and not
// merged into the project's branch yet, it starts nothing new (what is in
// progress goes on) until the person merges; past StopAfter pieces done in
// a run, it finishes what it has and stops.

// MaxCap caps both.
const MaxCap = 100

// unmerged: the run's commits not on the project's branch yet (the pieces
// it merged and the person has not); 0 when there is no run branch yet.
func unmerged(ctx context.Context, repo, branch string) int {
	if repo == "" || branch == "" {
		return 0
	}
	if _, err := gitIn(ctx, repo, "", "rev-parse", "--verify", "-q", "refs/heads/"+branch); err != nil {
		return 0
	}
	out, err := gitIn(ctx, repo, "", "rev-list", "--count", "HEAD.."+branch)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// runKey identifies b's current run across the whole Service: RunBranch
// alone names only the second it started, so two projects' runs starting in
// the same second would otherwise share one entry.
func runKey(b storage.BurnSession) string {
	return b.ProjectID + "|" + b.RunBranch
}

// doneInRun: the pieces done in b's current run.
func doneInRun(b storage.BurnSession, items []storage.BurnItem) int {
	n := 0
	for _, it := range items {
		if it.Status == "done" && it.RunBranch == b.RunBranch {
			n++
		}
	}
	return n
}

// heldForReview: b has ReviewCap pieces waiting for the person; it says so
// once each time it starts waiting.
func (s *Service) heldForReview(ctx context.Context, b storage.BurnSession) bool {
	if b.ReviewCap <= 0 {
		return false
	}
	p, err := s.store.Repos().Get(ctx, b.ProjectID)
	if err != nil {
		return false
	}
	n := unmerged(ctx, p.Path, b.RunBranch)
	held := n >= b.ReviewCap
	s.mu.Lock()
	was := s.held[runKey(b)]
	s.held[runKey(b)] = held
	s.mu.Unlock()
	switch {
	case held && !was:
		s.say(ctx, b, fmt.Sprintf("**Chờ bạn review**: %d việc trên nhánh `%s` chưa merge (giới hạn %d). Merge nhánh rồi Burn làm tiếp; việc đang dở vẫn chạy.", n, b.RunBranch, b.ReviewCap))
	case !held && was:
		s.say(ctx, b, "Đã merge: chạy tiếp.")
	}
	return held
}

// stopAfterReached: b did StopAfter pieces this run, the first time it is
// seen (resumed by hand, it goes on past it).
func (s *Service) stopAfterReached(b storage.BurnSession, items []storage.BurnItem) bool {
	if b.StopAfter <= 0 || doneInRun(b, items) < b.StopAfter {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capped[runKey(b)] {
		return false
	}
	s.capped[runKey(b)] = true
	return true
}
