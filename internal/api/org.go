package api

import (
	"context"
	"errors"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/perm"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/storage"
)

func (s *server) orgRoutes(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.Handler { return s.requireAuth(h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.requireRole(storage.RoleAdmin, h) }

	mux.Handle("GET /api/system", auth(s.system))
	mux.Handle("GET /api/system/stats", admin(s.systemStats))
	mux.Handle("GET /api/projects/{id}/burn", admin(s.getBurn))
	mux.Handle("PUT /api/projects/{id}/burn", admin(s.saveBurn))
	mux.Handle("POST /api/projects/{id}/burn/start", admin(s.startBurn))
	mux.Handle("POST /api/projects/{id}/burn/stop", admin(s.stopBurn))
	mux.Handle("POST /api/burn-items/{item}/{action}", admin(s.burnItemAction))
	mux.Handle("GET /api/system/summary", admin(s.systemSummary))
	mux.Handle("POST /api/events/topics", auth(s.eventTopic))
	mux.Handle("POST /api/system/processes/{pid}/stop", admin(s.stopProcess(false)))
	mux.Handle("POST /api/system/processes/{pid}/kill", admin(s.stopProcess(true)))

	mux.Handle("GET /api/provider-kinds", auth(s.providerKinds))
	mux.Handle("GET /api/providers", auth(s.listProviders))
	mux.Handle("GET /api/providers/stats", auth(s.providerStats))
	mux.Handle("GET /api/providers/limits", auth(s.providerLimits))
	mux.Handle("GET /api/assistant", auth(s.assistantInfo))
	mux.Handle("PUT /api/assistant/mode", admin(s.setAssistantMode))
	mux.Handle("GET /api/me/tokens", auth(s.listTokens))
	mux.Handle("POST /api/me/tokens", auth(s.createToken))
	mux.Handle("DELETE /api/me/tokens/{id}", auth(s.revokeToken))
	mux.Handle("GET /api/actions/pending", admin(s.pendingActions))
	mux.Handle("GET /api/projects/{id}/channels", admin(s.listChannels))
	mux.Handle("POST /api/projects/{id}/channels", admin(s.createChannel))
	mux.Handle("PATCH /api/channels/{id}", admin(s.updateChannel))
	mux.Handle("DELETE /api/channels/{id}", admin(s.deleteChannel))
	mux.Handle("POST /api/providers", admin(s.createProvider))
	mux.Handle("PATCH /api/providers/{id}", admin(s.updateProvider))
	mux.Handle("DELETE /api/providers/{id}", admin(s.deleteProvider))
	mux.Handle("POST /api/providers/{id}/default", admin(s.defaultProvider))
	mux.Handle("POST /api/providers/{id}/test", admin(s.testProvider))
	mux.Handle("GET /api/limit-alert", admin(s.getLimitAlert))
	mux.Handle("PUT /api/limit-alert", admin(s.setLimitAlert))
	mux.Handle("POST /api/limit-alert/test", admin(s.testLimitAlert))

	mux.Handle("GET /api/templates", auth(s.listTemplates))
	mux.Handle("POST /api/templates", admin(s.createTemplate))
	mux.Handle("POST /api/templates/validate", auth(s.validateTemplate))
	mux.Handle("POST /api/templates/{key}/reset", admin(s.resetTemplate))
	mux.Handle("GET /api/org-models/{id}", auth(s.getOrgModel))
	mux.Handle("GET /api/org-models/{id}/export", auth(s.exportOrgModel))
	mux.Handle("PATCH /api/org-models/{id}", admin(s.updateOrgModel))
	mux.Handle("DELETE /api/org-models/{id}", admin(s.deleteOrgModel))
	mux.Handle("POST /api/org-models/{id}/agents", admin(s.createAgent))
	mux.Handle("GET /api/org-models/{id}/revisions", auth(s.listRevisions))
	mux.Handle("GET /api/revisions/{id}", auth(s.getRevision))
	mux.Handle("POST /api/revisions/{id}/restore", admin(s.restoreRevision))
	mux.Handle("GET /api/agents/{id}", auth(s.getAgent))
	mux.Handle("GET /api/events", auth(s.events))
	if s.cfg.Memory != nil {
		mux.Handle("GET /api/projects/{id}/agents/{aid}/memories", auth(s.listMemories))
		mux.Handle("POST /api/projects/{id}/agents/{aid}/memories", admin(s.addMemory))
		mux.Handle("POST /api/projects/{id}/agents/{aid}/memories/compact", admin(s.compactMemories))
		mux.Handle("PATCH /api/memories/{id}", admin(s.editMemory))
		mux.Handle("DELETE /api/memories/{id}", admin(s.deleteMemory))
		mux.Handle("POST /api/memory-revisions/{id}/restore", admin(s.restoreMemories))
		mux.Handle("PUT /api/projects/{id}/memory-settings", admin(s.memorySettings))
	}
	mux.Handle("GET /api/agents/{id}/stats", auth(s.agentStats))
	mux.Handle("GET /api/agents/{id}/activity", auth(s.agentActivity))
	mux.Handle("GET /api/agents/{id}/history", auth(s.agentHistory))
	mux.Handle("POST /api/agents/{id}/restore", admin(s.restoreAgent))
	mux.Handle("PATCH /api/agents/{id}", admin(s.updateAgent))
	mux.Handle("PATCH /api/agents/{id}/enabled", admin(s.setAgentEnabled))
	mux.Handle("DELETE /api/agents/{id}", admin(s.deleteAgent))

	mux.Handle("GET /api/projects", auth(s.listRepos))
	mux.Handle("POST /api/projects", admin(s.createRepo))
	mux.Handle("POST /api/projects/clone", admin(s.cloneRepo))
	mux.Handle("GET /api/projects/{id}", auth(s.getRepo))
	mux.Handle("PATCH /api/projects/{id}", admin(s.updateRepo))
	mux.Handle("DELETE /api/projects/{id}", admin(s.deleteRepo))
	mux.Handle("POST /api/projects/{id}/model", admin(s.applyRepoModel))

	mux.Handle("GET /api/fs/dirs", admin(s.listDirs))
	s.fileRoutes(mux, admin)

	if s.cfg.Chat != nil {
		mux.Handle("GET /api/projects/{id}/chat/agents", auth(s.chatAgents))
		mux.Handle("GET /api/projects/{id}/conversations", auth(s.listConversations))
		mux.Handle("GET /api/conversations/recent", auth(s.recentConversations)) // the overview
		mux.Handle("POST /api/projects/{id}/conversations", auth(s.createConversation))
		mux.Handle("GET /api/projects/{id}/skills", auth(s.chatSkills))
		mux.Handle("POST /api/projects/{id}/attachments", auth(s.uploadAttachment))
		mux.Handle("GET /api/attachments/{id}", auth(s.getAttachment))
		mux.Handle("GET /api/conversations/{id}", auth(s.ownChat(s.getConversation)))
		mux.Handle("DELETE /api/conversations/{id}", auth(s.ownChat(s.deleteConversation)))
		mux.Handle("POST /api/conversations/{id}/messages", auth(s.ownChat(s.sendMessage)))
		mux.Handle("GET /api/chat/turns/{id}/stream", auth(s.ownTurn(s.streamTurn)))
		mux.Handle("POST /api/chat/turns/{id}/cancel", auth(s.ownTurn(s.cancelTurn)))
		mux.Handle("POST /api/conversations/{id}/seen", auth(s.ownChat(s.markSeen)))
		mux.Handle("PUT /api/conversations/{id}/tags", auth(s.ownChat(s.setTags)))
		mux.Handle("GET /api/projects/{id}/chat-tags", auth(s.projectTags))
		mux.Handle("POST /api/conversations/{id}/stop", auth(s.ownChat(s.stopConversation)))
		mux.Handle("GET /api/patches/{id}", auth(s.getPatch))
		mux.Handle("POST /api/patches/{id}/approve", admin(s.approvePatch))
		mux.Handle("POST /api/patches/{id}/reject", admin(s.rejectPatch))
		mux.Handle("POST /api/proposals/skip-all", admin(s.skipAllProposals))
	}
	if s.cfg.Automation != nil {
		s.automationRoutes(mux, admin)
	}
	if s.cfg.Ops != nil {
		s.opsRoutes(mux, auth, admin)
	}
	if s.cfg.Monitors != nil {
		s.monitorRoutes(mux, auth, admin)
	}
	mux.Handle("GET /api/permission-levels", auth(s.permissionLevels))
	mux.Handle("GET /api/projects/{id}/policy", auth(s.getPolicy))
	mux.Handle("PUT /api/projects/{id}/policy", admin(s.putPolicy))
	if s.cfg.Actions != nil {
		s.gitRoutes(mux, auth, admin)
		mux.Handle("POST /api/actions/{id}/approve", admin(s.decideAction(true)))
		mux.Handle("POST /api/actions/{id}/reject", admin(s.decideAction(false)))
		mux.Handle("GET /api/actions/{id}/always", admin(s.alwaysPreview))
	}
	mux.Handle("GET /api/system/update", admin(s.updateStatus))
	if s.cfg.Updater != nil {
		mux.Handle("POST /api/system/update", admin(s.startUpdate))
		mux.Handle("GET /api/system/update/stream", admin(s.streamUpdate))
		mux.Handle("GET /api/system/update/changes", admin(s.updateChanges))
	}
	if s.cfg.MCP != nil {
		mux.Handle("/mcp", s.cfg.MCP) // authenticated by its own per-run token
	}
	if s.cfg.Gateway != nil {
		s.mcpServerRoutes(mux, admin)
	}
	if s.cfg.Cleanup != nil {
		s.dataRoutes(mux, admin)
	}
	mux.Handle("GET /api/cli-tools", admin(s.cliTools))
	if s.cfg.CLITools != nil {
		mux.Handle("GET /api/cli-tools/{id}", admin(s.cliTool))
		mux.Handle("POST /api/cli-tools/{id}/install", admin(s.cliInstall))
		mux.Handle("POST /api/cli-tools/{id}/login", admin(s.cliLogin))
		mux.Handle("GET /api/cli-jobs/{id}", admin(s.cliJob))
		mux.Handle("POST /api/cli-jobs/{id}/input", admin(s.cliJobInput))
		mux.Handle("POST /api/cli-jobs/{id}/cancel", admin(s.cliJobCancel))
	}
	if s.cfg.Usage != nil {
		mux.Handle("GET /api/usage/summary", auth(s.usageSummary))
		mux.Handle("GET /api/usage/runs", auth(s.usageRuns))
		mux.Handle("PUT /api/usage/settings", admin(s.usageSettings))
		mux.Handle("GET /api/projects/{id}/budget", auth(s.projectBudget))
		mux.Handle("PUT /api/projects/{id}/budget", admin(s.setProjectBudget))
	}
	if s.cfg.Transfer != nil {
		mux.Handle("GET /api/transfer/export", admin(s.transferExport))
		mux.Handle("POST /api/transfer/import", admin(s.transferImport))
	}
	if s.cfg.Backup != nil {
		mux.Handle("POST /api/transfer/backup", admin(s.transferBackup))
	}
	if s.cfg.Setup != nil {
		mux.Handle("POST /api/projects/{id}/setup/scan", admin(s.setupScan))
		mux.Handle("POST /api/projects/{id}/setup/propose", admin(s.setupPropose))
		mux.Handle("POST /api/projects/{id}/setup/build", admin(s.setupBuild))
		mux.Handle("POST /api/projects/{id}/setup/apply", admin(s.setupApply))
	}
}

// writeDomainError maps service errors to HTTP statuses.
func (s *server) writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *orgmodel.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Mô hình không hợp lệ", "problems": ve.Problems})
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "Không tìm thấy")
	case errors.Is(err, storage.ErrConflict), errors.Is(err, provider.ErrNameTaken), errors.Is(err, orgmodel.ErrHasInstance):
		writeError(w, http.StatusConflict, conflictMsg(err))
	case errors.Is(err, provider.ErrInvalidKind), errors.Is(err, provider.ErrNeedsKey), errors.Is(err, provider.ErrBadTier),
		errors.Is(err, provider.ErrKeyRequiredForNewURL),
		errors.Is(err, orgmodel.ErrNotTemplate), errors.Is(err, orgmodel.ErrBuiltinReset), errors.Is(err, errBadInput):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.internal(w, r, err)
	}
}

