package actions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	if spec.Action == "workflow" { // a workflow the project has
		if _, err := s.store.Workflows().GetByKey(ctx, projectID, spec.Workflow); err != nil {
			return spec, errors.New("project chưa cài quy trình #" + spec.Workflow)
		}
	}
	if kind == "update_automation" {
		a, err := s.store.Automations().Get(ctx, spec.AutomationID)
		if err != nil || a.ProjectID != projectID {
			return spec, errors.New("không tìm thấy tự động hóa cần sửa trong project")
		}
	}
	return spec, nil
}

// keepLeftOut: an edit (an agent rewriting the prompt) keeps what it leaves
// out, as it is: its time zone, answering agent, prompt, goal and condition.
// Dropped, a cron moved to UTC, the team lead answered, and a 5-minute
// schedule without its only_if ran every tick (ADR-219).
func keepLeftOut(spec *trigger.Spec, old storage.Automation) {
	if spec.Source != old.Source {
		return
	}
	if spec.Timezone == "" {
		spec.Timezone = old.Config.Timezone
	}
	if spec.Action == old.Action {
		if spec.AgentID == "" {
			spec.AgentID = old.AgentID
		}
		if spec.Prompt == "" {
			spec.Prompt = old.Prompt
		}
		if spec.Goal == nil {
			spec.Goal = old.Config.Goal
		}
	}
	if spec.OnlyIf == nil {
		spec.OnlyIf = old.Config.OnlyIf
	}
	if spec.Source == "event" && spec.Event == "" { // the same event, from the same one
		spec.Event, spec.EventFrom, spec.EventSameChat = old.Config.Event, old.Config.EventFrom, old.Config.EventSameChat
	}
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
		keepLeftOut(&spec, old)
		spec.Apply(&x, now)
		// the rest of its setup (where it reports, a pull request, a kept
		// chat…) is not the spec's: it stays as it was
		cfg := old.Config
		cfg.EveryMinutes, cfg.Cron, cfg.Timezone, cfg.Workflow, cfg.Goal, cfg.OnlyIf = x.Config.EveryMinutes, x.Config.Cron, x.Config.Timezone, x.Config.Workflow, x.Config.Goal, x.Config.OnlyIf
		cfg.Tags, cfg.EndsAt, cfg.Auth = x.Config.Tags, x.Config.EndsAt, x.Config.Auth
		cfg.Event, cfg.EventFrom, cfg.EventSameChat = x.Config.Event, x.Config.EventFrom, x.Config.EventSameChat
		x.Config = cfg
		err = s.store.Automations().UpdateSpec(ctx, x)
		if err == nil && spec.Enabled != nil && *spec.Enabled != old.Enabled { // turned on or off (ADR-200)
			if x, err = s.store.Automations().Get(ctx, x.ID); err == nil {
				x.Enabled = *spec.Enabled
				if x.Enabled { // on again: a past automatic turn-off is cleared
					x.Failures, x.DisabledCode, x.DisabledReason = 0, "", ""
				}
				err = s.store.Automations().Update(ctx, x)
			}
		}
		_ = audit.Record(ctx, s.store.Audit(), audit.Change{Action: "automation.update", ResourceID: x.ID, ProjectID: x.ProjectID, Before: old, After: x, Err: err})
		return err
	}
	x := storage.Automation{ProjectID: a.ProjectID, Enabled: spec.Enabled == nil || *spec.Enabled, EditMode: "worktree", CreatedBy: s.owner(ctx, a)}
	spec.Apply(&x, now)
	created, err := s.store.Automations().Create(ctx, x)
	_ = audit.Record(ctx, s.store.Audit(), audit.Change{Action: "automation.create", ResourceID: created.ID, ProjectID: x.ProjectID, After: created, Err: err})
	return err
}

// owner is the person an approved action is done for: who decided it, or for
// one office did on the agent's own permission, the person its turn spoke for
// (ADR-194). An automation keeps it as its owner.
func (s *Service) owner(ctx context.Context, a storage.Action) string {
	if !strings.HasPrefix(a.DecidedBy, "auto:") {
		return a.DecidedBy
	}
	if p := PrincipalOf(ctx); p != "" {
		return p
	}
	if j, err := s.store.Jobs().Get(ctx, a.JobID); err == nil {
		if as, ok := storage.SpeaksFor(ctx, s.store, j); ok {
			return strings.TrimPrefix(as, "human:")
		}
	}
	return a.DecidedBy
}
