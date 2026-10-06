package api

import (
	"errors"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/channels"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// Automations: schedules and webhooks that start jobs (ADR-040).

func (s *server) triggerRoutes(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.Handler { return s.requireAuth(h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.requireRole(storage.RoleAdmin, h) }
	mux.Handle("GET /api/projects/{id}/automations", auth(s.listAutomations))
	mux.Handle("POST /api/projects/{id}/automations", admin(s.createAutomation))
	mux.Handle("GET /api/automations/preview-schedule", auth(s.previewSchedule))
	mux.Handle("GET /api/projects/{id}/automations/weekly-reset", auth(s.automationWeeklyReset))
	mux.Handle("GET /api/automations/{id}", auth(s.getAutomation))
	mux.Handle("PATCH /api/automations/{id}", admin(s.updateAutomation))
	mux.Handle("DELETE /api/automations/{id}", admin(s.deleteAutomation))
	mux.Handle("POST /api/automations/{id}/run", admin(s.runAutomation))
	mux.Handle("POST /api/automations/{id}/rotate-secret", admin(s.rotateSecret))
	mux.Handle("POST /api/automations/{id}/conversation", auth(s.automationConversation))
	mux.Handle("POST /api/projects/{id}/automations/test-script", admin(s.testScript))

	mux.Handle("GET /api/jobs", auth(s.listJobs))
	mux.Handle("GET /api/jobs/groups", auth(s.jobGroups)) // the Job page: by piece of work
	mux.Handle("GET /api/incidents", auth(s.incidents))
	mux.Handle("POST /api/incidents/dismiss", admin(s.dismissIncident))
	mux.Handle("POST /api/incidents/retry", admin(s.retryIncident))
	mux.Handle("GET /api/jobs/stats", auth(s.jobStats))
	mux.Handle("GET /api/jobs/{id}", auth(s.getJob))
	mux.Handle("POST /api/jobs/{id}/cancel", admin(s.cancelJob))
	mux.Handle("POST /api/jobs/{id}/retry", admin(s.retryJob))
}

type automationDTO struct {
	ID             string                     `json:"id"`
	ProjectID      string                     `json:"project_id"`
	Name           string                     `json:"name"`
	Enabled        bool                       `json:"enabled"`
	Source         string                     `json:"source"`
	Config         map[string]any             `json:"config"`
	Action         string                     `json:"action"`
	AgentID        string                     `json:"agent_id"`
	Prompt         string                     `json:"prompt"`
	EditMode       string                     `json:"edit_mode"`
	ModelTier      string                     `json:"model_tier"`
	KeepContext    bool                       `json:"keep_context"`
	Limits         storage.AutomationLimits   `json:"limits"`
	Script         storage.AutomationScript   `json:"script"`
	Escalate       storage.AutomationEscalate `json:"escalate"`
	Failures       int                        `json:"failures"`
	DisabledCode   string                     `json:"disabled_code"`
	DisabledReason string                     `json:"disabled_reason"`
	LastRunAt      *time.Time                 `json:"last_run_at"`
	NextRunAt      *time.Time                 `json:"next_run_at"`
	WebhookURL     string                     `json:"webhook_url,omitempty"`
	Bot            *automationBot             `json:"bot,omitempty"`        // telegram | discord: the bot it listens to
	BotStatus      *automationBotStatus       `json:"bot_status,omitempty"` // …and how that bot is doing
	LastJob        *jobDTO                    `json:"last_job"`
	CreatedAt      time.Time                  `json:"created_at"`
	// PermissionMode: "agent" (default, follows the agent's own Permissions) or
	// "override" (admin only, replaces them for this automation) — ADR-074.
	PermissionMode     string   `json:"permission_mode"`
	OverrideFullAccess bool     `json:"override_full_access"`
	OverrideAdminBy    string   `json:"override_admin_by"`
	OverrideExtraDirs  []string `json:"override_extra_dirs"`
	Version            string   `json:"version"` // what an edit is made from (ADR-072)
}

func (s *server) toAutomationDTO(r *http.Request, a storage.Automation) automationDTO {
	c := a.Config
	cfg := map[string]any{"every_minutes": c.EveryMinutes, "cron": c.Cron, "timezone": c.Timezone, "auth": c.Auth, "auth_name": c.AuthName,
		"pull_request": c.PullRequest, "notify_channel_id": c.NotifyChannelID, "notify_chat_id": c.NotifyChatID, "ends_at": c.EndsAt}
	if trigger.IsChannel(a.Source) {
		keywords := c.Keywords
		if keywords == nil {
			keywords = []string{}
		}
		cfg = map[string]any{"channel_id": c.ChannelID, "keywords": keywords, "scope": c.Scope,
			"command": c.Command, "command_description": c.CommandDescription, "command_arg": c.CommandArg, "skill": c.Skill, "reply_mode": c.ReplyMode, "own_setup": c.OwnSetup}
	}
	tags := c.Tags
	if tags == nil {
		tags = []string{}
	}
	cfg["tags"] = tags
	d := automationDTO{ID: a.ID, ProjectID: a.ProjectID, Name: a.Name, Enabled: a.Enabled, Source: a.Source, Config: cfg, Action: a.Action,
		AgentID: a.AgentID, Prompt: a.Prompt, EditMode: a.EditMode, ModelTier: a.ModelTier, KeepContext: a.KeepContext, Limits: a.Limits, Script: a.Script, Escalate: a.Escalate, Failures: a.Failures,
		DisabledCode: a.DisabledCode, DisabledReason: a.DisabledReason, LastRunAt: a.LastRunAt, NextRunAt: a.NextRunAt, CreatedAt: a.CreatedAt,
		PermissionMode: a.PermissionMode, OverrideFullAccess: a.OverrideFullAccess, OverrideAdminBy: a.OverrideAdminBy, OverrideExtraDirs: a.OverrideExtraDirs}
	d.Version = automationVersion(a)
	if a.Source == "webhook" {
		d.WebhookURL = "/hooks/" + a.ID
	}
	if trigger.IsChannel(a.Source) {
		d.Bot, d.BotStatus = s.botOf(r, a)
	}
	if jobs, err := s.cfg.Store.Jobs().List(r.Context(), storage.JobFilter{Origin: "automation", OriginID: a.ID, Limit: 1}); err == nil && len(jobs) > 0 {
		j := s.toJobDTO(r, jobs[0], nil)
		d.LastJob = &j
	}
	return d
}

type automationInput struct {
	Version     string                     `json:"version"` // the automation as it was read (409 when changed since)
	Name        string                     `json:"name"`
	Enabled     *bool                      `json:"enabled"`
	Source      string                     `json:"source"`
	Action      string                     `json:"action"`
	AgentID     string                     `json:"agent_id"`
	Prompt      string                     `json:"prompt"`
	EditMode    string                     `json:"edit_mode"`
	ModelTier   string                     `json:"model_tier"` // "" = the agent's own
	KeepContext bool                       `json:"keep_context"`
	Config      storage.AutomationConfig   `json:"config"`
	Limits      storage.AutomationLimits   `json:"limits"`
	Script      storage.AutomationScript   `json:"script"`
	Escalate    storage.AutomationEscalate `json:"escalate"`
	// ConversationID ties the chat that built it (ADR-042)
	ConversationID string `json:"conversation_id"`
	// Bot: telegram | discord, the bot's own settings (a new one without channel_id)
	Bot *botInput `json:"bot"`
	// PermissionMode: "agent" (default) or "override" (admin only) — ADR-074
	PermissionMode     string   `json:"permission_mode"`
	OverrideFullAccess bool     `json:"override_full_access"`
	OverrideExtraDirs  []string `json:"override_extra_dirs"`
	// read back from the automation as the dashboard got it: ignored, the
	// server stamps who turned full access on (the admin saving it)
	OverrideAdminBy string `json:"override_admin_by"`
}

// apply validates in and puts it on a (the secret hash and state stay).
func (s *server) applyAutomation(r *http.Request, in automationInput, a *storage.Automation) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("hãy đặt tên cho tự động hóa")
	}
	user := userFrom(r)
	isAdmin := user.Role == storage.RoleAdmin
	// PermissionMode: "agent" (default, follows the agent's own Permissions) or
	// "override" (admin only, replaces them) — ADR-074
	in.PermissionMode = strings.TrimSpace(in.PermissionMode)
	if in.PermissionMode != "override" {
		in.PermissionMode = "agent"
	}
	if in.PermissionMode == "override" && !isAdmin {
		return errors.New("chỉ admin được ghi đè quyền của tự động hóa")
	}
	// ADR-074 security fix: chỉ tự động hóa chạy theo lịch mới ghi đè được
	// quyền administrator — webhook (ai có URL cũng gọi được), webhook PR
	// (diff do người ngoài viết) và tin nhắn kênh không bao giờ đáng tin.
	if in.PermissionMode == "override" && in.OverrideFullAccess && in.Source != "schedule" {
		return errors.New("chỉ tự động hóa chạy theo lịch mới ghi đè được quyền administrator (webhook và tin nhắn kênh không đủ tin cậy)")
	}
	if in.PermissionMode == "override" {
		projectPath := ""
		if repo, err := s.cfg.Store.Repos().Get(r.Context(), a.ProjectID); err == nil {
			projectPath = repo.Path
		}
		for _, dir := range in.OverrideExtraDirs {
			if dir = strings.TrimSpace(dir); dir == "" {
				continue
			}
			if err := perm.CheckExtraDir(projectPath, dir); err != nil {
				return err
			}
		}
		a.PermissionMode, a.OverrideExtraDirs = "override", in.OverrideExtraDirs
		if in.OverrideFullAccess {
			a.OverrideFullAccess, a.OverrideAdminBy = true, user.Email
		} else {
			a.OverrideFullAccess, a.OverrideAdminBy = false, ""
		}
	} else {
		a.PermissionMode, a.OverrideFullAccess, a.OverrideAdminBy, a.OverrideExtraDirs = "agent", false, "", nil
	}
	if in.Source != "schedule" && in.Source != "webhook" && !trigger.IsChannel(in.Source) {
		return errors.New("nguồn phải là lịch chạy, webhook hoặc tin nhắn kênh (telegram, discord)")
	}
	if in.ModelTier != "" && !storage.ValidTier(in.ModelTier) {
		return errors.New("cấp model phải là mạnh, cân bằng hoặc nhanh")
	}
	a.ModelTier = in.ModelTier
	if in.Action != "chat" && in.Action != "script" {
		return errors.New("hành động phải là gửi tin (chat) hoặc chạy code (script)")
	}
	if in.Action == "script" {
		src := in.Source
		if trigger.IsChannel(src) {
			src = "webhook" // a message arrives like a delivery: the script checks the same way
		}
		spec := trigger.Spec{Name: in.Name, Source: src, EveryMinutes: in.Config.EveryMinutes, Cron: in.Config.Cron, Timezone: in.Config.Timezone,
			Action: in.Action, Script: in.Script}
		if err := spec.Check(); err != nil {
			return err
		}
	}
	if err := trigger.CheckAgents(r.Context(), s.cfg.Store, a.ProjectID, in.AgentID, ""); err != nil {
		return err
	}
	cfg := storage.AutomationConfig{ConversationID: a.Config.ConversationID, SecretHash: a.Config.SecretHash}
	if in.Source == "schedule" {
		cfg.EveryMinutes, cfg.Cron, cfg.Timezone = in.Config.EveryMinutes, strings.TrimSpace(in.Config.Cron), strings.TrimSpace(in.Config.Timezone)
		if cfg.Cron != "" {
			cfg.EveryMinutes = 0
		}
		if err := trigger.Validate(cfg); err != nil {
			return err
		}
		if in.Config.EndsAt != nil { // its stop time, as a Burn's
			end := in.Config.EndsAt.UTC()
			on := a.Enabled
			if in.Enabled != nil {
				on = *in.Enabled
			}
			if on && !end.After(time.Now()) {
				return errors.New("giờ dừng đã qua: hãy đặt giờ dừng mới hoặc bỏ giờ dừng")
			}
			cfg.EndsAt = &end
		}
	} else if trigger.IsChannel(in.Source) { // ADR-049
		ch, err := s.cfg.Store.Channels().Get(r.Context(), in.Config.ChannelID)
		if err != nil || ch.ProjectID != a.ProjectID {
			return errors.New("hãy chọn kênh chat của project này")
		}
		if ch.Kind != in.Source {
			return errors.New("kênh đã chọn là " + ch.Kind + ", không phải " + in.Source)
		}
		cfg.ChannelID, cfg.Scope = ch.ID, strings.TrimSpace(in.Config.Scope)
		for _, k := range in.Config.Keywords {
			if k = strings.TrimSpace(k); k != "" {
				cfg.Keywords = append(cfg.Keywords, k)
			}
		}
		switch in.Config.ReplyMode { // "" = the bot's own
		case "", storage.ReplyAnswer, storage.ReplySteps:
			cfg.ReplyMode = in.Config.ReplyMode
			cfg.OwnSetup = in.Config.OwnSetup
		default:
			return errors.New("kiểu trả lời không hợp lệ")
		}
		if strings.TrimSpace(in.Config.Command) != "" { // a custom command: it runs only as one
			name := channels.CommandName(in.Config.Command)
			if name == "" {
				return errors.New("tên lệnh cần có chữ hoặc số")
			}
			if slices.Contains(channels.Reserved, name) {
				return errors.New("/" + name + " là lệnh có sẵn của bot, hãy đặt tên khác")
			}
			others, _ := s.cfg.Store.Automations().List(r.Context(), a.ProjectID)
			for _, o := range others {
				if o.ID != a.ID && o.Config.ChannelID == ch.ID && o.Config.Command == name {
					return errors.New("bot đã có lệnh /" + name + " (tự động hóa \"" + o.Name + "\")")
				}
			}
			cfg.Command, cfg.Keywords, cfg.Scope = name, nil, ""
			cfg.CommandDescription = strings.TrimSpace(in.Config.CommandDescription)
			cfg.CommandArg = strings.TrimSpace(in.Config.CommandArg)
			cfg.Skill = strings.TrimSpace(in.Config.Skill)
		}
	} else {
		cfg.Auth, cfg.AuthName = in.Config.Auth, strings.TrimSpace(in.Config.AuthName)
		if cfg.Auth != "header" && cfg.Auth != "query" {
			cfg.Auth = "bearer"
		}
		cfg.PullRequest = in.Config.PullRequest // a GitHub/Bitbucket PR webhook
	}
	if !trigger.IsChannel(in.Source) && strings.TrimSpace(in.Config.NotifyChannelID) != "" { // its answers go to a bot's chat of the project too
		ch, err := s.cfg.Store.Channels().Get(r.Context(), strings.TrimSpace(in.Config.NotifyChannelID))
		if err != nil || ch.ProjectID != a.ProjectID {
			return errors.New("hãy chọn bot của project này để gửi kết quả")
		}
		cfg.NotifyChannelID, cfg.NotifyChatID = ch.ID, strings.TrimSpace(in.Config.NotifyChatID)
		if cfg.NotifyChatID == "" {
			return errors.New("hãy điền channel/chat id để gửi kết quả")
		}
	}
	tags, err := cleanTags(in.Config.Tags) // put on the chat of each run
	if err != nil {
		return err
	}
	if len(tags) > 0 {
		cfg.Tags = tags
	}
	lim := in.Limits
	lim.MaxRunsPerHour, lim.DisableAfterFailures = max(lim.MaxRunsPerHour, 0), max(lim.DisableAfterFailures, 0)
	lim.DebounceSeconds, lim.DebounceMaxSeconds = min(max(lim.DebounceSeconds, 0), 3600), min(max(lim.DebounceMaxSeconds, 0), 6*3600)
	lim.DailyCostUSD = max(lim.DailyCostUSD, 0)
	lim.MaxParallel, lim.MaxMinutes = min(max(lim.MaxParallel, 0), 10), max(lim.MaxMinutes, 0) // runs at once, minutes each (ADR-082)
	// full access needs no limits: a cost cap or an auto-disable are the admin's own choice
	a.Name, a.Source, a.Action, a.AgentID, a.Prompt, a.KeepContext = in.Name, in.Source, in.Action, in.AgentID, in.Prompt, in.KeepContext
	a.EditMode, a.Config, a.Limits = s.allowedEditMode(r, in.EditMode), cfg, lim
	a.Script, a.Escalate = storage.AutomationScript{}, storage.AutomationEscalate{} // a script calls no agent in (ADR-057)
	if a.Action == "script" {
		a.Script = in.Script
	}
	if in.Enabled != nil {
		if *in.Enabled && !a.Enabled {
			a.Failures, a.DisabledCode, a.DisabledReason = 0, "", "" // turned back on
		}
		a.Enabled = *in.Enabled
	}
	a.NextRunAt = nil
	if a.Source == "schedule" {
		if next, err := trigger.NextRun(a.Config, time.Now().UTC()); err == nil {
			a.NextRunAt = &next
		}
	}
	return nil
}

