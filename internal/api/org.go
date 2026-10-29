package api

import (
	"context"
	"errors"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/perm"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/features"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/transfer"
	"github.com/tung1998/agent-office/internal/workflow"
	"github.com/tung1998/agent-office/templates/packs"
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
	mux.Handle("POST /api/projects/{id}/burn/drain", admin(s.drainBurn))
	mux.Handle("POST /api/projects/{id}/burn/resume", admin(s.resumeBurn))
	mux.Handle("POST /api/projects/{id}/burn/quests", admin(s.addBurnQuest))
	mux.Handle("POST /api/burn-items/{item}/{action}", admin(s.burnItemAction))
	mux.Handle("GET /api/projects/{id}/burn/review-profiles", admin(s.listBurnProfiles))
	mux.Handle("POST /api/projects/{id}/burn/review-profiles", admin(s.createBurnProfile))
	mux.Handle("PUT /api/burn-review-profiles/{profile}", admin(s.updateBurnProfile))
	mux.Handle("DELETE /api/burn-review-profiles/{profile}", admin(s.deleteBurnProfile))
	mux.Handle("GET /api/system/summary", admin(s.systemSummary))
	mux.Handle("POST /api/events/topics", auth(s.eventTopic))
	mux.Handle("POST /api/events/turns", auth(s.eventTurn))
	mux.Handle("POST /api/events/logs", auth(s.eventLog))
	mux.Handle("POST /api/system/processes/{pid}/stop", admin(s.stopProcess(false)))
	mux.Handle("POST /api/system/processes/{pid}/kill", admin(s.stopProcess(true)))

	mux.Handle("GET /api/provider-kinds", auth(s.providerKinds))
	mux.Handle("GET /api/providers", auth(s.listProviders))
	mux.Handle("GET /api/providers/stats", auth(s.providerStats))
	mux.Handle("GET /api/providers/limits", auth(s.providerLimits))
	mux.Handle("GET /api/assistant", auth(s.assistantInfo))
	mux.Handle("PUT /api/assistant/mode", admin(s.setAssistantMode))
	mux.Handle("GET /api/me/prefs/{key}", auth(s.getPref))
	mux.Handle("PUT /api/me/prefs/{key}", auth(s.setPref))
	mux.Handle("GET /api/actions/pending", admin(s.pendingActions))
	mux.Handle("GET /api/bots", admin(s.listAllBots)) // every project's, to borrow one for a notice
	mux.Handle("GET /api/projects/{id}/channels", admin(s.listChannels))
	mux.Handle("POST /api/projects/{id}/channels", admin(s.createChannel))
	mux.Handle("PATCH /api/channels/{id}", admin(s.updateChannel))
	mux.Handle("DELETE /api/channels/{id}", admin(s.deleteChannel))
	mux.Handle("POST /api/providers", admin(s.createProvider))
	mux.Handle("PATCH /api/providers/{id}", admin(s.updateProvider))
	mux.Handle("DELETE /api/providers/{id}", admin(s.deleteProvider))
	mux.Handle("GET /api/providers/{id}/agents", admin(s.providerAgents))
	mux.Handle("POST /api/providers/{id}/default", admin(s.defaultProvider))
	mux.Handle("PATCH /api/providers/{id}/enabled", admin(s.setProviderEnabled))
	mux.Handle("POST /api/providers/{id}/test", admin(s.testProvider))
	mux.Handle("GET /api/language", admin(s.getLanguage))
	mux.Handle("PUT /api/language", admin(s.setLanguage))
	mux.Handle("GET /api/limit-alert", admin(s.getLimitAlert))
	mux.Handle("PUT /api/limit-alert", admin(s.setLimitAlert))
	mux.Handle("POST /api/limit-alert/test", admin(s.testLimitAlert))

	mux.Handle("GET /api/agent-library", auth(s.agentLibrary))
	mux.Handle("GET /api/agent-library/{set}/{key}", auth(s.agentLibraryItem))
	mux.Handle("POST /api/projects/{id}/agents", admin(s.createAgent))
	mux.Handle("PUT /api/projects/{id}/default-agent", admin(s.setDefaultAgent))
	mux.Handle("GET /api/projects/{id}/agents/export", auth(s.exportAgents))
	mux.Handle("GET /api/projects/{id}/revisions", auth(s.listRevisions))
	mux.Handle("GET /api/revisions/{id}", auth(s.getRevision))
	mux.Handle("POST /api/revisions/{id}/restore", admin(s.restoreRevision))
	mux.Handle("GET /api/agents/{id}", auth(s.getAgent))
	mux.Handle("GET /api/events", auth(s.events))
	mux.Handle("GET /api/projects/{id}/agents/{aid}/skills", auth(s.listAgentSkills))
	mux.Handle("POST /api/projects/{id}/agents/{aid}/skills", admin(s.addAgentSkill))
	mux.Handle("DELETE /api/projects/{id}/agents/{aid}/skills/{name}", admin(s.removeAgentSkill))
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
	mux.Handle("POST /api/projects/new", admin(s.newRepo))
	mux.Handle("GET /api/project-packs", auth(s.projectPacks))
	mux.Handle("GET /api/activity", auth(s.activityOverview)) // ADR-210
	mux.Handle("GET /api/projects/{id}/pack-sync", admin(s.packSyncPreview))
	mux.Handle("POST /api/projects/{id}/pack-sync", admin(s.packSyncApply))
	mux.Handle("GET /api/projects/{id}", auth(s.getRepo))
	mux.Handle("PATCH /api/projects/{id}", admin(s.updateRepo))
	mux.Handle("DELETE /api/projects/{id}", admin(s.deleteRepo))

	mux.Handle("GET /api/fs/dirs", admin(s.listDirs))
	s.fileRoutes(mux, admin)

	if s.cfg.Chat != nil {
		mux.Handle("GET /api/projects/{id}/chat/agents", auth(s.chatAgents))
		mux.Handle("GET /api/projects/{id}/conversations", auth(s.listConversations))
		mux.Handle("GET /api/conversations/recent", auth(s.recentConversations)) // the overview
		mux.Handle("POST /api/projects/{id}/conversations", auth(s.createConversation))
		mux.Handle("GET /api/projects/{id}/editor-chat", auth(s.editorChat))
		mux.Handle("GET /api/projects/{id}/skills", auth(s.chatSkills))
		mux.Handle("POST /api/projects/{id}/attachments", auth(s.uploadAttachment))
		mux.Handle("GET /api/attachments/{id}", auth(s.getAttachment))
		mux.Handle("GET /api/conversations/{id}", auth(s.ownChat(s.getConversation)))
		mux.Handle("DELETE /api/conversations/{id}", auth(s.ownChat(s.deleteConversation)))
		mux.Handle("POST /api/conversations/{id}/messages", auth(s.ownChat(s.sendMessage)))
		mux.Handle("GET /api/chat/turns/{id}/stream", auth(s.ownTurn(s.streamTurn)))
		mux.Handle("POST /api/chat/turns/{id}/cancel", auth(s.ownTurn(s.cancelTurn)))
		mux.Handle("POST /api/conversations/{id}/seen", auth(s.ownChat(s.markSeen)))
		mux.Handle("POST /api/conversations/seen", auth(s.markAllSeen))
		mux.Handle("PUT /api/conversations/{id}/tags", auth(s.ownChat(s.setTags)))
		mux.Handle("PUT /api/conversations/{id}/subject", auth(s.ownChat(s.setSubject)))
		mux.Handle("GET /api/projects/{id}/chat-tags", auth(s.projectTags))
		mux.Handle("POST /api/conversations/{id}/stop", auth(s.ownChat(s.stopConversation)))
		mux.Handle("DELETE /api/conversations/{id}/queued", auth(s.ownChat(s.unqueue)))
		mux.Handle("GET /api/patches/{id}", auth(s.getPatch))
		mux.Handle("POST /api/patches/{id}/approve", admin(s.approvePatch))
		mux.Handle("POST /api/patches/{id}/reject", admin(s.rejectPatch))
		mux.Handle("POST /api/proposals/skip-all", admin(s.skipAllProposals))
	}
	if s.cfg.Automation != nil {
		s.automationRoutes(mux, auth, admin)
	}
	if s.cfg.Ops != nil {
		s.opsRoutes(mux, auth, admin)
	}
	if s.cfg.Monitors != nil {
		s.monitorRoutes(mux, auth, admin)
	}
	s.featureRoutes(mux, auth, admin)
	s.collectionRoutes(mux, auth, admin)
	for _, p := range s.cfg.Packs {
		p.Routes(mux, auth, admin)
	}
	mux.Handle("GET /api/permission-levels", auth(s.permissionLevels))
	mux.Handle("GET /api/projects/{id}/policy", auth(s.getPolicy))
	mux.Handle("PUT /api/projects/{id}/policy", admin(s.putPolicy))
	if s.cfg.Actions != nil {
		s.gitRoutes(mux, auth, admin)
		mux.Handle("POST /api/actions/{id}/approve", admin(s.decideAction(true)))
		mux.Handle("POST /api/actions/{id}/reject", admin(s.decideAction(false)))
		mux.Handle("GET /api/actions/{id}/always", admin(s.alwaysPreview))
		mux.Handle("GET /api/actions/{id}", admin(s.getAction))
	}
	mux.Handle("GET /api/system/update", admin(s.updateStatus))
	if s.cfg.Updater != nil {
		mux.Handle("POST /api/system/update", admin(s.startUpdate))
		mux.Handle("GET /api/system/update/stream", admin(s.streamUpdate))
		mux.Handle("GET /api/system/update/changes", admin(s.updateChanges))
	}
	if s.cfg.MCP != nil {
		mux.Handle("/mcp", s.cfg.MCP)              // authenticated by its own per-run token
		mux.Handle("/mcp/other_office", s.cfg.MCP) // other offices (ADR-169), same tokens
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
		mux.Handle("POST /api/cli-tools/{id}/update", admin(s.cliUpdate))
		mux.Handle("POST /api/cli-tools/{id}/login", admin(s.cliLogin))
		mux.Handle("POST /api/cli-tools/{id}/logout", admin(s.cliLogout))
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
	var ve *team.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Agent không hợp lệ", "problems": ve.Problems})
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "Không tìm thấy")
	case errors.Is(err, storage.ErrConflict), errors.Is(err, provider.ErrNameTaken), errors.Is(err, provider.ErrIsDefault):
		writeError(w, http.StatusConflict, conflictMsg(err))
	case errors.Is(err, provider.ErrInvalidKind), errors.Is(err, provider.ErrNeedsKey), errors.Is(err, provider.ErrBadTier), errors.Is(err, provider.ErrBadCap),
		errors.Is(err, provider.ErrKeyRequiredForNewURL), errors.Is(err, provider.ErrNoAccounts),
		errors.Is(err, errBadInput):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.internal(w, r, err)
	}
}

