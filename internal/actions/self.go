package actions

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// Bounds of what an agent with perm.CapSelf does on its own (ADR-194).
const (
	SelfMinGap      = 30 * time.Minute // between two runs of a schedule
	SelfMinGapIf    = 5 * time.Minute  // one that runs only when there is work (only_if, ADR-207, ADR-219)
	SelfMaxDailyUSD = 5.0              // a schedule's daily cost cap, which one that always runs must have
	SelfMaxAgents   = 15               // agents a project may reach by agents making them (ADR-204)
)

// selfAutomation: a schedule an agent may make or change on its own: it
// sends a message or runs a workflow (never a script), runs at most every
// SelfMinGap with a daily cost cap of at most SelfMaxDailyUSD, or, when it
// runs only when there is work (only_if), at most every SelfMinGapIf with
// no cap needed (ADR-219). One a person set to run with other rights
// (override) stays theirs.
func (s *Service) selfAutomation(ctx context.Context, a storage.Action) bool {
	var spec trigger.Spec
	if json.Unmarshal(a.Args.Automation, &spec) != nil || spec.Source != "schedule" || (spec.Action != "chat" && spec.Action != "workflow") {
		return false
	}
	l := spec.Limits
	if a.Kind == "update_automation" {
		x, err := s.store.Automations().Get(ctx, spec.AutomationID)
		if err != nil || x.ProjectID != a.ProjectID || x.PermissionMode == "override" || x.Action == "script" {
			return false
		}
		if l == nil {
			l = &x.Limits
		}
	}
	least := SelfMinGap
	if spec.OnlyIf != nil || a.Kind == "update_automation" && s.hasOnlyIf(ctx, spec.AutomationID) {
		least = SelfMinGapIf // no work, no run: it costs nothing between
	} else if l == nil || l.DailyCostUSD <= 0 || l.DailyCostUSD > SelfMaxDailyUSD {
		return false
	}
	return minGap(storage.AutomationConfig{EveryMinutes: spec.EveryMinutes, Cron: spec.Cron, Timezone: spec.Timezone}) >= least
}

// hasOnlyIf: the automation id runs only when there is work.
func (s *Service) hasOnlyIf(ctx context.Context, id string) bool {
	x, err := s.store.Automations().Get(ctx, id)
	return err == nil && x.Config.OnlyIf != nil
}

// inheritAdmin: the agents at Vận hành an administrator's run made (a
// project's, or ones it added: those not in skip) run as administrator too,
// in the name of the admin that run acts for (ADR-207): a company's units
// and staff work on their own, as its board does. Office checks at each run
// that this person is still an admin (ADR-074).
func (s *Service) inheritAdmin(ctx context.Context, projectID, by string, skip map[string]bool) error {
	agents, err := s.store.Agents().List(ctx, projectID)
	if err != nil {
		return err
	}
	for _, x := range agents {
		if skip[x.ID] || perm.Agent(x) != perm.Operate || x.Permissions.FullAccess {
			continue
		}
		x.Permissions.FullAccess, x.Permissions.FullAccessBy = true, by
		if err := s.store.Agents().Update(ctx, x); err != nil {
			return err
		}
	}
	return nil
}

// leadAdmin: an agent an administrator's run makes a lead (Vận hành with
// self.manage: it adds agents, workflows and schedules of its own) runs as
// administrator too, as the CEO and the heads of the units it opens do
// (ADR-214), in the name of the same admin.
func (s *Service) leadAdmin(ctx context.Context, id, by string) error {
	x, err := s.store.Agents().Get(ctx, id)
	if err != nil || x.Permissions.FullAccess || perm.Agent(x) != perm.Operate ||
		x.Permissions.Caps == nil || !slices.Contains(*x.Permissions.Caps, perm.CapSelf) {
		return nil
	}
	x.Permissions.FullAccess, x.Permissions.FullAccessBy = true, by
	return s.store.Agents().Update(ctx, x)
}

// selfChange: a settings change an agent may make on its own: a workflow of
// its project, made, changed or deleted; a schedule of its project it could
// have made itself, deleted; or a new agent of its project (ADR-204) that
// is no administrator, reads no folder outside, and keeps the project under
// SelfMaxAgents.
func (s *Service) selfChange(ctx context.Context, projectID string, c *storage.ConfigChange) bool {
	if c == nil {
		return false
	}
	switch {
	case c.Resource == "workflow": // made, changed or retired as the work changes (ADR-208)
		return c.Op == "create" || c.Op == "update" || c.Op == "delete"
	case c.Resource == "automation" && c.Op == "delete": // a schedule it could have made itself
		x, err := s.store.Automations().Get(ctx, c.ID)
		return err == nil && x.ProjectID == projectID && x.Source == "schedule" && x.Action != "script" && x.PermissionMode != "override"
	case c.Resource == "agent" && c.Op == "create":
		var in struct {
			Permissions struct {
				FullAccess bool     `json:"full_access"`
				ExtraDirs  []string `json:"extra_dirs"`
			} `json:"permissions"`
		}
		if json.Unmarshal(c.Patch, &in) != nil || in.Permissions.FullAccess || len(in.Permissions.ExtraDirs) > 0 {
			return false
		}
		list, err := s.store.Agents().List(ctx, projectID)
		return err == nil && len(list) < SelfMaxAgents
	}
	return false
}

// minGap is the shortest time between two runs of a schedule over a week
// (0 when it cannot be read).
func minGap(cfg storage.AutomationConfig) time.Duration {
	if cfg.Cron != "" {
		cfg.EveryMinutes = 0
	}
	t, err := trigger.NextRun(cfg, time.Now())
	if err != nil {
		return 0
	}
	gap, end := 7*24*time.Hour, t.Add(7*24*time.Hour)
	for i := 0; i < 400 && t.Before(end); i++ {
		n, err := trigger.NextRun(cfg, t)
		if err != nil {
			return 0
		}
		if d := n.Sub(t); d < gap {
			gap = d
		}
		if gap < SelfMinGap {
			break
		}
		t = n
	}
	return gap
}