func (s *server) listAutomations(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Automations().List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]automationDTO, 0, len(list))
	for _, a := range list {
		out = append(out, s.toAutomationDTO(r, a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"automations": out})
}

// auditAutomationFullAccess logs a dedicated entry when an automation's
// permission override changed (ADR-074): easy to spot in Nhật ký, besides the
// full before/after already on automation.create/automation.update.
func (s *server) auditAutomationFullAccess(r *http.Request, old, a storage.Automation) {
	// "" (never set) and "agent" (applyAutomation's explicit default) mean the
	// same thing: no override — comparing the raw strings would log a change
	// on every create/update of an automation that never used an override.
	normalize := func(mode string) string {
		if mode != "override" {
			return "agent"
		}
		return mode
	}
	if normalize(old.PermissionMode) == normalize(a.PermissionMode) && old.OverrideFullAccess == a.OverrideFullAccess &&
		old.OverrideAdminBy == a.OverrideAdminBy && slices.Equal(old.OverrideExtraDirs, a.OverrideExtraDirs) {
		return
	}
	s.audit(r, audit.Change{Action: "automation.full_access", ResourceID: a.ID, ProjectID: a.ProjectID,
		Before: map[string]any{"permission_mode": old.PermissionMode, "override_full_access": old.OverrideFullAccess,
			"override_admin_by": old.OverrideAdminBy, "override_extra_dirs": old.OverrideExtraDirs},
		After: map[string]any{"permission_mode": a.PermissionMode, "override_full_access": a.OverrideFullAccess,
			"override_admin_by": a.OverrideAdminBy, "override_extra_dirs": a.OverrideExtraDirs},
		Detail: map[string]any{"name": a.Name}})
}

