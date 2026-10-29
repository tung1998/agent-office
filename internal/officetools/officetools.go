// Package officetools gives agents read-only access to what office knows
// about a project at run time: processes (build/dev/test), docker compose
// services, health checks and their incidents. The same tools are served to
// Claude Code over MCP (internal/mcpserver) and to API agents directly.
package officetools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/agentskills"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/gitops"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/perm"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/transfer"
)

// Tool is one callable tool.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	// ReadOnly: the tool never changes state (MCP readOnlyHint). Defaults to
	// false so a new tool must be marked read-only on purpose.
	ReadOnly bool
}

// Toolbox runs the tools for one project at a time.
type Toolbox struct {
	store   storage.Store
	ops     *ops.Manager
	actions *actions.Service
	// skills is the office's skill library (find_skills, ADR-187; nil = off)
	skills SkillLibrary
	// delegate hands a task to another agent of the chat (ADR-044), set by the chat engine
	delegate func(ctx context.Context, sc Scope, agent, task, effort string) (string, error)
	// askExpert asks an expert of a linked project (ADR-177); hasExperts:
	// the project has one to ask (set by the chat engine)
	askExpert  func(ctx context.Context, sc Scope, expert, question string) (string, error)
	hasExperts func(ctx context.Context, projectID string) bool
	sendFile   func(ctx context.Context, sc Scope, path, caption string) (string, error)
	// decide approves or rejects a waiting card (kind action | patch) for an
	// administrator's run (ADR-188), set by the office binary
	decide func(ctx context.Context, sc Scope, kind, id string, approve bool) (string, error)
	burn   func(ctx context.Context, sc Scope, name string, in BurnInput) (string, error)
	// config reads settings for describe/list/get (the API's registry, ADR-045)
	config ConfigReader
	// assistant is the office assistant's own project (hidden from the list)
	assistant func(ctx context.Context) string
	// wf runs the workflow_* tools (the chat engine)
	wf Workflows
	// peers: linked projects in other offices (ADR-167; nil = none)
	peers *peer.Service
	// transfer and home: create, export and import projects (ADR-170; nil = none)
	transfer *transfer.Service
	home     string
	// providers: feature packs' tools (support, ADR-171)
	providers []Provider
	// mcpTools discovers and calls office MCP tools without loading their schemas.
	mcpTools Provider
	// related: the project's notes nearest a query by meaning (Cognee,
	// ADR-195; nil = keywords only)
	related func(ctx context.Context, projectID, query, skip string, n int) ([]string, error)
}

// Provider is a feature pack's tools (support, ADR-171): Tools lists none
// for a project without the pack on, so with it off nothing changes.
type Provider interface {
	Tools(sc Scope) []Tool
	Call(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool)
}

// AddProvider adds a feature pack's tools.
func (t *Toolbox) AddProvider(p Provider) { t.providers = append(t.providers, p) }

// SetMCPTools supplies the gateway's discovery tools, including for the office
// assistant. Experts keep their existing knowledge-only tools.
func (t *Toolbox) SetMCPTools(p Provider) { t.mcpTools = p }

// CallMCP preserves rich content when the gateway supplies it. Ordinary office
// tools retain their text result. Visibility and scope are checked by the MCP
// server before dispatch, and again for this optional gateway path.
func (t *Toolbox) CallMCP(ctx context.Context, sc Scope, name string, raw json.RawMessage) map[string]any {
	if p, ok := t.mcpTools.(interface {
		CallMCP(context.Context, Scope, string, json.RawMessage) map[string]any
	}); ok && !sc.Consult {
		for _, tool := range t.mcpTools.Tools(sc) {
			if tool.Name == name {
				return p.CallMCP(ctx, sc, name, raw)
			}
		}
	}
	text, failed := t.Call(ctx, sc, name, raw)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": failed}
}

// provider: the pack whose tool name is, for sc.
func (t *Toolbox) provider(sc Scope, name string) Provider {
	for _, p := range t.providers {
		for _, x := range p.Tools(sc) {
			if x.Name == name {
				return p
			}
		}
	}
	return nil
}

// SetRelated lets recall also find notes by meaning, of every agent of the
// project (ADR-195).
func (t *Toolbox) SetRelated(fn func(ctx context.Context, projectID, query, skip string, n int) ([]string, error)) {
	t.related = fn
}

// SetPeers gives the MCP other_office (ADR-169) the paired offices.
func (t *Toolbox) SetPeers(p *peer.Service) { t.peers = p }

// Workflows runs the workflow_* tools for the coordinator of a workflow
// running in a chat (spec 2026-10-07-workflows-design).
type Workflows interface {
	// WorkflowScope: coordinator = the run of sc coordinates a workflow;
	// inRun = it is a turn of a running workflow (no delegate then).
	WorkflowScope(sc Scope) (coordinator, inRun bool)
	WorkflowCall(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, error)
	// StartWorkflow runs workflow key of the project in sc's chat with input,
	// as if the person wrote "#key input": it starts when the turn ends.
	StartWorkflow(ctx context.Context, sc Scope, key, input string) (string, error)
}

// SetWorkflow turns on the workflow_* tools.
func (t *Toolbox) SetWorkflow(w Workflows) { t.wf = w }

// SetOffice tells the tools which project is the office assistant's.
func (t *Toolbox) SetOffice(fn func(ctx context.Context) string) { t.assistant = fn }

// ConfigReader reads settings for the generic tools.
type ConfigReader interface {
	DescribeConfig(kind string) (string, error)
	ListConfig(ctx context.Context, projectID, kind string) (string, error)
	GetConfig(ctx context.Context, projectID, kind, id string) (string, error)
}

// SetConfig turns on describe, list, get and propose_change.
func (t *Toolbox) SetConfig(c ConfigReader) { t.config = c }

// SkillLibrary is the office library of skills an agent may find and
// propose for its project (ADR-187).
type SkillLibrary interface {
	Find(ctx context.Context, projectID, query string) (string, error)
	Has(name string) bool
}

// SetSkills turns on find_skills, and lets my_skills propose installing a
// library skill the project lacks.
func (t *Toolbox) SetSkills(l SkillLibrary) { t.skills = l }

type boardTaskKey struct{}

// WithBoardTask: the hand-off is the work board's task id (ADR-210).
func WithBoardTask(ctx context.Context, id string) context.Context {
	if id = strings.TrimSpace(id); id == "" {
		return ctx
	}
	return context.WithValue(ctx, boardTaskKey{}, id)
}

// BoardTaskOf is the task id WithBoardTask set ("" = none).
func BoardTaskOf(ctx context.Context) string {
	v, _ := ctx.Value(boardTaskKey{}).(string)
	return v
}

// SetDelegate turns on the delegate tool (the chat engine runs hand-offs).
func (t *Toolbox) SetDelegate(fn func(ctx context.Context, sc Scope, agent, task, effort string) (string, error)) {
	t.delegate = fn
}

// SetAskExpert turns on the ask_expert tool.
func (t *Toolbox) SetAskExpert(fn func(ctx context.Context, sc Scope, expert, question string) (string, error), has func(ctx context.Context, projectID string) bool) {
	t.askExpert, t.hasExperts = fn, has
}

// ConsultTools are the only office tools an expert answering a question
// has (ADR-177): it reads what its project knows, changes nothing.
var ConsultTools = []string{"recall", "data_collections", "data_query", "data_get"}

// BurnInput is what the burn_* tools take.
type BurnInput struct{ Title, Kind, Detail, Item, Summary, Reason, What, Scanned, Map, Priority, Lessons, Coverage, Owner, From string }

// SetBurn turns on the burn_* tools (a Burn's conversation only).
func (t *Toolbox) SetBurn(fn func(ctx context.Context, sc Scope, name string, in BurnInput) (string, error)) {
	t.burn = fn
}

// SetSendFile turns on the send_file tool (a bot's chat posts the file, ADR-083).
// SetDecide lets an administrator's run decide the project's waiting cards (ADR-188).
func (t *Toolbox) SetDecide(fn func(ctx context.Context, sc Scope, kind, id string, approve bool) (string, error)) {
	t.decide = fn
}

func (t *Toolbox) SetSendFile(fn func(ctx context.Context, sc Scope, path, caption string) (string, error)) {
	t.sendFile = fn
}

// Scope is who calls a tool: the project, and the conversation/task and run
// that proposals are attached to.
type Scope = actions.Scope