var errBadInput = errors.New("dữ liệu không hợp lệ")

func conflictMsg(err error) string {
	switch {
	case errors.Is(err, orgmodel.ErrHasInstance):
		return "Project đã có mô hình, chọn thay thế để áp mô hình mới"
	case errors.Is(err, provider.ErrNameTaken):
		return err.Error()
	}
	return "Dữ liệu bị trùng (key đã tồn tại, hoặc thư mục đã được thêm làm project)"
}

func (s *server) system(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		SystemInfo
		CloneRoot string `json:"clone_root"` // where "clone a repo" puts it by default
	}{s.cfg.System, s.cloneRoot()})
}

// ---- providers ----

type providerDTO struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Preset       string            `json:"preset"`
	BaseURL      string            `json:"base_url"`
	HasAPIKey    bool              `json:"has_api_key"`
	APIKeyHint   string            `json:"api_key_hint"`
	APIKeyEnv    string            `json:"api_key_env"`
	TierModels   map[string]string `json:"tier_models"`
	Models       []string          `json:"models"`
	IsDefault    bool              `json:"is_default"`
	Enabled      bool              `json:"enabled"`
	Status       string            `json:"status"`
	StatusDetail string            `json:"status_detail"`
	CheckedAt    *time.Time        `json:"checked_at"`
}