func (s *server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var in automationInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	defer s.lockAutomations(p.ID)()
	a := storage.Automation{ProjectID: p.ID, Enabled: true, CreatedBy: userFrom(r).Email}
	newBot, commitBot, err := s.saveBot(r, &in, p.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.applyAutomation(r, in, &a); err != nil {
		s.dropBot(r, newBot)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := ""
	if a.Source == "webhook" {
		secret, a.Config.SecretHash = trigger.NewSecret()
	}
	a, err = s.cfg.Store.Automations().Create(r.Context(), a)
	if err != nil {
		s.dropBot(r, newBot)
		s.audit(r, audit.Change{Action: "automation.create", ProjectID: p.ID, After: a, Err: err})
		s.internal(w, r, err)
		return
	}
	if err := commitBot(); err != nil {
		s.internal(w, r, err)
		return
	}
	s.reloadBot(a)
	s.linkBuilder(r, a, in.ConversationID)
	s.audit(r, audit.Change{Action: "automation.create", ResourceID: a.ID, ProjectID: p.ID, After: s.toAutomationDTO(r, a),
		Detail: map[string]any{"name": a.Name, "source": a.Source}})
	s.auditAutomationFullAccess(r, storage.Automation{}, a)
	out := map[string]any{"automation": s.toAutomationDTO(r, a)}
	if secret != "" {
		out["secret"] = secret // shown once
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) getAutomation(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.Automations().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automation": s.toAutomationDTO(r, a)})
}

func (s *server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	var in automationInput
	if !decode(w, r, &in) {
		return
	}
	a, err := s.cfg.Store.Automations().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, in.Version, automationVersion(a)) {
		return
	}
	defer s.lockAutomations(a.ProjectID)()
	old := a
	wasWebhook := a.Source == "webhook"
	newBot, commitBot, err := s.saveBot(r, &in, a.ProjectID)
	if errors.Is(err, errConflict) {
		conflicted(w, "x", "y") // the bot was changed since it was read
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.applyAutomation(r, in, &a); err != nil {
		s.dropBot(r, newBot)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := ""
	if a.Source == "webhook" && !wasWebhook {
		secret, a.Config.SecretHash = trigger.NewSecret()
	}
	if a.Source != "webhook" {
		a.Config.SecretHash = ""
	}
	change := audit.Change{Action: "automation.update", ResourceID: a.ID, ProjectID: a.ProjectID,
		Before: s.toAutomationDTO(r, old), After: s.toAutomationDTO(r, a), Detail: map[string]any{"name": a.Name, "enabled": a.Enabled}}
	if err := s.cfg.Store.Automations().Update(r.Context(), a); err != nil {
		change.Err = err
		s.audit(r, change)
		s.internal(w, r, err)
		return
	}
	if err := commitBot(); err != nil { // the bot changes only once the automation is saved
		s.internal(w, r, err)
		return
	}
	s.linkBuilder(r, a, in.ConversationID)
	s.audit(r, change)
	s.auditAutomationFullAccess(r, old, a)
	s.reloadBot(a)
	if old.Config.ChannelID != a.Config.ChannelID {
		s.releaseBot(r, old) // it moved to another bot, or off bots
	}
	out := map[string]any{"automation": s.toAutomationDTO(r, a)}
	if secret != "" {
		out["secret"] = secret
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, err := s.cfg.Store.Automations().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Automations().Delete(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.releaseBot(r, old)
	s.audit(r, audit.Change{Action: "automation.delete", ResourceID: id, ProjectID: old.ProjectID, Before: s.toAutomationDTO(r, old)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) runAutomation(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Trigger == nil {
		writeError(w, http.StatusServiceUnavailable, "bộ chạy tự động chưa bật")
		return
	}
	a, err := s.cfg.Store.Automations().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if trigger.IsChannel(a.Source) {
		writeError(w, http.StatusBadRequest, "tự động hóa này chạy khi có tin nhắn tới bot, không chạy tay được")
		return
	}
	j, _, err := s.cfg.Trigger.Enqueue(r.Context(), a, "manual", "", "", "")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.cfg.Trigger.StartReady(detached(r), time.Now().UTC())
	s.auditAction(r, "automation.run", a.ID, map[string]any{"job": j.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": s.toJobDTO(r, j, nil)})
}

func (s *server) rotateSecret(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.Automations().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if a.Source != "webhook" {
		writeError(w, http.StatusBadRequest, "chỉ webhook mới có secret")
		return
	}
	var secret string
	secret, a.Config.SecretHash = trigger.NewSecret()
	if err := s.cfg.Store.Automations().Update(r.Context(), a); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "automation.rotate", a.ID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *server) previewSchedule(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	every, _ := strconv.Atoi(q.Get("every"))
	cfg := storage.AutomationConfig{EveryMinutes: every, Cron: strings.TrimSpace(q.Get("cron")), Timezone: q.Get("tz")}
	if err := trigger.Validate(cfg); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"next": []time.Time{}, "error": err.Error()})
		return
	}
	next := trigger.Upcoming(cfg, time.Now().UTC(), 5)
	if end, err := time.Parse(time.RFC3339, q.Get("ends")); err == nil { // its stop time: none after it
		kept := []time.Time{}
		for _, t := range next {
			if t.Before(end) {
				kept = append(kept, t)
			}
		}
		next = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": next})
}

// automationWeeklyReset: when the agent's AI connection's weekly limit resets
// next (the suggested stop time, as a Burn's), if it said.
func (s *server) automationWeeklyReset(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"weekly_reset": nil}
	if s.cfg.Burn != nil {
		agentID := r.URL.Query().Get("agent_id")
		if agentID == "" { // the lead that is on
			if agents, err := s.cfg.Chat.Agents(r.Context(), r.PathValue("id")); err == nil {
				for _, a := range storage.OnAgents(agents) {
					if a.Tier == storage.TierLead {
						agentID = a.ID
						break
					}
				}
			}
		}
		if t, ok := s.cfg.Burn.WeeklyReset(r.Context(), agentID); ok {
			out["weekly_reset"] = t
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// linkBuilder ties a building chat of the same project to the automation.
func (s *server) linkBuilder(r *http.Request, a storage.Automation, conversationID string) {
	if conversationID == "" {
		return
	}
	if c, err := s.cfg.Store.Chat().GetConversation(r.Context(), conversationID); err == nil && c.ProjectID == a.ProjectID && c.Purpose == "automation" && c.AutomationID == "" {
		_ = s.cfg.Store.Chat().LinkAutomation(r.Context(), c.ID, a.ID)
	}
}

// automationConversation is the chat that builds an automation (made on first use).
func (s *server) automationConversation(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.Automations().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	c, err := s.cfg.Store.Chat().AutomationConversation(r.Context(), a.ID)
	if err != nil {
		if c, err = s.cfg.Chat.StartConversationPurpose(r.Context(), a.ProjectID, "", "automation"); err != nil {
			s.chatError(w, r, err)
			return
		}
		if err := s.cfg.Store.Chat().LinkAutomation(r.Context(), c.ID, a.ID); errors.Is(err, storage.ErrConflict) {
			// another tab made it first: use that one
			_ = s.cfg.Store.Chat().DeleteConversation(r.Context(), c.ID)
			if c, err = s.cfg.Store.Chat().AutomationConversation(r.Context(), a.ID); err != nil {
				s.internal(w, r, err)
				return
			}
		} else if err != nil {
			s.internal(w, r, err)
			return
		} else {
			c.AutomationID = a.ID
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": s.toConvDTO(c)})
}

// testScript runs a script now, without saving it (the builder's "Chạy thử").
func (s *server) testScript(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Script  storage.AutomationScript `json:"script"`
		Payload string                   `json:"payload"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if !trigger.ValidLang(in.Script.Lang) || strings.TrimSpace(in.Script.Body) == "" || len(in.Script.Body) > trigger.MaxScript {
		writeError(w, http.StatusBadRequest, "script cần ngôn ngữ bash, node hoặc python và nội dung không quá 64KB")
		return
	}
	in.Script.TimeoutS = min(max(in.Script.TimeoutS, 1), 120)
	dir := p.Path
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	env := []string{"OFFICE_PAYLOAD=" + in.Payload, "OFFICE_TRIGGER=test", "OFFICE_AUTOMATION=test"}
	out, code, timedOut, err := trigger.RunScript(r.Context(), dir, in.Script, env, in.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "automation.test_script", ProjectID: p.ID, Detail: map[string]any{"lang": in.Script.Lang, "exit_code": code}})
	writeJSON(w, http.StatusOK, map[string]any{"output": out, "exit_code": code, "timed_out": timedOut})
}
