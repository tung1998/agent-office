package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/features"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// incident is something across the office that needs a person, with where to
// fix it: a monitor down, a process crashed, an automation office turned off,
// a bot that lost its connection, tasks and runs that failed, a card waiting.
type incident struct {
	Kind        string    `json:"kind"`     // monitor | process | automation | bot | jobs | approval | patch | unread | stalled | peer_request
	Severity    string    `json:"severity"` // error | warning
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail"`
	At          time.Time `json:"at"`
	Link        string    `json:"link"`
	ID          string    `json:"id"`  // what it is about: the monitor, process, automation, bot, latest failed job or action
	Key         string    `json:"key"` // for Bỏ qua: kind and what it is about
	// Prompt: stalled — what Tiếp tục sends to the chat (to the agent that stopped)
	Prompt string `json:"prompt,omitempty"`
	conv   string // approval / patch: the chat it waits in (orderApprovals)
}

func dismissKey(key string) string { return "incident_dismissed/" + key }

func (s *server) incidents(w http.ResponseWriter, r *http.Request) {
	out, err := s.incidentsFor(r.Context(), userFrom(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out, "count": len(out)})
}

// incidentsFor is what needs u now (also pushed to their open pages, ADR-078).
func (s *server) incidentsFor(ctx context.Context, u storage.User) ([]incident, error) {
	projects, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		return nil, err
	}
	sc, err := s.scopeOf(ctx, u)
	if err != nil {
		return nil, err
	}
	hidden := assistant.ID(ctx, s.cfg.Store)
	day := time.Now().UTC().Add(-24 * time.Hour)
	out := []incident{}
	for _, p := range projects {
		if p.ID == hidden || !sc.has(p.ID) {
			continue
		}
		base := "/projects/" + p.ID
		on, _ := features.Effective(features.LoadConfig(ctx, s.cfg.Store, p.ID), p.Path != "")
		add := func(kind, sev, title, detail string, at *time.Time, link, id, key string) {
			it := incident{Kind: kind, Severity: sev, ProjectID: p.ID, ProjectName: p.Name, Title: title, Detail: detail, Link: link, ID: id, Key: kind + ":" + key}
			if at != nil {
				it.At = *at
			}
			// let go (Bỏ qua): hidden until it happens again
			var gone time.Time
			if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey(it.Key), &gone); ok && !it.At.After(gone) {
				return
			}
			out = append(out, it)
		}
		if features.Has(on, features.Ops) { // hidden: no badge either
			ms, _ := s.cfg.Store.Monitors().List(ctx, p.ID)
			for _, m := range ms {
				if m.Enabled && m.Status == "down" {
					add("monitor", "error", m.Name, m.LastMessage, m.LastChangeAt, base+"?tab=ops", m.ID, m.ID)
				}
			}
		}
		if s.cfg.Ops != nil && features.Has(on, features.Ops) {
			if ps, err := s.cfg.Store.Processes().List(ctx, p.ID); err == nil {
				for _, x := range ps {
					if st := s.cfg.Ops.State(x.ID); st.Status == "crashed" {
						add("process", "error", x.Name, fmt.Sprintf("thoát, đã khởi động lại %d lần", st.Restarts), st.FinishedAt, base+"?tab=ops", x.ID, x.ID)
					}
				}
			}
		}
		if as, err := s.cfg.Store.Automations().List(ctx, p.ID); err == nil {
			for _, a := range as {
				if a.DisabledCode != "" && a.DisabledCode != "ended" { // stopped at its stop time: as asked, no incident
					add("automation", "warning", a.Name, a.DisabledReason, &a.UpdatedAt, base+"/automations/"+a.ID, a.ID, a.ID)
				}
			}
		}
		if cs, err := s.cfg.Store.Channels().List(ctx, p.ID); err == nil {
			for _, c := range cs {
				if c.Enabled && c.LastError != "" {
					add("bot", "error", firstNonEmptyStr(c.BotName, c.Name), c.LastError, &c.UpdatedAt, base+"/bots/"+c.ID, c.ID, c.ID)
				}
			}
		}
		if failed, err := s.cfg.Store.Jobs().List(ctx, storage.JobFilter{ProjectID: p.ID, Status: "failed", Since: day, Limit: 200}); err == nil && len(failed) > 0 {
			// counted since it was last let go
			var gone time.Time
			_, _ = s.cfg.Store.Settings().Get(ctx, dismissKey("jobs:"+p.ID), &gone)
			fresh := failed[:0:0]
			for _, j := range failed {
				if j.CreatedAt.After(gone) && !(j.Kind == "chat_turn" && chat.Stalled(j.ErrorCode)) { // a chat out of tokens is its own item
					fresh = append(fresh, j)
				}
			}
			if len(fresh) > 0 {
				last := fresh[0]
				add("jobs", "warning", fmt.Sprintf("%d job lỗi trong 24 giờ", len(fresh)), last.Title+": "+last.Error, &last.CreatedAt, "/jobs?project="+p.ID, last.ID, p.ID)
			}
		}
		for _, it := range s.stalledChats(ctx, u, p) {
			var gone time.Time
			if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey(it.Key), &gone); ok && !it.At.After(gone) {
				continue
			}
			out = append(out, it)
		}
	}
	if manages(u) { // cards waiting for a person
		if acts, err := s.cfg.Store.Actions().Pending(ctx, 100); err == nil {
			for _, a := range acts {
				if _, busy := deciding.Is(a.ID); busy || !sc.has(a.ProjectID) { // approved (on its own, or elsewhere), running: nobody need act
					continue
				}
				name := a.ProjectID
				for _, p := range projects {
					if p.ID == a.ProjectID {
						name = p.Name
					}
				}
				link := "/projects/" + a.ProjectID + "?tab=chat"
				if a.ConversationID != "" {
					link += "&c=" + a.ConversationID
				} else {
					link = "/assistant"
				}
				out = append(out, incident{Kind: "approval", Severity: "warning", ProjectID: a.ProjectID, ProjectName: name,
					Title: firstNonEmptyStr(a.Target, a.Kind), Detail: a.Reason, At: a.CreatedAt, Link: link, ID: a.ID, Key: "approval:" + a.ID, conv: a.ConversationID})
			}
		}
	}
	if manages(u) { // chats' diffs waiting for a person
		if ps, err := s.cfg.Store.Chat().PendingPatches(ctx, 100); err == nil {
			for _, p := range ps {
				if _, busy := deciding.Is(p.ID); busy {
					continue
				}
				c, err := s.cfg.Store.Chat().GetConversation(ctx, p.ConversationID)
				if err != nil || c.ProjectID == hidden || !sc.has(c.ProjectID) {
					continue
				}
				name := c.ProjectID
				for _, x := range projects {
					if x.ID == c.ProjectID {
						name = x.Name
					}
				}
				out = append(out, incident{Kind: "patch", Severity: "warning", ProjectID: c.ProjectID, ProjectName: name,
					Title: strings.Join(p.Files, ", "), Detail: c.Title, At: p.CreatedAt, Link: "/projects/" + c.ProjectID + "?tab=chat&c=" + c.ID + "&m=" + p.MessageID,
					ID: p.ID, Key: "patch:" + p.ID, conv: p.ConversationID})
			}
		}
	}
	// the person's chats an agent answered since they last looked (theirs alone)
	if ids, err := s.cfg.Store.Chat().Unread(ctx, u.ID, "human:"+u.Email); err == nil {
		for _, id := range ids {
			c, err := s.cfg.Store.Chat().GetConversation(ctx, id)
			if err != nil {
				continue
			}
			name := c.ProjectID
			for _, x := range projects {
				if x.ID == c.ProjectID {
					name = x.Name
				}
			}
			link := "/projects/" + c.ProjectID + "?tab=chat&c=" + c.ID
			if c.ProjectID == hidden {
				link = "/assistant"
			}
			out = append(out, incident{Kind: "unread", Severity: "info", ProjectID: c.ProjectID, ProjectName: name,
				Title: c.Title, At: c.UpdatedAt, Link: link, ID: c.ID, Key: "unread:" + c.ID})
		}
	}
	if u.Role == storage.RoleAdmin && s.cfg.Peers != nil { // another office asks to pair (ADR-177)
		for _, rq := range s.cfg.Peers.Requests(ctx) {
			var gone time.Time
			if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey("peer_request:"+rq.ID), &gone); ok {
				continue
			}
			out = append(out, incident{Kind: "peer_request", Severity: "warning", Title: rq.Name, Detail: rq.URL + " · " + rq.Code,
				At: rq.CreatedAt, Link: "/admin/peers", ID: rq.ID, Key: "peer_request:" + rq.ID})
		}
	}
	for _, it := range s.limitIncidents(ctx) { // an AI connection close to its limit
		// it.Key already carries the occurrence (provider/window/ResetsAt, see
		// limitalert.go), so dismiss holds until the window itself resets (a new
		// key) — not until it.At, which UpdatedAt bumps on every run that reports.
		var gone time.Time
		if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey(it.Key), &gone); ok {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Severity == "error") != (out[j].Severity == "error") {
			return out[i].Severity == "error"
		}
		return out[i].At.After(out[j].At)
	})
	s.orderApprovals(ctx, out)
	return out, nil
}

