package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/deps"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// ErrNoDeps: the diff is not merged, or changes no dependencies.
var ErrNoDeps = errors.New("diff chưa gộp vào project hoặc không đổi thư viện nào")

// InstallDeps proposes again the install of a merged diff's dependencies
// (after one failed, or the project's folders were cleaned): the cards made,
// or the one still waiting.
func (e *Engine) InstallDeps(ctx context.Context, patchID string) ([]ActionDTO, error) {
	p, err := e.store.Chat().GetPatch(ctx, patchID)
	if err != nil {
		return nil, err
	}
	if p.Status != "applied" {
		return nil, ErrNoDeps
	}
	projectID := ""
	if p.TaskID != "" {
		t, err := e.store.Tasks().Get(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		projectID = t.ProjectID
	} else {
		c, err := e.store.Chat().GetConversation(ctx, p.ConversationID)
		if err != nil {
			return nil, err
		}
		projectID = c.ProjectID
	}
	project, err := e.store.Repos().Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	acts, why := e.proposeDeps(ctx, project, p)
	if len(acts) == 0 {
		if why != nil { // there were changes, no card could be made: say why
			return nil, why
		}
		return nil, ErrNoDeps
	}
	out := make([]ActionDTO, len(acts))
	for i, a := range acts {
		out[i] = ToActionDTO(a)
	}
	return out, nil
}

// depsAgent proposes the install a merged diff needs (it is no agent's).
const depsAgent = "office"

// proposeDeps: a diff just merged into the project changes its dependencies
// (ADR-251): one deps_install card per package, next to the diff, so the
// project's node_modules/.venv follow the new lockfile. The project may
// install them at once (policy AutoDeps). A card never holds the chat's other
// proposals back (followUp). The cards made, and why the others could not
// be (each also told in the chat).
func (e *Engine) proposeDeps(ctx context.Context, project storage.Repo, p storage.Patch) ([]storage.Action, error) {
	if e.acts == nil || project.Path == "" {
		return nil, nil
	}
	// read against the merged files: a package.json whose scripts alone
	// changed needs no install
	changes := deps.ChangesOf(project.Path, p.Diff, p.Files)
	if len(changes) == 0 {
		return nil, nil
	}
	sc := actions.Scope{ProjectID: project.ID, ConversationID: p.ConversationID, TaskID: p.TaskID, Agent: depsAgent}
	tree := e.treeOf(project.ID, p)
	auto := perm.LoadPolicy(ctx, e.store, project.ID).AutoDeps
	var out []storage.Action
	var failed []error
	for _, c := range changes {
		args := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: c.Dir, Manager: c.Manager, Files: c.Files}, Patch: p.ID, Tree: tree}
		a, err := e.acts.Propose(ctx, sc, "deps_install", c.Dir, "Diff vừa gộp đổi thư viện của "+c.Dir+": cài lại để project chạy đúng bản trong lockfile mới", args) // i18n-ignore
		if err != nil {
			// the project keeps its old libraries: say so where the diff is, or
			// no one knows its install never got a card
			slog.Warn("chat: deps install card", "patch", p.ID, "dir", c.Dir, "err", err)
			failed = append(failed, fmt.Errorf("%s: %w", c.Label(), err))
			if p.ConversationID != "" {
				e.note(storage.Conversation{ID: p.ConversationID}, "Không tạo được thẻ cài thư viện cho "+c.Label()+" sau khi gộp diff: "+err.Error()+". Project vẫn dùng thư viện cũ; sửa rồi bấm Cài lại trên thẻ diff.") // i18n-ignore
			}
			continue
		}
		if a.MessageID != p.MessageID { // shown with the diff it follows
			a.MessageID = p.MessageID
			_ = e.store.Actions().Update(context.WithoutCancel(ctx), a)
		}
		out = append(out, a)
		if auto && a.Status == "pending" {
			e.autoInstall(ctx, a)
		}
	}
	return out, errors.Join(failed...)
}

// depsInstalled (tests only) is told each time a background install ends.
var depsInstalled func(storage.Action)

// autoInstall carries out a merged diff's install the project lets run on
// its own, in the background: approving the diff (a person's request, a
// bot's) does not wait minutes for it, and nothing it was asked from can cut
// it half-way (Decide goes on without the caller). Its card says pending
// (being decided) until it ends, so a workflow step waits for it.
func (e *Engine) autoInstall(ctx context.Context, a storage.Action) {
	ctx = context.WithoutCancel(ctx)
	safeGo("deps-install", func() {
		done, err := e.acts.Decide(ctx, a.ID, true, "auto:"+depsAgent+" (tự cài thư viện sau khi gộp)") // i18n-ignore
		if err != nil {
			slog.Warn("chat: deps install", "action", a.ID, "err", err)
		}
		if depsInstalled != nil {
			depsInstalled(done)
		}
	})
}
