package activity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
)

// Tools gives agents project_report and failure_digest (an officetools.Provider).
type Tools struct{ st storage.Store }

// NewTools is project_report over st.
func NewTools(st storage.Store) Tools { return Tools{st} }

// Tools: project_report, in any project.
func (t Tools) Tools(sc officetools.Scope) []officetools.Tool {
	if sc.ProjectID == "" || sc.Office {
		return nil
	}
	return []officetools.Tool{{Name: "project_report", ReadOnly: true,
		Description: "How this project and the projects it links to (its units) worked, in figures: each work board's open tasks by status and what was added, done or failed; each agent's runs, errors and cost; the changes to agents, workflows, schedules and tables; the schedules. For a report (a morning report) or to see where the work stands.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"hours": map[string]any{"type": "integer", "description": "How many hours back (default 24, at most 720)"}}}},
		{Name: "failure_digest", ReadOnly: true,
			Description: "What failed in this project, grouped by cause (ADR-253): failed jobs and failed model calls, the same error seen many times counted once per cause, with its kind (cli_broken, needs_login, quota, rate_limit, timeout, mcp, network, workflow…), agents, connections, first and last time, job ids and the latest full error. Use it to find why runs fail instead of reading the database.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"days":     map[string]any{"type": "integer", "description": "How many days back (default 7, at most 90)"},
				"agent":    map[string]any{"type": "string", "description": "Only this agent's failures (name or id)"},
				"category": map[string]any{"type": "string", "description": "Only this kind of cause, e.g. cli_broken, quota"}}}},
		{Name: "success_rate", ReadOnly: true,
			Description: "Goal completion by Burn piece owner: of the pieces closed in the window, how many finished done without the result reviewer sending them back for another round. A number of work completed cleanly, next to failure_digest's log of what went wrong.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"days":  map[string]any{"type": "integer", "description": "How many days back (default 7, at most 90)"},
				"agent": map[string]any{"type": "string", "description": "Only this owner's pieces (name or key)"}}}}}
}

// Call runs project_report, failure_digest or success_rate.
func (t Tools) Call(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, bool) {
	if name == "failure_digest" {
		var in struct {
			Days            int
			Agent, Category string
		}
		_ = json.Unmarshal(raw, &in)
		out, err := failureDigest(ctx, t.st, sc.ProjectID, in.Days, in.Agent, in.Category)
		if err != nil {
			return err.Error(), true
		}
		return out, false
	}
	if name == "success_rate" {
		var in struct {
			Days  int
			Agent string
		}
		_ = json.Unmarshal(raw, &in)
		out, err := agentSuccessRate(ctx, t.st, sc.ProjectID, in.Days, in.Agent)
		if err != nil {
			return err.Error(), true
		}
		return out, false
	}
	var in struct{ Hours int }
	_ = json.Unmarshal(raw, &in)
	if in.Hours <= 0 {
		in.Hours = 24
	}
	in.Hours = min(in.Hours, 720)
	ov, err := Load(ctx, t.st, Linked(ctx, t.st, sc.ProjectID), time.Now().Add(-time.Duration(in.Hours)*time.Hour))
	if err != nil {
		return err.Error(), true
	}
	return ov.Report(), false
}