var errBadInput = errors.New("dữ liệu không hợp lệ")

func conflictMsg(err error) string {
	switch {
	case errors.Is(err, provider.ErrNameTaken):
		return err.Error()
	case errors.Is(err, provider.ErrIsDefault):
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
	LimitCaps    map[string]int    `json:"limit_caps"`
	Models       []string          `json:"models"`
	IsDefault    bool              `json:"is_default"`
	Enabled      bool              `json:"enabled"`
	Status       string            `json:"status"`
	StatusDetail string            `json:"status_detail"`
	CheckedAt    *time.Time        `json:"checked_at"`
	OwnAccount   bool              `json:"own_account"` // signed in to an account of its own (ADR-178)
}

func toProviderDTO(p storage.Provider) providerDTO {
	return providerDTO{ID: p.ID, Name: p.Name, Kind: string(p.Kind), Preset: p.Preset, BaseURL: p.BaseURL, HasAPIKey: p.APIKeyEnc != "",
		APIKeyHint: p.APIKeyHint, APIKeyEnv: p.APIKeyEnv, TierModels: p.TierModels, LimitCaps: nonNilCaps(p.LimitCaps), Models: p.Models, IsDefault: p.IsDefault,
		Enabled: p.Enabled, Status: p.Status, StatusDetail: p.StatusDetail, CheckedAt: p.CheckedAt, OwnAccount: p.CLIHome != ""}
}

func nonNilCaps(c map[string]int) map[string]int {
	if c == nil {
		return map[string]int{}
	}
	return c
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
		if installed, version, err := s.cfg.CLITools.Detect(r.Context(), id); err == nil {
			return &detected{Installed: installed, Version: version}
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
	// each CLI runs --version; detect them together so the page waits for the slowest only
	tools := []string{"claude", "codex", "gemini", "antigravity"}
	found := make([]*detected, len(tools))
	var wg sync.WaitGroup
	for i, id := range tools {
		wg.Add(1)
		go func() { defer wg.Done(); found[i] = s.detectTool(r, id) }()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"presets": provider.Presets(), "kinds": []kindInfo{
		k(storage.ProviderClaudeCLI, "Claude Code trên máy",
			"Dùng tài khoản Claude đã đăng nhập trong Claude Code trên máy này (gói Pro/Max). Không cần API key.",
			"claude", false, true, found[0]),
		k(storage.ProviderAnthropic, "Claude API",
			"Trả tiền theo lượng dùng qua API key lấy tại console.anthropic.com.",
			"https://api.anthropic.com", true, true, detectEnv("ANTHROPIC_API_KEY")),
		k(storage.ProviderOpenAI, "OpenAI (GPT) API",
			"Dùng GPT qua API key lấy tại platform.openai.com.",
			"https://api.openai.com/v1", true, true, detectEnv("OPENAI_API_KEY")),
		k(storage.ProviderCodexCLI, "Codex trên máy",
			"Dùng tài khoản ChatGPT đã đăng nhập trong Codex CLI trên máy này.",
			"codex", false, false, found[1]),
		k(storage.ProviderGeminiCLI, "Gemini CLI trên máy",
			"Dùng tài khoản Google đã đăng nhập trong Gemini CLI trên máy này (hoặc GEMINI_API_KEY). Không cần nhập key ở đây.",
			"gemini", false, false, found[2]),
		k(storage.ProviderAntigravityCLI, "Antigravity CLI trên máy",
			"Dùng tài khoản Google (gói Google AI Pro/Ultra hoặc miễn phí) đã đăng nhập trong Antigravity CLI trên máy này. Không cần API key.",
			"agy", false, false, found[3]),
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
	LimitCaps  map[string]int    `json:"limit_caps"`
	Enabled    *bool             `json:"enabled"`
	NewAccount bool              `json:"new_account"` // on create (ADR-178)
}

func (in providerInput) toInput() provider.Input {
	return provider.Input{Name: in.Name, Kind: storage.ProviderKind(in.Kind), Preset: in.Preset, BaseURL: in.BaseURL, APIKey: in.APIKey,
		APIKeyEnv: in.APIKeyEnv, TierModels: in.TierModels, LimitCaps: in.LimitCaps, Enabled: in.Enabled, NewAccount: in.NewAccount}
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
	// an account of its own is signed out first: macOS keeps it in the
	// Keychain, not in the folder the delete removes
	if old.CLIHome != "" && s.cfg.CLITools != nil && !old.IsDefault {
		if err := s.cfg.CLITools.LogoutIn(r.Context(), provider.CLITool(old), old.CLIHome); err != nil {
			slog.Warn("provider delete: không đăng xuất được tài khoản", "provider_id", id, "err", err)
		}
	}
	if err := s.cfg.Providers.Delete(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "provider.delete", ResourceID: id, Before: toProviderDTO(old)})
	w.WriteHeader(http.StatusNoContent)
}

// providerAgents lists the agents a connection's delete confirm dialog should
// warn about: agents that will be unlinked (ON DELETE SET NULL) and fall back
// silently to the default connection.
func (s *server) providerAgents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	agents, err := s.cfg.Providers.AgentsUsing(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	type agentRef struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
		Own       bool   `json:"own"` // false = only in fallback list
	}
	out := make([]agentRef, 0, len(agents))
	for _, a := range agents {
		out = append(out, agentRef{ID: a.ID, ProjectID: a.ProjectID, Name: a.Name, Own: a.ProviderID == id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
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

// setProviderEnabled turns a connection on or off: off, agents on it go to
// their next connection (provider.Chain).
func (s *server) setProviderEnabled(w http.ResponseWriter, r *http.Request) {
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
	p, err := s.cfg.Store.Providers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if p.Enabled != *in.Enabled {
		p.Enabled = *in.Enabled
		if err := s.cfg.Store.Providers().Update(r.Context(), p); err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		s.audit(r, audit.Change{Action: "provider.enabled", ResourceID: p.ID,
			Before: map[string]any{"enabled": !p.Enabled}, After: map[string]any{"enabled": p.Enabled}, Detail: map[string]any{"name": p.Name}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": toProviderDTO(p)})
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

// ---- agents ----

type agentDTO struct {
	ID           string              `json:"id"`
	ProjectID    string              `json:"project_id"`
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Role         string              `json:"role"`
	Description  string              `json:"description"`
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
	fb := a.FallbackProviderIDs
	if fb == nil {
		fb = []string{}
	}
	d := agentDTO{ID: a.ID, ProjectID: a.ProjectID, Key: a.Key, Name: a.Name, Role: a.Role,
		Description: a.Description, ProviderID: a.ProviderID, Fallbacks: fb, ModelTier: a.ModelTier, LLMModel: a.LLMModel,
		Effort: a.Effort, Instructions: a.Instructions, Permissions: a.Permissions, Avatar: a.Avatar, Sort: a.Sort, Enabled: !a.Disabled}
	d.Version = agentVersion(d)
	return d
}

// agentLibrary lists the library agents (ADR-154): names and descriptions
// only; the instructions are read when one is opened.
func (s *server) agentLibrary(w http.ResponseWriter, r *http.Request) {
	list, err := team.Library()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": team.LibraryCategories, "agents": list})
}

// agentLibraryItem is one library agent in full, to add or to read.
func (s *server) agentLibraryItem(w http.ResponseWriter, r *http.Request) {
	a, err := team.LibraryAgentByID(r.PathValue("set") + "/" + r.PathValue("key"))
	if errors.Is(err, team.ErrNoLibraryAgent) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": a})
}

// setDefaultAgent: {agent_id} answers the project's chats, bots and
// automations that name no agent.
func (s *server) setDefaultAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID string `json:"agent_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	old, err := s.cfg.Store.Repos().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Team.SetDefault(r.Context(), id, in.AgentID); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "project.default_agent", ResourceID: id, ProjectID: id,
		Before: map[string]any{"default_agent_id": old.DefaultAgentID}, After: map[string]any{"default_agent_id": in.AgentID}})
	if a, err := s.cfg.Store.Agents().Get(r.Context(), in.AgentID); err == nil && old.DefaultAgentID != in.AgentID {
		s.briefRole(r, a, a, "you now lead this project: you are its default agent, who answers its chats and reports up")
	}
	w.WriteHeader(http.StatusNoContent)
}

// exportAgents downloads a project's agents (connections by name).
func (s *server) exportAgents(w http.ResponseWriter, r *http.Request) {
	snap, err := s.cfg.Team.Load(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="agents.json"`)
	writeJSON(w, http.StatusOK, snap)
}

type agentInput struct {
	Version      string              `json:"version"` // the agent as it was read (409 when changed since)
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Role         string              `json:"role"`
	Description  string              `json:"description"`
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
	// Skills: on create, the skills it keeps at hand (a library agent's,
	// installed into the project when missing, ADR-185)
	Skills []string `json:"skills"`
}

// applyAgent validates in and puts it on a. FullAccess/ExtraDirs are admin
// only (ADR-074): a non-admin save keeps whatever a already had for them.
func (s *server) applyAgent(r *http.Request, in agentInput, a *storage.Agent) error {
	isAdmin := userFrom(r).Role == storage.RoleAdmin
	keepFullAccess, keepFullAccessBy, keepExtraDirs := a.Permissions.FullAccess, a.Permissions.FullAccessBy, a.Permissions.ExtraDirs
	a.Key, a.Name, a.Role = strings.TrimSpace(in.Key), strings.TrimSpace(in.Name), in.Role
	a.Description, a.ProviderID = in.Description, in.ProviderID
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
		projectPath := ""
		if p, err := s.cfg.Store.Repos().Get(r.Context(), a.ProjectID); err == nil {
			projectPath = p.Path
		}
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
		for _, entry := range *in.Fallbacks { // the same connection again only with another model
			id, model := storage.SplitFallback(entry)
			entry = storage.FallbackEntry(id, model)
			if id != "" && entry != a.ProviderID && !slices.Contains(a.FallbackProviderIDs, entry) {
				a.FallbackProviderIDs = append(a.FallbackProviderIDs, entry)
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
		for _, entry := range *fallbacks {
			fid, _ := storage.SplitFallback(entry)
			ids = append(ids, fid)
		}
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
	s.audit(r, audit.Change{Action: "agent.full_access", ResourceID: a.ID, ProjectID: a.ProjectID,
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
	if _, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id")); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	a := storage.Agent{ProjectID: r.PathValue("id")}
	if err := s.applyAgent(r, in, &a); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.cfg.Team.SaveAgent(r.Context(), a, storage.Agent{})
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
	s.createAgentSkills(r, a, in.Skills)
	s.audit(r, audit.Change{Action: "agent.create", ResourceID: a.ID, ProjectID: a.ProjectID, After: toAgentDTO(a),
		Detail: map[string]any{"key": a.Key, "skills": in.Skills}})
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
	a, err = s.cfg.Team.SaveAgent(r.Context(), a, old)
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
	s.audit(r, audit.Change{Action: "agent.update", ResourceID: a.ID, ProjectID: a.ProjectID,
		Before: toAgentDTO(old), After: toAgentDTO(a), Detail: map[string]any{"key": a.Key}})
	s.auditAgentFullAccess(r, old, a)
	s.briefRole(r, old, a)
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
	s.audit(r, audit.Change{Action: "agent.enabled", ResourceID: a.ID, ProjectID: a.ProjectID,
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
	project := old.ProjectID
	if err := s.cfg.Team.DeleteAgent(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "agent.delete", ResourceID: id, ProjectID: project, Before: toAgentDTO(old)})
	w.WriteHeader(http.StatusNoContent)
}

// ---- repos ----

type repoDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	GitRemote   string `json:"git_remote"`
	Description string `json:"description"`
	Scope       string `json:"scope"` // folder | machine (no path: helper for the whole machine)
	Exists      bool   `json:"exists"`
	// AgentCount: 0 = the project has no agent yet (pick from the library)
	AgentCount     int        `json:"agent_count"`
	DefaultAgentID string     `json:"default_agent_id"` // the one answering when none is named
	Features       []string   `json:"features"`         // features on (internal/features)
	Agents         []agentDTO `json:"agents,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *server) repoDTO(r *http.Request, x storage.Repo, withAgents bool) (repoDTO, error) {
	d := repoDTO{ID: x.ID, Name: x.Name, Path: x.Path, GitRemote: repos.Remote(x.Path, x.GitRemote), Description: x.Description,
		Scope: "machine", Exists: true, CreatedAt: x.CreatedAt}
	if x.Path != "" {
		_, statErr := os.Stat(x.Path)
		d.Scope, d.Exists = "folder", statErr == nil
	}
	d.Features, _ = features.Effective(features.LoadConfig(r.Context(), s.cfg.Store, x.ID), x.Path != "")
	agents, err := s.cfg.Store.Agents().List(r.Context(), x.ID)
	if err != nil {
		return d, err
	}
	d.AgentCount = len(agents)
	if def, ok := storage.DefaultAgent(x, agents); ok {
		d.DefaultAgentID = def.ID
	}
	if withAgents {
		d.Agents = make([]agentDTO, 0, len(agents))
		for _, a := range agents {
			d.Agents = append(d.Agents, toAgentDTO(a))
		}
	}
	return d, nil
}

func (s *server) listRepos(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Repos().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	sc, err := s.scope(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]repoDTO, 0, len(list))
	hidden := assistant.ID(r.Context(), s.cfg.Store) // the office assistant's own (ADR-046)
	for _, x := range list {
		if x.ID == hidden || !sc.has(x.ID) {
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
	s.registerRepo(w, r, info, in.Name, in.Description, nil)
}

// registerRepo adds a detected folder as a project, with the library's
// starter agent so it can chat at once (ADR-166). It reports false when it
// wrote an error (nothing was registered).
// then runs on the project made, before the reply (its errors are not the
// request's).
func (s *server) registerRepo(w http.ResponseWriter, r *http.Request, info repos.Info, name, desc string, extra map[string]any, then ...func(storage.Repo)) bool {
	if name = strings.TrimSpace(name); name != "" {
		info.Name = name
	}
	if desc != "" {
		info.Description = desc
	}
	var pack *transfer.Pack // a folder carrying its own pack becomes that project (ADR-190)
	if s.cfg.Transfer != nil {
		p, err := transfer.RepoPack(info.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return false
		}
		pack = p
	}
	x, err := s.cfg.Store.Repos().Create(r.Context(), storage.Repo{Name: info.Name, Path: info.Path, GitRemote: info.GitRemote, Description: info.Description})
	if err != nil {
		s.writeDomainError(w, r, err)
		return false
	}
	if pack != nil {
		_, err = s.cfg.Transfer.ImportPack(r.Context(), *pack, x, pack.Include)
	} else {
		err = s.cfg.Team.AddFromLibrary(r.Context(), x.ID, team.StarterAgent)
	}
	if err != nil {
		_ = s.cfg.Store.Repos().Delete(r.Context(), x.ID)
		if pack != nil {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			s.writeDomainError(w, r, err)
		}
		return false
	}
	detail := map[string]any{"path": x.Path}
	for k, v := range extra {
		detail[k] = v
	}
	s.ownProject(r, x.ID)
	s.auditAction(r, "project.create", x.ID, detail)
	for _, f := range then {
		f(x)
	}
	d, _ := s.repoDTO(r, x, true)
	writeJSON(w, http.StatusCreated, map[string]any{"project": d})
	return true
}

// cloneRepo clones a pasted git link into a folder on this machine (by
// default next to the folder holding this office) and adds it as a project.
func (s *server) cloneRepo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL    string `json:"url"`
		Parent string `json:"parent"`
		Dir    string `json:"dir"`
		Name   string `json:"name"`
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
	if err == nil && s.registerRepo(w, r, info, in.Name, "", map[string]any{"cloned_from": repos.StripCredentials(strings.TrimSpace(in.URL))}) {
		return
	}
	_ = os.RemoveAll(dest) // not registered: the clone does not stay behind
	if err != nil {
		s.internal(w, r, err)
	}
}

// newRepo creates a fresh, empty git repo on this machine from an idea and
// adds it as a project. The idea and stack are kept only as project
// metadata: this does not scaffold code yet. With template (a pack of office's
// library, @agentoffice/…; "startup" or "company" = @agentoffice/startup) it is made from
// that pack instead (ADR-199): the pack is written and committed, so the
// folder's pack makes the project; for a root pack the goal given becomes the
// first record in goals.
func (s *server) newRepo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Idea     string `json:"idea"`
		Stack    string `json:"stack"`
		Parent   string `json:"parent"`
		Dir      string `json:"dir"`
		Name     string `json:"name"`
		Template string `json:"template"` // "" | a library pack (@agentoffice/…)
		Goal     string `json:"goal"`     // a root pack: the input goal
	}
	if !decode(w, r, &in) {
		return
	}
	ref := strings.TrimSpace(in.Template)
	if ref == "startup" || ref == "company" {
		ref = packs.Scope + "/startup"
	}
	var tpl packs.Entry
	if ref != "" {
		var err error
		if _, tpl, err = packs.Find(ref); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	parent := strings.TrimSpace(in.Parent)
	if parent == "" {
		parent = s.cloneRoot()
	}
	dir := strings.TrimSpace(in.Dir)
	if dir == "" {
		dir = strings.TrimSpace(in.Name)
	}
	if dir == "" {
		writeError(w, http.StatusBadRequest, "Project cần tên thư mục")
		return
	}
	dest, err := repos.Init(r.Context(), parent, dir)
	var ce *repos.CloneError
	switch {
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
	if ref != "" {
		if _, err = packs.Write(r.Context(), ref, dest, true); err != nil {
			_ = os.RemoveAll(dest)
			s.internal(w, r, err)
			return
		}
	}
	desc := strings.TrimSpace(in.Idea)
	if st := strings.TrimSpace(in.Stack); st != "" {
		desc = strings.TrimSpace(desc + "\nStack: " + st) // the builder's scan reads the description
	}
	info, err := repos.Detect(dest)
	then, extra := func(x storage.Repo) { s.builderBurn(r, x) }, map[string]any{"stack": strings.TrimSpace(in.Stack), "created_from": "idea"}
	if ref != "" {
		then, extra = func(x storage.Repo) {
			_ = s.cfg.Store.Settings().Set(r.Context(), packKey(x.ID), tpl.Ref) // what a later sync brings it up to (ADR-206)
			s.packAdmin(r, x)                                                   // ADR-209: it runs on its own from the first message
			if tpl.Root {
				s.companyGoal(r, x, in.Goal)
			}
		}, map[string]any{"created_from": ref}
	}
	if err == nil && s.registerRepo(w, r, info, in.Name, desc, extra, then) {
		return
	}
	_ = os.RemoveAll(dest) // not registered: the new repo does not stay behind
	if err != nil {
		s.internal(w, r, err)
	}
}

// companyGoal puts the goal a new organisation was given into its goals table
// (ADR-199), for its head to plan from; none given: the person gives it in chat.
func (s *server) companyGoal(r *http.Request, x storage.Repo, goal string) {
	if goal = strings.TrimSpace(goal); goal == "" {
		return
	}
	title := goal
	if rs := []rune(title); len(rs) > 120 {
		title = string(rs[:120]) + "…"
	}
	// request keeps the person's words as they are: its head plans under
	// them, never in their place (ADR-213)
	_, _, _ = s.coll().Upsert(r.Context(), x.ID, "goals", "", map[string]any{"title": title, "detail": goal, "request": goal, "status": "active", "owner": "ceo"}, userFrom(r).Email)
	// the goal is the one message the organisation needs (ADR-209): its head
	// gets it now, in a chat that runs at Vận hành, as the person sending it;
	// through its workflow for new work when the pack has one (ADR-213)
	c, err := s.cfg.Chat.NewConversation(r.Context(), x.ID, "")
	if err != nil {
		return
	}
	_ = s.cfg.Chat.SetMode(r.Context(), c.ID, perm.Operate)
	msg := goal
	for _, key := range []string{"intake", "nhan-viec"} { // nhan-viec: packs made before ADR-230
		if wf, err := s.cfg.Store.Workflows().GetByKey(r.Context(), x.ID, key); err == nil && wf.Enabled {
			msg = workflow.Call(wf.Key, goal)
			break
		}
	}
	_, _, _ = s.cfg.Chat.Send(r.Context(), c.ID, msg, nil)
}

// packAdmin: a project an admin makes from the library runs on its own (ADR-209):
// its agents at Vận hành run as administrator in that admin's name, as the
// units its head opens do (ADR-207). Office checks at each run that the
// person is still an admin (ADR-074).
func (s *server) packAdmin(r *http.Request, x storage.Repo) {
	by := userFrom(r).Email
	agents, err := s.cfg.Store.Agents().List(r.Context(), x.ID)
	if err != nil || by == "" {
		return
	}
	for _, a := range agents {
		if perm.Agent(a) != perm.Operate || a.Permissions.FullAccess {
			continue
		}
		prev := a
		a.Permissions.FullAccess, a.Permissions.FullAccessBy = true, by
		if err := s.cfg.Store.Agents().Update(r.Context(), a); err == nil {
			s.auditAgentFullAccess(r, prev, a)
		}
	}
}

// builderBurn saves a project's Burn on the builder template (ADR-139), not
// started: the person starts it from the project's Burn.
func (s *server) builderBurn(r *http.Request, x storage.Repo) {
	b, err := burn.SessionOr(r.Context(), s.cfg.Store, x.ID, s.cfg.Chat.DefaultAgent)
	if err != nil {
		return
	}
	b.Template = "builder"
	_, _ = s.cfg.Store.Burn().SaveSession(r.Context(), b)
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
		GitRemote   *string `json:"git_remote"` // kept for when the folder has no origin
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
	if in.GitRemote != nil {
		v := strings.TrimSpace(*in.GitRemote)
		if x.GitRemote = peer.PublicRemote(v); v != "" && x.GitRemote == "" {
			writeError(w, http.StatusBadRequest, "Git remote không hợp lệ (https://…, ssh://… hoặc git@host:path, không kèm mật khẩu)")
			return
		}
	}
	if err := s.cfg.Store.Repos().UpdateInfoRemote(r.Context(), x.ID, x.Name, x.Description, x.GitRemote); err != nil {
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

func (s *server) auditAction(r *http.Request, action, target string, detail map[string]any) {
	s.audit(r, audit.Change{Action: action, ResourceID: target, Detail: detail})
}

// listDirs powers the folder tree in the dashboard. Browsers cannot hand a web
// page an absolute path, but the office server runs on the machine that holds
// the projects, so it lists sub-folder names (never file contents) for admins.
// listDirsWait: how long the folder picker waits on one folder.
var listDirsWait = 15 * time.Second

func (s *server) listDirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.cloneRoot()
	}
	// reading a folder macOS guards waits on its permission dialog, on the
	// machine office runs on: answer before the browser gives up
	type listed struct {
		l   repos.Listing
		err error
	}
	got := make(chan listed, 1)
	go func() {
		l, err := repos.ListDirs(path, r.URL.Query().Get("hidden") == "1")
		got <- listed{l, err}
	}()
	var l repos.Listing
	var err error
	select {
	case x := <-got:
		l, err = x.l, x.err
	case <-time.After(listDirsWait):
		writeError(w, http.StatusGatewayTimeout, "Đọc thư mục quá lâu: máy chạy office có thể đang hỏi quyền truy cập thư mục (macOS). Bấm Cho phép trên máy đó, hoặc cấp Truy cập toàn bộ ổ đĩa cho office, rồi thử lại")
		return
	case <-r.Context().Done():
		return
	}
	switch {
	case errors.Is(err, os.ErrPermission):
		msg := "Không có quyền đọc thư mục này"
		if runtime.GOOS == "darwin" { // Desktop, Documents, Downloads, volumes: macOS asks per app
			msg += ": cấp cho office trong Cài đặt hệ thống → Quyền riêng tư & Bảo mật → Truy cập toàn bộ ổ đĩa (hoặc Tệp và thư mục), rồi khởi động lại office"
		}
		writeError(w, http.StatusForbidden, msg)
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
