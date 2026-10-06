package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/scan"
	"github.com/tung1998/agent-office/internal/setup"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// scanProject reads the project folder; machine-wide projects have nothing to scan.
func (s *server) scanProject(w http.ResponseWriter, r *http.Request) (storage.Repo, *scan.Summary, bool) {
	x, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return x, nil, false
	}
	if x.Path == "" {
		return x, nil, true
	}
	sum, err := scan.Scan(x.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Không quét được thư mục project: "+err.Error())
		return x, nil, false
	}
	return x, sum, true
}

func (s *server) setupScan(w http.ResponseWriter, r *http.Request) {
	_, sum, ok := s.scanProject(w, r)
	if !ok {
		return
	}
	size := 0
	if sum != nil {
		size = len(sum.Text())
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": sum, "prompt_chars": size})
}

func (s *server) setupPropose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Goal string `json:"goal"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	x, sum, ok := s.scanProject(w, r)
	if !ok {
		return
	}
	text := ""
	if sum != nil {
		text = sum.Text()
	} else if strings.TrimSpace(in.Goal) == "" {
		writeError(w, http.StatusBadRequest, "Project toàn máy không có thư mục để quét: hãy mô tả bạn muốn helper làm gì")
		return
	}
	res, err := s.cfg.Setup.Propose(r.Context(), x.ID, x.Name, text, in.Goal)
	var be *usage.BudgetError
	if errors.As(err, &be) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": be.Error(), "code": "budget"})
		return
	}
	if errors.Is(err, setup.ErrNoProvider) {
		writeJSON(w, http.StatusPreconditionFailed, map[string]any{"error": "Cần kết nối AI trước khi thiết lập bằng AI", "code": "no_provider"})
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.auditAction(r, "project.setup_propose", x.ID, map[string]any{"template": res.Proposal.TemplateKey, "model": res.Model,
		"input_tokens": res.Usage.InputTokens, "output_tokens": res.Usage.OutputTokens})
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "summary": sum})
}

type setupChoice struct {
	TemplateKey string              `json:"template_key"`
	Changes     []setup.AgentChange `json:"changes"`
	Description *string             `json:"description"`
}

func (s *server) setupBuild(w http.ResponseWriter, r *http.Request) {
	var in setupChoice
	if !decode(w, r, &in) {
		return
	}
	t, problems, err := s.cfg.Setup.Build(r.Context(), in.TemplateKey, in.Changes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if problems == nil {
		problems = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": t, "problems": problems})
}

func (s *server) setupApply(w http.ResponseWriter, r *http.Request) {
	var in setupChoice
	if !decode(w, r, &in) {
		return
	}
	x, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	m, err := s.cfg.Setup.Accept(r.Context(), x.ID, in.TemplateKey, in.Changes)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if in.Description != nil {
		x.Description = strings.TrimSpace(*in.Description)
		if err := s.cfg.Store.Repos().Update(r.Context(), x); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	s.auditAction(r, "project.setup_apply", x.ID, map[string]any{"template": in.TemplateKey, "model": m.ID, "changes": len(in.Changes)})
	d, _ := s.repoDTO(r, x, true)
	writeJSON(w, http.StatusOK, map[string]any{"project": d})
}
