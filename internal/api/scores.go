package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/scoring"
	"github.com/tung1998/agent-office/internal/storage"
)

// Model/effort scoring and suggestions (ADR-267): office only suggests; the
// person applies a suggestion to one chat.

func (s *server) scoreRoutes(mux *http.ServeMux, auth, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("PUT /api/conversations/{id}/agents/{agent}/model", auth(s.ownChat(s.setMemberModel)))
	mux.Handle("PUT /api/conversations/{id}/messages/{mid}/feedback", auth(s.ownChat(s.setFeedback)))
	mux.Handle("GET /api/conversations/{id}/suggest", auth(s.ownChat(s.suggestConfig)))
	mux.Handle("GET /api/projects/{id}/model-stats", auth(s.modelStats))
	mux.Handle("PUT /api/projects/{id}/job-scores/{job}/labels", admin(s.setScoreLabels))
	mux.Handle("GET /api/agents/{id}/fallback-suggest", admin(s.fallbackSuggest))
}

func (s *server) setMemberModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := s.cfg.Chat.SetMemberModel(r.Context(), r.PathValue("id"), r.PathValue("agent"), in.Model)
	if errors.Is(err, chat.ErrUnknownModel) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": in.Model})
}

func (s *server) setFeedback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value int `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	m, err := s.cfg.Store.Chat().MessageHead(ctx, r.PathValue("mid"))
	if err != nil || m.ConversationID != r.PathValue("id") {
		writeError(w, http.StatusNotFound, "không tìm thấy tin nhắn")
		return
	}
	if m.Role != "assistant" {
		writeError(w, http.StatusBadRequest, "chỉ đánh giá được câu trả lời của agent")
		return
	}
	switch err := s.cfg.Chat.ScoreRecorder().SetFeedback(ctx, m.ID, in.Value); {
	case errors.Is(err, storage.ErrStale):
		writeError(w, http.StatusConflict, "câu trả lời đã quá 7 ngày, điểm đã chốt")
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "câu trả lời này chưa được chấm")
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"value": in.Value})
	}
}

// options are what the project's agents can run on: each agent's own
// connection with its own model and that connection's tier models.
func (s *server) options(r *http.Request, projectID string) ([]scoring.Option, []storage.Provider, error) {
	ctx := r.Context()
	agents, err := s.cfg.Store.Agents().List(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	ps, err := s.cfg.Store.Providers().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	var out []scoring.Option
	for _, a := range agents {
		if a.Disabled || s.cfg.Providers == nil {
			continue
		}
		p, model, err := s.cfg.Providers.ResolveModel(ctx, a)
		if err != nil || !p.Enabled {
			continue
		}
		o := scoring.Option{Agent: a, Provider: p}
		for _, m := range append([]string{model}, p.TierModels[storage.TierStrong], p.TierModels[storage.TierBalanced], p.TierModels[storage.TierFast]) {
			if m != "" && !slices.Contains(o.Models, m) {
				o.Models = append(o.Models, m)
			}
		}
		out = append(out, o)
	}
	return out, ps, nil
}

func (s *server) projectScores(r *http.Request, projectID string, days int, agentID string) ([]storage.JobScore, error) {
	if err := s.cfg.Chat.ScoreRecorder().Refresh(r.Context(), projectID); err != nil {
		return nil, err
	}
	return s.cfg.Store.Scores().List(r.Context(), storage.ScoreFilter{ProjectID: projectID, AgentID: agentID, Since: time.Now().UTC().AddDate(0, 0, -days)})
}

func (s *server) modelStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	pid := r.PathValue("id")
	scores, err := s.projectScores(r, pid, days, r.URL.Query().Get("agent"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	_, ps, err := s.options(r, pid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	names := map[string]string{}
	if agents, err := s.cfg.Store.Agents().List(r.Context(), pid); err == nil {
		for _, a := range agents {
			names[a.ID] = a.Name
		}
	}
	quota := s.cfg.Chat.Quotas(r.Context(), ps)
	conns := []map[string]any{}
	for _, p := range ps {
		if p.Enabled {
			q := quota[p.ID]
			conns = append(conns, map[string]any{"provider_id": p.ID, "name": p.Name, "over": q.Over, "max_util": q.MaxUtil,
				"resets_in_s": int(q.ResetsIn.Seconds()), "subscription": q.Subscription})
		}
	}
	recent := scores
	if len(recent) > 50 {
		recent = recent[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "table": scoring.Stats(scores, scoring.Tiers(ps)), "quota": conns,
		"recent": recent, "agents": names, "kinds": scoring.Kinds, "difficulties": scoring.Difficulties})
}

func (s *server) setScoreLabels(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind       string `json:"kind"`
		Difficulty string `json:"difficulty"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !slices.Contains(scoring.Kinds, in.Kind) || !slices.Contains(scoring.Difficulties, in.Difficulty) {
		writeError(w, http.StatusBadRequest, "loại việc hoặc độ khó không hợp lệ")
		return
	}
	sc, err := s.cfg.Store.Scores().Get(r.Context(), r.PathValue("job"))
	if err != nil || sc.ProjectID != r.PathValue("id") {
		writeError(w, http.StatusNotFound, "không tìm thấy việc")
		return
	}
	if err := s.cfg.Store.Scores().SetLabels(r.Context(), sc.JobID, in.Kind, in.Difficulty); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": in.Kind, "difficulty": in.Difficulty})
}

