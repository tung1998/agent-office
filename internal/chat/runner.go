package chat

import (
	"context"
	"encoding/json"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/officetools"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/storage"
)

// Event is streamed to the dashboard while an agent answers.
type Event struct {
	Seq     int               `json:"seq"`
	Type    string            `json:"type"` // text | tool | status | patch | action | done | error
	Text    string            `json:"text,omitempty"`
	Tool    *storage.ToolCall `json:"tool,omitempty"`
	Patch   *PatchDTO         `json:"patch,omitempty"`
	Action  *ActionDTO        `json:"action,omitempty"`
	Message *MessageDTO       `json:"message,omitempty"`
	// done/error: the turn of the next agent tagged in the chat (ADR-044)
	NextTurnID string `json:"next_turn_id,omitempty"`
}

// HistoryItem is a previous turn given to runtimes that do not keep sessions.
type HistoryItem struct {
	Role    string // user | assistant
	Content string
	Author  string // another agent's answer (after a switch); "" = this agent's or the person's
}

// Said is the content with who said it, when another agent did.
func (h HistoryItem) Said() string {
	if h.Author == "" {
		return h.Content
	}
	return "(" + h.Author + " trả lời trước đây) " + h.Content // i18n-ignore
}

// RunRequest is one agent turn.
type RunRequest struct {
	Provider storage.Provider
	APIKey   string
	Bin      string // CLI binary for CLI providers
	// FullAccess: every tool and command on the machine, nothing asked (the
	// office assistant as administrator, for an admin; Claude Code only)
	FullAccess bool
	Model      string
	Effort     string // how hard it thinks (storage.Efforts; "" = the CLI's own): Claude Code and Codex, not API runs
	System     string
	History    []HistoryItem
	Prompt     string
	WorkDir    string
	SessionID  string
	// Files attached to this turn's prompt (see attachments.go).
	Attachments []attach.File
	// Office tools (build/run/monitoring info) for this run; nil = none.
	Office *OfficeAccess
	// Write lets the agent edit files in WorkDir (its worktree, or the
	// project in direct mode), except DenyPaths.
	Write     bool
	DenyPaths []string
	// ExtraDirs: additional directories the agent may read (ADR-074)
	ExtraDirs []string
	// UserMCP lets it use the MCP servers of the person's own setup
	// (perm.CapUserMCP); otherwise only office's tools.
	UserMCP bool
	// NoTools: people outside office drive this run (a Telegram/Discord
	// channel, ADR-048): no file, MCP or office tools at all.
	NoTools bool
}

// OfficeAccess lets one run read the project's operations data: Claude Code
// through the office MCP server, API agents through the same tools directly.
type OfficeAccess struct {
	MCPURL string
	Token  string
	Scope  officetools.Scope
	Tools  *officetools.Toolbox
	// Gateway: MCP servers office manages, reached at MCPURL/s/<name> with
	// the same token (ADR-091): Claude Code and Codex through their MCP
	// config, API runs through GatewayTools (ADR-093).
	Gateway      []string
	GatewayTools GatewayTools
}

// ClientHeader tells the gateway which AI calls (claude | codex).
const ClientHeader = "X-Office-Client"

// GatewayTools are the gateway's tools for an API run: office calls them
// itself, under the same policy as a call through the gateway.
type GatewayTools interface {
	List(ctx context.Context) []GatewayTool
	Call(ctx context.Context, name string, args json.RawMessage) (string, bool)
}

// GatewayTool is one of them, named mcp__<server>__<tool>.
type GatewayTool struct {
	Name, Description string
	Schema            json.RawMessage
}

// RunResult is what the runtime produced.
type RunResult struct {
	Text      string
	SessionID string
	Usage     llm.Result
	Tools     []storage.ToolCall
	Limits    *Limits    // subscription usage windows, when the provider reports them
	Context   ContextUse // the context after this turn (0 = unknown)
}

// Runner executes a turn on one kind of provider.
type Runner interface {
	Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error)
}

func runnerFor(kind storage.ProviderKind) Runner {
	switch kind {
	case storage.ProviderClaudeCLI:
		return claudeRunner{}
	case storage.ProviderCodexCLI:
		return codexRunner{}
	case storage.ProviderAnthropic:
		return anthropicRunner{}
	default:
		return openAIRunner{official: kind == storage.ProviderOpenAI}
	}
}
