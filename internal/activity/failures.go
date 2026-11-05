package activity

import (
	"context"
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
	groups, err := errdigest.Load(ctx, st, storage.JobFilter{ProjectID: projectID, AgentID: agentID, Since: since})
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
	return head + "):\n" + b.String(), nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