// New builds a Toolbox; ops may be nil (processes/containers unavailable),
// acts may be nil (no propose_action).
func New(store storage.Store, o *ops.Manager, acts *actions.Service) *Toolbox {
	return &Toolbox{store: store, ops: o, actions: acts}
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var linesProp = map[string]any{"type": "integer", "description": "Number of last log lines (default 200, max 1000)"}

// effortParam is how hard an agent given work thinks (delegate, workflow_delegate).
var effortParam = map[string]any{"type": "string", "enum": storage.Efforts,
	"description": "How hard it thinks (empty = the agent's own). low/medium: lookups, routine edits, summaries; high/xhigh: tricky code, many edge cases, security, careful review; max: rarely. A misread request needs a clearer task, not more effort; careful work still wrong needs a stronger agent."}

// Tools lists the tools (names are stable: agents and the UI refer to them).
func (t *Toolbox) Tools() []Tool {
	list := []Tool{
		{Name: "ops_overview", Description: "Operations overview: processes (dev, build, test…) with status/exit code/port, docker compose services, monitors and recent incidents. Call it first for build or runtime errors, deploys or monitoring.",
			Schema: obj(map[string]any{}), ReadOnly: true},
		{Name: "process_logs", Description: "Read the latest log of a process office runs for the project (e.g. dev, build, test), with its command, status and exit code.",
			Schema: obj(map[string]any{"name": map[string]any{"type": "string", "description": "Process name, see ops_overview"}, "lines": linesProp}, "name"), ReadOnly: true},
		{Name: "container_logs", Description: "Read the latest log of a docker compose service of the project, with the container status.",
			Schema: obj(map[string]any{"service": map[string]any{"type": "string"}, "lines": linesProp}, "service"), ReadOnly: true},
		{Name: "monitor_detail", Description: "Details of a monitor: config, recent checks, Up/Down events and earlier AI analysis.",
			Schema: obj(map[string]any{"name": map[string]any{"type": "string", "description": "Monitor name, see ops_overview"}}, "name"), ReadOnly: true},
	}
	list = append(list,
		Tool{Name: "git_status", Description: "Git status of the project: branch, unpushed commits, changed files.", Schema: obj(map[string]any{}), ReadOnly: true},
		Tool{Name: "git_diff", Description: "Diff of uncommitted changes (against HEAD), optionally limited to files.",
			Schema: obj(map[string]any{"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}), ReadOnly: true},
		Tool{Name: "git_log", Description: "The latest commits.", Schema: obj(map[string]any{"lines": map[string]any{"type": "integer"}}), ReadOnly: true},
	)
	if t.actions != nil {
		list = append(list, Tool{Name: "run_command", Description: "Run one command in the project folder (no shell: no | ; & > $), e.g. test, lint, build. Commands on your allowed list run at once; others become a proposal awaiting the person's approval.",
			Schema: obj(map[string]any{
				"command": map[string]any{"type": "string", "description": "Command line, e.g. go test ./internal/..."},
				"reason":  map[string]any{"type": "string", "description": "Why it needs to run"},
			}, "command", "reason")})
		list = append(list, Tool{Name: "propose_action", Description: "Propose an action for the person to approve (runs on its own if your permissions allow; git_push always needs approval). Use it to re-run build/test after a fix, restart a stuck service, or commit/push when asked.",
			Schema: obj(map[string]any{
				"action":  map[string]any{"type": "string", "enum": []string{"run_process", "restart_process", "stop_process", "start_container", "restart_container", "stop_container", "git_commit", "git_branch", "git_merge", "git_push"}},
				"target":  map[string]any{"type": "string", "description": "*_process: process name; *_container: docker compose service"},
				"reason":  map[string]any{"type": "string", "description": "Why this action is needed"},
				"message": map[string]any{"type": "string", "description": "git_commit: commit message following the repo's conventions"},
				"files":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "git_commit: files to commit (empty = all changes)"},
				"branch":  map[string]any{"type": "string", "description": "git_branch: new branch name; git_merge: the local branch to merge into the current one (e.g. a Burn run's burn/<when>, once reviewed)"},
			}, "action", "reason")})
		list = append(list, Tool{Name: "send_to_chat", Description: "Send a message into ANOTHER chat of this office as the person you chat with, for its agent to carry on, when they ask to pass work there. Find the chat with search_history or a link they pasted. To hand work to a linked project (one this project links to in its settings), give project and no chat: a new chat opens there; read its progress later with read_link on the link returned.",
			Schema: obj(map[string]any{
				"chat":    map[string]any{"type": "string", "description": "Target chat: dashboard link (…?tab=chat&c=…) or id cnv_…; empty with project = a new chat there"},
				"project": map[string]any{"type": "string", "description": "A linked project (id or name), for a chat of that project"},
				"text":    map[string]any{"type": "string", "description": "Message text, clear enough for the agent there to act without asking back"},
				"reason":  map[string]any{"type": "string", "description": "Why it needs sending"},
			}, "text", "reason")})
		list = append(list, Tool{Name: "remember", Description: "Save one short point to your notes for this project, kept at once: only what saves you time next time, i.e. something you had to dig for or were corrected on and would otherwise look up again (where a thing lives, the command that works, a non-obvious convention or pitfall, the person's preference or decision). Not: what you just did or its progress, what the code, README, docs or git history already say, anything one-off, secrets. Rarely more than one per turn; a note already in your notes is refused. When it corrects or updates notes you have, give their ids in replaces: they go and this one takes their place. A durable rule for the whole project: no topic (core, in every later chat). Details about one area (a module, a flow, a service): give a topic and a one-line summary; only the summary is in every chat, the details are read with recall.",
			Schema: obj(map[string]any{
				"note":     map[string]any{"type": "string", "description": "What to remember, e.g. The repo uses pnpm; run pnpm test before reporting done"},
				"topic":    map[string]any{"type": "string", "description": "Optional short slug of the area, e.g. payments or deploy (reuse a topic from your index when it fits); empty = core"},
				"summary":  map[string]any{"type": "string", "description": "With a topic: one line saying what its notes cover, e.g. PayPal/Stripe refunds, webhooks, test keys"},
				"reason":   map[string]any{"type": "string", "description": "What it saves next time, e.g. took 10 minutes to find where refunds are retried"},
				"replaces": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ids of your notes this one corrects or updates (recall shows ids); they are dropped"},
			}, "note", "reason")})
		list = append(list, Tool{Name: "forget", Description: "Drop notes of yours that are wrong, outdated, repeated or no longer of use (recall shows their ids; topic core = the notes with no topic). Kept as a revision the person can put back. Keep your notes short: drop as readily as you add.",
			Schema: obj(map[string]any{
				"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ids of the notes to drop"},
				"reason": map[string]any{"type": "string", "description": "Why, e.g. the command changed; the old repo is gone"},
			}, "ids", "reason")})
		list = append(list, Tool{Name: "recall", Description: "Read your notes, with their ids, on a topic from the index in your notes (they are not in the prompt; topic core = the notes with no topic), or search all your notes by keywords, ignoring accents, best matches first (a word no note has does not empty the answer) (and, when the office has Cognee, by meaning across the notes of every agent of the project). With no arguments, lists your topics.",
			Schema: obj(map[string]any{
				"topic": map[string]any{"type": "string", "description": "Topic name from the index, e.g. payments"},
				"query": map[string]any{"type": "string", "description": "Keywords to search the notes by (optional)"},
			}), ReadOnly: true})
		list = append(list, Tool{Name: "use_skill", Description: "Read the instructions of a project skill (one of Your skills, or any the project has) when the task in front of you clearly needs it. Not for questions, small edits or work you already know how to do.",
			Schema: obj(map[string]any{
				"name": map[string]any{"type": "string", "description": "Skill name, e.g. launch-checklist"},
				"task": map[string]any{"type": "string", "description": "What you need it for, in a few words"},
			}, "name"), ReadOnly: true})
		if t.skills != nil {
			list = append(list, Tool{Name: "find_skills", Description: "Search the office's skill library (beyond the skills this project has) when a task needs a procedure none of the project's skills covers. A match the project lacks can be added with my_skills: the person approves installing it.",
				Schema: obj(map[string]any{
					"query": map[string]any{"type": "string", "description": "Words of what you need, e.g. seo launch; empty lists them all"},
				}), ReadOnly: true})
		}
		list = append(list, Tool{Name: "my_skills", Description: "Keep Your skills useful: add a skill you needed and will need again in this project (one only in the office library becomes a card the person approves to install it), or remove one you never use or that no longer fits. The list is capped: when full, remove the least useful first.",
			Schema: obj(map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"add", "remove"}},
				"name":   map[string]any{"type": "string", "description": "Skill name"},
				"why":    map[string]any{"type": "string", "description": "add: when it helps, one line, e.g. before a website goes live"},
			}, "action", "name")})
		str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
		list = append(list, Tool{Name: "propose_automation", Description: "Propose an automation (the person approves it). Prefer action=script (NO AI tokens); chat only when every run needs AI; workflow runs a project workflow. A script feeds the project's Burn: each output line '@@quest <title> :: <detail>' becomes a Burn quest (open ones with the same title are skipped). chat/workflow with goal runs until a check passes. A frequent schedule takes only_if (it runs only when its data or the work board has work: no tokens otherwise) and limits. With the self.manage capability, a chat/workflow schedule at most every 30 minutes with limits.daily_cost_usd up to 5 is done at once, no approval. With automation_id it edits that one: what the edit leaves out (time zone, agent, prompt, goal, only_if, limits, where it reports) stays as it is.",
			Schema: obj(map[string]any{
				"automation_id": str("Edit this automation (empty = create a new one)"),
				"enabled":       map[string]any{"type": "boolean", "description": "Turn it on (true) or off (false); empty = a new one on, an edited one as it is"},
				"name":          str("Short name"),
				"source":        map[string]any{"type": "string", "enum": []string{"schedule", "webhook"}},
				"every_minutes": map[string]any{"type": "integer"},
				"cron":          str("5-field cron, e.g. 0 8 * * 1-5"),
				"timezone":      str("IANA, e.g. Asia/Ho_Chi_Minh"),
				"action":        map[string]any{"type": "string", "enum": []string{"script", "chat", "workflow"}},
				"workflow":      str("workflow: key of the workflow to run (agent_id is the coordinating agent)"),
				"agent_id":      str("chat: answering agent (empty = team lead)"),
				"prompt":        str("chat: text sent to the agent; workflow: the workflow's input; supports {{payload}}, {{today}}…"),
				"script": obj(map[string]any{
					"lang":       map[string]any{"type": "string", "enum": []string{"bash", "node", "python"}},
					"body":       str("Source; runs in the project folder, payload on stdin and $OFFICE_PAYLOAD, result on stdout, non-zero exit on error; a line '@@quest <title> :: <detail>' gives the project's Burn a quest (detail optional)"),
					"timeout_s":  map[string]any{"type": "integer", "description": "Default 300, max 3600"},
					"burn_start": map[string]any{"type": "boolean", "description": "Quests were added and the Burn is stopped: start it with its saved settings (schedule or manual runs only)"},
				}),
				"goal": obj(map[string]any{
					"text":       str("What reached looks like (needed with judge)"),
					"check":      str("Shell command run after each turn in the project folder (the chat's worktree if any); exit 0 = reached"),
					"judge":      map[string]any{"type": "boolean", "description": "A separate read-only chat with the agent judges whether text is met"},
					"max_rounds": map[string]any{"type": "integer", "description": "Turns at most, default 5, max 20; each failed round sends the check's output back to the chat"},
				}),
				"ends_at": str("schedule: time to stop on its own (RFC3339, e.g. 2026-10-12T00:00:00+07:00); empty = runs until turned off"),
				"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "chat: tags put on each run's chat (max 10; empty = keep the current tags)"},
				"only_if": obj(map[string]any{
					"collection": str("Data table (collection key) of the project to look at"),
					"where": map[string]any{"type": "array", "description": "Run when a record matches all of these; \"$today\"/\"$now\" stand for the tick's date/time, e.g. [{\"field\":\"status\",\"op\":\"in\",\"value\":[\"cho-kiem-tra\"]}] or [{\"field\":\"due\",\"op\":\"lt\",\"value\":\"$today\"}]",
						"items": obj(map[string]any{"field": str("Field"), "op": str("eq ne lt lte gt gte contains in"), "value": map[string]any{"description": "Value (a list for in)"}}, "field", "op", "value")},
					"changed": map[string]any{"type": "boolean", "description": "Also run when a record changed since its last run"},
					"board":   str("Also (or only, with no collection) the project's work board, the Burn's: open = work no one is doing (Burn off with pieces left, or a piece waiting for review); changed = a piece added, finished or waiting for review since its last run (not one being worked on); any = either"),
				}),
				"limits": obj(map[string]any{
					"daily_cost_usd": map[string]any{"type": "number", "description": "Cost cap a day; it turns off when reached"},
					"max_minutes":    map[string]any{"type": "integer", "description": "How long one run may take"},
				}),
				"reason": str("Why this automation is needed"),
			}, "name", "source", "action", "reason")})
	}
	if t.config != nil && t.actions != nil {
		kind := map[string]any{"type": "string", "description": "Setting type, see describe"}
		list = append(list,
			Tool{Name: "describe", Description: "The setting types that can be changed (automation, agent, workflow, monitor, process, policy, project, usage_settings, provider) and the read-only agent_library a new agent starts from; with resource, lists its editable fields.",
				Schema: obj(map[string]any{"resource": kind}), ReadOnly: true},
			Tool{Name: "list", Description: "The settings of one type in the project (id, name, status).", Schema: obj(map[string]any{"resource": kind}, "resource"), ReadOnly: true},
			Tool{Name: "get", Description: "One setting in full (secrets masked). policy, project and usage_settings need no id.",
				Schema: obj(map[string]any{"resource": kind, "id": map[string]any{"type": "string"}}, "resource"), ReadOnly: true},
			Tool{Name: "propose_change", Description: "PROPOSE changing a setting; office applies it once the person approves the card. patch holds only the fields to change (see describe/get); never include API keys: the person pastes them on the card. With the self.manage capability, a workflow (made, changed or deleted), a deleted chat/workflow schedule, or a new agent of this project (no full_access, no extra_dirs, at most 15 agents), is done at once.",
				Schema: obj(map[string]any{
					"resource": kind,
					"op":       map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}},
					"id":       map[string]any{"type": "string", "description": "Setting to edit/delete (empty when creating, and for policy/project/usage_settings)"},
					"patch":    map[string]any{"type": "object"},
					"reason":   map[string]any{"type": "string", "description": "Why it needs changing"},
				}, "resource", "op", "reason")},
		)
	}
	list = append(list, staffLoadTool()) // ADR-197
	list = append(list, nextRunTool())   // ADR-202
	list = append(list, Tool{Name: "search_history", Description: "Search chat history (web, Discord, Telegram, automations) by keyword, ignoring accents. When the person refers to earlier work, search first, don't guess; read a result in full with read_link.",
		Schema: obj(map[string]any{
			"query":   map[string]any{"type": "string", "description": "Keywords, a few words are enough"},
			"days":    map[string]any{"type": "integer", "description": "How many recent days to search (default 30, max 365)"},
			"author":  map[string]any{"type": "string", "description": "Only messages from this person/agent (optional)"},
			"project": map[string]any{"type": "string", "description": "Search a linked project's chats instead (id or name; optional)"},
		}, "query"), ReadOnly: true})
	list = append(list, Tool{Name: "read_link", Description: "Read an office link the person pasted: a chat (…?tab=chat&c=…), a message (&m=…) or a task (…?tab=tasks&task=…) of this project or a linked one.",
		Schema: obj(map[string]any{"url": map[string]any{"type": "string", "description": "Office dashboard link"}}, "url"), ReadOnly: true})
	if t.burn != nil {
		item := map[string]any{"type": "string", "description": "Piece id (bit_…)"}
		list = append(list,
			Tool{Name: "burn_add", Description: "Burn: record a piece of work found, for a worker to do (no duplicates; a scan records 10 at most, the next scan goes on).", Schema: obj(map[string]any{
				"title":    map[string]any{"type": "string", "description": "Short title"},
				"kind":     map[string]any{"type": "string", "enum": []string{"unfinished", "upgrade", "bug", "idea"}, "description": "idea: a new feature, integration or flow"},
				"priority": map[string]any{"type": "string", "enum": []string{"high", "normal", "low"}},
				"detail":   map[string]any{"type": "string", "description": "The worker's brief: Where (file:line), Evidence, Fix, Verify"},
			}, "title", "kind", "detail")},
			Tool{Name: "burn_pick", Description: "Burn: pick the next piece to work on.", Schema: obj(map[string]any{"item": item}, "item")},
			Tool{Name: "burn_skip", Description: "Burn: drop a piece not worth doing for good (permanent, never picked again). A piece that is only outside the current focus or not its turn yet: do NOT skip it, leave it in the queue.", Schema: obj(map[string]any{"item": item, "reason": map[string]any{"type": "string"}}, "item", "reason")},
			Tool{Name: "burn_done", Description: "Burn: report the current piece done.", Schema: obj(map[string]any{"item": item, "summary": map[string]any{"type": "string", "description": "What was done and how it was checked"}}, "item", "summary")},
			Tool{Name: "burn_scan_done", Description: "Burn scan: end your scan, after recording what you found with burn_add (or nothing worth doing: say why).", Schema: obj(map[string]any{
				"item":     item,
				"scanned":  map[string]any{"type": "string", "description": "The areas you looked at, briefly"},
				"coverage": map[string]any{"type": "string", "description": "The coverage plan lines you changed or added (office merges them into the plan; the whole plan only when there is none yet): one \"- [ ] area\" line per area, \"- [x] area — what came of it (file read)\" once looked at"},
				"code_map": map[string]any{"type": "string", "description": "Only when it changed: the whole code map, updated: parts, where they live, key files, conventions, how to build/test (at most about 6000 characters)"},
				"reason":   map[string]any{"type": "string", "description": "Nothing recorded: why"},
				"lessons":  map[string]any{"type": "string", "description": "Only when it changed: the whole lessons list, updated from the pieces turned down or failed (at most about 3000 characters)"},
			}, "item", "scanned", "coverage")},
			Tool{Name: "burn_list", Description: "Burn: list this Burn's pieces: the open ones in full, all closed ones (done/skipped/failed, to avoid doing one again), every area looked at, or the code map the scans keep.", Schema: obj(map[string]any{
				"what": map[string]any{"type": "string", "enum": []string{"open", "closed", "scanned", "map"}},
			}, "what"), ReadOnly: true},
			Tool{Name: "burn_fail", Description: "Burn: report the current piece could not be done.", Schema: obj(map[string]any{"item": item, "reason": map[string]any{"type": "string"}}, "item", "reason")},
			Tool{Name: "burn_quest", Description: "Put a piece of work (a quest) on the project's work board (the Burn's), when the person asks to note work for later or hand it to the board. It waits there for the person to give it to an agent; a running Burn takes it next. One self-contained task per quest; several steps: several quests, in order.", Schema: obj(map[string]any{
				"title":  map[string]any{"type": "string", "description": "Short title (an open quest with the same title is not added twice)"},
				"detail": map[string]any{"type": "string", "description": "The brief, clear enough to do without asking back: goal, where (file:line), constraints, how to verify"},
				"owner":  map[string]any{"type": "string", "description": "Who follows it up (reads its result, accepts or rewrites it): an agent of the board's project, key or name; empty = you, or the project's default agent"},
			}, "title", "detail")},
			Tool{Name: "burn_assign", Description: "Hand quests on the work board to another agent of the project to follow up: one quest (item), or every open quest of one agent (from), when work is split between managers or handed over. The Burn still does the work.", Schema: obj(map[string]any{
				"owner": map[string]any{"type": "string", "description": "The agent that follows them up from now on (key or name)"},
				"item":  map[string]any{"type": "string", "description": "One quest's id (from burn_quests)"},
				"from":  map[string]any{"type": "string", "description": "Or: every open quest this agent follows up (key or name)"},
			}, "owner")},
			Tool{Name: "burn_drop", Description: "Take an open quest off the work board, with why: a duplicate, or one a newer plan replaced. It is closed as skipped (the Burn or an agent working on it stops there); the person sees the reason.", Schema: obj(map[string]any{
				"item":   map[string]any{"type": "string", "description": "The quest's id (from burn_quests)"},
				"reason": map[string]any{"type": "string", "description": "Why it is dropped, e.g. duplicate of bit_…, replaced by the new contract"},
			}, "item", "reason")},
			Tool{Name: "burn_quests", Description: "The project's work board: the Burn's state, its run's work not merged yet, what it found on its own, and each quest's status and, once finished, its summary.", Schema: obj(map[string]any{"owner": map[string]any{"type": "string", "description": "Only the quests this agent follows up (key or name); empty = all"}}), ReadOnly: true},
		)
	}
	if t.decide != nil {
		list = append(list,
			Tool{Name: "pending_requests", Description: "The project's cards waiting for a person: operations (kind action) and diffs (kind patch), with id, who proposed it and from which chat. Administrator only.",
				Schema: obj(map[string]any{}), ReadOnly: true},
			Tool{Name: "decide_request", Description: "Approve or reject a waiting card (see pending_requests) as administrator: an approved one is carried out at once and its agent goes on. A push to master/main is never yours to approve.",
				Schema: obj(map[string]any{
					"kind":    map[string]any{"type": "string", "enum": []string{"action", "patch"}},
					"id":      map[string]any{"type": "string"},
					"approve": map[string]any{"type": "boolean"},
					"reason":  map[string]any{"type": "string", "description": "Why"},
				}, "kind", "id", "approve")})
	}
	if t.sendFile != nil {
		list = append(list, Tool{Name: "send_file", Description: "Send a file (a screenshot, chart, log…) into the current Discord/Telegram chat at once; images show inline. It must be inside the project's working folder (copy it there first if needed).",
			Schema: obj(map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path (relative to the working folder, or absolute inside it)"},
				"caption": map[string]any{"type": "string", "description": "Short caption for the file (optional)"},
			}, "path")})
	}
	if t.delegate != nil {
		list = append(list, Tool{Name: "delegate", Description: "Hand part of the work to another agent: it works in the background while you answer the person; you are called again when it is done. Only when needed. @Name in a reply hands over nothing.",
			Schema: obj(map[string]any{
				"agent":   map[string]any{"type": "string", "description": "Name of a project agent"},
				"task":    map[string]any{"type": "string", "description": "The work to do, clear enough to do without asking back"},
				"effort":  effortParam,
				"task_id": map[string]any{"type": "string", "description": "Optional: the id of the task on the project's work board this is (burn_quests): office marks it in progress with that agent, then done with its answer (or failed)"},
			}, "agent", "task")})
	}
	if t.askExpert != nil {
		list = append(list, Tool{Name: "ask_expert", Description: "Ask an expert: an agent a linked project lends (see the Experts section), in this office or another. It answers from its own project's knowledge (code, notes, data); it sees NOTHING of this chat, code or files, so the question must stand on its own: the situation, the data it needs, what was agreed. Never send secrets. Its answer comes back to this chat and you are called again: end your turn after asking.",
			Schema: obj(map[string]any{
				"expert":   map[string]any{"type": "string", "description": "The expert as the Experts section names it (project/agent or office/project/agent)"},
				"question": map[string]any{"type": "string", "description": "The self-contained question, with the context needed"},
			}, "expert", "question")})
	}
	if t.wf != nil {
		str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
		list = append(list, Tool{Name: "run_workflow", Description: "Run one of the project's workflows in this chat yourself, as the person would by writing \"#key input\": it starts when your turn ends and its result comes back to this chat. Use it when a listed workflow fits the request; write the input the workflow needs.",
			Schema: obj(map[string]any{"key": str("Workflow key (without #)"), "input": str("The workflow's input: the request, with the context it needs")}, "key", "input")})
		brief := map[string]any{"type": "object", "description": "The brief, by the sections the workflow asks for: outcome, question, context, constraints (verified), current_option (open to challenge), tried (and why dropped), files, done_when, must_not. Not how to change each file.",
			"additionalProperties": map[string]any{"type": "string"}}
		list = append(list,
			Tool{Name: "workflow_delegate", Description: "Workflow: give a role its first piece of work; it starts when your turn ends, and you are called again once all roles just given work are done. Pass agent when the role has none assigned.",
				Schema: obj(map[string]any{"role": str("Role key"), "agent": str("Name of the agent taking the role (empty = the agent assigned to the role)"), "brief": brief, "effort": effortParam}, "role", "brief")},
			Tool{Name: "workflow_send", Description: "Workflow: send more to a role already given work (the role keeps its thread and remembers what it did). Each send is one round, and rounds are limited.",
				Schema: obj(map[string]any{"role": str("Role key"), "message": str("What to send, e.g. another role's argument it should answer")}, "role", "message")},
			Tool{Name: "workflow_ask", Description: "Workflow: ask a role (not propose/edit, not a sub-workflow) and WAIT for its answer this turn (up to wait_seconds), for short questions. On timeout it carries on in the background and you are called again.",
				Schema: obj(map[string]any{"role": str("Role key (access analyze)"), "question": str("The question, with the context needed"),
					"wait_seconds": map[string]any{"type": "integer", "description": "Max seconds to wait (10–120, default 60)"}}, "role", "question")},
			Tool{Name: "workflow_vote", Description: "Workflow: put a question to a vote among the workflow's roles (in parallel, analyze only). Office counts the votes by quorum/veto and calls you again with the result.",
				Schema: obj(map[string]any{"question": str("What to vote on, with enough context"),
					"agents": map[string]any{"type": "object", "description": "Assign agents to voting roles that have none: {\"role\": \"agent name\"}", "additionalProperties": map[string]any{"type": "string"}}}, "question")},
			Tool{Name: "workflow_gate", Description: "Workflow: open a gate. approve: a card for the person (note = what to approve), then the turn ends to wait. check: runs a check command (command, if the gate has none), in role's worktree if given.",
				Schema: obj(map[string]any{"gate": str("Gate key"), "note": str("approve: what the person needs to approve"), "command": str("check: check command"), "role": str("check: run in this role's worktree")}, "gate")},
			Tool{Name: "workflow_done", Description: "Workflow: finish when done (and every required gate passed). summary is the workflow's RESULT, the only answer the caller sees (Markdown, complete, readable on its own).",
				Schema: obj(map[string]any{"summary": str("Summary of the result"),
					"outputs":  map[string]any{"type": "object", "description": "Outputs by key, when the workflow declares outputs (required for required keys)", "additionalProperties": map[string]any{"type": "string"}},
					"overrule": str("When a role still OBJECTS and you keep your course: why (the caller sees both the objection and the reason)")}, "summary")},
		)
	}
	return list
}