func toProviderDTO(p storage.Provider) providerDTO {
	return providerDTO{ID: p.ID, Name: p.Name, Kind: string(p.Kind), Preset: p.Preset, BaseURL: p.BaseURL, HasAPIKey: p.APIKeyEnc != "",
		APIKeyHint: p.APIKeyHint, APIKeyEnv: p.APIKeyEnv, TierModels: p.TierModels, Models: p.Models, IsDefault: p.IsDefault,
		Enabled: p.Enabled, Status: p.Status, StatusDetail: p.StatusDetail, CheckedAt: p.CheckedAt}
}

type kindInfo struct {
	Kind        string            `json:"kind"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Common      bool              `json:"common"` // shown as a card; the rest go under "Khác"
	NeedsKey    bool              `json:"needs_key"`
	IsCLI       bool              `json:"is_cli"`
	BaseURLHint string            `json:"base_url_hint"`
	TierModels  map[string]string `json:"tier_models"`
	Detected    *detected         `json:"detected,omitempty"`
}

// detected is what this machine already has for a kind.
type detected struct {
	Installed bool   `json:"installed,omitempty"` // CLI found in PATH
	Version   string `json:"version,omitempty"`
	EnvKey    string `json:"env_key,omitempty"` // API key env var that is set (name only)
}

func detectCLI(bin string) *detected {
	path, err := exec.LookPath(bin)
	if err != nil {
		return &detected{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--version").Output()
	return &detected{Installed: true, Version: strings.TrimSpace(string(out))}
}

// detectTool asks the CLI manager (same lookup as install/login) when enabled.
func (s *server) detectTool(r *http.Request, id string) *detected {
	if s.cfg.CLITools != nil {
		if st, err := s.cfg.CLITools.Status(r.Context(), id); err == nil {
			return &detected{Installed: st.Installed, Version: st.Version}
		}
	}
	return detectCLI(id)
}

func detectEnv(name string) *detected {
	if os.Getenv(name) != "" {
		return &detected{EnvKey: name}
	}
	return &detected{}
}

func (s *server) providerKinds(w http.ResponseWriter, r *http.Request) {
	k := func(kind storage.ProviderKind, label, desc, hint string, needsKey, common bool, d *detected) kindInfo {
		return kindInfo{Kind: string(kind), Label: label, Description: desc, Common: common, NeedsKey: needsKey, IsCLI: kind.IsCLI(),
			BaseURLHint: hint, TierModels: llm.DefaultTierModels(kind), Detected: d}
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": provider.Presets(), "kinds": []kindInfo{
		k(storage.ProviderClaudeCLI, "Claude Code trên máy",
			"Dùng tài khoản Claude đã đăng nhập trong Claude Code trên máy này (gói Pro/Max). Không cần API key.",
			"claude", false, true, s.detectTool(r, "claude")),
		k(storage.ProviderAnthropic, "Claude API",
			"Trả tiền theo lượng dùng qua API key lấy tại console.anthropic.com.",
			"https://api.anthropic.com", true, true, detectEnv("ANTHROPIC_API_KEY")),
		k(storage.ProviderOpenAI, "OpenAI (GPT) API",
			"Dùng GPT qua API key lấy tại platform.openai.com.",
			"https://api.openai.com/v1", true, true, detectEnv("OPENAI_API_KEY")),
		k(storage.ProviderCodexCLI, "Codex trên máy",
			"Dùng tài khoản ChatGPT đã đăng nhập trong Codex CLI trên máy này.",
			"codex", false, false, s.detectTool(r, "codex")),
		k(storage.ProviderOpenAICompatible, "API tương thích OpenAI",
			"Model chạy local hoặc cổng trung gian: Ollama, LM Studio, vLLM, OpenRouter, LiteLLM…",
			"http://localhost:11434/v1", false, false, nil),
	}})
}

type providerInput struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Preset     *string           `json:"preset"`
	BaseURL    string            `json:"base_url"`
	APIKey     *string           `json:"api_key"`
	APIKeyEnv  string            `json:"api_key_env"`
	TierModels map[string]string `json:"tier_models"`
	Enabled    *bool             `json:"enabled"`
}

func (in providerInput) toInput() provider.Input {
	return provider.Input{Name: in.Name, Kind: storage.ProviderKind(in.Kind), Preset: in.Preset, BaseURL: in.BaseURL, APIKey: in.APIKey,
		APIKeyEnv: in.APIKeyEnv, TierModels: in.TierModels, Enabled: in.Enabled}
}

func (s *server) listProviders(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Providers().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]providerDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toProviderDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *server) createProvider(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.cfg.Providers.Create(r.Context(), in.toInput())
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "provider.create", ResourceID: p.ID, After: toProviderDTO(p), Detail: map[string]any{"name": p.Name, "kind": string(p.Kind)}})
	writeJSON(w, http.StatusCreated, map[string]any{"provider": toProviderDTO(p)})
}

func (s *server) updateProvider(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if !decode(w, r, &in) {
		return
	}
	old, err := s.cfg.Store.Providers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	p, err := s.cfg.Providers.Update(r.Context(), r.PathValue("id"), in.toInput())
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "provider.update", ResourceID: p.ID, Before: toProviderDTO(old), After: toProviderDTO(p),
		Detail: map[string]any{"name": p.Name, "key_changed": in.APIKey != nil}})
	writeJSON(w, http.StatusOK, map[string]any{"provider": toProviderDTO(p)})
}

func (s *server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, err := s.cfg.Store.Providers().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Providers().Delete(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "provider.delete", ResourceID: id, Before: toProviderDTO(old)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) defaultProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.Store.Providers().SetDefault(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "provider.set_default", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) testProvider(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	res, err := s.cfg.Providers.Test(r.Context(), r.PathValue("id"), strings.TrimSpace(in.Prompt), in.Model)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "provider.test", r.PathValue("id"), map[string]any{"ok": res.OK, "prompt": in.Prompt != ""})
	writeJSON(w, http.StatusOK, res)
}

// ---- org models ----

type agentDTO struct {
	ID           string              `json:"id"`
	OrgModelID   string              `json:"org_model_id"`
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Tier         string              `json:"tier"`
	Role         string              `json:"role"`
	Description  string              `json:"description"`
	ReportsTo    []string            `json:"reports_to"`
	ProviderID   string              `json:"provider_id"`
	Fallbacks    []string            `json:"fallback_provider_ids"` // tried next, top to bottom
	ModelTier    string              `json:"model_tier"`
	LLMModel     string              `json:"llm_model"`
	Effort       string              `json:"effort"` // thinking level ("" = the CLI's own)
	Instructions string              `json:"instructions"`
	Permissions  storage.Permissions `json:"permissions"`
	Avatar       storage.Avatar      `json:"avatar"`
	Sort         int                 `json:"sort"`
	Enabled      bool                `json:"enabled"` // false = paused (PATCH /api/agents/{id}/enabled)
	Version      string              `json:"version"` // what an edit is made from (ADR-072)
}

func toAgentDTO(a storage.Agent) agentDTO {
	rt := a.ReportsTo
	if rt == nil {
		rt = []string{}
	}
	fb := a.FallbackProviderIDs
	if fb == nil {
		fb = []string{}
	}
	d := agentDTO{ID: a.ID, OrgModelID: a.OrgModelID, Key: a.Key, Name: a.Name, Tier: a.Tier, Role: a.Role,
		Description: a.Description, ReportsTo: rt, ProviderID: a.ProviderID, Fallbacks: fb, ModelTier: a.ModelTier, LLMModel: a.LLMModel,
		Effort: a.Effort, Instructions: a.Instructions, Permissions: a.Permissions, Avatar: a.Avatar, Sort: a.Sort, Enabled: !a.Disabled}
	d.Version = agentVersion(d)
	return d
}

type orgDTO struct {
	ID               string             `json:"id"`
	RepoID           string             `json:"repo_id"`
	SourceTemplateID string             `json:"source_template_id"`
	Key              string             `json:"key"`
	Name             string             `json:"name"`
	Description      string             `json:"description"`
	Kind             string             `json:"kind"`
	Governance       storage.Governance `json:"governance"`
	Builtin          bool               `json:"builtin"`
	IsTemplate       bool               `json:"is_template"`
	AgentCount       int                `json:"agent_count"`
	Tiers            map[string]int     `json:"tiers"`
	Agents           []agentDTO         `json:"agents,omitempty"`
	UpdatedAt        time.Time          `json:"updated_at"`
	Version          string             `json:"version"` // what an edit is made from (ADR-072)
}

func toOrgDTO(m storage.OrgModel, agents []storage.Agent, withAgents bool) orgDTO {
	d := orgDTO{ID: m.ID, RepoID: m.RepoID, SourceTemplateID: m.SourceTemplateID, Key: m.Key, Name: m.Name,
		Description: m.Description, Kind: m.Kind, Governance: m.Governance, Builtin: m.Builtin, IsTemplate: m.IsTemplate(),
		AgentCount: len(agents), Tiers: map[string]int{}, UpdatedAt: m.UpdatedAt, Version: modelVersion(m)}
	for _, a := range agents {
		d.Tiers[a.Tier]++
		if withAgents {
			d.Agents = append(d.Agents, toAgentDTO(a))
		}
	}
	if withAgents && d.Agents == nil {
		d.Agents = []agentDTO{}
	}
	return d
}

func (s *server) loadOrg(r *http.Request, id string, withAgents bool) (orgDTO, error) {
	m, err := s.cfg.Store.OrgModels().Get(r.Context(), id)
	if err != nil {
		return orgDTO{}, err
	}
	agents, err := s.cfg.Store.Agents().List(r.Context(), id)
	if err != nil {
		return orgDTO{}, err
	}
	return toOrgDTO(m, agents, withAgents), nil
}

func (s *server) listTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.OrgModels().ListTemplates(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]orgDTO, 0, len(list))
	for _, m := range list {
		agents, err := s.cfg.Store.Agents().List(r.Context(), m.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out = append(out, toOrgDTO(m, agents, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

// createTemplate clones an existing model ({source_id, key, name}) or imports
// a full template ({template: {...}}).
func (s *server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SourceID string             `json:"source_id"`
		Key      string             `json:"key"`
		Name     string             `json:"name"`
		Template *orgmodel.Template `json:"template"`
	}
	if !decode(w, r, &in) {
		return
	}
	var (
		m   storage.OrgModel
		err error
	)
	switch {
	case in.Template != nil:
		m, err = s.cfg.Org.CreateTemplate(r.Context(), *in.Template)
	case in.SourceID != "":
		m, err = s.cfg.Org.CloneTemplate(r.Context(), in.SourceID, strings.TrimSpace(in.Key), strings.TrimSpace(in.Name))
	default:
		err = errBadInput
	}
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "template.create", m.ID, map[string]any{"key": m.Key, "source": in.SourceID})
	d, _ := s.loadOrg(r, m.ID, true)
	writeJSON(w, http.StatusCreated, map[string]any{"model": d})
}

// validateTemplate checks a draft (the new-template page, as it is written):
// the problems, none when it can be saved.
func (s *server) validateTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template orgmodel.Template `json:"template"`
	}
	if !decode(w, r, &in) {
		return
	}
	problems := []string{}
	var ve *orgmodel.ValidationError
	if err := orgmodel.Validate(in.Template); errors.As(err, &ve) {
		problems = ve.Problems
	} else if err != nil {
		problems = append(problems, err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": problems})
}

func (s *server) resetTemplate(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Org.ResetBuiltin(r.Context(), r.PathValue("key"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "template.reset", m.ID, map[string]any{"key": m.Key})
	d, _ := s.loadOrg(r, m.ID, true)
	writeJSON(w, http.StatusOK, map[string]any{"model": d})
}

func (s *server) getOrgModel(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadOrg(r, r.PathValue("id"), true)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": d})
}

func (s *server) exportOrgModel(w http.ResponseWriter, r *http.Request) {
	t, err := s.cfg.Org.Load(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+t.Key+`.json"`)
	writeJSON(w, http.StatusOK, t)
}

