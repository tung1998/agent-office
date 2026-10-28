package api

import (
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/audit"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/verify"
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
	agents := []map[string]string{} // whom it may lend as experts (ADR-177)
	if list, err := s.cfg.Store.Agents().List(r.Context(), p.ID); err == nil {
		for _, a := range list {
			agents = append(agents, map[string]string{"value": a.ID, "label": a.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": pol, "levels": perm.All, "packs": perm.ProjectPacks(p.Path, pol.Packs), "safe": pol.Safe, "version": policyVersion(pol),
		"offices": s.officeProjects(r), "agents": agents, "default_agent": p.DefaultAgentID})
}

// officeProjects are the projects other offices open to this one, to link
// to (ADR-167): their ref and "<office>/<project>".
func (s *server) officeProjects(r *http.Request) []map[string]string {
	out := []map[string]string{}
	if s.cfg.Peers == nil {
		return out
	}
	list, _ := s.cfg.Peers.List(r.Context())
	for _, p := range list {
		for _, rp := range p.Remote {
			if rp.Assistant { // its machine: only the office assistant reaches it
				continue
			}
			out = append(out, map[string]string{"value": peer.RefPrefix + p.ID + "/" + rp.ID, "label": p.Name + "/" + rp.Name, "description": rp.Description})
		}
	}
	return out
}

func (s *server) putPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		perm.Policy
		Version string `json:"version"` // the policy as it was read (409 when changed since)
	}
	// a body without quick_check/linked_projects (an older client) keeps what is set
	old, raw := perm.LoadPolicyRaw(r.Context(), s.cfg.Store, r.PathValue("id"))
	body.QuickCheck, body.QuickChecks, body.MergeChecks, body.LinkedProjects, body.Experts = old.QuickCheck, old.QuickChecks, old.MergeChecks, old.LinkedProjects, old.Experts
	if !decode(w, r, &body) {
		return
	}
	in := body.Policy
	in.QuickChecks = strings.TrimSpace(in.QuickChecks)
	if _, err := perm.ParseQuickChecks(in.QuickChecks); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.MergeChecks = strings.TrimSpace(in.MergeChecks)
	if _, err := verify.ParseRules(in.MergeChecks); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), projectID); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, body.Version, policyVersion(old)) {
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
	linked := []string{}
	for _, id := range clean(in.LinkedProjects) {
		if id == projectID || slices.Contains(linked, id) {
			continue
		}
		if peerID, _, ok := peer.SplitRef(id); ok { // in another office (ADR-167): a paired one
			if s.cfg.Peers == nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("không có office liên kết cho %q", id))
				return
			}
			if _, err := s.cfg.Peers.Get(r.Context(), peerID); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("không có office liên kết cho %q", id))
				return
			}
		} else if _, err := s.cfg.Store.Repos().Get(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("không có project %q để liên kết", id))
			return
		}
		linked = append(linked, id)
	}
	in.LinkedProjects = linked
	if in.Experts != nil { // the agents it lends (ADR-177): its own only
		experts := []string{}
		for _, id := range clean(*in.Experts) {
			if a, err := s.cfg.Store.Agents().Get(r.Context(), id); err != nil || a.ProjectID != projectID {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("không có agent %q trong project", id))
				return
			}
			if !slices.Contains(experts, id) {
				experts = append(experts, id)
			}
		}
		in.Experts = &experts
	}
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
	if err := perm.SavePolicyIf(r.Context(), s.cfg.Store, projectID, raw, in); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeSaveConflict(w) // a write landed between this Get and Update
			return
		}
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "project.policy", Resource: "policy", ResourceID: projectID, ProjectID: projectID, Before: old,
		After: perm.LoadPolicy(r.Context(), s.cfg.Store, projectID), Detail: map[string]any{"packs": len(in.Packs)}})
	writeJSON(w, http.StatusOK, map[string]any{"policy": perm.LoadPolicy(r.Context(), s.cfg.Store, projectID)})
}