// ToolsFor lists the tools an agent at level may use (proposals need
// "propose"; below it run_command runs only its safe commands).
func (t *Toolbox) ToolsFor(sc Scope) []Tool {
	var out []Tool
	if sc.Consult { // an expert: its project's knowledge, nothing else
		for _, x := range t.Tools() {
			if slices.Contains(ConsultTools, x.Name) {
				out = append(out, x)
			}
		}
		for _, p := range t.providers {
			for _, x := range p.Tools(sc) {
				if slices.Contains(ConsultTools, x.Name) {
					out = append(out, x)
				}
			}
		}
		return out
	}
	for _, x := range t.Tools() {
		if x.Name == "schedule_next_run" { // a run of a schedule, that may propose
			if sc.Office || !perm.AtLeast(sc.Level, perm.Propose) {
				continue
			}
			if _, err := t.scheduleOf(context.Background(), sc); err != nil {
				continue
			}
		}
		if x.Name == "ask_expert" && (sc.ConversationID == "" || sc.TaskID != "" || sc.Office || t.hasExperts == nil || !t.hasExperts(context.Background(), sc.ProjectID)) {
			continue
		}
		if (x.Name == "propose_action" || x.Name == "propose_automation" || x.Name == "send_to_chat") && !perm.AtLeast(sc.Level, perm.Propose) {
			continue
		}
		if x.Name == "send_to_chat" && sc.ConversationID == "" {
			continue
		}
		if (x.Name == "use_skill" || x.Name == "my_skills") && sc.Office {
			continue // the assistant works across projects: no skills of its own (ADR-185)
		}
		if questTool(x.Name) {
			if t.burnChat(sc) || (x.Name != "burn_quests" && !perm.AtLeast(sc.Level, perm.Propose)) {
				continue // a Burn records its own work with burn_add
			}
		} else if strings.HasPrefix(x.Name, "burn_") && !t.burnChat(sc) {
			continue // a Burn's conversation only
		}
		if (x.Name == "pending_requests" || x.Name == "decide_request") && !sc.Admin {
			continue // an administrator's run only (ADR-188)
		}
		if x.Name == "send_file" && !t.botChat(sc) {
			continue // only a bot's chat has somewhere to post it
		}
		if sc.Office && x.Name == "delegate" {
			continue // the assistant hands work to a project's chat instead
		}
		if t.wf != nil && (x.Name == "delegate" || x.Name == "ask_expert" || x.Name == "run_workflow" || strings.HasPrefix(x.Name, "workflow_")) {
			coord, inRun := t.wf.WorkflowScope(sc)
			if x.Name == "run_workflow" && (inRun || sc.ConversationID == "" || sc.Office || !perm.AtLeast(sc.Level, perm.Propose)) {
				continue // a chat's own turn, by an agent that may run things
			}
			if (x.Name == "delegate" || x.Name == "ask_expert") && inRun {
				continue // a workflow gives work through its own tools
			}
			if strings.HasPrefix(x.Name, "workflow_") && !coord {
				continue // only its coordinator
			}
		}
		if sc.AnswerOnly && proposes(x.Name) {
			continue
		}
		if sc.Office {
			x = withProject(x)
		} else if sc.Admin {
			x = withLinked(x) // ADR-191
		}
		out = append(out, x)
	}
	if sc.Admin && !sc.Office && t.transfer != nil && !sc.AnswerOnly && sc.ConversationID != "" {
		out = append(out, t.createProjectTool())
	}
	if !sc.Office {
		for _, p := range t.providers {
			for _, x := range p.Tools(sc) {
				if !sc.AnswerOnly || !proposes(x.Name) {
					out = append(out, x)
				}
			}
		}
	}
	if sc.Office { // the office assistant (ADR-046)
		for _, x := range t.officeTools() {
			if !sc.AnswerOnly || !proposes(x.Name) {
				out = append(out, x)
			}
		}
	}
	if t.mcpTools != nil {
		out = append(out, t.mcpTools.Tools(sc)...)
	}
	return out
}

