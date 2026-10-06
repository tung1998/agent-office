package api

import (
	"context"
	"errors"
	"github.com/tung1998/agent-office/internal/audit"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/monitor"
	"github.com/tung1998/agent-office/internal/storage"
)

func (s *server) monitorRoutes(mux *http.ServeMux, auth, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/monitors", auth(s.listMonitors))
	mux.Handle("POST /api/projects/{id}/monitors", admin(s.createMonitor))
	mux.Handle("PATCH /api/monitors/{id}", admin(s.updateMonitor))
	mux.Handle("DELETE /api/monitors/{id}", admin(s.deleteMonitor))
	mux.Handle("POST /api/monitors/{id}/check", admin(s.checkMonitor))
	mux.Handle("GET /api/monitor-events", auth(s.monitorEvents))
	// public: services push heartbeats without a session (the token is the secret)
	mux.HandleFunc("GET /api/heartbeat/{token}", s.heartbeat)
	mux.HandleFunc("POST /api/heartbeat/{token}", s.heartbeat)
}

type checkPoint struct {
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	LatencyMS int       `json:"latency_ms"`
	Message   string    `json:"message"`
}

type monitorDTO struct {
	ID            string                `json:"id"`
	ProjectID     string                `json:"project_id"`
	Name          string                `json:"name"`
	Type          string                `json:"type"`
	Target        string                `json:"target"`
	Config        storage.MonitorConfig `json:"config"`
	IntervalS     int                   `json:"interval_s"`
	Enabled       bool                  `json:"enabled"`
	AIEnabled     bool                  `json:"ai_enabled"`
	AIBudgetUSD   float64               `json:"ai_budget_usd"`
	HeartbeatPath string                `json:"heartbeat_path,omitempty"`
	Status        string                `json:"status"`
	LastCheckedAt *time.Time            `json:"last_checked_at"`
	LastChangeAt  *time.Time            `json:"last_change_at"`
	LastLatencyMS int                   `json:"last_latency_ms"`
	LastMessage   string                `json:"last_message"`
	Uptime24h     *float64              `json:"uptime_24h"` // percent; nil without checks
	AvgLatencyMS  int                   `json:"avg_latency_ms"`
	Trend         []checkPoint          `json:"trend"` // last 30 checks, oldest first
}

func (s *server) toMonitorDTO(r *http.Request, m storage.Monitor, withStats bool) monitorDTO {
	d := monitorDTO{ID: m.ID, ProjectID: m.ProjectID, Name: m.Name, Type: m.Type, Target: m.Target, Config: m.Config, IntervalS: m.IntervalS,
		Enabled: m.Enabled, AIEnabled: m.AIEnabled, AIBudgetUSD: m.AIBudgetUSD, Status: m.Status, LastCheckedAt: m.LastCheckedAt,
		LastChangeAt: m.LastChangeAt, LastLatencyMS: m.LastLatencyMS, LastMessage: m.LastMessage, Trend: []checkPoint{}}
	if !m.Enabled {
		d.Status = "paused"
	}
	if m.Type == "heartbeat" && m.Token != "" {
		d.HeartbeatPath = "/api/heartbeat/" + m.Token
	}
	if !withStats {
		return d
	}
	checks, err := s.cfg.Store.Monitors().Checks(r.Context(), m.ID, time.Now().Add(-24*time.Hour))
	if err != nil || len(checks) == 0 {
		return d
	}
	ok, lat, latN := 0, 0, 0
	for _, c := range checks {
		if c.OK {
			ok++
			if c.LatencyMS > 0 {
				lat += c.LatencyMS
				latN++
			}
		}
	}
	up := float64(ok) * 100 / float64(len(checks))
	d.Uptime24h = &up
	if latN > 0 {
		d.AvgLatencyMS = lat / latN
	}
	for _, c := range checks[max(0, len(checks)-30):] {
		d.Trend = append(d.Trend, checkPoint{At: c.At, OK: c.OK, LatencyMS: c.LatencyMS, Message: c.Message})
	}
	return d
}

func (s *server) listMonitors(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Monitors().List(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]monitorDTO, 0, len(list))
	sum := map[string]int{"up": 0, "down": 0, "pending": 0, "paused": 0}
	for _, m := range list {
		d := s.toMonitorDTO(r, m, true)
		sum[d.Status]++
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": out, "summary": sum})
}

type monitorInput struct {
	Name        *string                `json:"name"`
	Type        *string                `json:"type"`
	Target      *string                `json:"target"`
	Config      *storage.MonitorConfig `json:"config"`
	IntervalS   *int                   `json:"interval_s"`
	Enabled     *bool                  `json:"enabled"`
	AIEnabled   *bool                  `json:"ai_enabled"`
	AIBudgetUSD *float64               `json:"ai_budget_usd"`
}

