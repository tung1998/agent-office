package officetools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/templates/packs"
)

// An administrator's run manages the projects its project links to, as a
// company runs its departments (ADR-191): it makes one (linked both ways to
// the parent) and changes their settings. Code, git and data stay each
// project's own: work goes there with send_to_chat, or on its work board
// with burn_quest (ADR-196).

// linkedTools take a "project" (a linked project) in an administrator's run.
var linkedTools = []string{"describe", "list", "get", "propose_change", "propose_automation", "burn_quest", "burn_quests", "burn_assign", "burn_drop", "staff_load"}

// withLinked gives a settings tool a "project" argument: this project or one it links to.
func withLinked(x Tool) Tool {
	props, _ := x.Schema["properties"].(map[string]any)
	if props == nil || !slices.Contains(linkedTools, x.Name) {
		return x
	}
	schema := make(map[string]any, len(x.Schema))
	for k, v := range x.Schema {
		schema[k] = v
	}
	np := make(map[string]any, len(props)+1)
	for k, v := range props {
		np[k] = v
	}
	np["project"] = map[string]any{"type": "string", "description": "A project this one links to (id or name), to manage its agents, automations, workflows, policy; empty = this project"}
	if questTool(x.Name) { // ADR-196: a unit's work board
		np["project"] = map[string]any{"type": "string", "description": "A project this one links to (id or name), to use its work board instead (a unit you direct); empty = this project"}
	}
	schema["properties"] = np
	x.Schema = schema
	return x
}

// createProjectTool makes a sub-project from an administrator's run.
func (t *Toolbox) createProjectTool() Tool {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return Tool{Name: "propose_create_project", Description: "Create a new project under this one or under a project it links to: a department, a team, a product. It is a new empty git repo in " +
		t.transfer.CloneRoot() + " (or a clone of url), set up from pack when given. The new project and its parent link to each other, and its default agent is told who it reports to. As administrator it is done at once.",
		Schema: obj(map[string]any{
			"name": str("Project name"), "description": str("What it is for, its mandate"),
			"pack":   str("Project pack to set it up from: a pack of office's library by name (e.g. @agentoffice/startup/marketing; list resource project_pack shows them), or a .json/.zip file (absolute, or relative to this project's folder)"),
			"parent": str("The project it belongs to: this one (default) or one it links to (id or name)"),
			"url":    str("Git URL to clone instead of a new repo (optional)"), "dir": str("Folder name (optional)"), "reason": str("Why")}, "name", "reason")}
}

// linkedProject is ref among the projects sc's project links to (or itself).
func (t *Toolbox) linkedProject(ctx context.Context, sc Scope, ref string) (storage.Repo, error) {
	if ref = strings.TrimSpace(ref); ref == "" || ref == sc.ProjectID {
		return t.store.Repos().Get(ctx, sc.ProjectID)
	}
	if p, err := t.store.Repos().Get(ctx, sc.ProjectID); err == nil && strings.EqualFold(p.Name, ref) {
		return p, nil
	}
	return perm.LinkedProject(ctx, t.store, sc.ProjectID, ref)
}

// createProject runs propose_create_project for an administrator's run.
func (t *Toolbox) createProject(ctx context.Context, sc Scope, raw json.RawMessage) (string, bool) {
	var in struct{ Name, Description, Pack, Parent, URL, Dir, Reason string }
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "Tham số không hợp lệ: " + err.Error(), true
		}
	}
	parent, err := t.linkedProject(ctx, sc, in.Parent)
	if err != nil {
		return err.Error(), true
	}
	np := storage.NewProject{Name: in.Name, Description: in.Description, URL: in.URL, Dir: in.Dir, Parent: parent.ID}
	kind := "create_project"
	if pack := strings.TrimSpace(in.Pack); pack != "" {
		if !filepath.IsAbs(pack) && !packs.IsRef(pack) {
			own, err := t.store.Repos().Get(ctx, sc.ProjectID)
			if err != nil || own.Path == "" {
				return "pack phải là đường dẫn tuyệt đối (project này không có thư mục)", true
			}
			pack = filepath.Join(own.Path, pack)
		}
		np.Pack, kind = pack, "import_project"
	}
	out, err := t.proposeProject(ctx, sc, kind, np, in.Reason)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}