// orderApprovals: a chat's cards are decided in their order (chat.CheckOrder),
// so where its cards stand in the list they go oldest first, each with its
// #number: going down the list approves them in turn.
func (s *server) orderApprovals(ctx context.Context, out []incident) {
	if s.cfg.Chat == nil {
		return
	}
	slots := map[string][]int{} // by chat: where its cards stand
	for i, x := range out {
		if (x.Kind == "approval" || x.Kind == "patch") && x.conv != "" {
			slots[x.conv] = append(slots[x.conv], i)
		}
	}
	for conv, at := range slots {
		order, seq := s.cfg.Chat.WaitingOrder(ctx, conv)
		rank := func(id string) int {
			if i := slices.Index(order, id); i >= 0 {
				return i
			}
			return len(order)
		}
		items := make([]incident, len(at))
		for i, j := range at {
			items[i] = out[j]
		}
		slices.SortStableFunc(items, func(a, b incident) int { return cmp.Compare(rank(a.ID), rank(b.ID)) })
		for i, j := range at {
			if n := seq[items[i].ID]; n > 0 {
				items[i].Title = fmt.Sprintf("#%d %s", n, items[i].Title)
			}
			out[j] = items[i]
		}
	}
}

// dismissIncident lets an incident go (Bỏ qua): it shows again only when it
// happens again.
func (s *server) dismissIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key  string   `json:"key"`
		Keys []string `json:"keys"` // Bỏ qua tất cả: several at once
	}
	if !decode(w, r, &in) {
		return
	}
	keys := in.Keys
	if in.Key != "" {
		keys = append(keys, in.Key)
	}
	if len(keys) == 0 {
		writeError(w, http.StatusBadRequest, "không bỏ qua được mục này")
		return
	}
	for _, k := range keys {
		if k == "" || strings.HasPrefix(k, "approval:") || strings.HasPrefix(k, "patch:") { // a card is decided, not let go
			writeError(w, http.StatusBadRequest, "không bỏ qua được mục này")
			return
		}
	}
	now := time.Now().UTC()
	for _, k := range keys {
		if err := s.cfg.Store.Settings().Set(r.Context(), dismissKey(k), now); err != nil {
			s.internal(w, r, err)
			return
		}
		s.auditAction(r, "incident.dismiss", k, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// retryIncident tries again what an incident is about: checks a monitor now,
// restarts a process, turns an automation back on, reconnects a bot. (A
// failed job is retried through /api/jobs/{id}/retry.)
func (s *server) retryIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	var err error
	switch in.Kind {
	case "monitor":
		if s.cfg.Monitors == nil {
			err = errors.New("chưa bật theo dõi")
		} else {
			_, err = s.cfg.Monitors.CheckNow(ctx, in.ID)
		}
	case "process":
		if s.cfg.Ops == nil {
			err = errors.New("chưa bật vận hành")
		} else {
			err = s.cfg.Ops.Restart(ctx, in.ID)
		}
	case "automation":
		var a storage.Automation
		if a, err = s.cfg.Store.Automations().Get(ctx, in.ID); err == nil && a.Config.EndsAt != nil && !a.Config.EndsAt.After(time.Now()) {
			err = errors.New("giờ dừng đã qua: hãy đặt giờ dừng mới hoặc bỏ giờ dừng")
		} else if err == nil {
			var nextRunAt *time.Time
			if a.Source == "schedule" {
				if next, nerr := trigger.NextRun(a.Config, time.Now().UTC()); nerr == nil {
					nextRunAt = &next
				}
			}
			if err = s.cfg.Store.Automations().Reenable(ctx, a.ID, nextRunAt); err == nil {
				if a, err = s.cfg.Store.Automations().Get(ctx, a.ID); err == nil {
					s.reloadBot(a)
				}
			}
		}
	case "bot":
		if s.cfg.Channels == nil {
			err = errors.New("chưa bật bot")
		} else if _, err = s.cfg.Store.Channels().Get(ctx, in.ID); err == nil {
			s.cfg.Channels.Reload(in.ID)
		}
	default:
		writeError(w, http.StatusBadRequest, "không chạy lại được mục này")
		return
	}
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "incident.retry", in.Kind+":"+in.ID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
