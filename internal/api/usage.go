package api

import (
	"github.com/tung1998/agent-office/internal/audit"
	"net/http"
	"strconv"
	"time"

	"github.com/tung1998/agent-office/internal/usage"
)

func (s *server) usageSummary(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	sum, err := s.cfg.Usage.Summarize(r.Context(), days)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

type runDTO struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	ProjectID    string    `json:"project_id"`
	ProjectName  string    `json:"project_name"`
	AgentID      string    `json:"agent_id"`
	ProviderName string    `json:"provider_name"`
	Model        string    `json:"model"`
	Status       string    `json:"status"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CostUSD      *float64  `json:"cost_usd"`
	CostSource   string    `json:"cost_source"`
	DurationMS   int64     `json:"duration_ms"`
	Error        string    `json:"error"`
	Actor        string    `json:"actor"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *server) usageRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	runs, err := s.cfg.Usage.Runs(r.Context(), q.Get("project"), days, limit, q.Get("before"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	names := map[string]string{}
	out := make([]runDTO, 0, len(runs))
	for _, x := range runs {
		name, ok := names[x.ProjectID]
		if !ok && x.ProjectID != "" {
			if p, err := s.cfg.Store.Repos().Get(r.Context(), x.ProjectID); err == nil {
				name = p.Name
			}
			names[x.ProjectID] = name
		}
		out = append(out, runDTO{ID: x.ID, Kind: x.Kind, ProjectID: x.ProjectID, ProjectName: name, AgentID: x.AgentID,
			ProviderName: x.ProviderName, Model: x.Model, Status: x.Status, InputTokens: x.InputTokens, OutputTokens: x.OutputTokens,
			CostUSD: x.CostUSD, CostSource: x.CostSource, DurationMS: x.DurationMS, Error: x.Error, Actor: x.Actor, CreatedAt: x.CreatedAt})
	}
	res := map[string]any{"runs": out}
	if len(out) == limit { // there may be more: the page after the last one
		res["next_before"] = out[len(out)-1].ID
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) usageSettings(w http.ResponseWriter, r *http.Request) {
	var in usage.Settings
	if !decode(w, r, &in) {
		return
	}
	old, _ := s.cfg.Usage.Settings(r.Context())
	if err := s.cfg.Usage.SaveSettings(r.Context(), in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, _ := s.cfg.Usage.Settings(r.Context())
	s.audit(r, audit.Change{Action: "usage.settings", Resource: "usage_settings", Before: old, After: st,
		Detail: map[string]any{"daily_limit_usd": in.DailyLimitUSD, "project_limits": len(in.ProjectLimits), "prices": len(in.Prices)}})
	writeJSON(w, http.StatusOK, st)
}

// projectBudget is a project's daily budget, what it spent today, and the
// office-wide limit above it.
func (s *server) projectBudget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.cfg.Usage.Settings(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	today := 0.0
	if sum, err := s.cfg.Usage.Summarize(r.Context(), 1); err == nil {
		today = sum.ProjectToday[id]
	}
	writeJSON(w, http.StatusOK, map[string]any{"daily_limit_usd": st.ProjectLimits[id], "today_usd": today, "office_limit_usd": st.DailyLimitUSD})
}

// setProjectBudget sets one project's daily budget (0 = none); the rest of
// the settings stay as they are.
func (s *server) setProjectBudget(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DailyLimitUSD float64 `json:"daily_limit_usd"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.DailyLimitUSD < 0 {
		writeError(w, http.StatusBadRequest, "ngân sách không được âm")
		return
	}
	id := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	old, _, err := s.cfg.Usage.SetProjectLimit(r.Context(), id, in.DailyLimitUSD)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "usage.project_budget", Resource: "usage_settings", ProjectID: id,
		Before: map[string]any{"daily_limit_usd": old}, After: map[string]any{"daily_limit_usd": in.DailyLimitUSD}})
	writeJSON(w, http.StatusOK, map[string]any{"daily_limit_usd": in.DailyLimitUSD})
}