func (s *server) updateOrgModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string             `json:"name"`
		Description *string             `json:"description"`
		Kind        *string             `json:"kind"`
		Key         *string             `json:"key"`
		Governance  *storage.Governance `json:"governance"`
		Version     string              `json:"version"` // the model as it was read (409 when changed since)
	}
	if !decode(w, r, &in) {
		return
	}
	old, err := s.cfg.Store.OrgModels().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, in.Version, modelVersion(old)) {
		return
	}
	m, err := s.cfg.Org.UpdateModel(r.Context(), r.PathValue("id"), func(m *storage.OrgModel) {
		if in.Name != nil {
			m.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			m.Description = *in.Description
		}
		if in.Kind != nil {
			m.Kind = *in.Kind
		}
		if in.Key != nil && m.IsTemplate() && !m.Builtin {
			m.Key = strings.TrimSpace(*in.Key)
		}
		if in.Governance != nil {
			m.Governance = *in.Governance
		}
	})
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "org_model.update", ResourceID: m.ID, ProjectID: m.RepoID, Before: modelSnapshot(old), After: modelSnapshot(m)})
	d, _ := s.loadOrg(r, m.ID, true)
	writeJSON(w, http.StatusOK, map[string]any{"model": d})
}

func (s *server) deleteOrgModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.cfg.Store.OrgModels().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if m.Builtin {
		writeError(w, http.StatusBadRequest, "Không xóa mô hình có sẵn; dùng Khôi phục mặc định để hoàn tác chỉnh sửa")
		return
	}
	if err := s.cfg.Store.OrgModels().Delete(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "org_model.delete", ResourceID: id, ProjectID: m.RepoID, Before: modelSnapshot(m), Detail: map[string]any{"key": m.Key}})
	w.WriteHeader(http.StatusNoContent)
}

