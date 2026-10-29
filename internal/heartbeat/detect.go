// Package heartbeat is a project's self-check while agents work (ADR-230):
// every few minutes office reads what the agents did in the last hour and,
// with no model, looks for signs the work is stuck or going in circles:
// cards nobody decides while agents go on, the same command proposed again
// and again, two agents handing a task back and forth with nothing merged,
// the same failure, a diff that keeps failing the checks. When it finds one,
// it opens a turn for the project's coordinator with the evidence, to find
// the cause and fix it within its permissions, instead of the person
// finding out the next morning.
package heartbeat

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/verify"
)

// Window is how far back the check reads.
const Window = time.Hour

const (
	cardWait      = 15 * time.Minute // a card older than this is waiting on no one…
	cardRunsAfter = 3                // …when this many turns started since
	cardMany      = 5                // or this many cards wait at once
	sameProposal  = 3                // the same command or action proposed this often
	churnSpan     = 30 * time.Minute // turns in one chat over this span…
	churnTurns    = 8                // …switching agent this often, with no diff merged there
	sameFailure   = 3                // the same error this often
	gateHeld      = 3                // diffs held by the checks before merging (ADR-222)
	maxEvidence   = 5
)

// Signal is one sign of stuck work, with its evidence.
type Signal struct {
	Kind     string   // stalled_cards | repeated_proposal | churn | repeated_failure | checks_failing
	Key      string   // what it is about: a fingerprint, so the same sign is not raised twice
	Summary  string   // one line for the coordinator
	Evidence []string // ids, chats, commands
}

// Detect reads the facts of the last Window and returns the signs found.
func Detect(f storage.HarnessFacts, now time.Time) []Signal {
	var out []Signal
	out = append(out, stalledCards(f, now)...)
	out = append(out, repeatedProposals(f)...)
	out = append(out, churn(f, now)...)
	out = append(out, repeatedFailures(f)...)
	out = append(out, checksFailing(f)...)
	return out
}