// suggestConfig suggests a model and effort for the chat's next answer: for
// the work its draft (?text=) or its last scored answer is.
func (s *server) suggestConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conv, err := s.cfg.Store.Chat().GetConversation(ctx, r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	kind, diff := "", ""
	if text := r.URL.Query().Get("text"); text != "" {
		kind, diff = scoring.Classify(text, scoring.Source{Purpose: conv.Purpose})
	} else if last, _ := s.cfg.Store.Scores().List(ctx, storage.ScoreFilter{ConversationID: conv.ID}); len(last) > 0 {
		kind, diff = last[0].TaskKind, last[0].Difficulty
	}
	out := map[string]any{"suggestion": nil, "kind": kind, "difficulty": diff, "current_model": "", "models": []string{}}
	// the asking agent's own choice here and the models its connection offers
	if id := r.URL.Query().Get("agent"); id != "" {
		if ms, err := s.cfg.Store.Chat().Members(ctx, conv.ID); err == nil {
			for _, m := range ms {
				if m.AgentID == id {
					out["current_model"] = m.LLMModel
				}
			}
		}
		if a, err := s.cfg.Store.Agents().Get(ctx, id); err == nil && a.ProjectID == conv.ProjectID && s.cfg.Providers != nil {
			if p, model, err := s.cfg.Providers.ResolveModel(ctx, a); err == nil {
				models := []string{}
				for _, m := range append(append([]string{model}, p.Models...), p.TierModels[storage.TierStrong], p.TierModels[storage.TierBalanced], p.TierModels[storage.TierFast]) {
					if m != "" && !slices.Contains(models, m) {
						models = append(models, m)
					}
				}
				out["models"], out["agent_model"] = models, model
			}
		}
	}
	if kind == "" {
		out["reason"] = "no_work_yet"
		writeJSON(w, http.StatusOK, out)
		return
	}
	// no re-score here: it runs while the person types (the stats refresh)
	scores, err := s.cfg.Store.Scores().List(ctx, storage.ScoreFilter{ProjectID: conv.ProjectID, Since: time.Now().UTC().AddDate(0, 0, -90)})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	opts, ps, err := s.options(r, conv.ProjectID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	quota := s.cfg.Chat.Quotas(ctx, ps)
	sug, ok := scoring.Suggest(scores, opts, kind, diff, quota, scoring.Tiers(ps))
	if !ok {
		out["reason"] = "no_option"
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["suggestion"] = sug
	out["quota"] = quota[sug.ProviderID]
	writeJSON(w, http.StatusOK, out)
}

// fallbackSuggest suggests the order of an agent's fallback connections.
func (s *server) fallbackSuggest(w http.ResponseWriter, r *http.Request) {
	a, _, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ps, err := s.cfg.Store.Providers().List(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if s.cfg.Providers == nil {
		writeError(w, http.StatusNotFound, "không có kết nối")
		return
	}
	primary, _, err := s.cfg.Providers.ResolveModel(ctx, a)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	order, reasons := scoring.FallbackOrder(primary, ps, s.cfg.Chat.Quotas(ctx, ps), nil)
	if order == nil {
		order, reasons = []string{}, []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"primary": primary.ID, "order": order, "reasons": reasons, "current": a.FallbackProviderIDs})
}
