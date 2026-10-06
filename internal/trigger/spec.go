package trigger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Spec is an automation as an agent proposes it (ADR-041): flat, validated
// before anyone is asked to approve it.
type Spec struct {
	AutomationID string                     `json:"automation_id,omitempty"` // update: which one
	Name         string                     `json:"name"`
	Source       string                     `json:"source"` // schedule | webhook
	EveryMinutes int                        `json:"every_minutes,omitempty"`
	Cron         string                     `json:"cron,omitempty"`
	Timezone     string                     `json:"timezone,omitempty"`
	Action       string                     `json:"action"` // script | chat | task
	AgentID      string                     `json:"agent_id,omitempty"`
	Prompt       string                     `json:"prompt,omitempty"`
	Script       storage.AutomationScript   `json:"script"`
	Escalate     storage.AutomationEscalate `json:"escalate"`
	Tags         []string                   `json:"tags,omitempty"`    // on each run's chat (none = the ones it has)
	EndsAt       *time.Time                 `json:"ends_at,omitempty"` // schedule: it stops then (none = the one it has)
}

// Check rejects what could not run: a bad schedule, an unknown language, a
// script too large or too slow, an unknown escalation.
func (s *Spec) Check() error {
	s.Name = strings.TrimSpace(s.Name)
	switch {
	case s.Name == "":
		return errors.New("tự động hóa cần tên")
	case s.Source != "schedule" && s.Source != "webhook":
		return errors.New("nguồn phải là schedule hoặc webhook")
	case s.Action != "script" && s.Action != "chat":
		return errors.New("hành động phải là script hoặc chat")
	}
	var tags []string
	for _, t := range s.Tags {
		if t = strings.Join(strings.Fields(t), " "); t != "" {
			if len([]rune(t)) > 30 {
				return errors.New("tag tối đa 30 ký tự")
			}
			tags = append(tags, t)
		}
	}
	if len(tags) > 10 {
		return errors.New("tối đa 10 tag")
	}
	s.Tags = tags
	if s.EndsAt != nil && !s.EndsAt.After(time.Now()) {
		return errors.New("giờ dừng đã qua")
	}
	if s.Source == "schedule" {
		if err := Validate(s.config()); err != nil {
			return err
		}
	}
	if s.Action == "script" {
		if !ValidLang(s.Script.Lang) {
			return fmt.Errorf("ngôn ngữ script %q không hỗ trợ (bash, node, python)", s.Script.Lang)
		}
		if strings.TrimSpace(s.Script.Body) == "" || len(s.Script.Body) > MaxScript {
			return errors.New("script trống hoặc quá 64KB")
		}
		if s.Script.TimeoutS < 0 || time.Duration(s.Script.TimeoutS)*time.Second > MaxScriptTimeout {
			return errors.New("timeout của script từ 1 tới 3600 giây")
		}
	}
	return nil
}

func (s Spec) config() storage.AutomationConfig {
	c := storage.AutomationConfig{EveryMinutes: s.EveryMinutes, Cron: strings.TrimSpace(s.Cron), Timezone: strings.TrimSpace(s.Timezone)}
	if c.Cron != "" {
		c.EveryMinutes = 0
	}
	return c
}

// Apply puts the spec on a (its id, project, secret and state stay) and
// works out the next run.
func (s Spec) Apply(a *storage.Automation, now time.Time) {
	cfg := s.config()
	cfg.Auth, cfg.AuthName, cfg.SecretHash, cfg.ConversationID = a.Config.Auth, a.Config.AuthName, a.Config.SecretHash, a.Config.ConversationID
	cfg.Tags = a.Config.Tags
	if len(s.Tags) > 0 {
		cfg.Tags = s.Tags
	}
	cfg.EndsAt = a.Config.EndsAt
	if s.EndsAt != nil {
		end := s.EndsAt.UTC()
		cfg.EndsAt = &end
	}
	if s.Source == "webhook" && cfg.Auth == "" {
		cfg.Auth = "bearer"
	}
	a.Name, a.Source, a.Action, a.AgentID, a.Prompt, a.Config = s.Name, s.Source, s.Action, s.AgentID, s.Prompt, cfg
	a.Script, a.Escalate = s.Script, storage.AutomationEscalate{} // a script calls no agent in (ADR-057)
	a.NextRunAt = nil
	if a.Source == "schedule" {
		if next, err := NextRun(a.Config, now); err == nil {
			a.NextRunAt = &next
		}
	}
}

// CheckAgents makes sure every agent an automation calls belongs to the
// project's own model (the UI and agent paths share it).
func CheckAgents(ctx context.Context, st storage.Store, projectID string, ids ...string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		ag, err := st.Agents().Get(ctx, id)
		if err != nil {
			return errors.New("không tìm thấy agent")
		}
		if m, err := st.OrgModels().Get(ctx, ag.OrgModelID); err != nil || m.RepoID != projectID {
			return errors.New("agent không thuộc mô hình của project này")
		}
	}
	return nil
}
