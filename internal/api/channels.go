package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// ChannelReloader restarts a channel's bot after its settings change.
type ChannelReloader interface{ Reload(id string) }

// botState: how the bot is doing right now, when the reloader knows it.
func (s *server) botState(id string) string {
	if sr, ok := s.cfg.Channels.(interface{ State(string) string }); ok {
		return sr.State(id)
	}
	return ""
}

type channelDTO struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"project_id"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"`
	HasToken      bool       `json:"has_token"`
	AgentID       string     `json:"agent_id"`
	Mode          string     `json:"mode"`
	Enabled       bool       `json:"enabled"`
	Allow         []string   `json:"allow"`
	Scope         string     `json:"scope"`
	FilterEnabled bool       `json:"filter_enabled"`
	Refusal       string     `json:"refusal"`
	Approvers     []string   `json:"approvers"`
	Approval      string     `json:"approval"`
	Header        string     `json:"header"`
	ReplyMode     string     `json:"reply_mode"` // "" = the answer only, steps = the steps too
	State         string     `json:"state"`      // connecting | running | "" (off or stopped: last_error)
	Version       string     `json:"version"`    // what an edit is made from (ADR-072)
	BotName       string     `json:"bot_name"`
	LastError     string     `json:"last_error"`
	LastMessageAt *time.Time `json:"last_message_at"`
}

// channelDTO is the channel with how its bot is doing now.
func (s *server) channelDTO(c storage.Channel) channelDTO {
	d := toChannelDTO(c)
	d.State, d.Version = s.botState(c.ID), channelVersion(c)
	return d
}

func toChannelDTO(c storage.Channel) channelDTO {
	allow := c.Allow
	if allow == nil {
		allow = []string{}
	}
	approvers := c.Approvers
	if approvers == nil {
		approvers = []string{}
	}
	return channelDTO{c.ID, c.ProjectID, c.Kind, c.Name, c.TokenEnc != "", c.AgentID, c.Mode, c.Enabled, allow, c.Scope, c.FilterEnabled, c.Refusal,
		approvers, firstNonEmptyStr(c.Approval, "ask"), c.Header, c.ReplyMode, "", "", c.BotName, c.LastError, c.LastMessageAt}
}

type channelInput struct {
	Kind          string    `json:"kind"`
	Name          *string   `json:"name"`
	Token         *string   `json:"token"` // write only
	AgentID       *string   `json:"agent_id"`
	Mode          *string   `json:"mode"`
	Enabled       *bool     `json:"enabled"`
	Allow         *[]string `json:"allow"`
	Scope         *string   `json:"scope"`
	FilterEnabled *bool     `json:"filter_enabled"`
	Refusal       *string   `json:"refusal"`
	Approvers     *[]string `json:"approvers"`
	Approval      *string   `json:"approval"`
	Header        *string   `json:"header"`
	ReplyMode     *string   `json:"reply_mode"`
	Version       string    `json:"version"` // the bot as it was read (409 when changed since)
}

func (s *server) applyChannel(in channelInput, c *storage.Channel) error {
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Token != nil && strings.TrimSpace(*in.Token) != "" {
		if s.cfg.Providers == nil {
			return errors.New("office không lưu được bí mật")
		}
		enc, err := s.cfg.Providers.Box().Seal(strings.TrimSpace(*in.Token))
		if err != nil {
			return err
		}
		c.TokenEnc = enc
	}
	if in.AgentID != nil {
		c.AgentID = *in.AgentID
	}
	if in.Mode != nil {
		if !perm.Valid(*in.Mode) {
			return errors.New("mức quyền không hợp lệ")
		}
		c.Mode = *in.Mode
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Allow != nil {
		c.Allow = []string{}
		for _, a := range *in.Allow {
			if a = strings.TrimSpace(a); a != "" {
				c.Allow = append(c.Allow, a)
			}
		}
	}
	if in.Approvers != nil {
		c.Approvers = []string{}
		for _, a := range *in.Approvers {
			if a = strings.TrimSpace(a); a != "" { // "*": anyone allowed to message the bot
				c.Approvers = append(c.Approvers, a)
			}
		}
	}
	if in.Header != nil {
		c.Header = strings.TrimSpace(*in.Header)
	}
	if in.ReplyMode != nil {
		if *in.ReplyMode != "" && *in.ReplyMode != storage.ReplySteps {
			return errors.New("kiểu trả lời của bot không hợp lệ")
		}
		c.ReplyMode = *in.ReplyMode
	}
	c.Approval = "ask"                     // the modes are gone (ADR-081): the Admin and Người dùng lists say who runs how
	if slices.Contains(c.Approvers, "*") { // anyone would run as the agent, with the machine when it has it
		return errors.New(`danh sách Admin phải ghi rõ từng người, không dùng "*"`)
	}
	if in.Scope != nil {
		c.Scope = strings.TrimSpace(*in.Scope)
	}
	if in.FilterEnabled != nil {
		c.FilterEnabled = *in.FilterEnabled
	}
	if in.Refusal != nil {
		c.Refusal = strings.TrimSpace(*in.Refusal)
	}
	if c.Name == "" {
		return errors.New("hãy đặt tên cho kênh")
	}
	if c.TokenEnc == "" {
		return errors.New("cần token của bot")
	}
	if c.Enabled && len(c.Allow) == 0 {
		return errors.New("hãy nhập chat/user id được phép nhắn bot, hoặc * để cho mọi người (người lạ sẽ nhắn được agent)")
	}
	return nil
}

func (s *server) listChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Channels().List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]channelDTO, 0, len(list))
	for _, c := range list {
		out = append(out, s.channelDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (s *server) createChannel(w http.ResponseWriter, r *http.Request) {
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	if in.Kind != "telegram" && in.Kind != "discord" {
		writeError(w, http.StatusBadRequest, "loại kênh phải là telegram hoặc discord")
		return
	}
	if _, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id")); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	c := storage.Channel{ProjectID: r.PathValue("id"), Kind: in.Kind, Mode: perm.Read, Enabled: true}
	if err := s.applyChannel(in, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.cfg.Store.Channels().Create(r.Context(), c)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "channel.create", ResourceID: c.ID, ProjectID: c.ProjectID, After: toChannelDTO(c)})
	if s.cfg.Channels != nil {
		s.cfg.Channels.Reload(c.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"channel": s.channelDTO(c)})
}

func (s *server) updateChannel(w http.ResponseWriter, r *http.Request) {
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	c, err := s.cfg.Store.Channels().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, in.Version, channelVersion(c)) {
		return
	}
	old := c
	if err := s.applyChannel(in, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Store.Channels().Update(r.Context(), c); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "channel.update", ResourceID: c.ID, ProjectID: c.ProjectID, Before: toChannelDTO(old), After: toChannelDTO(c),
		Detail: map[string]any{"token_changed": in.Token != nil && *in.Token != ""}})
	if s.cfg.Channels != nil {
		s.cfg.Channels.Reload(c.ID)
	}
	c, _ = s.cfg.Store.Channels().Get(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, map[string]any{"channel": s.channelDTO(c)})
}

func (s *server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	c, err := s.cfg.Store.Channels().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Channels().Delete(r.Context(), c.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "channel.delete", ResourceID: c.ID, ProjectID: c.ProjectID, Before: toChannelDTO(c)})
	if s.cfg.Channels != nil {
		s.cfg.Channels.Reload(c.ID) // stops it
	}
	w.WriteHeader(http.StatusNoContent)
}
