package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/officetools"
	"io"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"

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
	return "(" + h.Author + " answered earlier) " + h.Content
}

// RunRequest is one agent turn.
type RunRequest struct {
	Provider storage.Provider
	APIKey   string
	Bin      string // CLI binary for CLI providers
	// FullAccess: every tool and command on the machine, nothing asked (the
	// office assistant as administrator, for an admin)
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
	// Web lets it search and read the web (perm.CapWeb, ADR-192; Claude Code).
	Web bool
	// QuickCheck: each edited file is checked at once, what is wrong goes
	// back to the agent (perm.Policy.QuickCheck, ADR-133; Claude Code only)
	QuickCheck bool
	// QuickChecks: the project's own, by file ending (perm.Policy.QuickChecks, ADR-135)
	QuickChecks string
	// NoTools: people outside office drive this run (a Telegram/Discord
	// channel, ADR-048): no file, MCP or office tools at all.
	NoTools bool
	// NoSubagents: no subagents of its own (a workflow's role: the office
	// hands out the work); only full access has them otherwise
	NoSubagents bool
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
	// GatewayOthers: servers office has but gives only to other agents
	// (ADR-231); the run is told of them, not given them.
	GatewayOthers []string
	// Other: the run reaches projects in other offices, through the MCP
	// other_office at MCPURL/other_office (ADR-169)
	Other bool
}

// ClientHeader tells the gateway which AI calls (claude | codex | gemini).
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
	case storage.ProviderGeminiCLI:
		return geminiRunner{}
	case storage.ProviderAntigravityCLI:
		return antigravityRunner{}
	case storage.ProviderAnthropic:
		return anthropicRunner{}
	default:
		return openAIRunner{official: kind == storage.ProviderOpenAI}
	}
}

// ownGroup runs an agent's CLI in a process group of its own: the signal
// that stops office does not reach it, office ends it itself (Shutdown) so
// the turn goes on after the restart; a cancel ends what it started too.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		time.AfterFunc(5*time.Second, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }) // what ignored it
		return syscall.Kill(-pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 6 * time.Second // its pipes closed even if a child keeps them
}

// ErrSilent: an agent's CLI started but printed nothing at all. Every CLI
// office drives prints its first line within seconds, so one that does not
// is stuck before any work (signed out and waiting on a login, held by the
// system, no network): the turn fails with a cause instead of hanging.
var ErrSilent = errors.New("không phản hồi")

// silentLimit is how long a CLI may print nothing after it started.
var silentLimit = 2 * time.Minute

// silence watches a CLI for its first line of output.
type silence struct {
	timer *time.Timer
	fired atomic.Bool
}

// watchSilence gives the context to start a CLI with: it ends when the CLI
// prints nothing within silentLimit; output passes through out.
func watchSilence(ctx context.Context) (context.Context, *silence, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	s := &silence{}
	s.timer = time.AfterFunc(silentLimit, func() { s.fired.Store(true); cancel() })
	return ctx, s, func() { s.timer.Stop(); cancel() }
}

// out reads the CLI's output, its first byte ending the watch.
func (s *silence) out(r io.Reader) io.Reader { return silentReader{r, s} }

// err is why the run ended when the watch stopped it, else nil.
func (s *silence) err(name string) error {
	if !s.fired.Load() {
		return nil
	}
	return fmt.Errorf("%s %w sau %s (chưa đăng nhập, bị hệ điều hành chặn hoặc mất mạng): vào Kết nối AI bấm Kiểm tra", name, ErrSilent, silentLimit)
}

type silentReader struct {
	r io.Reader
	s *silence
}

func (x silentReader) Read(p []byte) (int, error) {
	n, err := x.r.Read(p)
	if n > 0 {
		x.s.timer.Stop()
	}
	return n, err
}
