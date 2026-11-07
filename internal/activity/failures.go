package activity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/errdigest"
	"github.com/tung1998/agent-office/internal/storage"
)

// failureDigest is failure_digest (ADR-253): the failed jobs and model calls
// of one project since `days` ago, grouped by cause, biggest first. agent is
// an agent's name or id in the project; category one of errdigest's.
func failureDigest(ctx context.Context, st storage.Store, projectID string, days int, agent, category string) (string, error) {
	if days <= 0 {
		days = 7
	}
	days = min(days, 90)
	since := time.Now().UTC().AddDate(0, 0, -days)
	agents, err := st.Agents().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	name := map[string]string{}
	agentID := ""
	for _, a := range agents {
		name[a.ID] = a.Name
		if agent != "" && (strings.EqualFold(a.Name, agent) || a.ID == agent) {
			agentID = a.ID
		}
	}
	if agent != "" && agentID == "" {
		return "", fmt.Errorf("no agent %q in this project", agent)
	}
	if category != "" && !slices.Contains(errdigest.AllCategories, category) {
		return "", fmt.Errorf("unknown category %q (one of %s)", category, strings.Join(errdigest.AllCategories, ", "))
	}
	groups, truncated, err := errdigest.Load(ctx, st, storage.JobFilter{ProjectID: projectID, AgentID: agentID, Since: since})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	total, shown := 0, 0
	for _, g := range groups {
		if category != "" && g.Category != category {
			continue
		}
		total += g.Count
		if shown >= 20 {
			continue
		}
		shown++
		who := make([]string, 0, len(g.AgentIDs))
		for _, id := range g.AgentIDs {
			who = append(who, cmpOr(name[id], id))
		}
		fmt.Fprintf(&b, "- %d× [%s] %s\n  jobs %d, model calls %d", g.Count, g.Category, g.Signature, g.Jobs, g.Calls)
		if len(who) > 0 {
			fmt.Fprintf(&b, "; agents: %s", strings.Join(who, ", "))
		}
		if len(g.Providers) > 0 {
			fmt.Fprintf(&b, "; connections: %s", strings.Join(g.Providers, ", "))
		}
		if len(g.AutomationIDs) > 0 { // whose runs they were, workflow steps included (ADR-263)
			autos := make([]string, 0, len(g.AutomationIDs))
			for _, id := range g.AutomationIDs {
				n := id
				if a, err := st.Automations().Get(ctx, id); err == nil {
					n = a.Name
				}
				autos = append(autos, n)
			}
			fmt.Fprintf(&b, "; automations: %s", strings.Join(autos, ", "))
		}
		fmt.Fprintf(&b, "; first %s, last %s", g.First.Local().Format("01-02 15:04"), g.Last.Local().Format("01-02 15:04"))
		if len(g.JobIDs) > 0 {
			fmt.Fprintf(&b, "; jobs: %s", strings.Join(g.JobIDs, ", "))
		}
		sample := strings.Join(strings.Fields(g.Sample), " ")
		if r := []rune(sample); len(r) > 300 {
			sample = string(r[:299]) + "…"
		}
		fmt.Fprintf(&b, "\n  latest: %s\n", sample)
	}
	if total == 0 {
		return fmt.Sprintf("No failures in the last %d days.", days), nil
	}
	head := fmt.Sprintf("Failures in the last %d days: %d, by cause (biggest first", days, total)
	if shown < len(groups) && category == "" {
		head += fmt.Sprintf(", %d of %d causes shown", shown, len(groups))
	}
	out := head + "):\n" + b.String()
	if truncated {
		out += fmt.Sprintf("\nNote: only the latest %d failed jobs and %d failed model calls of the window were read; older failures are not counted, so the totals are lower than the truth. Ask for fewer days.\n", errdigest.MaxRows, errdigest.MaxRows)
	}
	return out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// agentSuccessRate is success_rate: goal completion by agent (Agents
// Companion, ch. Agent Success Metrics) alongside failureDigest's error log —
// of the Burn pieces closed (done/failed; skipped does not count: no attempt
// was made) in the last `days`, how many finished done without the result
// reviewer sending them back (storage.BurnItem.Attempts > 1 means they did,
// see reviewAgain in internal/burn/burn.go). agent filters to one owner
// (name or key); "" reports every owner.
func agentSuccessRate(ctx context.Context, st storage.Store, projectID string, days int, agent string) (string, error) {
	if days <= 0 {
		days = 7
	}
	days = min(days, 90)
	since := time.Now().UTC().AddDate(0, 0, -days)
	b, err := st.Burn().Session(ctx, projectID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "No Burn has run in this project yet.", nil
		}
		return "", err
	}
	items, err := st.Burn().Items(ctx, b.ID)
	if err != nil {
		return "", err
	}
	type tally struct{ clean, total int }
	byOwner := map[string]*tally{}
	for _, it := range items {
		if it.Status != "done" && it.Status != "failed" {
			continue // skipped: never attempted; found/queued/doing/paused: not closed yet
		}
		if it.UpdatedAt.Before(since) {
			continue
		}
		owner := cmpOr(it.Owner, "(default)")
		if agent != "" && !strings.EqualFold(owner, agent) {
			continue
		}
		t := byOwner[owner]
		if t == nil {
			t = &tally{}
			byOwner[owner] = t
		}
		t.total++
		if it.Status == "done" && it.Attempts <= 1 {
			t.clean++
		}
	}
	if len(byOwner) == 0 {
		return fmt.Sprintf("No closed pieces in the last %d days.", days), nil
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	slices.Sort(owners)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal completion in the last %d days, by owner (done without review sending it back ÷ closed):\n", days)
	for _, o := range owners {
		t := byOwner[o]
		fmt.Fprintf(&sb, "- %s: %d/%d (%.0f%%)\n", o, t.clean, t.total, 100*float64(t.clean)/float64(t.total))
	}
	return sb.String(), nil
}
