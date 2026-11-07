package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// Saves check what they were edited from (ADR-072): an edit page reads a
// version with the data and sends it back when it saves; someone else's
// change in between answers 409 and nothing is overwritten. The version is a
// hash of what a person edits (a run's last-run time or failures are not an
// edit). A save without a version (a tool, an agent) is taken as before.

func versionOf(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// errConflict: a save over someone else's change (answered 409 by writeDomainError's callers).
var errConflict = errors.New("Đã có thay đổi mới ở nơi khác kể từ lúc bạn mở (người khác hoặc agent). Tải lại để xem bản mới rồi sửa lại.")

// conflicted answers 409 when want is given and is not now.
func conflicted(w http.ResponseWriter, want, now string) bool {
	if want == "" || want == now {
		return false
	}
	writeSaveConflict(w)
	return true
}

// writeSaveConflict answers 409 the same way conflicted does, for a save
// that lost the storage-level CAS (a write landed between the handler's Get
// and Update) rather than the version check above.
func writeSaveConflict(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]any{"code": "conflict",
		"error": "Đã có thay đổi mới ở nơi khác kể từ lúc bạn mở (người khác hoặc agent). Tải lại để xem bản mới rồi sửa lại."})
}

func automationVersion(a storage.Automation) string {
	c := a.Config
	c.ConversationID, c.SecretHash = "", "" // kept by office, not edited
	return versionOf(struct {
		Name, Source, Action, AgentID, Prompt, EditMode, ModelTier, PermissionMode string
		Enabled, KeepContext, OverrideFullAccess                                   bool
		Config                                                                     storage.AutomationConfig
		Limits                                                                     storage.AutomationLimits
		Script                                                                     storage.AutomationScript
		OverrideExtraDirs                                                          []string
	}{a.Name, a.Source, a.Action, a.AgentID, a.Prompt, a.EditMode, a.ModelTier, a.PermissionMode, a.Enabled, a.KeepContext, a.OverrideFullAccess, c, a.Limits, a.Script, a.OverrideExtraDirs})
}

func repoVersion(x storage.Repo) string {
	return versionOf(struct {
		Name, Description, GitRemote string
	}{x.Name, x.Description, x.GitRemote})
}

func agentVersion(d agentDTO) string {
	d.Version, d.Sort, d.Enabled = "", 0, false // pausing is its own switch: an open edit form stays valid
	return versionOf(d)
}

func policyVersion(p perm.Policy) string {
	return versionOf(struct {
		Packs          []perm.Pack
		DenyPaths      []string
		WorktreeLinks  []string
		QuickCheck     bool
		QuickChecks    string
		LinkedProjects []string
		Experts        *[]string
	}{p.Packs, p.DenyPaths, p.WorktreeLinks, p.QuickCheck, p.QuickChecks, p.LinkedProjects, p.Experts})
}

func workflowVersion(wf storage.Workflow) string {
	return versionOf(struct {
		Source   string
		Bindings map[string]string
	}{wf.Source, wf.Bindings})
}

// mcpServerVersion skips what office writes itself: OAuth tokens (refreshed
// by the gateway between edits), check results and timestamps.
func mcpServerVersion(m storage.MCPServer) string {
	return versionOf(struct {
		Name, Kind, URL, Command, EnvEnc, HeadersEnc, Scope string
		Enabled                                             bool
		Args, Agents, TrustedTools                          []string
	}{m.Name, m.Kind, m.URL, m.Command, m.EnvEnc, m.HeadersEnc, m.Scope, m.Enabled, m.Args, m.Agents, m.TrustedTools})
}

func monitorVersion(m storage.Monitor) string {
	return versionOf(struct {
		Name, Type, Target string
		Config             storage.MonitorConfig
		IntervalS          int
		AIEnabled          bool
		AIBudgetUSD        float64
	}{m.Name, m.Type, m.Target, m.Config, m.IntervalS, m.AIEnabled, m.AIBudgetUSD})
}

func processVersion(p storage.Process) string {
	return versionOf(struct {
		Name, Command, Cwd, Kind, Source string
		Autostart, Autorestart           bool
		Env                              map[string]string
	}{p.Name, p.Command, p.Cwd, p.Kind, p.Source, p.Autostart, p.Autorestart, p.Env})
}

func channelVersion(c storage.Channel) string {
	return versionOf(struct {
		Name, AgentID, Mode, Scope, Refusal, Approval, Header, ReplyMode, Defaults string
		Enabled, FilterEnabled                                                     bool
		Allow, Approvers                                                           []string
		Token                                                                      string
	}{c.Name, c.AgentID, c.Mode, c.Scope, c.Refusal, c.Approval, c.Header, c.ReplyMode, c.Defaults, c.Enabled, c.FilterEnabled, c.Allow, c.Approvers, c.TokenEnc})
}
