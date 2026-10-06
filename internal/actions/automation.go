package actions

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// automationSpec reads and checks a proposed automation (ADR-041); an update
// must name an automation of the same project.
func (s *Service) automationSpec(ctx context.Context, projectID, kind string, raw json.RawMessage) (trigger.Spec, error) {
	var spec trigger.Spec
	if len(raw) == 0 || json.Unmarshal(raw, &spec) != nil {
		return spec, errors.New("thiếu đặc tả tự động hóa")
	}
	if err := spec.Check(); err != nil {
		return spec, err
	}
	if err := trigger.CheckAgents(ctx, s.store, projectID, spec.AgentID, spec.Escalate.AgentID); err != nil {
		return spec, err
	}
	if kind == "update_automation" {
		a, err := s.store.Automations().Get(ctx, spec.AutomationID)
		if err != nil || a.ProjectID != projectID {
			return spec, errors.New("không tìm thấy tự động hóa cần sửa trong project")
		}
	}
	return spec, nil
}

// saveAutomation creates or updates the automation of an approved proposal.
func (s *Service) saveAutomation(ctx context.Context, a storage.Action) error {
	spec, err := s.automationSpec(ctx, a.ProjectID, a.Kind, a.Args.Automation)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if a.Kind == "update_automation" {
		x, err := s.store.Automations().Get(ctx, spec.AutomationID)
		if err != nil {
			return err
		}
		old := x
		spec.Apply(&x, now)
		err = s.store.Automations().Update(ctx, x)
		_ = audit.Record(ctx, s.store.Audit(), audit.Change{Action: "automation.update", ResourceID: x.ID, ProjectID: x.ProjectID, Before: old, After: x, Err: err})
		return err
	}
	x := storage.Automation{ProjectID: a.ProjectID, Enabled: true, EditMode: "worktree", CreatedBy: a.DecidedBy}
	spec.Apply(&x, now)
	created, err := s.store.Automations().Create(ctx, x)
	_ = audit.Record(ctx, s.store.Audit(), audit.Change{Action: "automation.create", ResourceID: created.ID, ProjectID: x.ProjectID, After: created, Err: err})
	return err
}
