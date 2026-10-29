package api

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/storage"
)

// A project's Burn (spec 2026-10-01-burn-design): its settings, state and
// the pieces of work it found. Admins only: it runs with the machine.

type burnDTO struct {
	ID             string `json:"id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	AgentID        string `json:"agent_id"`
	ModelTier      string `json:"model_tier"`
	MaxParallel    int    `json:"max_parallel"`
	Focus          string `json:"focus"`
	Order          string `json:"order"`
	// what its workers look for (ADR-128); hunt_prompt: the custom template's
	Template   string `json:"template"`
	HuntPrompt string `json:"hunt_prompt"`
	// review (ADR-113): the profile followed ("" = none)
	ReviewProfileID string `json:"review_profile_id"`
	// where its summary goes when it stops (ADR-120): a bot and its chat
	NotifyChannelID string     `json:"notify_channel_id"`
	NotifyChatID    string     `json:"notify_chat_id"`
	EndsAt          *time.Time `json:"ends_at"`
	State           string     `json:"state"`
	WaitingUntil    *time.Time `json:"waiting_until,omitempty"`
	StartedBy       string     `json:"started_by,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	RunBranch       string     `json:"run_branch,omitempty"` // the run's branch (ADR-123)
	RunTree         string     `json:"run_tree,omitempty"`   // its worktree, to review there
	// ADR-131: its checks ("" = guessed), what its scans keep (read-only here)
	Verify  string `json:"verify"`
	CodeMap string `json:"code_map"`
	Lessons string `json:"lessons"`
	// ADR-141: its scans' coverage plan (read-only here)
	Coverage string `json:"coverage"`
	// ADR-135: waits past review_cap pieces not merged; finishes after stop_after done (0 = none)
	ReviewCap int `json:"review_cap"`
	StopAfter int `json:"stop_after"`
	// ADR-139: a builder Burn's feature list, to show its idea→feature progress
	Features []burn.Feature `json:"features,omitempty"`
	// how long a run lasts once on, when it turns on by itself; next_start: its next time
	Plan      storage.BurnPlan `json:"plan"`
	NextStart *time.Time       `json:"next_start,omitempty"`
}

type burnItemDTO struct {
	ID                  string            `json:"id"`
	Title               string            `json:"title"`
	Kind                string            `json:"kind"`
	Detail              string            `json:"detail"`
	Status              string            `json:"status"`
	Priority            int               `json:"priority"`
	Branch              string            `json:"branch"`
	Worktree            string            `json:"worktree"`
	Summary             string            `json:"summary"`
	Subagents           int               `json:"subagents"`
	CostUSD             float64           `json:"cost_usd"`
	Reviewed            []string          `json:"reviewed"`
	ReviewNote          string            `json:"review_note"`
	ReviewConversations map[string]string `json:"review_conversations"` // stage → its hidden review chat (ADR-114)
	WorkConversationID  string            `json:"work_conversation_id"` // its hidden work chat (ADR-116)
	RunBranch           string            `json:"run_branch"`           // the run it belongs to; "" = a quest not started yet
	Owner               string            `json:"owner,omitempty"`      // the agent key that follows it up (ADR-197)
	UpdatedAt           time.Time         `json:"updated_at"`
}

func toBurnDTO(b storage.BurnSession) burnDTO {
	return burnDTO{b.ID, b.ConversationID, b.AgentID, b.ModelTier, b.MaxParallel, b.Focus, cmp.Or(b.Order, "roadmap"), cmp.Or(b.Template, "general"), b.HuntPrompt,
		b.ReviewProfileID, b.NotifyChannelID, b.NotifyChatID, b.EndsAt, b.State, b.WaitingUntil, b.StartedBy, b.StartedAt, b.RunBranch, "", b.Verify, b.CodeMap, b.Lessons, b.Coverage, b.ReviewCap, b.StopAfter, nil, b.Plan, nextStart(b.Plan)}
}

func nextStart(p storage.BurnPlan) *time.Time {
	if t, ok := burn.NextStart(p, time.Now()); ok {
		return &t
	}
	return nil
}

// burnSession is the project's, or the defaults for a first one (not saved).
func (s *server) burnSession(r *http.Request, projectID string) (storage.BurnSession, error) {
	return burn.SessionOr(r.Context(), s.cfg.Store, projectID, s.cfg.Chat.DefaultAgent)
}

