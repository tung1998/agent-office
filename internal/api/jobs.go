package api

import (
	"context"
	"github.com/tung1998/agent-office/internal/assistant"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Jobs: every run (a chat answer, a task, an automation's turn), to follow
// and act on in one place (ADR-040).

func detached(r *http.Request) context.Context { return context.WithoutCancel(r.Context()) }

type jobDTO struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	Kind           string     `json:"kind"`
	Origin         string     `json:"origin"`
	OriginID       string     `json:"origin_id"`
	Trigger        string     `json:"trigger"`
	CreatedBy      string     `json:"created_by"`
	ConversationID string     `json:"conversation_id"`
	MessageID      string     `json:"message_id"`
	TaskID         string     `json:"task_id"`
	Status         string     `json:"status"`
	Error          string     `json:"error"`
	ErrorCode      string     `json:"error_code"`
	AgentID        string     `json:"agent_id"`
	AgentName      string     `json:"agent_name"`
	AutomationName string     `json:"automation_name"`
	ProjectName    string     `json:"project_name"`
	Title          string     `json:"title"`
	CostUSD        float64    `json:"cost_usd"`
	InputTokens    int        `json:"input_tokens"`
	OutputTokens   int        `json:"output_tokens"`
	DurationMS     int64      `json:"duration_ms"`
	Payload        string     `json:"payload,omitempty"`
	Output         string     `json:"output,omitempty"` // script: only with the job itself
	ExitCode       *int       `json:"exit_code"`
	ParentJobID    string     `json:"parent_job_id"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}

// names caches the labels a job listing needs.
type names struct{ agents, automations, projects map[string]string }

// look finds a name once per request.
func look(m map[string]string, id string, get func() (string, error)) string {
	if id == "" {
		return ""
	}
	if v, ok := m[id]; ok {
		return v
	}
	v, _ := get()
	m[id] = v
	return v
}

func (s *server) toJobDTO(r *http.Request, j storage.Job, n *names) jobDTO {
	if n == nil {
		n = &names{map[string]string{}, map[string]string{}, map[string]string{}}
	}
	ctx := r.Context()
	d := jobDTO{ID: j.ID, ProjectID: j.ProjectID, Kind: j.Kind, Origin: j.Origin, OriginID: j.OriginID, Trigger: j.Trigger, CreatedBy: j.CreatedBy,
		ConversationID: j.ConversationID, MessageID: j.MessageID, TaskID: j.TaskID, Status: j.Status, Error: j.Error, ErrorCode: j.ErrorCode,
		AgentID: j.AgentID, Title: j.Title, CostUSD: j.CostUSD, InputTokens: j.InputTokens, OutputTokens: j.OutputTokens, DurationMS: j.DurationMS,
		NextAttemptAt: j.NextAttemptAt, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, ExitCode: j.ExitCode, ParentJobID: j.ParentJobID}
	d.AgentName = look(n.agents, j.AgentID, func() (string, error) {
		a, err := s.cfg.Store.Agents().Get(ctx, j.AgentID)
		return a.Name, err
	})
	if j.Origin == "automation" {
		d.AutomationName = look(n.automations, j.OriginID, func() (string, error) {
			a, err := s.cfg.Store.Automations().Get(ctx, j.OriginID)
			return a.Name, err
		})
	}
	d.ProjectName = look(n.projects, j.ProjectID, func() (string, error) {
		p, err := s.cfg.Store.Repos().Get(ctx, j.ProjectID)
		return p.Name, err
	})
	return d
}

func jobFilter(r *http.Request) storage.JobFilter {
	q := r.URL.Query()
	f := storage.JobFilter{ProjectID: q.Get("project"), Kind: q.Get("kind"), Origin: q.Get("origin"), OriginID: q.Get("origin_id"),
		Status: q.Get("status"), AgentID: q.Get("agent"), Before: q.Get("before"), Source: q.Get("source"), Query: q.Get("q"), ConversationID: q.Get("conversation")}
	if ids := q.Get("origin_ids"); ids != "" { // comma separated
		f.OriginIDs = strings.Split(ids, ",")
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if d, err := time.ParseDuration(q.Get("since")); err == nil && d > 0 {
		f.Since = time.Now().UTC().Add(-d)
	} else if days, err := strconv.Atoi(q.Get("days")); err == nil && days > 0 {
		f.Since = time.Now().UTC().AddDate(0, 0, -days)
	}
	return f
}

func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	f := jobFilter(r)
	jobs, err := s.cfg.Store.Jobs().List(r.Context(), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	n := &names{map[string]string{}, map[string]string{}, map[string]string{}}
	out := make([]jobDTO, 0, len(jobs))
	own, me := assistant.ID(r.Context(), s.cfg.Store), "human:"+userFrom(r).Email
	for _, j := range jobs {
		if own != "" && j.ProjectID == own && j.CreatedBy != me {
			continue // an assistant chat is its creator's alone (ADR-046)
		}
		out = append(out, s.toJobDTO(r, j, n))
	}
	next := ""
	limit := f.Limit // the page asked for (the store's default when none)
	if limit <= 0 {
		limit = 50
	}
	if len(jobs) >= min(limit, 200) {
		next = jobs[len(jobs)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "next_before": next})
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.cfg.Store.Jobs().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	d := s.toJobDTO(r, j, nil)
	d.Payload, d.Output = j.Payload, j.Output
	children, _ := s.cfg.Store.Jobs().List(r.Context(), storage.JobFilter{OriginID: j.OriginID, Since: j.CreatedAt, Limit: 50})
	var kids []jobDTO
	for _, c := range children {
		if c.ParentJobID == j.ID {
			kids = append(kids, s.toJobDTO(r, c, nil))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": d, "children": kids})
}

func (s *server) jobStats(w http.ResponseWriter, r *http.Request) {
	f := jobFilter(r)
	if f.Since.IsZero() {
		f.Since = time.Now().UTC().AddDate(0, 0, -7)
	}
	f.Status, f.Before, f.Limit = "", "", 0
	by := r.URL.Query().Get("by")
	rows, err := s.cfg.Store.Jobs().Stats(r.Context(), f, by)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// the numbers of the moment: running and waiting now, failed and spent in 24h
	now := storage.JobFilter{ProjectID: f.ProjectID, Kind: f.Kind, Origin: f.Origin, OriginID: f.OriginID, AgentID: f.AgentID}
	count := func(status string, since time.Time) int {
		x := now
		x.Status, x.Since, x.Limit = status, since, 200
		list, _ := s.cfg.Store.Jobs().List(r.Context(), x)
		return len(list)
	}
	day := time.Now().UTC().Add(-24 * time.Hour)
	x := now
	x.Since = day
	cost := 0.0
	if d, err := s.cfg.Store.Jobs().Stats(r.Context(), x, "status"); err == nil {
		for _, row := range d {
			cost += row.CostUSD
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "totals": map[string]any{
		"running": count("running", time.Time{}), "pending": count("pending", time.Time{}), "needs_input": count("needs_input", day),
		"failed_24h": count("failed", day), "cost_24h": cost,
	}})
}

func (s *server) cancelJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.cfg.Store.Jobs().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	switch {
	case j.Status == "pending":
		j, err = s.cfg.Store.Jobs().Finish(r.Context(), j.ID, "cancelled", "cancelled", "", time.Now().UTC())
		if err != nil {
			s.internal(w, r, err)
			return
		}
	case j.Status == "running" && j.Kind == "chat_turn" && j.ConversationID != "":
		if t, ok := s.cfg.Chat.TurnByJob(j.ID); ok { // a hand-off in the background has its own job
			t.Cancel()
		}
	default:
		writeError(w, http.StatusConflict, "job này không còn chạy")
		return
	}
	s.auditAction(r, "job.cancel", j.ID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"job": s.toJobDTO(r, j, nil)})
}

func (s *server) retryJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.cfg.Store.Jobs().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	switch {
	case j.ParentJobID != "" && s.cfg.Trigger != nil:
		// an agent a script called in: call it again with the same result
		now := time.Now().UTC()
		nj, err := s.cfg.Store.Jobs().Create(r.Context(), storage.Job{ProjectID: j.ProjectID, Kind: j.Kind, Origin: j.Origin, OriginID: j.OriginID,
			Trigger: "escalate", Status: "pending", Payload: j.Payload, ParentJobID: j.ParentJobID, AgentID: j.AgentID, Title: j.Title, NextAttemptAt: &now})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		s.cfg.Trigger.StartReady(detached(r), now)
		s.auditAction(r, "job.retry", j.ID, map[string]any{"job": nj.ID})
		writeJSON(w, http.StatusAccepted, map[string]any{"job": s.toJobDTO(r, nj, nil)})
	case j.Origin == "automation" && s.cfg.Trigger != nil:
		a, err := s.cfg.Store.Automations().Get(r.Context(), j.OriginID)
		if err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		// chạy lại không bao giờ được quyền cao hơn quyền đã lưu trên job gốc
		// (ADR-074 security fix): dù job gốc là "manual" (từ một lần chạy lại
		// trước), chạy lại tiếp vẫn bị chặn nếu job đó chưa có toàn quyền.
		nj, _, err := s.cfg.Trigger.Retry(r.Context(), a, j.Trigger, j.FullAccess, j.Payload)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		s.cfg.Trigger.StartReady(detached(r), time.Now().UTC())
		s.auditAction(r, "job.retry", j.ID, map[string]any{"job": nj.ID})
		writeJSON(w, http.StatusAccepted, map[string]any{"job": s.toJobDTO(r, nj, nil)})
	default:
		writeError(w, http.StatusBadRequest, "job này chạy lại từ cuộc Chat")
	}
}

// jobGroupDTO is one piece of work on the Job page: a chat, a task, an
// automation's runs, with how its jobs went.
type jobGroupDTO struct {
	Key            string    `json:"key"`
	Kind           string    `json:"kind"` // chat | task | automation | job
	ProjectID      string    `json:"project_id"`
	ProjectName    string    `json:"project_name"`
	Source         string    `json:"source"` // web | discord | telegram | auto
	Title          string    `json:"title"`
	Status         string    `json:"status"` // the latest job's
	Runs           int       `json:"runs"`
	Failed         int       `json:"failed"`
	Active         int       `json:"active"`
	CostUSD        float64   `json:"cost_usd"`
	LastAt         time.Time `json:"last_at"`
	Link           string    `json:"link"`
	JobID          string    `json:"job_id,omitempty"`          // a job of its own: opened in place
	ConversationID string    `json:"conversation_id,omitempty"` // a chat: its turns
}

func (s *server) jobGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := jobFilter(r)
	gs, err := s.cfg.Store.Jobs().Groups(ctx, f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	n := &names{map[string]string{}, map[string]string{}, map[string]string{}}
	own, me := assistant.ID(ctx, s.cfg.Store), "human:"+userFrom(r).Email
	out := make([]jobGroupDTO, 0, len(gs))
	for _, g := range gs {
		if own != "" && g.ProjectID == own && g.CreatedBy != me {
			continue // an assistant chat is its creator's alone (ADR-046)
		}
		d := jobGroupDTO{Key: g.Key, ProjectID: g.ProjectID, Title: g.Title, Status: g.Status, Runs: g.Runs, Failed: g.Failed, Active: g.Active,
			CostUSD: g.CostUSD, LastAt: g.LastAt, Source: "web"}
		switch {
		case g.Trigger == "discord" || g.Trigger == "telegram":
			d.Source = g.Trigger
		case g.Origin == "automation":
			d.Source = "auto"
		}
		base := "/projects/" + g.ProjectID
		switch g.Key[:2] {
		case "c:":
			d.Kind, d.Link, d.ConversationID = "chat", base+"?tab=chat&c="+g.ConversationID, g.ConversationID
			if g.ProjectID == own { // the assistant's chats live on its own page
				d.Link = "/assistant?c=" + g.ConversationID
			}
			if c, err := s.cfg.Store.Chat().GetConversation(ctx, g.ConversationID); err == nil {
				if c.Title != "" {
					d.Title = c.Title
				}
				if c.Purpose == "automation" && c.AutomationID != "" { // the builder chat opens with its automation
					d.Link = base + "/automations/" + c.AutomationID
				}
				if c.Purpose == "skill" { // a skill editor's chat opens the editor again
					d.Link = base + "/skills/edit?c=" + c.ID
				}
				if c.Purpose == "template" { // a template written with the assistant: its editor
					d.Link = "/templates/new?c=" + c.ID
				}
			}
		case "t:": // a task from before Việc was dropped (ADR-057): its latest job
			d.Kind, d.JobID = "task", g.LatestID
			if t, err := s.cfg.Store.Tasks().Get(ctx, g.TaskID); err == nil {
				d.Title = t.Title
			}
		default: // one run: an automation's (named after it) or a job of its own
			d.Kind, d.JobID = "job", strings.TrimPrefix(g.Key, "j:")
			if g.Origin == "automation" {
				d.Kind = "automation"
				d.Title = firstNonEmptyStr(look(n.automations, g.OriginID, func() (string, error) {
					a, err := s.cfg.Store.Automations().Get(ctx, g.OriginID)
					return a.Name, err
				}), g.Title)
			}
		}
		d.ProjectName = look(n.projects, g.ProjectID, func() (string, error) {
			p, err := s.cfg.Store.Repos().Get(ctx, g.ProjectID)
			return p.Name, err
		})
		out = append(out, d)
	}
	next := ""
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(gs) >= min(limit, 100) {
		next = gs[len(gs)-1].Key
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out, "next_before": next})
}