func (s *server) applyMonitor(r *http.Request, in monitorInput, m *storage.Monitor) error {
	if in.Name != nil {
		m.Name = strings.TrimSpace(*in.Name)
	}
	if in.Type != nil {
		m.Type = *in.Type
	}
	if in.Target != nil {
		m.Target = strings.TrimSpace(*in.Target)
	}
	if in.Config != nil {
		m.Config = *in.Config
	}
	if in.IntervalS != nil {
		m.IntervalS = *in.IntervalS
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if in.AIEnabled != nil {
		m.AIEnabled = *in.AIEnabled
	}
	if in.AIBudgetUSD != nil {
		m.AIBudgetUSD = *in.AIBudgetUSD
	}
	if m.Name == "" || len(m.Name) > 120 {
		return errors.New("tên từ 1 đến 120 ký tự")
	}
	if m.IntervalS < 10 || m.IntervalS > 86400 {
		return errors.New("chu kỳ kiểm tra từ 10 giây đến 1 ngày")
	}
	if m.AIBudgetUSD < 0 {
		return errors.New("trần AI không được âm")
	}
	switch m.Type {
	case "http":
		u, err := url.Parse(m.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("URL phải bắt đầu bằng http:// hoặc https://")
		}
	case "tcp":
		host, port, err := net.SplitHostPort(m.Target)
		if err != nil || host == "" || port == "" {
			return errors.New("đích TCP dạng host:port, ví dụ localhost:6379")
		}
	case "heartbeat":
		m.Target = ""
		if m.Token == "" {
			m.Token = monitor.NewToken()
		}
	case "process":
		p, err := s.cfg.Store.Processes().Get(r.Context(), m.Target)
		if err != nil || p.ProjectID != m.ProjectID {
			return errors.New("tiến trình không thuộc project")
		}
	case "container":
		if m.Target == "" {
			return errors.New("cần tên service trong docker compose")
		}
	default:
		return errors.New("loại giám sát phải là http, tcp, heartbeat, process hoặc container")
	}
	return nil
}

func (s *server) createMonitor(w http.ResponseWriter, r *http.Request) {
	var in monitorInput
	if !decode(w, r, &in) {
		return
	}
	if _, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id")); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	m := storage.Monitor{ProjectID: r.PathValue("id"), IntervalS: 60, Enabled: true, AIBudgetUSD: 0.5}
	if err := s.applyMonitor(r, in, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := s.cfg.Store.Monitors().Create(r.Context(), m)
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, http.StatusConflict, "đã có giám sát cùng tên trong project")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "monitor.create", ResourceID: m.ID, ProjectID: m.ProjectID, After: s.toMonitorDTO(r, m, false),
		Detail: map[string]any{"type": m.Type, "target": m.Target}})
	if m.Type != "heartbeat" {
		go s.cfg.Monitors.CheckNow(context.Background(), m.ID) // first result right away
	}
	writeJSON(w, http.StatusCreated, s.toMonitorDTO(r, m, false))
}

func (s *server) updateMonitor(w http.ResponseWriter, r *http.Request) {
	var in monitorInput
	if !decode(w, r, &in) {
		return
	}
	m, err := s.cfg.Store.Monitors().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	old := m
	if err := s.applyMonitor(r, in, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Store.Monitors().Update(r.Context(), m); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "đã có giám sát cùng tên trong project")
			return
		}
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "monitor.update", ResourceID: m.ID, ProjectID: m.ProjectID,
		Before: s.toMonitorDTO(r, old, false), After: s.toMonitorDTO(r, m, false), Detail: map[string]any{"enabled": m.Enabled, "ai_enabled": m.AIEnabled}})
	writeJSON(w, http.StatusOK, s.toMonitorDTO(r, m, true))
}

func (s *server) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	old, err := s.cfg.Store.Monitors().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Monitors().Delete(r.Context(), r.PathValue("id")); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "monitor.delete", ResourceID: old.ID, ProjectID: old.ProjectID, Before: s.toMonitorDTO(r, old, false)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) checkMonitor(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Monitors.CheckNow(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toMonitorDTO(r, m, true))
}

type monitorEventDTO struct {
	ID             string    `json:"id"`
	MonitorID      string    `json:"monitor_id"`
	MonitorName    string    `json:"monitor_name"`
	ProjectID      string    `json:"project_id"`
	Kind           string    `json:"kind"`
	Message        string    `json:"message"`
	Analysis       string    `json:"analysis"`
	AnalysisStatus string    `json:"analysis_status"`
	CostUSD        float64   `json:"cost_usd"`
	At             time.Time `json:"at"`
}

func (s *server) monitorEvents(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := s.cfg.Store.Monitors().Events(r.Context(), project, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	names := map[string]string{}
	if list, err := s.cfg.Store.Monitors().List(r.Context(), project); err == nil {
		for _, m := range list {
			names[m.ID] = m.Name
		}
	}
	out := make([]monitorEventDTO, 0, len(evs))
	for _, e := range evs {
		out = append(out, monitorEventDTO{ID: e.ID, MonitorID: e.MonitorID, MonitorName: names[e.MonitorID], ProjectID: e.ProjectID, Kind: e.Kind,
			Message: e.Message, Analysis: e.Analysis, AnalysisStatus: e.AnalysisStatus, CostUSD: e.CostUSD, At: e.At})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (s *server) heartbeat(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Monitors.Ping(r.Context(), r.PathValue("token")); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "không có heartbeat này")
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
