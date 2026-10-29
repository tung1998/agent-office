package officetools

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// A manager staffs its project from evidence (ADR-197): what each agent
// carried over the last days (turns, cost, failures), what it runs on its own
// (schedules), how full its notes are (compactions), and the quests it
// follows up. staff_load reads it; deciding stays with the manager.

// staffLoadTool reads the project's agents' load.
func staffLoadTool() Tool {
	return Tool{Name: "staff_load", Description: "The load of each agent of the project over the last days, to staff it from evidence: turns, failed turns, cost and its share, schedules it runs, notes (core size, topics, compactions), open quests it follows up on the Burn board, open rows of the tasks table assigned to it. Use it before adding, splitting, merging or handing over work.",
		Schema: obj(map[string]any{"days": map[string]any{"type": "integer", "description": "Days to look back (default 14, at most 90)"}}), ReadOnly: true}
}

// staffLoad is staff_load for sc's project.
func (t *Toolbox) staffLoad(ctx context.Context, sc Scope, days int) (string, error) {
	if days <= 0 {
		days = 14
	}
	days = min(days, 90)
	since := time.Now().AddDate(0, 0, -days)
	r, err := t.store.Repos().Get(ctx, sc.ProjectID)
	if err != nil {
		return "", err
	}
	agents, err := t.store.Agents().List(ctx, r.ID)
	if err != nil {
		return "", err
	}
	def, _ := storage.DefaultAgentAny(r, agents)
	stats, err := t.store.Jobs().Stats(ctx, storage.JobFilter{ProjectID: r.ID, Since: since}, "agent")
	if err != nil {
		return "", err
	}
	byAgent, total := map[string]storage.JobStats{}, 0.0
	for _, s := range stats {
		byAgent[s.Key] = s
		total += s.CostUSD
	}
	schedules := map[string]int{}
	if autos, err := t.store.Automations().List(ctx, r.ID); err == nil {
		for _, a := range autos {
			if a.Enabled && a.Source == "schedule" && a.Action != "script" {
				schedules[cmp.Or(a.AgentID, def.ID)]++
			}
		}
	}
	quests := map[string]int{}
	if b, err := t.store.Burn().Session(ctx, r.ID); err == nil {
		items, _ := t.store.Burn().Items(ctx, b.ID)
		for _, it := range items {
			if it.Kind == "quest" && it.Status != "done" && it.Status != "failed" && it.Status != "skipped" {
				quests[cmp.Or(it.Owner, def.Key)]++
			}
		}
	}
	tasks := t.openTasks(ctx, r.ID, agents) // a company's work table (ADR-201)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Project %s, last %d days, %.2f USD in all. Core notes are given up to %d characters; a topic is compacted past %d.\n", r.Name, days, total, memory.CoreLimit, memory.TopicLimit)
	sb.WriteString("| agent | role | level | turns | failed | cost USD (share) | schedules | core notes (chars) | topics | compactions | open quests | open tasks |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, a := range agents {
		s := byAgent[a.ID]
		share := 0.0
		if total > 0 {
			share = s.CostUSD / total * 100
		}
		notes, _ := t.store.Memories().List(ctx, r.ID, a.ID)
		core, topics := 0, map[string]bool{}
		for _, m := range notes {
			if m.Topic == "" {
				core += len([]rune(m.Text))
			} else {
				topics[m.Topic] = true
			}
		}
		compactions := 0
		if revs, err := t.store.Memories().Revisions(ctx, r.ID, a.ID); err == nil {
			for _, rv := range revs {
				if rv.CreatedAt.After(since) && !strings.Contains(rv.Reason, "khôi phục") {
					compactions++
				}
			}
		}
		name := a.Name + " (" + a.Key + ")"
		if a.Disabled {
			name += ", off"
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %d | %d | %.2f (%.0f%%) | %d | %d | %d | %d | %d | %d |\n", name, cmp.Or(a.Role, "-"), perm.Agent(a),
			s.Jobs, s.Failed, s.CostUSD, share, schedules[a.ID], core, len(topics), compactions, quests[a.Key], tasks[a.ID])
	}
	sb.WriteString("Open tasks: rows of the project's tasks table not done or cancelled whose assignee is that agent (key or name).\n")
	sb.WriteString("Signals to weigh (the organization skill): one agent carrying most of the cost and turns, its core notes near the limit or compacted often, many failed turns, many quests to follow up; an agent idle for the whole period may be merged back.")
	return sb.String(), nil
}

// openTasks counts, by agent id, the open rows of the project's tasks table
// (status not xong/huy) assigned to it by key or name; none: nothing.
func (t *Toolbox) openTasks(ctx context.Context, projectID string, agents []storage.Agent) map[string]int {
	out := map[string]int{}
	col, err := t.store.Collections().Get(ctx, projectID, "tasks")
	if err != nil {
		return out
	}
	recs, _, err := t.store.Collections().Query(ctx, storage.RecordQuery{CollectionID: col.ID, Limit: 1000})
	if err != nil {
		return out
	}
	for _, rec := range recs {
		status, _ := rec.Data["status"].(string)
		who, _ := rec.Data["assignee"].(string)
		if status == "xong" || status == "huy" || who == "" {
			continue
		}
		for _, a := range agents {
			if strings.EqualFold(who, a.Key) || strings.EqualFold(who, a.Name) {
				out[a.ID]++
				break
			}
		}
	}
	return out
}
