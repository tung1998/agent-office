package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/storage"
)

// Accounts see only their projects (ADR-224): an admin sees every project; a
// manager (project admin) and a member see the projects they created or were
// given. A manager manages its projects as an admin would; what is the whole
// office's (users, AI connections, MCP, peers, the machine) stays the admin's.

// projectScope is the projects u sees; all = every project (an admin).
type projectScope struct {
	all bool
	ids map[string]bool
}

func (p projectScope) has(id string) bool { return p.all || p.ids[id] }

// list is the ids seen, for a store filter (nil = every project).
func (p projectScope) list() []string {
	if p.all {
		return nil
	}
	out := make([]string, 0, len(p.ids))
	for id := range p.ids {
		out = append(out, id)
	}
	return out
}

func (s *server) scopeOf(ctx context.Context, u storage.User) (projectScope, error) {
	if u.Role == storage.RoleAdmin {
		return projectScope{all: true}, nil
	}
	ids, err := s.cfg.Store.Users().Projects(ctx, u.ID)
	if err != nil {
		return projectScope{}, err
	}
	p := projectScope{ids: map[string]bool{}}
	for _, id := range ids {
		p.ids[id] = true
	}
	return p, nil
}

// scope is the projects the person of r sees.
func (s *server) scope(r *http.Request) (projectScope, error) {
	return s.scopeOf(r.Context(), userFrom(r))
}

// sees reports whether the person of r sees project id.
func (s *server) sees(r *http.Request, id string) bool {
	p, err := s.scope(r)
	return err == nil && p.has(id)
}

// manages reports whether u manages the projects it sees: an admin, or a
// manager (its own ones; the routes are gated to those).
func manages(u storage.User) bool {
	return u.Role == storage.RoleAdmin || u.Role == storage.RoleManager
}

// errNoProject: the request names a thing that does not exist.
var errNoProject = errors.New("not found")

// routeProject is the project the route of r is about ("" when it is about the
// whole office), found from its path: /api/projects/{id}/… or a thing of a
// project (/api/agents/{id}, /api/conversations/{id}…).
func (s *server) routeProject(r *http.Request) (string, error) {
	pattern := r.Pattern
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = pattern[i+1:]
	}
	rest, ok := strings.CutPrefix(pattern, "/api/")
	if !ok {
		return "", nil
	}
	ctx, st := r.Context(), s.cfg.Store
	pick := func(id string, err error) (string, error) {
		if errors.Is(err, storage.ErrNotFound) {
			return "", errNoProject
		}
		return id, err
	}
	conv := func(id string) (string, error) {
		c, err := st.Chat().GetConversation(ctx, id)
		return pick(c.ProjectID, err)
	}
	head := rest
	if i := strings.Index(rest, "/{"); i >= 0 {
		head = rest[:i]
	} else {
		return "", nil // no thing named in the path
	}
	switch head {
	case "projects":
		if !strings.HasPrefix(rest, "projects/{id}") {
			return "", nil
		}
		return r.PathValue("id"), nil
	case "agents":
		a, err := st.Agents().Get(ctx, r.PathValue("id"))
		return pick(a.ProjectID, err)
	case "conversations":
		return conv(r.PathValue("id"))
	case "chat/turns":
		if s.cfg.Chat == nil {
			return "", errNoProject
		}
		t, ok := s.cfg.Chat.Turn(r.PathValue("id"))
		if !ok {
			return "", errNoProject
		}
		return conv(t.ConversationID)
	case "patches":
		p, err := st.Chat().GetPatch(ctx, r.PathValue("id"))
		if err != nil {
			return pick("", err)
		}
		return conv(p.ConversationID)
	case "automations":
		a, err := st.Automations().Get(ctx, r.PathValue("id"))
		return pick(a.ProjectID, err)
	case "workflows":
		w, err := st.Workflows().Get(ctx, r.PathValue("id"))
		return pick(w.ProjectID, err)
	case "workflow-runs":
		w, err := st.WorkflowRuns().Get(ctx, r.PathValue("id"))
		return pick(w.ProjectID, err)
	case "jobs":
		j, err := st.Jobs().Get(ctx, r.PathValue("id"))
		return pick(j.ProjectID, err)
	case "processes":
		p, err := st.Processes().Get(ctx, r.PathValue("id"))
		return pick(p.ProjectID, err)
	case "monitors":
		m, err := st.Monitors().Get(ctx, r.PathValue("id"))
		return pick(m.ProjectID, err)
	case "actions":
		a, err := st.Actions().Get(ctx, r.PathValue("id"))
		return pick(a.ProjectID, err)
	case "channels":
		c, err := st.Channels().Get(ctx, r.PathValue("id"))
		return pick(c.ProjectID, err)
	case "memories":
		m, err := st.Memories().Get(ctx, r.PathValue("id"))
		return pick(m.ProjectID, err)
	case "memory-revisions":
		m, err := st.Memories().GetRevision(ctx, r.PathValue("id"))
		return pick(m.ProjectID, err)
	case "collections":
		c, err := st.Collections().ByID(ctx, r.PathValue("id"))
		return pick(c.ProjectID, err)
	case "revisions":
		v, err := st.Revisions().Get(ctx, r.PathValue("id"))
		return pick(v.ProjectID, err)
	case "attachments":
		if s.cfg.Chat == nil {
			return "", errNoProject
		}
		m, _, err := s.cfg.Chat.Attachments().Get(r.PathValue("id"))
		if err != nil {
			return "", errNoProject
		}
		return m.ProjectID, nil
	case "burn-items":
		it, err := st.Burn().Item(ctx, r.PathValue("item"))
		if err != nil {
			return pick("", err)
		}
		b, err := st.Burn().SessionByID(ctx, it.SessionID)
		return pick(b.ProjectID, err)
	case "burn-review-profiles":
		p, err := st.Burn().ReviewProfile(ctx, r.PathValue("profile"))
		return pick(p.ProjectID, err)
	}
	return "", nil
}