func (s *server) getBurn(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Burn == nil {
		writeError(w, http.StatusNotImplemented, "Burn chưa bật trên office này")
		return
	}
	pid := r.PathValue("id")
	b, err := s.burnSession(r, pid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := []burnItemDTO{}
	runs := map[string]time.Time{} // run branch → when it started
	if b.ID != "" {
		list, _ := s.cfg.Store.Burn().Items(r.Context(), b.ID)
		for _, it := range list {
			items = append(items, burnItemDTO{it.ID, it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Subagents, it.CostUSD,
				listOrEmpty(it.Reviewed), it.ReviewNote, mapOrEmpty(it.ReviewConversations), it.WorkConversationID, it.RunBranch, it.Owner, it.UpdatedAt})
			if at, ok := burn.RunStarted(it.RunBranch); ok {
				runs[it.RunBranch] = at
			}
		}
	}
	if at, ok := burn.RunStarted(b.RunBranch); ok {
		runs[b.RunBranch] = at
	}
	dto := toBurnDTO(b)
	dto.RunTree = s.cfg.Burn.RunTree(b)
	if b.Template == "builder" {
		dir := dto.RunTree
		if dir == "" {
			if rp, err := s.cfg.Store.Repos().Get(r.Context(), pid); err == nil {
				dir = rp.Path
			}
		}
		dto.Features = burn.Features(dir)
	}
	out := map[string]any{"burn": dto, "items": items, "runs": runs}
	if reset, ok := s.cfg.Burn.WeeklyReset(r.Context(), b.AgentID); ok { // the suggested stop time
		out["weekly_reset"] = reset
	}
	writeJSON(w, http.StatusOK, out)
}

// addBurnQuest: a piece of work the person gives the Burn (kind quest). It
// waits in found; a running Burn does it as given before looking for more.
func (s *server) addBurnQuest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if !decode(w, r, &in) {
		return
	}
	pid := r.PathValue("id")
	b, err := s.burnSession(r, pid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if b.ID == "" { // no Burn saved yet: the defaults, to hold it
		if b, err = s.cfg.Store.Burn().SaveSession(r.Context(), b); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	it, err := s.cfg.Burn.AddQuest(r.Context(), b, in.Title, in.Detail)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "burn.quest", Resource: "burn_item", ResourceID: it.ID, ProjectID: pid, After: map[string]string{"title": it.Title}})
	writeJSON(w, http.StatusOK, map[string]any{"id": it.ID})
}

// maxFocus caps what the person writes as the Burn's focus.
const maxFocus = 2000

// maxHuntPrompt caps the custom template's prompt (ADR-128).
const maxHuntPrompt = 4000

type burnInput struct {
	AgentID         *string           `json:"agent_id"`
	ModelTier       *string           `json:"model_tier"`
	MaxParallel     *int              `json:"max_parallel"`
	Focus           *string           `json:"focus"`
	Order           *string           `json:"order"`
	Template        *string           `json:"template"`
	HuntPrompt      *string           `json:"hunt_prompt"`
	Verify          *string           `json:"verify"`
	ReviewCap       *int              `json:"review_cap"`
	StopAfter       *int              `json:"stop_after"`
	ReviewProfileID *string           `json:"review_profile_id"` // "" = no review
	NotifyChannelID *string           `json:"notify_channel_id"` // "" = only its own chat
	NotifyChatID    *string           `json:"notify_chat_id"`
	EndsAt          *time.Time        `json:"ends_at"`
	NoEnd           bool              `json:"no_end"` // run until stopped by hand
	Plan            *storage.BurnPlan `json:"plan"`
}

func (s *server) applyBurn(r *http.Request, in burnInput, b *storage.BurnSession) error {
	if in.AgentID != nil && *in.AgentID != "" {
		b.AgentID = *in.AgentID
	}
	if in.ModelTier != nil {
		switch *in.ModelTier {
		case storage.TierStrong, storage.TierBalanced, storage.TierFast:
			b.ModelTier = *in.ModelTier
		}
	}
	if in.MaxParallel != nil {
		b.MaxParallel = min(max(*in.MaxParallel, 1), 5)
	}
	if in.Focus != nil {
		b.Focus = strings.TrimSpace(*in.Focus)
		if r := []rune(b.Focus); len(r) > maxFocus {
			return fmt.Errorf("trọng tâm dài quá %d ký tự", maxFocus)
		}
	}
	if in.NotifyChannelID != nil {
		b.NotifyChannelID = strings.TrimSpace(*in.NotifyChannelID)
		if b.NotifyChannelID != "" {
			if ch, err := s.cfg.Store.Channels().Get(r.Context(), b.NotifyChannelID); err != nil || !s.sees(r, ch.ProjectID) {
				return errors.New("không có bot này")
			}
		}
	}
	if in.NotifyChatID != nil {
		b.NotifyChatID = strings.TrimSpace(*in.NotifyChatID)
	}
	if in.Order != nil && (*in.Order == "roadmap" || *in.Order == "bugs" || *in.Order == "auto") {
		b.Order = *in.Order
	}
	if in.Template != nil {
		if !burn.ValidTemplate(*in.Template) {
			return errors.New("không có mẫu Burn này")
		}
		b.Template = *in.Template
	}
	if in.HuntPrompt != nil {
		b.HuntPrompt = strings.TrimSpace(*in.HuntPrompt)
		if r := []rune(b.HuntPrompt); len(r) > maxHuntPrompt {
			return fmt.Errorf("prompt tùy chỉnh dài quá %d ký tự", maxHuntPrompt)
		}
	}
	if in.Verify != nil {
		b.Verify = strings.TrimSpace(*in.Verify)
		if err := burn.ValidVerify(b.Verify); err != nil {
			return err
		}
	}
	if in.ReviewCap != nil {
		b.ReviewCap = min(max(*in.ReviewCap, 0), burn.MaxCap)
	}
	if in.StopAfter != nil {
		b.StopAfter = min(max(*in.StopAfter, 0), burn.MaxCap)
	}
	if in.NoEnd {
		b.EndsAt = nil
	} else if in.EndsAt != nil {
		t := in.EndsAt.UTC()
		b.EndsAt = &t
	}
	if in.Plan != nil {
		if err := burn.CheckPlan(*in.Plan); err != nil {
			return err
		}
		b.Plan = *in.Plan
	}
	if in.ReviewProfileID != nil {
		id := strings.TrimSpace(*in.ReviewProfileID)
		if id != "" {
			if p, err := s.cfg.Store.Burn().ReviewProfile(r.Context(), id); err != nil || p.ProjectID != b.ProjectID {
				return errors.New("không có hồ sơ review này")
			}
		}
		b.ReviewProfileID = id
	}
	if s.cfg.Burn != nil {
		if err := s.cfg.Burn.CheckAgent(r.Context(), b.AgentID); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) saveBurn(w http.ResponseWriter, r *http.Request) {
	var in burnInput
	if !decode(w, r, &in) {
		return
	}
	b, err := s.burnSession(r, r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	before := toBurnDTO(b)
	if err := s.applyBurn(r, in, &b); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if b, err = s.cfg.Store.Burn().SaveSession(r.Context(), b); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "burn.update", Resource: "burn", ResourceID: b.ID, ProjectID: b.ProjectID, Before: before, After: toBurnDTO(b)})
	writeJSON(w, http.StatusOK, map[string]any{"burn": toBurnDTO(b)})
}