// ownTools are the assistant's own, not a project's: they take no project.
var ownTools = []string{"remember", "forget", "recall", "read_link"}

// withProject gives a project tool of the office assistant a "project"
// argument (ADR-046): any project of the office, not only linked ones.
func withProject(x Tool) Tool {
	props, _ := x.Schema["properties"].(map[string]any)
	if props == nil || slices.Contains(ownTools, x.Name) {
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
	np["project"] = map[string]any{"type": "string", "description": "Project it is about: id or name (see projects); any project of the office"}
	schema["properties"] = np
	x.Schema = schema
	return x
}

// proposes: a tool that proposes a change or runs something (none for an
// assistant that only answers).
func proposes(name string) bool {
	return strings.HasPrefix(name, "propose") || name == "export_project" || name == "run_automation" || name == "send_to_chat" || name == "run_workflow"
}

// Has reports whether name is one of the tools.
func (t *Toolbox) Has(sc Scope, name string) bool {
	for _, x := range t.ToolsFor(sc) {
		if x.Name == name {
			return true
		}
	}
	return false
}

// Call runs a tool for a project and returns text (and whether it failed).
func (t *Toolbox) Call(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool) {
	if t.mcpTools != nil && !sc.Consult {
		for _, tool := range t.mcpTools.Tools(sc) {
			if tool.Name == name {
				return t.mcpTools.Call(ctx, sc, name, raw)
			}
		}
	}
	if p := t.provider(sc, name); p != nil && !sc.Office {
		return p.Call(ctx, sc, name, raw)
	}
	if name == "propose_create_project" && !sc.Office {
		if !sc.Admin || t.transfer == nil || sc.AnswerOnly {
			return "Chỉ agent chạy như administrator mới tạo project", true
		}
		return t.createProject(ctx, sc, raw)
	}
	projectID := sc.ProjectID
	var in struct {
		Expert   string          `json:"expert"`
		Question string          `json:"question"`
		Project  string          `json:"project"`
		Goal     string          `json:"goal"`
		Days     int             `json:"days"`
		By       string          `json:"by"`
		Status   string          `json:"status"`
		Limit    int             `json:"limit"`
		Name     string          `json:"name"`
		Service  string          `json:"service"`
		Lines    int             `json:"lines"`
		Action   string          `json:"action"`
		Target   string          `json:"target"`
		Reason   string          `json:"reason"`
		Message  string          `json:"message"`
		Files    []string        `json:"files"`
		Path     string          `json:"path"`
		Query    string          `json:"query"`
		Title    string          `json:"title"`
		Kind     string          `json:"kind"`
		Detail   string          `json:"detail"`
		Item     string          `json:"item"`
		What     string          `json:"what"`
		Scanned  string          `json:"scanned"`
		CodeMap  string          `json:"code_map"`
		Priority string          `json:"priority"`
		Lessons  string          `json:"lessons"`
		Coverage string          `json:"coverage"`
		Summary  string          `json:"summary"`
		Author   string          `json:"author"`
		Caption  string          `json:"caption"`
		Branch   string          `json:"branch"`
		Command  string          `json:"command"`
		Agent    string          `json:"agent"`
		Task     string          `json:"task"`
		Effort   string          `json:"effort"`
		TaskID   string          `json:"task_id"` // delegate: the work board's task (ADR-210)
		URL      string          `json:"url"`
		Resource string          `json:"resource"`
		Op       string          `json:"op"`
		ID       string          `json:"id"`
		Patch    json.RawMessage `json:"patch"`
		Note     string          `json:"note"`
		Replaces []string        `json:"replaces"`
		IDs      []string        `json:"ids"`
		Topic    string          `json:"topic"`
		Chat     string          `json:"chat"`
		Text     string          `json:"text"`
		Why      string          `json:"why"`
		Owner    string          `json:"owner"`
		From     string          `json:"from"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "Tham số không hợp lệ: " + err.Error(), true
		}
	}
	if sc.Office { // across projects: the one named, if any
		if out, isErr, ok := t.officeCall(ctx, sc, name, raw); ok {
			return out, isErr
		}
		if in.Project != "" {
			p, err := t.findProject(ctx, in.Project)
			if err != nil {
				return err.Error(), true
			}
			if name == "send_to_chat" { // sent from the assistant's chat, into that project
				in.Project = p.ID
			} else { // the assistant's own worktree is not that project's folder
				projectID, sc.ProjectID, sc.Dir = p.ID, p.ID, ""
			}
		}
	} else if sc.Admin && in.Project != "" && in.Project != projectID && slices.Contains(linkedTools, name) { // ADR-191: a linked project's settings
		p, err := t.linkedProject(ctx, sc, in.Project)
		if err != nil {
			return err.Error(), true
		}
		projectID, sc.ProjectID, sc.Dir = p.ID, p.ID, ""
	} else if in.Project != "" && in.Project != projectID && name != "send_to_chat" && name != "search_history" { // these two: a linked project
		return "Bạn chỉ làm việc trong project của mình", true
	}
	if in.Lines <= 0 {
		in.Lines = 200
	}
	in.Lines = min(in.Lines, 1000)
	var (
		out string
		err error
	)
	switch name {
	case "ops_overview":
		out, err = t.overview(ctx, projectID)
	case "process_logs":
		out, err = t.processLogs(ctx, projectID, in.Name, in.Lines)
	case "container_logs":
		out, err = t.containerLogs(ctx, projectID, in.Service, in.Lines)
	case "monitor_detail":
		out, err = t.monitorDetail(ctx, projectID, in.Name)
	case "git_status", "git_diff", "git_log":
		out, err = t.gitRead(ctx, projectID, sc.Dir, name, in.Files, in.Lines)
	case "run_command":
		if t.actions == nil {
			return "Office không chạy lệnh được lúc này", true
		}
		if !perm.AtLeast(sc.Level, perm.Propose) {
			// read only: checks and reads run, nothing is proposed
			args, err := perm.SplitCommand(in.Command)
			if _, safe := perm.MatchCommand(sc.Access.Safe, args); err != nil || !safe {
				return "Ở mức " + perm.Label(sc.Level) + " bạn chỉ được chạy lệnh kiểm tra an toàn: " + strings.Join(sc.Access.Safe, ", "), true
			}
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, "run_command", in.Command, in.Reason); err == nil {
			switch a.Status {
			case "done":
				out = "$ " + a.Target + "\n" + a.Detail
			case "failed":
				return "$ " + a.Target + "\nLỗi: " + a.Detail, true
			default:
				out = fmt.Sprintf("Command %q is not on the list you may run yourself; created proposal %s, waiting for the person's approval. Nothing has run yet; tell the person.", a.Target, a.ID)
			}
		}
	case "describe", "list", "get", "propose_change":
		if t.config == nil || t.actions == nil {
			return "Không có công cụ cài đặt ở đây", true
		}
		switch name {
		case "describe":
			out, err = t.config.DescribeConfig(in.Resource)
		case "list":
			out, err = t.config.ListConfig(ctx, projectID, in.Resource)
		case "get":
			out, err = t.config.GetConfig(ctx, projectID, in.Resource, in.ID)
		default:
			if !sc.Office && !perm.AtLeast(sc.Level, perm.Propose) {
				return "Bạn không có quyền đề xuất đổi cài đặt (gói hiện tại: " + perm.Label(sc.Level) + ")", true
			}
			var a storage.Action
			a, err = t.actions.Propose(ctx, sc, "config_change", "", in.Reason, storage.ActionArgs{Change: &storage.ConfigChange{Resource: in.Resource, Op: in.Op, ID: in.ID, Patch: in.Patch}})
			switch {
			case err != nil:
			case a.Status == "done": // within its own rights (self.manage) or as administrator
				out = "Done: " + a.Target + ". " + a.Detail
			case a.Status == "failed":
				return "Không đổi được " + a.Target + ": " + a.Detail, true
			default:
				out = "Created approval card: " + a.Target + ". Office changes it only once the person approves the card; tell them briefly what you proposed."
			}
		}
	case "read_link":
		out, err = t.readLink(ctx, sc, in.URL)
	case "search_history":
		out, err = t.searchHistory(ctx, sc, in.Query, in.Days, in.Author, in.Project)
	case "schedule_next_run":
		if sc.Office || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền đổi lịch chạy (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		out, err = t.scheduleNextRun(ctx, sc, raw, time.Now())
	case "staff_load":
		var d struct{ Days int }
		_ = json.Unmarshal(raw, &d)
		out, err = t.staffLoad(ctx, sc, d.Days)
	case "burn_quest", "burn_quests", "burn_assign", "burn_drop":
		if t.burn == nil {
			return "Burn chưa bật trên office này", true
		}
		if name != "burn_quests" && !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền giao việc cho Burn (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		sc.ProjectID = projectID
		out, err = t.burn(ctx, sc, name, BurnInput{Title: in.Title, Detail: in.Detail, Item: in.Item, Reason: in.Reason, Owner: in.Owner, From: in.From})
	case "burn_add", "burn_pick", "burn_skip", "burn_done", "burn_fail", "burn_list", "burn_scan_done":
		if t.burn == nil || !t.burnChat(sc) {
			return "Các công cụ burn_* chỉ dùng trong hội thoại Burn", true
		}
		out, err = t.burn(ctx, sc, name, BurnInput{Title: in.Title, Kind: in.Kind, Detail: in.Detail, Item: in.Item, Summary: in.Summary, Reason: in.Reason, What: in.What, Scanned: in.Scanned, Map: in.CodeMap, Priority: in.Priority, Lessons: in.Lessons, Coverage: in.Coverage})
	case "pending_requests":
		if !sc.Admin {
			return "Chỉ administrator xem được các thẻ chờ duyệt", true
		}
		out, err = t.pendingRequests(ctx, sc)
	case "decide_request":
		if t.decide == nil || !sc.Admin {
			return "Chỉ administrator duyệt được thẻ", true
		}
		var d struct {
			Kind, ID string
			Approve  bool
		}
		_ = json.Unmarshal(raw, &d)
		out, err = t.decide(ctx, sc, d.Kind, d.ID, d.Approve)
	case "send_file":
		if t.sendFile == nil || !t.botChat(sc) {
			return "send_file chỉ dùng trong cuộc chat của bot Discord/Telegram", true
		}
		out, err = t.sendFile(ctx, sc, in.Path, in.Caption)
	case "workflow_delegate", "workflow_send", "workflow_ask", "workflow_vote", "workflow_gate", "workflow_done":
		if t.wf == nil {
			return "Không có quy trình ở đây", true
		}
		out, err = t.wf.WorkflowCall(ctx, sc, name, raw)
	case "run_workflow":
		if t.wf == nil || !t.Has(sc, name) {
			return "Không chạy được quy trình ở đây", true
		}
		var w struct{ Key, Input string }
		_ = json.Unmarshal(raw, &w)
		out, err = t.wf.StartWorkflow(ctx, sc, w.Key, w.Input)
	case "ask_expert":
		if t.askExpert == nil {
			return "Không có chuyên gia để hỏi ở đây", true
		}
		out, err = t.askExpert(ctx, sc, in.Expert, in.Question)
	case "delegate":
		if t.delegate == nil {
			return "Không có công cụ giao việc ở đây", true
		}
		out, err = t.delegate(WithBoardTask(ctx, in.TaskID), sc, in.Agent, in.Task, in.Effort)
	case "propose_automation":
		if t.actions == nil || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền đề xuất tự động hóa (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		var id struct {
			AutomationID string `json:"automation_id"`
		}
		_ = json.Unmarshal(raw, &id)
		kind, target := "create_automation", in.Name
		if id.AutomationID != "" {
			kind, target = "update_automation", id.AutomationID
		}
		var a storage.Action
		a, err = t.actions.Propose(ctx, sc, kind, target, in.Reason, storage.ActionArgs{Automation: raw})
		switch {
		case err != nil:
		case a.Status == "done": // within its own rights (self.manage) or as administrator
			out = fmt.Sprintf("Done: %s %q. %s", map[bool]string{true: "updated", false: "created"}[kind == "update_automation"], a.Target, a.Detail)
		case a.Status == "failed":
			return fmt.Sprintf("Không lưu được tự động hóa %q: %s", a.Target, a.Detail), true
		default:
			out = fmt.Sprintf("Created proposal %q (id %s), waiting for the person's approval. Nothing runs yet; summarize the script/schedule for the person and remind them to approve it.", a.Target, a.ID)
		}
	case "send_to_chat":
		if t.actions == nil || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền gửi tin sang chat khác (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		var a storage.Action
		project := in.Project
		if !sc.Office && project == sc.ProjectID {
			project = "" // its own project is no link
		}
		if a, err = t.actions.Propose(ctx, sc, "send_message", in.Chat, in.Reason, storage.ActionArgs{Message: in.Text, Project: project}); err == nil {
			switch a.Status {
			case "done":
				out = fmt.Sprintf("Sent the message to chat %q under the person's name (%s); the agent there will answer in that chat.", a.Target, a.Detail)
			case "failed":
				return "Gửi vào chat " + a.Target + " lỗi: " + a.Detail, true
			default:
				out = fmt.Sprintf("Created a proposal to send the message to chat %q (id %s), waiting for the person's approval. Nothing sent yet; tell the person.", a.Target, a.ID)
			}
		}
	case "remember":
		if t.actions == nil {
			return "Không ghi nhớ được ở đây", true
		}
		var a storage.Action
		topic, summary := memory.Clean(in.Topic, in.Summary)
		if a, err = t.actions.Propose(ctx, sc, "remember", in.Note, in.Reason, storage.ActionArgs{Topic: topic, Summary: summary, Replaces: in.Replaces}); err == nil {
			if a.Status == "done" {
				out = "Written to memory notes: " + a.Target
			} else {
				out = "Proposed the memory note (id " + a.ID + "), waiting for the person's approval."
			}
			if topic != "" {
				out += " (topic " + topic + ")"
			}
		}
	case "forget":
		if t.actions == nil {
			return "Không bỏ ghi nhớ được ở đây", true
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, "forget", "", in.Reason, storage.ActionArgs{Replaces: in.IDs}); err == nil {
			if a.Status == "done" {
				out = "Dropped from your notes: " + a.Target
			} else {
				out = "Proposed dropping the notes (id " + a.ID + "), waiting for the person's approval."
			}
		}
	case "recall":
		var id string
		if id, err = t.agentID(ctx, projectID, sc.Agent); err == nil {
			out, err = memory.Recall(ctx, t.store, projectID, id, in.Topic, in.Query)
		}
		if err == nil && t.related != nil && strings.TrimSpace(in.Query) != "" {
			out += t.recallRelated(ctx, projectID, in.Query, out)
		}
	case "use_skill", "my_skills":
		out, err = t.skillCall(ctx, sc, projectID, name, in.Action, in.Name, in.Why, in.Task)
	case "find_skills":
		if t.skills == nil {
			return "Không có thư viện skill ở đây", true
		}
		out, err = t.skills.Find(ctx, projectID, in.Query)
	case "propose_action":
		if t.actions == nil || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền đề xuất thao tác (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, in.Action, in.Target, in.Reason, storage.ActionArgs{Message: in.Message, Files: in.Files, Branch: in.Branch}); err == nil {
			switch {
			case a.Args.AfterDiff && a.Status == "pending": // ADR-189
				out = fmt.Sprintf("Commit %q (%d files, id %s) waits for this turn's changes: once your turn ends and its diff is merged into the project, it runs on its own if your permissions allow, else it waits for the person's approval after the diff. Do not propose it again; report the commit as pending, not done.", a.Target, len(a.Args.Files), a.ID)
			case a.Status == "done":
				out = fmt.Sprintf("Did %q for %s on your own, as your permissions allow: %s. Use process_logs/container_logs to see the result.", actions.Kinds[a.Kind], a.Target, a.Detail)
			case a.Status == "failed":
				out = fmt.Sprintf("Did %q for %s on your own, but it failed: %s", actions.Kinds[a.Kind], a.Target, a.Detail)
			default:
				out = fmt.Sprintf("Created proposal %q for %s (id %s), waiting for the person's approval. Nothing has been done yet; tell the person it needs their approval.", actions.Kinds[a.Kind], a.Target, a.ID)
			}
		}
	default:
		return "Công cụ không tồn tại: " + name, true
	}
	if err != nil {
		return "Lỗi: " + err.Error(), true
	}
	return out, false
}

func when(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Local().Format("02/01 15:04:05")
}

func (t *Toolbox) overview(ctx context.Context, projectID string) (string, error) {
	var b strings.Builder
	procs, err := t.store.Processes().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	b.WriteString("## Processes\n")
	if len(procs) == 0 {
		b.WriteString("(none)\n")
	}
	for _, p := range procs {
		line := fmt.Sprintf("- %s [%s] `%s`", p.Name, p.Kind, p.Command)
		if t.ops != nil {
			st := t.ops.State(p.ID)
			line += " — " + st.Status
			if st.ExitCode != nil {
				line += fmt.Sprintf(", exit code %d", *st.ExitCode)
			}
			if st.Port > 0 {
				line += fmt.Sprintf(", port %d", st.Port)
			}
			if st.FinishedAt != nil && st.Status != "running" {
				line += ", ended " + when(st.FinishedAt)
			}
		}
		b.WriteString(line + "\n")
	}
	if t.ops != nil {
		if v, err := t.ops.Compose(ctx, projectID, "", false); err == nil && len(v.Files) > 0 {
			fmt.Fprintf(&b, "\n## Docker compose (%s)\n", v.File)
			if v.Docker.Error != "" {
				b.WriteString("Docker: " + v.Docker.Error + "\n")
			}
			for _, s := range v.Services {
				if s.Container == nil {
					fmt.Fprintf(&b, "- %s — no container yet\n", s.Name)
					continue
				}
				fmt.Fprintf(&b, "- %s — %s (%s)\n", s.Name, s.Container.State, s.Container.Status)
			}
		}
	}
	mons, err := t.store.Monitors().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	b.WriteString("\n## Monitors\n")
	if len(mons) == 0 {
		b.WriteString("(none)\n")
	}
	for _, m := range mons {
		status := m.Status
		if !m.Enabled {
			status = "paused"
		}
		fmt.Fprintf(&b, "- %s [%s %s] — %s: %s (checked %s)\n", m.Name, m.Type, m.Target, status, m.LastMessage, when(m.LastCheckedAt))
	}
	evs, err := t.store.Monitors().Events(ctx, projectID, 10)
	if err == nil && len(evs) > 0 {
		names := map[string]string{}
		for _, m := range mons {
			names[m.ID] = m.Name
		}
		b.WriteString("\n## Recent events\n")
		for _, e := range evs {
			fmt.Fprintf(&b, "- %s %s %s: %s\n", e.At.Local().Format("02/01 15:04"), strings.ToUpper(e.Kind), names[e.MonitorID], e.Message)
		}
	}
	return b.String(), nil
}

func (t *Toolbox) processLogs(ctx context.Context, projectID, name string, lines int) (string, error) {
	if t.ops == nil {
		return "", errors.New("office không quản lý tiến trình")
	}
	procs, err := t.store.Processes().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, p := range procs {
		if strings.EqualFold(p.Name, name) || p.ID == name {
			st := t.ops.State(p.ID)
			head := fmt.Sprintf("Process %s: `%s` (folder %s) — %s", p.Name, p.Command, p.Cwd, st.Status)
			if st.ExitCode != nil {
				head += fmt.Sprintf(", exit code %d", *st.ExitCode)
			}
			tail := t.ops.Tail(p.ID, lines)
			if strings.TrimSpace(tail) == "" {
				tail = "(no log yet in this office session)"
			}
			return head + "\n\n```\n" + tail + "```", nil
		}
	}
	return "", fmt.Errorf("không có tiến trình %q; xem ops_overview", name)
}

func (t *Toolbox) containerLogs(ctx context.Context, projectID, service string, lines int) (string, error) {
	if t.ops == nil {
		return "", errors.New("office không quản lý container")
	}
	c, err := t.ops.ServiceContainer(ctx, projectID, "", service)
	if err != nil {
		return "", err
	}
	head := "Service " + service + ": no container yet"
	if c != nil {
		head = fmt.Sprintf("Service %s: %s (%s), image %s", service, c.State, c.Status, c.Image)
	}
	tail, err := t.ops.ComposeTail(ctx, projectID, "", service, lines)
	if err != nil {
		return head + "\n(could not read the log: " + err.Error() + ")", nil
	}
	return head + "\n\n```\n" + tail + "```", nil
}

func (t *Toolbox) monitorDetail(ctx context.Context, projectID, name string) (string, error) {
	mons, err := t.store.Monitors().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, m := range mons {
		if !strings.EqualFold(m.Name, name) && m.ID != name {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Monitor %s [%s] target %s, every %ds — %s: %s\n", m.Name, m.Type, m.Target, m.IntervalS, m.Status, m.LastMessage)
		if checks, err := t.store.Monitors().Checks(ctx, m.ID, time.Now().Add(-6*time.Hour)); err == nil && len(checks) > 0 {
			b.WriteString("\nRecent checks:\n")
			for _, c := range checks[max(0, len(checks)-20):] {
				ok := "OK"
				if !c.OK {
					ok = "FAIL"
				}
				fmt.Fprintf(&b, "- %s %s %dms %s\n", c.At.Local().Format("15:04:05"), ok, c.LatencyMS, c.Message)
			}
		}
		if evs, err := t.store.Monitors().Events(ctx, projectID, 50); err == nil {
			n := 0
			for _, e := range evs {
				if e.MonitorID != m.ID || n >= 5 {
					continue
				}
				n++
				fmt.Fprintf(&b, "\nEvent %s %s: %s\n", e.At.Local().Format("02/01 15:04"), strings.ToUpper(e.Kind), e.Message)
				if e.AnalysisStatus == "done" && e.Analysis != "" {
					fmt.Fprintf(&b, "Earlier analysis:\n%s\n", e.Analysis)
				}
			}
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("không có giám sát %q; xem ops_overview", name)
}

// gitRead reads git in the project folder, or in the run's worktree (dir).
func (t *Toolbox) gitRead(ctx context.Context, projectID, dir, name string, files []string, lines int) (string, error) {
	p, err := t.store.Repos().Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	if p.Path == "" {
		return "", errors.New("project không gắn thư mục")
	}
	if dir != "" {
		p.Path = dir
	}
	switch name {
	case "git_status":
		st, err := gitops.ReadStatus(ctx, p.Path)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Branch %s", st.Branch)
		if st.Upstream != "" {
			fmt.Fprintf(&b, " (tracking %s, ahead %d, behind %d commits)", st.Upstream, st.Ahead, st.Behind)
		}
		fmt.Fprintf(&b, "\n%d changed files:\n", len(st.Changes))
		for _, c := range st.Changes {
			fmt.Fprintf(&b, "- %s %s\n", c.Status, c.Path)
		}
		return b.String(), nil
	case "git_diff":
		return gitops.Diff(ctx, p.Path, files, 60000)
	default:
		return gitops.Log(ctx, p.Path, min(max(lines, 10), 50))
	}
}

// botChat: the run is a bot's chat (Discord/Telegram), where send_file posts.
func (t *Toolbox) botChat(sc Scope) bool {
	if sc.ConversationID == "" {
		return false
	}
	c, err := t.store.Chat().GetConversation(context.Background(), sc.ConversationID)
	return err == nil && c.Purpose == "channel"
}

// searchHistory finds what was said in the chats (ADR-086): the project's,
// or every project's for the office assistant — never someone else's private
// chat with the assistant.
func (t *Toolbox) searchHistory(ctx context.Context, sc Scope, query string, days int, author, project string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", errors.New("hãy ghi từ khóa cần tìm")
	}
	if days <= 0 {
		days = 30
	}
	f := storage.SearchFilter{Since: time.Now().UTC().AddDate(0, 0, -min(days, 365)), Author: strings.TrimSpace(author), Limit: 10}
	assistant := ""
	if t.assistant != nil {
		assistant = t.assistant(ctx)
	}
	if sc.Office {
		repos, err := t.store.Repos().List(ctx)
		if err != nil {
			return "", err
		}
		for _, r := range repos {
			f.ProjectIDs = append(f.ProjectIDs, r.ID)
		}
	} else if project != "" && project != sc.ProjectID { // a linked project's
		p, err := perm.LinkedProject(ctx, t.store, sc.ProjectID, project)
		if err != nil && t.peers != nil { // in another office: the MCP other_office (ADR-169)
			if _, _, perr := peer.Resolve(ctx, t.store, t.peers, sc.ProjectID, project); perr == nil {
				return "", errors.New(OtherHint)
			}
		}
		if err != nil {
			return "", err
		}
		if p.ID == assistant {
			return "", errors.New("chat của trợ lý office là riêng của từng người")
		}
		f.ProjectIDs = []string{p.ID}
	} else {
		f.ProjectIDs = []string{sc.ProjectID}
	}
	if assistant != "" { // the assistant's chats are each person's own
		f.HideOthersIn = assistant
		if c, err := t.store.Chat().GetConversation(ctx, sc.ConversationID); err == nil {
			f.Me = c.CreatedBy
		}
	}
	hits, err := t.store.Chat().SearchMessages(ctx, query, f)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No match for \"%s\" in the last %d days.", query, days), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d results for \"%s\" (newest, last %d days):\n", len(hits), query, days)
	for _, h := range hits {
		who := h.Author
		if who == "" {
			who = h.Role
		}
		fmt.Fprintf(&b, "- %s · %s · %s: %s\n  /projects/%s?tab=chat&c=%s&m=%s\n", h.CreatedAt.Local().Format("02/01 15:04"), cmp.Or(h.Title, "(untitled)"), who,
			strings.ReplaceAll(h.Snippet, "\n", " "), h.ProjectID, h.ConversationID, h.MessageID)
	}
	return b.String(), nil
}

// agentID is the id of the project's agent called name (whose notes recall reads).
// skillCall runs use_skill and my_skills for the agent called agent (ADR-185).
func (t *Toolbox) skillCall(ctx context.Context, sc Scope, projectID, name, action, skill, why, task string) (string, error) {
	p, err := t.store.Repos().Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	id, err := t.agentID(ctx, projectID, sc.Agent)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	switch {
	case name == "use_skill":
		return agentskills.Use(ctx, t.store, home, p.Path, id, skill, task)
	case action == "add":
		err := agentskills.Add(ctx, t.store, home, p.Path, id, skill, why, "agent")
		if errors.Is(err, automation.ErrUnknownSkill) && t.skills != nil && t.actions != nil && t.skills.Has(skill) {
			return t.proposeSkill(ctx, sc, p.Path, id, skill, why)
		}
		if err != nil {
			return "", err
		}
		return "Added " + skill + " to your skills.", nil
	case action == "remove":
		if err := agentskills.Remove(ctx, t.store, id, skill); errors.Is(err, storage.ErrNotFound) {
			return skill + " is not on your skills.", nil
		} else if err != nil {
			return "", err
		}
		return "Removed " + skill + " from your skills.", nil
	}
	return "", errors.New("action phải là add hoặc remove")
}

// proposeSkill asks the person to install a library skill the project lacks
// and add it to the agent's skills (ADR-187). A full list refuses first:
// the agent drops one before asking for another.
func (t *Toolbox) proposeSkill(ctx context.Context, sc Scope, projectPath, agentID, skill, why string) (string, error) {
	list, err := t.store.AgentSkills().List(ctx, agentID)
	if err != nil {
		return "", err
	}
	if len(list) >= agentskills.Limit {
		return "", agentskills.ErrFull
	}
	a, err := t.actions.Propose(ctx, sc, "install_skill", skill, why, storage.ActionArgs{Summary: why})
	if err != nil {
		return "", err
	}
	if a.Status == "done" {
		return "Installed " + skill + " in the project and added it to your skills.", nil
	}
	return fmt.Sprintf("%s is only in the office library: created proposal %s to install it in the project and add it to your skills, waiting for the person's approval. Tell the person; carry on without it meanwhile.", skill, a.ID), nil
}

// recallRelated is what recall adds by meaning: a short wait, and nothing
// when Cognee is down (the keyword answer stands on its own).
func (t *Toolbox) recallRelated(ctx context.Context, projectID, query, shown string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lines, err := t.related(ctx, projectID, query, shown, 8)
	if err != nil || len(lines) == 0 {
		return ""
	}
	return "\nRelated by meaning (notes of all agents of this project):\n- " + strings.Join(lines, "\n- ") + "\n"
}

func (t *Toolbox) agentID(ctx context.Context, projectID, name string) (string, error) {
	agents, err := t.store.Agents().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Name == name {
			return a.ID, nil
		}
	}
	return "", errors.New("không có sổ ghi nhớ ở đây")
}

// questTool: the Burn tools of any chat (giving the Burn work, reading it back).
func questTool(name string) bool {
	return name == "burn_quest" || name == "burn_quests" || name == "burn_assign" || name == "burn_drop"
}

// burnChat: the run is a Burn's conversation or one of its pieces' work
// chats, where the burn_* tools work.
func (t *Toolbox) burnChat(sc Scope) bool {
	if sc.ConversationID == "" {
		return false
	}
	c, err := t.store.Chat().GetConversation(context.Background(), sc.ConversationID)
	return err == nil && (c.Purpose == "burn" || c.Purpose == "burn_work")
}

// pendingRequests: the cards waiting for a person in the project (every
// project for the office assistant), for an administrator to decide (ADR-188).
func (t *Toolbox) pendingRequests(ctx context.Context, sc Scope) (string, error) {
	var b strings.Builder
	acts, err := t.store.Actions().Pending(ctx, 200)
	if err != nil {
		return "", err
	}
	for _, a := range acts {
		if sc.Office || a.ProjectID == sc.ProjectID {
			fmt.Fprintf(&b, "- action %s: %s %s (by %s, chat %s)\n", a.ID, a.Kind, a.Target, a.ProposedBy, a.ConversationID)
		}
	}
	patches, err := t.store.Chat().PendingPatches(ctx, 200)
	if err != nil {
		return "", err
	}
	for _, p := range patches {
		c, err := t.store.Chat().GetConversation(ctx, p.ConversationID)
		if err != nil || (!sc.Office && c.ProjectID != sc.ProjectID) {
			continue
		}
		fmt.Fprintf(&b, "- patch %s: %s (chat %s)\n", p.ID, strings.Join(p.Files, ", "), p.ConversationID)
	}
	if b.Len() == 0 {
		return "Không có thẻ nào chờ duyệt", nil
	}
	return b.String(), nil
}