// gate checks the route of r against the projects of its person: a
// project's things are not found for someone who does not see it (an
// admin's route of a project passes for its manager: manages = true).
// It answers r itself and returns false when r stops here.
func (s *server) gate(w http.ResponseWriter, r *http.Request, adminOnly bool) bool {
	u := userFrom(r)
	if u.Role == storage.RoleAdmin {
		return true
	}
	pid, err := s.routeProject(r)
	if errors.Is(err, errNoProject) {
		writeError(w, http.StatusNotFound, "không tìm thấy")
		return false
	}
	if err != nil {
		s.internal(w, r, err)
		return false
	}
	if pid != "" {
		// the office assistant's chats are an admin's: it reads the whole office
		if !s.sees(r, pid) || pid == assistant.ID(r.Context(), s.cfg.Store) {
			writeError(w, http.StatusNotFound, "không tìm thấy project")
			return false
		}
	}
	if !adminOnly {
		return true
	}
	if u.Role == storage.RoleManager && (pid != "" || s.managerMay(r)) {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden")
	return false
}

// managerMay: an admin's route about no one project that a manager may use
// (making a project of its own, its approval inbox).
func (s *server) managerMay(r *http.Request) bool {
	switch r.Pattern {
	case "POST /api/projects", "POST /api/projects/new", "POST /api/projects/clone", "GET /api/fs/dirs",
		"GET /api/actions/pending": // filtered to its projects
		return true
	}
	return false
}

// ownProject gives the person of r the project it just made (ADR-224); an
// admin sees every project already.
func (s *server) ownProject(r *http.Request, projectID string) {
	if u := userFrom(r); u.Role != storage.RoleAdmin && projectID != "" {
		if err := s.cfg.Store.Users().AddProject(r.Context(), u.ID, projectID); err != nil {
			s.log.Warn("give the new project to its maker", "project", projectID, "user", u.ID, "err", err)
		}
	}
}