type agentInput struct {
	Version      string              `json:"version"` // the agent as it was read (409 when changed since)
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Tier         string              `json:"tier"`
	Role         string              `json:"role"`
	Description  string              `json:"description"`
	ReportsTo    []string            `json:"reports_to"`
	ProviderID   string              `json:"provider_id"`
	Fallbacks    *[]string           `json:"fallback_provider_ids"` // nil = keep
	ModelTier    string              `json:"model_tier"`
	LLMModel     string              `json:"llm_model"`
	Effort       *string             `json:"effort"` // nil = keep; "" = the CLI's own
	Instructions string              `json:"instructions"`
	Permissions  storage.Permissions `json:"permissions"`
	Avatar       *storage.Avatar     `json:"avatar"` // nil = keep
	Sort         *int                `json:"sort"`
	Enabled      *bool               `json:"enabled"` // nil = keep; false = paused
}

// applyAgent validates in and puts it on a. FullAccess/ExtraDirs are admin
// only (ADR-074): a non-admin save keeps whatever a already had for them.
func (s *server) applyAgent(r *http.Request, in agentInput, a *storage.Agent) error {
	isAdmin := userFrom(r).Role == storage.RoleAdmin
	keepFullAccess, keepFullAccessBy, keepExtraDirs := a.Permissions.FullAccess, a.Permissions.FullAccessBy, a.Permissions.ExtraDirs
	a.Key, a.Name, a.Tier, a.Role = strings.TrimSpace(in.Key), strings.TrimSpace(in.Name), in.Tier, in.Role
	a.Description, a.ReportsTo, a.ProviderID = in.Description, in.ReportsTo, in.ProviderID
	a.ModelTier, a.LLMModel, a.Instructions, a.Permissions = in.ModelTier, strings.TrimSpace(in.LLMModel), in.Instructions, in.Permissions
	if a.Permissions.Caps != nil { // own picks: only known capabilities
		caps := []string{}
		for _, c := range perm.Caps {
			if slices.Contains(*a.Permissions.Caps, c.ID) {
				caps = append(caps, c.ID)
			}
		}
		a.Permissions.Caps = &caps
	}
	if !perm.Valid(a.Permissions.Level) {
		a.Permissions.Level = perm.Agent(*a)
	}
	a.Permissions.ReadOnly = perm.Agent(*a) == perm.Read // the legacy flag follows
	if isAdmin {
		if a.Permissions.FullAccess {
			a.Permissions.FullAccessBy = userFrom(r).Email
		} else {
			a.Permissions.FullAccessBy = ""
		}
		projectPath := s.orgModelProjectPath(r.Context(), a.OrgModelID)
		for _, dir := range a.Permissions.ExtraDirs {
			if dir = strings.TrimSpace(dir); dir == "" {
				continue
			}
			if err := perm.CheckExtraDir(projectPath, dir); err != nil {
				return err
			}
		}
	} else { // not admin: these fields stay as they were
		a.Permissions.FullAccess, a.Permissions.FullAccessBy, a.Permissions.ExtraDirs = keepFullAccess, keepFullAccessBy, keepExtraDirs
	}
	if in.Sort != nil {
		a.Sort = *in.Sort
	}
	if in.Avatar != nil {
		a.Avatar = *in.Avatar
	}
	if in.Fallbacks != nil {
		a.FallbackProviderIDs = []string{}
		for _, id := range *in.Fallbacks {
			if id = strings.TrimSpace(id); id != "" && id != a.ProviderID && !slices.Contains(a.FallbackProviderIDs, id) {
				a.FallbackProviderIDs = append(a.FallbackProviderIDs, id)
			}
		}
	}
	if in.Effort != nil {
		if !storage.ValidEffort(*in.Effort) {
			return errors.New("mức suy nghĩ phải là low, medium, high, xhigh hoặc max")
		}
		a.Effort = *in.Effort
	}
	if a.ModelTier == "" {
		a.ModelTier = storage.TierBalanced
	}
	return nil
}

