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
	writeJSON(w, http.StatusConflict, map[string]any{"code": "conflict",
		"error": "Đã có thay đổi mới ở nơi khác kể từ lúc bạn mở (người khác hoặc agent). Tải lại để xem bản mới rồi sửa lại."})
	return true
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

func agentVersion(d agentDTO) string {
	d.Version, d.Sort, d.Enabled = "", 0, false // pausing is its own switch: an open edit form stays valid
	return versionOf(d)
}

func policyVersion(p perm.Policy) string {
	return versionOf(struct {
		Packs         []perm.Pack
		DenyPaths     []string
		WorktreeLinks []string
	}{p.Packs, p.DenyPaths, p.WorktreeLinks})
}

func modelVersion(m storage.OrgModel) string {
	return versionOf(struct {
		Name, Description, Kind, Key string
		Governance                   storage.Governance
	}{m.Name, m.Description, m.Kind, m.Key, m.Governance})
}

func channelVersion(c storage.Channel) string {
	return versionOf(struct {
		Name, AgentID, Mode, Scope, Refusal, Approval, Header, ReplyMode, Defaults string
		Enabled, FilterEnabled                                                     bool
		Allow, Approvers                                                           []string
		Token                                                                      string
	}{c.Name, c.AgentID, c.Mode, c.Scope, c.Refusal, c.Approval, c.Header, c.ReplyMode, c.Defaults, c.Enabled, c.FilterEnabled, c.Allow, c.Approvers, c.TokenEnc})
}
