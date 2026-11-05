package officetools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// UpdateTools gives request_update (ADR-255): an agent that changed office's
// own code asks office to rebuild and restart on it once no work runs,
// instead of leaving "restart office" in its report. Offered to the project
// whose folder is office's source, and to the office assistant.
type UpdateTools struct {
	Store   storage.Store
	Actions *actions.Service
	Root    string // office's source folder
}

// Tools: request_update where office's own code is worked on.
func (u UpdateTools) Tools(sc Scope) []Tool {
	if u.Actions == nil || sc.Consult || !u.offered(context.Background(), sc) {
		return nil
	}
	return []Tool{{Name: "request_update",
		Description: "Ask office to rebuild itself from its source and restart on the new build once no work runs (no chat answer, workflow, automation run, Burn piece or task in progress), at most a few hours; meanwhile nothing unattended starts. Use it after office's own code changed and was committed, instead of telling the person to restart office. A person approves it on a card unless your run has administrator access. It never cuts work off.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "What changed and why it needs the new build, e.g. ADR-255 merged: update when idle"}},
			"required": []string{"reason"}}}}
}

func (u UpdateTools) offered(ctx context.Context, sc Scope) bool {
	if sc.OfficeWide() {
		return true
	}
	if sc.Office || sc.ProjectID == "" || u.Root == "" || u.Store == nil {
		return false
	}
	p, err := u.Store.Repos().Get(ctx, sc.ProjectID)
	return err == nil && p.Path != "" && filepath.Clean(p.Path) == filepath.Clean(u.Root)
}

// Call proposes the update.
func (u UpdateTools) Call(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool) {
	if name != "request_update" || !u.offered(ctx, sc) {
		return "Công cụ không tồn tại: " + name, true
	}
	if !sc.Admin && !perm.AtLeast(sc.Level, perm.Propose) {
		return "Bạn không có quyền đề xuất thao tác (gói hiện tại: " + perm.Label(sc.Level) + ")", true
	}
	var in struct{ Reason string }
	_ = json.Unmarshal(raw, &in)
	a, err := u.Actions.Propose(ctx, sc, "office_update", "office", in.Reason)
	if err != nil {
		return "Lỗi: " + err.Error(), true
	}
	switch a.Status {
	case "done":
		return "Office is building its new version now and restarts on it once no work runs (this run included: it restarts after your turn and workflow end). Nothing is cut off. Tell the person it updates when idle; they can follow or cancel it on the Update page.", false
	case "failed":
		return "Asked office to update but it failed: " + a.Detail, true
	}
	return fmt.Sprintf("Created proposal %q (id %s): once the person approves, office builds and restarts when idle. Nothing is done yet; tell the person it needs their approval.", actions.Kinds[a.Kind], a.ID), false
}