// checkProvider: the agent's connection and its fallbacks all exist.
func (s *server) checkProvider(r *http.Request, id string, fallbacks *[]string) error {
	ids := []string{id}
	if fallbacks != nil {
		ids = append(ids, *fallbacks...)
	}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		_, err := s.cfg.Store.Providers().Get(r.Context(), id)
		if errors.Is(err, storage.ErrNotFound) {
			return errBadInput
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// auditAgentFullAccess logs a dedicated entry when an agent's FullAccess or
// ExtraDirs changed (ADR-074): easy to spot in Nhật ký, besides the full
// before/after already on agent.create/agent.update.
func (s *server) auditAgentFullAccess(r *http.Request, old, a storage.Agent) {
	op, np := old.Permissions, a.Permissions
	if op.FullAccess == np.FullAccess && op.FullAccessBy == np.FullAccessBy && slices.Equal(op.ExtraDirs, np.ExtraDirs) {
		return
	}
	s.audit(r, audit.Change{Action: "agent.full_access", ResourceID: a.ID, ProjectID: s.agentProject(r.Context(), a),
		Before: map[string]any{"full_access": op.FullAccess, "full_access_by": op.FullAccessBy, "extra_dirs": op.ExtraDirs},
		After:  map[string]any{"full_access": np.FullAccess, "full_access_by": np.FullAccessBy, "extra_dirs": np.ExtraDirs},
		Detail: map[string]any{"key": a.Key}})
}

func (s *server) createAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if !decode(w, r, &in) {
		return
	}
	if err := checkAvatar(in.Avatar); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.checkProvider(r, in.ProviderID, in.Fallbacks); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	a := storage.Agent{OrgModelID: r.PathValue("id")}
	if err := s.applyAgent(r, in, &a); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.cfg.Org.SaveAgent(r.Context(), a)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if in.Enabled != nil && !*in.Enabled {
		if err := s.cfg.Store.Agents().SetEnabled(r.Context(), a.ID, false); err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		a.Disabled = true
	}
	s.audit(r, audit.Change{Action: "agent.create", ResourceID: a.ID, ProjectID: s.agentProject(r.Context(), a), After: toAgentDTO(a),
		Detail: map[string]any{"key": a.Key, "org_model": a.OrgModelID}})
	s.auditAgentFullAccess(r, storage.Agent{}, a)
	writeJSON(w, http.StatusCreated, map[string]any{"agent": toAgentDTO(a)})
}

func (s *server) updateAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if !decode(w, r, &in) {
		return
	}
	if err := checkAvatar(in.Avatar); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.checkProvider(r, in.ProviderID, in.Fallbacks); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	a, err := s.cfg.Store.Agents().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, in.Version, agentVersion(toAgentDTO(a))) {
		return
	}
	old := a
	if err := s.applyAgent(r, in, &a); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err = s.cfg.Org.SaveAgent(r.Context(), a)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if in.Enabled != nil && *in.Enabled == a.Disabled { // changed (propose_change sends the whole agent)
		if err := s.cfg.Store.Agents().SetEnabled(r.Context(), a.ID, *in.Enabled); err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		a.Disabled = !*in.Enabled
	}
	s.audit(r, audit.Change{Action: "agent.update", ResourceID: a.ID, ProjectID: s.agentProject(r.Context(), a),
		Before: toAgentDTO(old), After: toAgentDTO(a), Detail: map[string]any{"key": a.Key}})
	s.auditAgentFullAccess(r, old, a)
	writeJSON(w, http.StatusOK, map[string]any{"agent": toAgentDTO(a)})
}

