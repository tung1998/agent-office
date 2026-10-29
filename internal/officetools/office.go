package officetools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Tools of the office assistant (ADR-046): it works across projects, so the
// project tools take a "project" (id or name) and these are added.
func (t *Toolbox) officeTools() []Tool {
	project := map[string]any{"type": "string", "description": "Project: id or name (see projects)"}
	return append([]Tool{
		{Name: "projects", Description: "Office's projects: id, name, description, folder. Call it first to find which project the work belongs to.", Schema: obj(map[string]any{}), ReadOnly: true},
		{Name: "jobs_query", Description: "Recent runs (chat, automation, script): status, cost, time. Filter by project and status.",
			Schema: obj(map[string]any{"project": project, "status": map[string]any{"type": "string", "enum": []string{"running", "pending", "done", "failed", "cancelled"}},
				"days": map[string]any{"type": "integer"}, "limit": map[string]any{"type": "integer"}}), ReadOnly: true},
		{Name: "usage_summary", Description: "Cost and tokens by day, project or model over the last N days.",
			Schema: obj(map[string]any{"days": map[string]any{"type": "integer", "description": "Default 7"}, "by": map[string]any{"type": "string", "enum": []string{"day", "project", "model"}}}), ReadOnly: true},
		{Name: "handoff", Description: "Hand code work to a project's Chat: returns a link that opens that Chat with a drafted message for the person to send. You do not change code yourself.",
			Schema: obj(map[string]any{"project": project, "message": map[string]any{"type": "string", "description": "Message for the project's team lead"}}, "project", "message"), ReadOnly: true},
		{Name: "run_automation", Description: "PROPOSE running an automation now (see list resource=automation): the person approves on a card.",
			Schema: obj(map[string]any{"project": project, "id": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}}, "project", "id")},
	}, t.projectTools()...)
}

// findProject is a project by id or name (case does not matter), never the
// assistant's own, and only one the person asking sees (ADR-240).
func (t *Toolbox) findProject(ctx context.Context, sc Scope, ref string) (storage.Repo, error) {
	ref = strings.TrimSpace(ref)
	list, err := t.store.Repos().List(ctx)
	if err != nil {
		return storage.Repo{}, err
	}
	own := ""
	if t.assistant != nil {
		own = t.assistant(ctx)
	}
	var names []string
	for _, p := range list {
		if p.ID == own || !sc.Sees(p.ID) {
			continue
		}
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
		names = append(names, p.Name)
	}
	return storage.Repo{}, fmt.Errorf("không có project %q (có: %s)", ref, strings.Join(names, ", "))
}

// officeCall runs the assistant's own tools; ok=false: not one of them.
func (t *Toolbox) officeCall(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool, bool) {
	if out, isErr, ok := t.projectCall(ctx, sc, name, raw); ok {
		return out, isErr, ok
	}
	var in struct {
		Project string `json:"project"`
		Status  string `json:"status"`
		Days    int    `json:"days"`
		Limit   int    `json:"limit"`
		By      string `json:"by"`
		Message string `json:"message"`
		Goal    string `json:"goal"`
		Agent   string `json:"agent"`
		ID      string `json:"id"`
		Reason  string `json:"reason"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "Tham số không hợp lệ: " + err.Error(), true, true
		}
	}
	project := func() (storage.Repo, error) {
		if in.Project == "" {
			return storage.Repo{}, errors.New("hãy chỉ rõ project (xem projects)")
		}
		return t.findProject(ctx, sc, in.Project)
	}
	var (
		out string
		err error
	)
	switch name {
	case "projects":
		var list []storage.Repo
		if list, err = t.store.Repos().List(ctx); err == nil {
			own := ""
			if t.assistant != nil {
				own = t.assistant(ctx)
			}
			rows := []map[string]any{}
			for _, p := range list {
				if p.ID != own && sc.Sees(p.ID) {
					rows = append(rows, map[string]any{"id": p.ID, "name": p.Name, "description": p.Description, "path": p.Path})
				}
			}
			b, _ := json.MarshalIndent(rows, "", "  ")
			out = string(b)
		}
	case "jobs_query":
		f := storage.JobFilter{Status: in.Status, Limit: min(max(in.Limit, 1), 50)}
		if in.Limit == 0 {
			f.Limit = 20
		}
		if in.Days > 0 {
			f.Since = time.Now().Add(-time.Duration(in.Days) * 24 * time.Hour)
		}
		if t.assistant != nil {
			f.ExcludeProjectID = t.assistant(ctx) // the assistant's chats are each person's own
		}
		if sc.Limited { // the person's projects only (ADR-240)
			f.ProjectIDs = append([]string{}, sc.Projects...)
		}
		if in.Project != "" {
			var p storage.Repo
			if p, err = project(); err != nil {
				break
			}
			f.ProjectID = p.ID
		}
		var jobs []storage.Job
		if jobs, err = t.store.Jobs().List(ctx, f); err == nil {
			rows := []map[string]any{}
			for _, j := range jobs {
				rows = append(rows, map[string]any{"id": j.ID, "project_id": j.ProjectID, "kind": j.Kind, "origin": j.Origin, "title": j.Title,
					"status": j.Status, "error": j.Error, "cost_usd": j.CostUSD, "created_at": j.CreatedAt.Format(time.RFC3339)})
			}
			b, _ := json.MarshalIndent(rows, "", "  ")
			out = string(b)
		}
	case "usage_summary":
		days := in.Days
		if days <= 0 {
			days = 7
		}
		by := in.By
		if by == "" {
			by = "project"
		}
		var rows []storage.UsageRow
		if sc.Limited { // by day or model sums every project: the person's own, by project (ADR-240)
			by = "project"
		}
		if rows, err = t.store.Runs().Aggregate(ctx, time.Now().Add(-time.Duration(days)*24*time.Hour), by, time.Local); err == nil {
			rows = slices.DeleteFunc(rows, func(r storage.UsageRow) bool { return !sc.Sees(r.Key) })
			b, _ := json.MarshalIndent(rows, "", "  ")
			out = fmt.Sprintf("Cost over %d days, by %s:\n%s", days, by, b)
		}
	case "handoff":
		var p storage.Repo
		if p, err = project(); err == nil {
			if strings.TrimSpace(in.Message) == "" {
				err = errors.New("hãy soạn tin nhắn cho project")
				break
			}
			out = fmt.Sprintf("Link opening the Chat of %s with the drafted message (the person clicks it, then sends): /projects/%s?tab=chat&draft=%s",
				p.Name, p.ID, url.QueryEscape(in.Message))
		}
	case "run_automation":
		if t.actions == nil {
			return "Không đề xuất được ở đây", true, true
		}
		var p storage.Repo
		if p, err = project(); err != nil {
			break
		}
		psc := sc
		psc.ProjectID = p.ID
		var a storage.Action
		a, err = t.actions.Propose(ctx, psc, "run_automation", in.ID, in.Reason)
		if err == nil {
			out = "Created approval card: " + a.Target + " (" + p.Name + "). It runs only once the person approves."
		}
	default:
		return "", false, false
	}
	if err != nil {
		return err.Error(), true, true
	}
	return out, false, true
}