func stalledCards(f storage.HarnessFacts, now time.Time) []Signal {
	var waiting []storage.HarnessAction
	for _, a := range f.Actions {
		if a.Status == "pending" {
			waiting = append(waiting, a)
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	oldest := waiting[0]
	for _, a := range waiting {
		if a.CreatedAt.Before(oldest.CreatedAt) {
			oldest = a
		}
	}
	if now.Sub(oldest.CreatedAt) < cardWait {
		return nil
	}
	after := 0
	for _, j := range f.Jobs {
		if j.CreatedAt.After(oldest.CreatedAt) {
			after++
		}
	}
	if after < cardRunsAfter && len(waiting) < cardMany {
		return nil
	}
	s := Signal{Kind: "stalled_cards", Key: "stalled_cards",
		Summary: fmt.Sprintf("%d card(s) wait for a decision, the oldest for %s, while %d turn(s) started since: the work is blocked on them", len(waiting), now.Sub(oldest.CreatedAt).Round(time.Minute), after)}
	for _, a := range waiting {
		if len(s.Evidence) == maxEvidence {
			break
		}
		s.Evidence = append(s.Evidence, fmt.Sprintf("%s %s: %s (chat %s)", a.ID, a.Kind, cut(a.Target, 120), a.ConversationID))
	}
	return []Signal{s}
}

func repeatedProposals(f storage.HarnessFacts) []Signal {
	type seen struct {
		n, open int
		chats   map[string]bool
		ids     []string
	}
	by := map[string]*seen{}
	var keys []string
	for _, a := range f.Actions {
		if strings.TrimSpace(a.Target) == "" { // settings changes, notes: told apart by their content, not here
			continue
		}
		k := a.Kind + ": " + cut(a.Target, 160)
		x := by[k]
		if x == nil {
			x = &seen{chats: map[string]bool{}}
			by[k] = x
			keys = append(keys, k)
		}
		x.n++
		x.chats[a.ConversationID] = true
		if a.Status == "pending" || a.Status == "rejected" { // a failed run is repeatedFailures'
			x.open++
		}
		if len(x.ids) < maxEvidence {
			x.ids = append(x.ids, a.ID+" ("+a.Status+")")
		}
	}
	var out []Signal
	for _, k := range keys {
		x := by[k]
		if x.n < sameProposal || x.open == 0 { // ran each time: a check run often, not a loop
			continue
		}
		out = append(out, Signal{Kind: "repeated_proposal", Key: "repeated_proposal:" + k,
			Summary:  fmt.Sprintf("%q was proposed %d times in %d chat(s), %d waiting or refused: agents redo it instead of getting it unblocked", k, x.n, len(x.chats), x.open),
			Evidence: x.ids})
	}
	return out
}

func churn(f storage.HarnessFacts, now time.Time) []Signal {
	// the turns of a chat switching agent (A, B, A, B): work handed back and
	// forth; the person talking to one agent never switches
	switches, last := map[string]int{}, map[string]string{}
	var chats []string
	for _, j := range f.Jobs { // oldest first
		if j.ConversationID == "" || j.AgentID == "" || now.Sub(j.CreatedAt) > churnSpan {
			continue
		}
		prev, seen := last[j.ConversationID]
		if !seen {
			chats = append(chats, j.ConversationID)
		} else if prev != j.AgentID {
			switches[j.ConversationID]++
		}
		last[j.ConversationID] = j.AgentID
	}
	merged := map[string]bool{}
	for _, p := range f.Patches {
		if p.Status == "applied" && now.Sub(p.CreatedAt) <= churnSpan {
			merged[p.ConversationID] = true
		}
	}
	var out []Signal
	for _, c := range chats {
		if switches[c] < churnTurns || merged[c] {
			continue
		}
		out = append(out, Signal{Kind: "churn", Key: "churn:" + c,
			Summary:  fmt.Sprintf("chat %s switched agent %d times in %s and no diff merged: agents hand the work back and forth without it moving", c, switches[c], churnSpan),
			Evidence: []string{"chat " + c}})
	}
	return out
}

var digitsRe = regexp.MustCompile(`\d+`)

func errorKey(code, msg string) string {
	if code != "" {
		return code
	}
	msg = strings.TrimSpace(strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0])
	return cut(digitsRe.ReplaceAllString(msg, "N"), 120)
}

func repeatedFailures(f storage.HarnessFacts) []Signal {
	type seen struct {
		n   int
		ids []string
	}
	by := map[string]*seen{}
	add := func(k, id string) {
		if strings.TrimSpace(k) == "" {
			return
		}
		x := by[k]
		if x == nil {
			x = &seen{}
			by[k] = x
		}
		x.n++
		if len(x.ids) < maxEvidence {
			x.ids = append(x.ids, id)
		}
	}
	for _, j := range f.Jobs {
		if j.Status == "failed" && j.ErrorCode != "cancelled" && j.ErrorCode != "restart" {
			add("turn: "+errorKey(j.ErrorCode, j.Error), j.ID+" (chat "+j.ConversationID+")")
		}
	}
	for _, a := range f.Actions {
		if a.Status == "failed" {
			add(a.Kind+" "+cut(a.Target, 80)+": "+errorKey("", a.Detail), a.ID+" (chat "+a.ConversationID+")")
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Signal
	for _, k := range keys {
		if x := by[k]; x.n >= sameFailure {
			out = append(out, Signal{Kind: "repeated_failure", Key: "repeated_failure:" + k,
				Summary: fmt.Sprintf("the same failure %d times: %s", x.n, k), Evidence: x.ids})
		}
	}
	return out
}

func checksFailing(f storage.HarnessFacts) []Signal {
	var ids []string
	n := 0
	for _, p := range f.Patches {
		if strings.HasPrefix(p.Detail, verify.Held) {
			n++
			if len(ids) < maxEvidence {
				ids = append(ids, p.ID+" (chat "+p.ConversationID+"): "+cut(strings.TrimPrefix(p.Detail, verify.Held), 120))
			}
		}
	}
	if n < gateHeld {
		return nil
	}
	return []Signal{{Kind: "checks_failing", Key: "checks_failing",
		Summary: fmt.Sprintf("%d diffs failed the checks before merging in the last hour: the build or tests are broken and fixes do not hold", n), Evidence: ids}}
}

func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