// setAgentEnabled is the switch on the agent list: {enabled} pauses or
// resumes an agent (a paused one is left out of chat and gets no work).
func (s *server) setAgentEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeError(w, http.StatusBadRequest, "thiếu enabled")
		return
	}
	a, err := s.cfg.Store.Agents().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if a.Disabled != *in.Enabled { // already so
		writeJSON(w, http.StatusOK, map[string]any{"agent": toAgentDTO(a)})
		return
	}
	if err := s.cfg.Store.Agents().SetEnabled(r.Context(), a.ID, *in.Enabled); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "agent.enabled", ResourceID: a.ID, ProjectID: s.agentProject(r.Context(), a),
		Before: map[string]any{"enabled": !a.Disabled}, After: map[string]any{"enabled": *in.Enabled}, Detail: map[string]any{"key": a.Key}})
	a.Disabled = !*in.Enabled
	writeJSON(w, http.StatusOK, map[string]any{"agent": toAgentDTO(a)})
}

func (s *server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, err := s.cfg.Store.Agents().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	project := s.agentProject(r.Context(), old)
	if err := s.cfg.Org.DeleteAgent(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "agent.delete", ResourceID: id, ProjectID: project, Before: toAgentDTO(old)})
	w.WriteHeader(http.StatusNoContent)
}

// ---- repos ----

type repoDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	GitRemote   string    `json:"git_remote"`
	Description string    `json:"description"`
	Scope       string    `json:"scope"` // folder | machine (no path: helper for the whole machine)
	Exists      bool      `json:"exists"`
	Model       *orgDTO   `json:"model"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *server) repoDTO(r *http.Request, x storage.Repo, withAgents bool) (repoDTO, error) {
	d := repoDTO{ID: x.ID, Name: x.Name, Path: x.Path, GitRemote: x.GitRemote, Description: x.Description,
		Scope: "machine", Exists: true, CreatedAt: x.CreatedAt}
	if x.Path != "" {
		_, statErr := os.Stat(x.Path)
		d.Scope, d.Exists = "folder", statErr == nil
	}
	m, err := s.cfg.Store.OrgModels().GetForRepo(r.Context(), x.ID)
	if errors.Is(err, storage.ErrNotFound) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	od, err := s.loadOrg(r, m.ID, withAgents)
	if err != nil {
		return d, err
	}
	d.Model = &od
	return d, nil
}

func (s *server) listRepos(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Repos().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]repoDTO, 0, len(list))
	hidden := assistant.ID(r.Context(), s.cfg.Store) // the office assistant's own (ADR-046)
	for _, x := range list {
		if x.ID == hidden {
			continue
		}
		d, err := s.repoDTO(r, x, false)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *server) createRepo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path        string `json:"path"`
		Name        string `json:"name"`
		Description string `json:"description"`
		TemplateID  string `json:"template_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	// No path: a machine-wide helper that is not tied to one folder.
	var info repos.Info
	if p := strings.TrimSpace(in.Path); p != "" {
		var err error
		if info, err = repos.Detect(p); err != nil {
			writeError(w, http.StatusBadRequest, "Đường dẫn không tồn tại hoặc không phải thư mục trên máy chạy office")
			return
		}
	} else if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "Project không có thư mục cần đặt tên")
		return
	}
	s.registerRepo(w, r, info, in.Name, in.Description, in.TemplateID, nil)
}

// registerRepo adds a detected folder as a project (with its model, if one is
// picked). It reports false when it wrote an error (nothing was registered).
func (s *server) registerRepo(w http.ResponseWriter, r *http.Request, info repos.Info, name, desc, templateID string, extra map[string]any) bool {
	if name = strings.TrimSpace(name); name != "" {
		info.Name = name
	}
	if desc != "" {
		info.Description = desc
	}
	x, err := s.cfg.Store.Repos().Create(r.Context(), storage.Repo{Name: info.Name, Path: info.Path, GitRemote: info.GitRemote, Description: info.Description})
	if err != nil {
		s.writeDomainError(w, r, err)
		return false
	}
	if templateID != "" {
		if _, err := s.cfg.Org.ApplyToRepo(r.Context(), x.ID, templateID, false); err != nil {
			_ = s.cfg.Store.Repos().Delete(r.Context(), x.ID)
			s.writeDomainError(w, r, err)
			return false
		}
	}
	detail := map[string]any{"path": x.Path, "template": templateID}
	for k, v := range extra {
		detail[k] = v
	}
	s.auditAction(r, "project.create", x.ID, detail)
	d, _ := s.repoDTO(r, x, true)
	writeJSON(w, http.StatusCreated, map[string]any{"project": d})
	return true
}

