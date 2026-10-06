package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// A chat bot is a trigger's own settings (ADR-049): saved with the
// automation, shared by the automations that name it, removed with the last.

// botInput is the bot part of an automation's input (the token is write only).
type botInput struct {
	Token     *string          `json:"token"`
	Allow     *[]string        `json:"allow"`
	Refusal   *string          `json:"refusal"`
	Approvers *[]string        `json:"approvers"`
	Approval  *string          `json:"approval"`
	Header    *string          `json:"header"` // the line on top of its answers ("" = default, "-" = none)
	ReplyMode *string          `json:"reply_mode"`
	Defaults  *json.RawMessage `json:"defaults"` // its commands' default setup
	Version   string           `json:"version"`  // the bot as it was read (409 when changed since)
}

// automationBot is the bot as an automation shows it: what may be edited…
type automationBot struct {
	HasToken  bool            `json:"has_token"`
	Allow     []string        `json:"allow"`
	Refusal   string          `json:"refusal"`
	Approvers []string        `json:"approvers"`
	Approval  string          `json:"approval"`
	Header    string          `json:"header"`
	ReplyMode string          `json:"reply_mode"`
	Defaults  json.RawMessage `json:"defaults"`
}

// …and how it is doing (kept apart: a new message never makes a proposal stale).
type automationBotStatus struct {
	Kind          string     `json:"kind"`
	BotName       string     `json:"bot_name"`
	Enabled       bool       `json:"enabled"`
	LastError     string     `json:"last_error"`
	LastMessageAt *time.Time `json:"last_message_at"`
	Shared        int        `json:"shared"` // automations using this bot
	State         string     `json:"state"`  // connecting | running | "" (off or stopped: last_error)
}

// saveBot creates the automation's bot (no channel yet) at once, and names it
// in in.Config (the automation is checked against it); it returns the id of
// a bot it created, to remove if the automation is then refused. A change to
// a bot it has is checked now but written by commit, once the automation is
// saved (review I5: a refused change changes nothing).
func (s *server) saveBot(r *http.Request, in *automationInput, projectID string) (created string, commit func() error, err error) {
	commit = func() error { return nil }
	if !trigger.IsChannel(in.Source) {
		return "", commit, nil
	}
	b := in.Bot
	if in.Config.ChannelID == "" {
		if b == nil || b.Token == nil || *b.Token == "" {
			return "", commit, errors.New("hãy dán token của bot")
		}
		name := in.Name
		c := storage.Channel{ProjectID: projectID, Kind: in.Source, Mode: "read", Enabled: true}
		if err := s.applyChannel(channelInput{Name: &name, Token: b.Token, Allow: b.Allow, Refusal: b.Refusal, Approvers: b.Approvers, Approval: b.Approval, Header: b.Header, ReplyMode: b.ReplyMode, Defaults: b.Defaults}, &c); err != nil {
			return "", commit, err
		}
		if c, err = s.cfg.Store.Channels().Create(r.Context(), c); err != nil {
			return "", commit, err
		}
		in.Config.ChannelID = c.ID
		// logged once the automation is saved: a refused one drops the bot, and no trace is left
		return c.ID, func() error {
			s.audit(r, audit.Change{Action: "channel.create", ResourceID: c.ID, ProjectID: projectID, After: toChannelDTO(c)})
			return nil
		}, nil
	}
	if b == nil || (b.Token == nil && b.Allow == nil && b.Refusal == nil && b.Approvers == nil && b.Approval == nil && b.Header == nil && b.ReplyMode == nil && b.Defaults == nil) {
		return "", commit, nil
	}
	c, err := s.cfg.Store.Channels().Get(r.Context(), in.Config.ChannelID)
	if err != nil || c.ProjectID != projectID {
		return "", commit, errors.New("hãy chọn bot của project này")
	}
	if b.Version != "" && b.Version != channelVersion(c) {
		return "", commit, errConflict
	}
	old := c
	if err := s.applyChannel(channelInput{Token: b.Token, Allow: b.Allow, Refusal: b.Refusal, Approvers: b.Approvers, Approval: b.Approval, Header: b.Header, ReplyMode: b.ReplyMode, Defaults: b.Defaults}, &c); err != nil {
		return "", commit, err
	}
	return "", func() error {
		if err := s.cfg.Store.Channels().Update(r.Context(), c); err != nil {
			return err
		}
		s.audit(r, audit.Change{Action: "channel.update", ResourceID: c.ID, ProjectID: projectID, Before: toChannelDTO(old), After: toChannelDTO(c),
			Detail: map[string]any{"token_changed": b.Token != nil && *b.Token != ""}})
		return nil
	}, nil
}

// dropBot removes a bot this request created for an automation it then refused.
func (s *server) dropBot(r *http.Request, id string) {
	if id != "" {
		_ = s.cfg.Store.Channels().Delete(r.Context(), id)
	}
}

// reloadBot restarts (or stops) the automation's bot after a change.
func (s *server) reloadBot(a storage.Automation) {
	if s.cfg.Channels != nil && trigger.IsChannel(a.Source) && a.Config.ChannelID != "" {
		s.cfg.Channels.Reload(a.Config.ChannelID)
	}
}

// botUsers counts the automations naming a bot.
func (s *server) botUsers(r *http.Request, projectID, channelID string) int {
	list, _ := s.cfg.Store.Automations().List(r.Context(), projectID)
	n := 0
	for _, a := range list {
		if trigger.IsChannel(a.Source) && a.Config.ChannelID == channelID {
			n++
		}
	}
	return n
}

// releaseBot removes a bot no automation names any more.
func (s *server) releaseBot(r *http.Request, a storage.Automation) {
	if !trigger.IsChannel(a.Source) || a.Config.ChannelID == "" || s.botUsers(r, a.ProjectID, a.Config.ChannelID) > 0 {
		return
	}
	if c, err := s.cfg.Store.Channels().Get(r.Context(), a.Config.ChannelID); err == nil {
		_ = s.cfg.Store.Channels().Delete(r.Context(), c.ID)
		s.audit(r, audit.Change{Action: "channel.delete", ResourceID: c.ID, ProjectID: c.ProjectID, Before: toChannelDTO(c)})
		s.reloadBot(a) // stops it
	}
}

func (s *server) botOf(r *http.Request, a storage.Automation) (*automationBot, *automationBotStatus) {
	c, err := s.cfg.Store.Channels().Get(r.Context(), a.Config.ChannelID)
	if err != nil {
		return nil, nil
	}
	allow := c.Allow
	if allow == nil {
		allow = []string{}
	}
	approvers := c.Approvers
	if approvers == nil {
		approvers = []string{}
	}
	return &automationBot{HasToken: c.TokenEnc != "", Allow: allow, Refusal: c.Refusal, Approvers: approvers, Approval: firstNonEmptyStr(c.Approval, "ask"), Header: c.Header, ReplyMode: c.ReplyMode, Defaults: defaultsJSON(c.Defaults)},
		&automationBotStatus{Kind: c.Kind, BotName: c.BotName, Enabled: c.Enabled, LastError: c.LastError, LastMessageAt: c.LastMessageAt,
			Shared: s.botUsers(r, a.ProjectID, c.ID), State: s.botState(c.ID)}
}
