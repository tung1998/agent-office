package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/tung1998/agent-office/internal/storage"
)

// What an account other than an admin reaches beyond its projects (ADR-239):
// the other offices (peers) it may link its projects to, each whole or some
// of its projects. A manager runs the member accounts: it makes them, gives
// them its projects and the offices it has, and never touches an admin or
// another manager.

// allProjects: the whole of an office, its projects added later too.
const allProjects = "*"

// officeAccess: peer id → the projects of it reached (allProjects = all).
type officeAccess map[string][]string

func officesKey(userID string) string { return "user_offices/" + userID }

// officeScope is what u reaches in other offices; all = an admin, every office.
type officeScope struct {
	all bool
	acc officeAccess
}

func (o officeScope) allows(peerID, projectID string) bool {
	if o.all {
		return true
	}
	list := o.acc[peerID]
	return slices.Contains(list, allProjects) || slices.Contains(list, projectID)
}

func (s *server) officesOf(ctx context.Context, u storage.User) officeScope {
	if u.Role == storage.RoleAdmin {
		return officeScope{all: true}
	}
	acc := officeAccess{}
	_, _ = s.cfg.Store.Settings().Get(ctx, officesKey(u.ID), &acc)
	return officeScope{acc: acc}
}

// errNotYours: a manager gives only what it has itself.
var errNotYours = errors.New("chỉ giao được project và office mà bạn đang quản lý")

// cleanOffices keeps in to paired offices and to what by reaches: an office
// whole stays whole only when by has it whole (else by's projects of it).
func (s *server) cleanOffices(ctx context.Context, by officeScope, in officeAccess) (officeAccess, error) {
	out := officeAccess{}
	for peerID, list := range in {
		if len(list) == 0 {
			continue
		}
		if s.cfg.Peers == nil {
			return nil, errors.New("không có office khác nào")
		}
		if _, err := s.cfg.Peers.Get(ctx, peerID); err != nil {
			return nil, errors.New("không có office khác này")
		}
		got := []string{}
		for _, p := range list {
			if p == "" || slices.Contains(got, p) {
				continue
			}
			if p == allProjects && !by.all && !slices.Contains(by.acc[peerID], allProjects) {
				got = append(got, by.acc[peerID]...)
				continue
			}
			if !by.allows(peerID, p) {
				return nil, errNotYours
			}
			got = append(got, p)
		}
		if slices.Contains(got, allProjects) {
			got = []string{allProjects}
		}
		out[peerID] = got
	}
	return out, nil
}

// mergeOffices: what a manager sets on a member replaces only what the
// manager reaches; the rest (given by an admin or another manager) stays.
func mergeOffices(old, set officeAccess, by officeScope) officeAccess {
	if by.all {
		return set
	}
	out := officeAccess{}
	for peerID, list := range old {
		for _, p := range list {
			if !by.allows(peerID, p) {
				out[peerID] = append(out[peerID], p)
			}
		}
	}
	for peerID, list := range set {
		for _, p := range list {
			if !slices.Contains(out[peerID], p) {
				out[peerID] = append(out[peerID], p)
			}
		}
	}
	return out
}

// mergeProjects: the same for projects.
func mergeProjects(old, set []string, by projectScope) []string {
	if by.all {
		return set
	}
	out := []string{}
	for _, id := range old {
		if !by.has(id) {
			out = append(out, id)
		}
	}
	for _, id := range set {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// shownProjects: what of a member's projects the person of r sees.
func shownProjects(list []string, by projectScope) []string {
	out := []string{}
	for _, id := range list {
		if by.has(id) {
			out = append(out, id)
		}
	}
	return out
}

func shownOffices(acc officeAccess, by officeScope) officeAccess {
	out := officeAccess{}
	for peerID, list := range acc {
		for _, p := range list {
			if by.allows(peerID, p) {
				out[peerID] = append(out[peerID], p)
			}
		}
	}
	return out
}

// memberTarget answers r and reports false unless its person may run the
// account id: an admin any, a manager a member only.
func (s *server) memberTarget(w http.ResponseWriter, r *http.Request, id string) (storage.User, bool) {
	t, err := s.cfg.Store.Users().GetByID(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return t, false
	}
	if err != nil {
		s.internal(w, r, err)
		return t, false
	}
	if userFrom(r).Role != storage.RoleAdmin && t.Role != storage.RoleMember {
		writeError(w, http.StatusForbidden, "chỉ quản lý được tài khoản member")
		return t, false
	}
	return t, true
}

// officeChoices: the offices (and their projects) the person of r may give.
func (s *server) officeChoices(r *http.Request) []map[string]any {
	out := []map[string]any{}
	if s.cfg.Peers == nil {
		return out
	}
	by := s.officesOf(r.Context(), userFrom(r))
	list, _ := s.cfg.Peers.List(r.Context())
	for _, p := range list {
		whole := by.all || slices.Contains(by.acc[p.ID], allProjects)
		projects := []map[string]string{}
		for _, rp := range p.Remote {
			if !rp.Assistant && by.allows(p.ID, rp.ID) {
				projects = append(projects, map[string]string{"id": rp.ID, "name": rp.Name})
			}
		}
		if whole || len(projects) > 0 {
			out = append(out, map[string]any{"id": p.ID, "name": p.Name, "whole": whole, "projects": projects})
		}
	}
	return out
}
