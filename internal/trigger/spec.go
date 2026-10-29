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
	AutomationID string `json:"automation_id,omitempty"` // update: which one
	// Enabled turns it on or off (ADR-200); nil = a new one on, an edited one as it is.
	Enabled *bool  `json:"enabled,omitempty"`
	Name    string `json:"name"`
	Source  string `json:"source"` // schedule | webhook | event
	// Event, EventFrom, EventSameChat: source event (ADR-243)
	Event         string                     `json:"event,omitempty"`
	EventFrom     string                     `json:"event_from,omitempty"`
	EventSameChat bool                       `json:"event_same_chat,omitempty"`
	EveryMinutes  int                        `json:"every_minutes,omitempty"`
	Cron          string                     `json:"cron,omitempty"`
	Timezone      string                     `json:"timezone,omitempty"`
	Action        string                     `json:"action"`             // script | chat | workflow
	Workflow      string                     `json:"workflow,omitempty"` // workflow: its key; prompt is the input
	AgentID       string                     `json:"agent_id,omitempty"`
	Prompt        string                     `json:"prompt,omitempty"`
	Script        storage.AutomationScript   `json:"script"`
	Escalate      storage.AutomationEscalate `json:"escalate"`          // gone (ADR-057): read, never kept
	Goal          *storage.AutomationGoal    `json:"goal,omitempty"`    // chat/workflow: run until it is reached (ADR-132)
	Tags          []string                   `json:"tags,omitempty"`    // on each run's chat (none = the ones it has)
	EndsAt        *time.Time                 `json:"ends_at,omitempty"` // schedule: it stops then (none = the one it has)
	// OnlyIf: a schedule runs only when its data says there is work (ADR-194)
	OnlyIf *storage.AutomationCondition `json:"only_if,omitempty"`
	// Limits: its guards (none = the ones it has)
	Limits *storage.AutomationLimits `json:"limits,omitempty"`
}

// Check rejects what could not run: a bad schedule, an unknown language, a
// script too large or too slow, an unknown escalation.
func (s *Spec) Check() error {
	s.Name, s.Workflow = strings.TrimSpace(s.Name), strings.TrimSpace(s.Workflow)
	switch {
	case s.Name == "":
		return errors.New("tự động hóa cần tên")
	case s.Source != "schedule" && s.Source != "webhook" && s.Source != "event":
		return errors.New("nguồn phải là schedule, webhook hoặc event")
	case s.Source == "event" && (s.Event != "" || s.AutomationID == "") && !ValidEvent(s.Event): // an edit may keep its event
		return fmt.Errorf("sự kiện phải là một trong: %s", strings.Join(Events, ", "))
	case s.Action != "script" && s.Action != "chat" && s.Action != "workflow":
		return errors.New("hành động phải là script, chat hoặc workflow")
	case s.Action == "workflow" && strings.TrimSpace(s.Workflow) == "":
		return errors.New("hãy chọn quy trình để chạy")
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
	goal, err := CleanGoal(s.Goal, s.Action)
	if err != nil {
		return err
	}
	s.Goal = goal
	if s.EndsAt != nil && !s.EndsAt.After(time.Now()) {
		return errors.New("giờ dừng đã qua")
	}
	if s.Source == "schedule" {
		if err := Validate(s.config()); err != nil {
			return err
		}
	}
	if c := s.OnlyIf; c != nil {
		c.Collection, c.Board = strings.TrimSpace(c.Collection), strings.TrimSpace(c.Board)
		switch {
		case s.Source != "schedule":
			return errors.New("điều kiện chạy (only_if) chỉ dành cho lịch chạy")
		case c.Board != "" && c.Board != storage.BoardOpen && c.Board != storage.BoardChanged && c.Board != storage.BoardAny:
			return errors.New("điều kiện theo bảng việc (board) là open, changed hoặc any")
		case c.Collection == "" && c.Board == "":
			return errors.New("điều kiện chạy cần bảng dữ liệu (collection) hoặc bảng việc (board)")
		}
	}
	if l := s.Limits; l != nil && (l.DailyCostUSD < 0 || l.MaxMinutes < 0 || l.MaxRunsPerHour < 0) {
		return errors.New("giới hạn không được âm")
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
	cfg.Workflow = ""
	if s.Action == "workflow" {
		cfg.Workflow = strings.TrimSpace(s.Workflow)
	}
	cfg.Goal = nil
	if s.Action == "chat" || s.Action == "workflow" {
		cfg.Goal = s.Goal
	}
	if len(s.Tags) > 0 {
		cfg.Tags = s.Tags
	}
	cfg.OnlyIf = nil
	if s.Source == "schedule" {
		cfg.OnlyIf = s.OnlyIf
	}
	if s.Limits != nil {
		a.Limits = *s.Limits
	}
	cfg.EndsAt = a.Config.EndsAt
	if s.EndsAt != nil {
		end := s.EndsAt.UTC()
		cfg.EndsAt = &end
	}
	if s.Source == "event" {
		cfg.Event, cfg.EventFrom, cfg.EventSameChat = s.Event, strings.TrimSpace(s.EventFrom), s.EventSameChat && strings.HasPrefix(s.Event, "turn.")
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
		if ag.ProjectID != projectID {
			return errors.New("agent không thuộc project này")
		}
	}
	return nil
}
