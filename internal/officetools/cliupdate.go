package officetools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// CLIUpdateTools gives update_cli (ADR-259): an agent that sees an AI CLI
// is out of date or broken (a newer version needed, "spawn … ENOENT") asks
// office to update it once no run uses it. Offered where request_update is
// (office's own project, the office assistant).
type CLIUpdateTools struct {
	Store   storage.Store
	Actions *actions.Service
	Root    string // office's source folder ("" = the office assistant only)
}

// Tools: update_cli.
func (u CLIUpdateTools) Tools(sc Scope) []Tool {
	if u.Actions == nil || sc.Consult || !(UpdateTools{Store: u.Store, Root: u.Root}).offered(context.Background(), sc) {
		return nil
	}
	return []Tool{{Name: "update_cli",
		Description: "Ask office to update an AI CLI (claude, codex, gemini, antigravity, dsh) once no run has it running: new runs wait only while its files change, and a version that does not start is put back. Use it when the CLI is out of date or its install is broken (e.g. spawn … ENOENT). A person approves it on a card.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"tool":   map[string]any{"type": "string", "enum": []string{"claude", "codex", "gemini", "antigravity", "dsh"}},
			"reason": map[string]any{"type": "string", "description": "Why, e.g. codex 0.167 fixes the MCP timeout; or: codex fails with spawn ENOENT"}},
			"required": []string{"tool", "reason"}}}}
}

// Call proposes the update.
func (u CLIUpdateTools) Call(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool) {
	if name != "update_cli" || len(u.Tools(sc)) == 0 {
		return "Công cụ không tồn tại: " + name, true
	}
	if !sc.Admin && !perm.AtLeast(sc.Level, perm.Propose) {
		return "Bạn không có quyền đề xuất thao tác (gói hiện tại: " + perm.Label(sc.Level) + ")", true
	}
	var in struct{ Tool, Reason string }
	_ = json.Unmarshal(raw, &in)
	a, err := u.Actions.Propose(ctx, sc, "cli_update", in.Tool, in.Reason)
	if err != nil {
		return "Lỗi: " + err.Error(), true
	}
	return fmt.Sprintf("Created proposal %q for %s (id %s): once the person approves, office updates it when no run uses it. Nothing is done yet.", actions.Kinds[a.Kind], in.Tool, a.ID), false
}
