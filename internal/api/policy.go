package api

import (
	"fmt"
	"github.com/tung1998/agent-office/internal/audit"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// allowedMode: members may only ask first; admins may pick any package
// (the project's cap and each agent's own package still apply).
func (s *server) allowedMode(r *http.Request, mode string) string {
	if !perm.Valid(mode) {
		return perm.Propose
	}
	if userFrom(r).Role != storage.RoleAdmin && perm.AtLeast(mode, perm.Check) {
		return perm.Propose
	}
	return mode
}

// allowedEditMode: editing the project folder directly is for admins
func (s *server) allowedEditMode(r *http.Request, mode string) string {
	if mode == perm.EditDirect && userFrom(r).Role == storage.RoleAdmin {
		return perm.EditDirect
	}
	return perm.EditWorktree
}

func (s *server) permissionLevels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"levels": perm.All, "caps": perm.Caps, "presets": presets()})
}

// presets: the capabilities of each package, for the dashboard.
func presets() map[string][]string {
	out := map[string][]string{}
	for _, l := range perm.All {
		out[l.Level] = perm.Preset(l.Level)
	}
	return out
}

func (s *server) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	pol := perm.LoadPolicy(r.Context(), s.cfg.Store, p.ID)
	writeJSON(w, http.StatusOK, map[string]any{"policy": pol, "levels": perm.All, "packs": perm.ProjectPacks(p.Path, pol.Packs), "safe": pol.Safe, "version": policyVersion(pol)})
}

func (s *server) putPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		perm.Policy
		Version string `json:"version"` // the policy as it was read (409 when changed since)
	}
	if !decode(w, r, &body) {
		return
	}
	in := body.Policy
	projectID := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), projectID); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, body.Version, policyVersion(perm.LoadPolicy(r.Context(), s.cfg.Store, projectID))) {
		return
	}
	in.MaxLevel = perm.Operate // no project cap: the chat/task mode is the limit
	clean := func(list []string) []string {
		out := []string{}
		for _, x := range list {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	in.DenyPaths = clean(in.DenyPaths)
	links := []string{}
	for _, l := range clean(in.WorktreeLinks) {
		l = strings.Trim(filepath.ToSlash(filepath.Clean(l)), "/")
		if l == "." || l == ".." || strings.HasPrefix(l, "../") || filepath.IsAbs(l) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("đường dẫn %q phải nằm trong project", l))
			return
		}
		links = append(links, l)
	}
	in.WorktreeLinks = links
	patterns := func(list []string) ([]string, error) {
		out := []string{}
		for _, x := range list {
			if strings.TrimSpace(x) == "" {
				continue
			}
			c, err := perm.CleanPattern(x)
			if err != nil {
				return nil, fmt.Errorf("lệnh %q không hợp lệ: chạy không qua shell nên không dùng | ; & > $, và * chỉ ở cuối", x)
			}
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
		return out, nil
	}
	var err error
	packs := []perm.Pack{}
	for i, p := range in.Packs {
		p.Label, p.Custom = strings.TrimSpace(p.Label), false
		if p.Label == "" {
			continue
		}
		if p.Commands, err = patterns(p.Commands); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if p.ID == "" || !strings.HasPrefix(p.ID, "custom-") {
			p.ID = fmt.Sprintf("custom-%d", i+1)
		}
		packs = append(packs, p)
	}
	in.Packs = packs
	old := perm.LoadPolicy(r.Context(), s.cfg.Store, projectID)
	if err := perm.SavePolicy(r.Context(), s.cfg.Store, projectID, in); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "project.policy", Resource: "policy", ResourceID: projectID, ProjectID: projectID, Before: old,
		After: perm.LoadPolicy(r.Context(), s.cfg.Store, projectID), Detail: map[string]any{"packs": len(in.Packs)}})
	writeJSON(w, http.StatusOK, map[string]any{"policy": perm.LoadPolicy(r.Context(), s.cfg.Store, projectID)})
}