// cloneRepo clones a pasted git link into a folder on this machine (by
// default next to the folder holding this office) and adds it as a project.
func (s *server) cloneRepo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL        string `json:"url"`
		Parent     string `json:"parent"`
		Dir        string `json:"dir"`
		Name       string `json:"name"`
		TemplateID string `json:"template_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	parent := strings.TrimSpace(in.Parent)
	if parent == "" {
		parent = s.cloneRoot()
	}
	dest, err := repos.Clone(r.Context(), in.URL, parent, strings.TrimSpace(in.Dir))
	var ce *repos.CloneError
	switch {
	case errors.Is(err, repos.ErrCloneURL):
		writeError(w, http.StatusBadRequest, "Link repo không hợp lệ: dùng https://…, ssh://… hoặc git@host:đường/dẫn.git")
		return
	case errors.Is(err, repos.ErrCloneDir):
		writeError(w, http.StatusBadRequest, "Tên thư mục chỉ gồm chữ, số, dấu chấm, gạch ngang, gạch dưới")
		return
	case errors.Is(err, repos.ErrCloneExists):
		writeError(w, http.StatusConflict, "Thư mục đích đã tồn tại: đổi tên thư mục hoặc nơi lưu")
		return
	case errors.Is(err, os.ErrNotExist), errors.Is(err, repos.ErrNotDir):
		writeError(w, http.StatusBadRequest, "Nơi lưu không tồn tại hoặc không phải thư mục")
		return
	case errors.Is(err, os.ErrPermission):
		writeError(w, http.StatusForbidden, "Không có quyền ghi vào nơi lưu")
		return
	case errors.As(err, &ce):
		writeError(w, http.StatusBadGateway, ce.Error())
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	info, err := repos.Detect(dest)
	if err == nil && s.registerRepo(w, r, info, in.Name, "", in.TemplateID, map[string]any{"cloned_from": repos.StripCredentials(strings.TrimSpace(in.URL))}) {
		return
	}
	_ = os.RemoveAll(dest) // not registered: the clone does not stay behind
	if err != nil {
		s.internal(w, r, err)
	}
}

// cloneRoot is where a cloned repo goes by default: next to the folder that
// holds this office (its project), else the home folder.
func (s *server) cloneRoot() string {
	if s.cfg.System.ProjectRoot != "" {
		return filepath.Dir(s.cfg.System.ProjectRoot)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func (s *server) getRepo(w http.ResponseWriter, r *http.Request) {
	x, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	d, err := s.repoDTO(r, x, true)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": d})
}

func (s *server) updateRepo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	x, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	old := x
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		x.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		x.Description = *in.Description
	}
	if err := s.cfg.Store.Repos().Update(r.Context(), x); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "project.update", ResourceID: x.ID, ProjectID: x.ID, Before: repoSnapshot(old), After: repoSnapshot(x)})
	d, _ := s.repoDTO(r, x, true)
	writeJSON(w, http.StatusOK, map[string]any{"project": d})
}

func (s *server) deleteRepo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, err := s.cfg.Store.Repos().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Repos().Delete(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "project.delete", ResourceID: id, ProjectID: id, Before: repoSnapshot(old)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) applyRepoModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TemplateID string `json:"template_id"`
		Replace    bool   `json:"replace"`
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := s.cfg.Org.ApplyToRepo(r.Context(), r.PathValue("id"), in.TemplateID, in.Replace)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "project.apply_model", r.PathValue("id"), map[string]any{"template": in.TemplateID, "model": m.ID, "replace": in.Replace})
	x, _ := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	d, _ := s.repoDTO(r, x, true)
	writeJSON(w, http.StatusOK, map[string]any{"project": d})
}

func (s *server) auditAction(r *http.Request, action, target string, detail map[string]any) {
	s.audit(r, audit.Change{Action: action, ResourceID: target, Detail: detail})
}

// listDirs powers the folder tree in the dashboard. Browsers cannot hand a web
// page an absolute path, but the office server runs on the machine that holds
// the projects, so it lists sub-folder names (never file contents) for admins.
func (s *server) listDirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.cloneRoot()
	}
	l, err := repos.ListDirs(path, r.URL.Query().Get("hidden") == "1")
	switch {
	case errors.Is(err, os.ErrPermission):
		writeError(w, http.StatusForbidden, "Không có quyền đọc thư mục này")
		return
	case errors.Is(err, os.ErrNotExist), errors.Is(err, repos.ErrNotDir):
		writeError(w, http.StatusNotFound, "Thư mục không tồn tại")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	type entry struct {
		repos.DirEntry
		Registered bool `json:"registered"`
	}
	entries := make([]entry, 0, len(l.Entries))
	for _, e := range l.Entries {
		_, err := s.cfg.Store.Repos().GetByPath(r.Context(), e.Path)
		entries = append(entries, entry{DirEntry: e, Registered: err == nil})
	}
	var extra []repos.Shortcut
	if s.cfg.System.ProjectRoot != "" {
		extra = append(extra, repos.Shortcut{Label: "Project hiện tại", Path: s.cfg.System.ProjectRoot})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": l.Path, "parent": l.Parent, "entries": entries, "truncated": l.Truncated,
		"shortcuts": repos.Shortcuts(extra...),
	})
}