// startBurn turns it on: it stops by its saved plan, unless ends_at or
// no_end say otherwise (a script, an older dashboard).
func (s *server) startBurn(w http.ResponseWriter, r *http.Request) {
	var in burnInput
	if !decode(w, r, &in) {
		return
	}
	pid := r.PathValue("id")
	b, err := s.burnSession(r, pid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.applyBurn(r, in, &b); err != nil { // before saving: nothing changes
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.cfg.Store.Burn().SaveSession(r.Context(), b); err != nil {
		s.internal(w, r, err)
		return
	}
	if in.EndsAt != nil || in.NoEnd {
		b, err = s.cfg.Burn.Begin(r.Context(), pid, userFrom(r).Email)
	} else {
		b, err = s.cfg.Burn.TurnOn(r.Context(), pid, userFrom(r).Email, time.Now())
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "burn.start", Resource: "burn", ResourceID: b.ID, ProjectID: pid, After: toBurnDTO(b)})
	writeJSON(w, http.StatusOK, map[string]any{"burn": toBurnDTO(b)})
}

func (s *server) stopBurn(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	if err := s.cfg.Burn.Stop(r.Context(), pid); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "burn.stop", Resource: "burn", ProjectID: pid})
	w.WriteHeader(http.StatusNoContent)
}

// drainBurn: the Burn finishes what it has in progress, then stops.
func (s *server) drainBurn(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	if err := s.cfg.Burn.Drain(r.Context(), pid); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "burn.drain", Resource: "burn", ProjectID: pid})
	w.WriteHeader(http.StatusNoContent)
}

// resumeBurn: a draining Burn goes back to work.
func (s *server) resumeBurn(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	if err := s.cfg.Burn.Resume(r.Context(), pid); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "burn.resume", Resource: "burn", ProjectID: pid})
	w.WriteHeader(http.StatusNoContent)
}

// burnItemAction: skip, first (do it next), or drop its worktree.
func (s *server) burnItemAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.cfg.Store.Burn().Item(ctx, r.PathValue("item"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	oldStatus := it.Status
	switch r.PathValue("action") {
	case "skip":
		if it.Status == "doing" {
			writeError(w, http.StatusConflict, "Việc đang làm: tắt Burn trước")
			return
		}
		it.Status = "skipped"
	case "first":
		if it.Status == "found" || it.Status == "skipped" || it.Status == "failed" {
			it.Status = "queued"
			it.ReviewErrAttempts = 0
		}
		it.Priority = int(time.Now().Unix())
	case "drop-worktree":
		if it.Status == "doing" {
			writeError(w, http.StatusConflict, "Việc đang làm: tắt Burn trước")
			return
		}
		if err := s.cfg.Burn.DropWorktree(ctx, it); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		it.Worktree = ""
	default:
		writeError(w, http.StatusNotFound, "không có thao tác này")
		return
	}
	if err := s.cfg.Store.Burn().UpdateItemFrom(ctx, it, oldStatus); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "việc vừa đổi trạng thái: tải lại")
			return
		}
		s.internal(w, r, err)
		return
	}
	if r.PathValue("action") == "first" && s.cfg.Burn != nil { // a free slot takes it at once
		if b, err := s.cfg.Store.Burn().SessionByID(ctx, it.SessionID); err == nil {
			s.cfg.Burn.Wake(b.ProjectID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listOrEmpty: [] rather than null in JSON.
func listOrEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// mapOrEmpty: {} rather than null in JSON.
func mapOrEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
