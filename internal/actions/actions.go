// Package actions turns an agent's request to operate the project (run,
// restart or stop a process or a compose service) into a proposal that a
// person approves; only then does office carry it out. Agents never operate
// anything directly.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/assistant"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/gitops"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

// Kinds agents may propose.
var Kinds = map[string]string{
	"run_process":       "Chạy tiến trình",
	"restart_process":   "Chạy lại tiến trình",
	"stop_process":      "Dừng tiến trình",
	"start_container":   "Bật container",
	"restart_container": "Chạy lại container",
	"stop_container":    "Dừng container",
	"git_commit":        "Commit",
	"git_branch":        "Tạo nhánh",
	"git_merge":         "Gộp nhánh",
	"git_push":          "Push lên remote",
	"run_command":       "Chạy lệnh",
	"create_automation": "Tạo tự động hóa",
	"update_automation": "Sửa tự động hóa",
	"config_change":     "Đổi cài đặt",
	"run_automation":    "Chạy tự động hóa",
	"remember":          "Ghi nhớ",
	"forget":            "Bỏ ghi nhớ",
	"mcp_call":          "Gọi tool MCP",
	"send_message":      "Gửi tin sang chat khác",
	"workflow_gate":     "Duyệt cổng quy trình",
	"create_project":    "Tạo project",
	"import_project":    "Nhập project",
	"data_delete":       "Xóa bản ghi dữ liệu",
	"install_skill":     "Cài skill cho agent",
}

// Projects makes the projects the office assistant proposes (ADR-170).
type Projects interface {
	// CheckNewProject checks np (and fills it from its pack) before a person decides.
	CheckNewProject(ctx context.Context, np *storage.NewProject) error
	CreateProject(ctx context.Context, np storage.NewProject) (storage.Repo, error)
}

// SetProjects lets the office assistant propose new projects.
func (s *Service) SetProjects(p Projects) { s.projects = p }

// Sender sends a message to another chat in the name of the person whose
// run proposed it (the chat engine, send_to_chat).
type Sender interface {
	// Relay sends it; what happened and the chat it went to (a new one's id)
	Relay(ctx context.Context, a storage.Action) (detail, conversationID string, err error)
	// Relayed: the chat's last message from a person was itself relayed
	Relayed(ctx context.Context, conversationID string) bool
}

// SetSender turns on send_message proposals.
func (s *Service) SetSender(x Sender) { s.sender = x }

// MCPCaller calls an office MCP tool once a person approved it (the MCP
// gateway, ADR-093).
type MCPCaller interface {
	CallTool(ctx context.Context, server, tool string, args json.RawMessage) (text string, isErr bool, err error)
}

// SetMCP turns on mcp_call proposals.
func (s *Service) SetMCP(m MCPCaller) { s.mcp = m }

// mcpResultMax bounds the tool result kept on the action.
const mcpResultMax = 20000

// Runner starts work the office assistant proposed (ADR-046).
type Runner interface {
	RunAutomation(ctx context.Context, automationID string) (string, error)
}

// SetRunner turns on run_automation proposals.
func (s *Service) SetRunner(r Runner) { s.runner = r }

// ConfigApplier checks and applies settings changes (the config registry of
// the API, ADR-045).
type ConfigApplier interface {
	// CheckChange validates a proposal of projectID's agent, fills c.Before
	// and returns a label for the card.
	// office: proposed in the office scope, the only one that may touch
	// office-wide settings (AI connections, the budget).
	CheckChange(ctx context.Context, projectID string, office bool, c *storage.ConfigChange) (string, error)
	// ApplyChange carries out an approved change (ctx carries the approver).
	ApplyChange(ctx context.Context, a storage.Action) (string, error)
}

// isAutomation: code that will run unattended, so always a person decides.
func isAutomation(kind string) bool {
	return kind == "create_automation" || kind == "update_automation"
}

// Memory keeps agents' notes (ADR-068).
type Memory interface {
	Keep(ctx context.Context, m storage.Memory) (storage.Memory, error)
	Revise(ctx context.Context, projectID, agentID string, add []storage.Memory, edit map[string]string, drop []string, reason string) (int, error)
	Auto(ctx context.Context, projectID string) bool
	Duplicate(ctx context.Context, projectID, agentID, text string) (storage.Memory, bool)
}

// SetMemory turns on remember proposals.
func (s *Service) SetMemory(m Memory) { s.memory = m }

// Skills installs library skills for an agent (ADR-187).
type Skills interface {
	Check(ctx context.Context, projectID, name string) error
	Install(ctx context.Context, projectID, agentID, name, why string) (string, error)
}

// SetSkills turns on install_skill proposals.
func (s *Service) SetSkills(k Skills) { s.skills = k }

// SetConfig turns on config_change proposals.
func (s *Service) SetConfig(c ConfigApplier) { s.config = c }

func isGit(kind string) bool { return strings.HasPrefix(kind, "git_") }

var (
	ErrKind    = errors.New("hành động không được phép")
	ErrTarget  = errors.New("không có mục tiêu này trong project")
	ErrDecided = errors.New("đề xuất này đã được xử lý")
)

// Scope is where a proposal comes from.
type Scope struct {
	ProjectID      string
	ConversationID string
	TaskID         string
	RunRef         string
	JobID          string // the chat answer/task run it comes from (ADR-043)
	Office         bool   // the office assistant: tools across projects (ADR-046)
	AnswerOnly     bool   // the office assistant only answers: no proposals (ADR-059)
	Consult        bool   // an expert answering a question: reads only (ADR-177)
	Agent          string
	Level          string      // what the agent may do in this run (internal/perm)
	Access         perm.Access // its capabilities and commands in this run
	// Admin: the run has administrator access (ADR-074, ADR-188): what it
	// proposes is carried out at once, all but a push to master/main and a
	// workflow's gate (a person's by design).
	Admin bool
	// AdminBy: the admin an administrator's run acts for (who turned its
	// access on, or the admin chatting): settings it changes are applied as theirs.
	AdminBy string
	// Dir is the run's own worktree ("" = the project folder): commands run
	// there, and the project's allowed commands run without asking since
	// nothing there touches the project until a person merges it.
	Dir string
}

// Service proposes and decides actions.
type Service struct {
	store    storage.Store
	ops      *ops.Manager
	config   ConfigApplier
	runner   Runner
	memory   Memory
	skills   Skills
	direct   AutoApprover
	mcp      MCPCaller
	sender   Sender
	peers    *peer.Service // other offices' linked projects (ADR-167; nil = none)
	projects Projects
}

// SetPeers lets send_message reach linked projects in other offices.
func (s *Service) SetPeers(p *peer.Service) { s.peers = p }

// AutoApprover says whether the chat an action comes from approves it at once
// (a bot's chat in direct mode), and in whose name.
type AutoApprover func(ctx context.Context, a storage.Action) (by string, ok bool)

// SetAutoApprover turns on direct approval from chats.
func (s *Service) SetAutoApprover(fn AutoApprover) { s.direct = fn }

// New builds a Service.
func New(store storage.Store, o *ops.Manager) *Service { return &Service{store: store, ops: o} }

func isProcess(kind string) bool { return strings.HasSuffix(kind, "_process") }

// agentID is the id of the project's agent of that name.
func (s *Service) agentID(ctx context.Context, projectID, name string) (string, error) {
	agents, err := s.store.Agents().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Name == name {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("%w: agent %q", ErrTarget, name)
}

// Propose validates and records a pending action (an identical pending one
// from the same conversation/task is returned instead of a duplicate).
func (s *Service) Propose(ctx context.Context, sc Scope, kind, target, reason string, args ...storage.ActionArgs) (storage.Action, error) {
	if _, ok := Kinds[kind]; !ok {
		return storage.Action{}, fmt.Errorf("%w: %s (cho phép: %s)", ErrKind, kind, strings.Join(slices.Sorted(maps.Keys(Kinds)), ", "))
	}
	target = strings.TrimSpace(target)
	a := storage.Action{ProjectID: sc.ProjectID, ConversationID: sc.ConversationID, TaskID: sc.TaskID, RunRef: sc.RunRef, JobID: sc.JobID,
		Kind: kind, Target: target, Reason: strings.TrimSpace(reason), ProposedBy: sc.Agent}
	if len(args) > 0 {
		a.Args = args[0]
	}
	if kind == "remember" { // a note the agent keeps: whose it is
		if s.memory == nil || a.Target == "" {
			return a, errors.New("không ghi nhớ được ở đây")
		}
		if utf8.RuneCountInString(a.Target) < memory.MinNote {
			return a, errors.New("ghi chú quá ngắn để có ích lần sau: nói rõ điều gì, ở đâu")
		}
		id, err := s.agentID(ctx, sc.ProjectID, sc.Agent)
		if err != nil {
			return a, err
		}
		if m, dup := s.memory.Duplicate(ctx, sc.ProjectID, id, a.Target); dup && !slices.Contains(a.Args.Replaces, m.ID) {
			return a, fmt.Errorf("đã có trong sổ ghi nhớ (%s): %q; sửa nó thì ghi lại với replaces", m.ID, m.Text)
		}
		a.TargetID = id
	} else if kind == "forget" { // notes of the agent's own that are wrong or outdated (ADR-215)
		if s.memory == nil || len(a.Args.Replaces) == 0 {
			return a, errors.New("không bỏ ghi nhớ được ở đây: cần id (recall cho thấy id)")
		}
		id, err := s.agentID(ctx, sc.ProjectID, sc.Agent)
		if err != nil {
			return a, err
		}
		a.TargetID = id
		own, _ := s.store.Memories().List(ctx, sc.ProjectID, id)
		if !slices.ContainsFunc(own, func(m storage.Memory) bool { return slices.Contains(a.Args.Replaces, m.ID) }) {
			return a, errors.New("không có ghi nhớ nào của bạn với id đó (recall cho thấy id)")
		}
		if a.Target == "" {
			a.Target = strings.Join(a.Args.Replaces, ", ")
		}
	} else if kind == "install_skill" { // a library skill into the project, for the agent proposing it
		if s.skills == nil || a.Target == "" {
			return a, errors.New("không cài skill được ở đây")
		}
		if err := s.skills.Check(ctx, sc.ProjectID, a.Target); err != nil {
			return a, err
		}
		id, err := s.agentID(ctx, sc.ProjectID, sc.Agent)
		if err != nil {
			return a, err
		}
		a.TargetID = id
	} else if kind == "mcp_call" { // the gateway sends only calls its policy did not let through
		if s.mcp == nil || a.Args.MCP == nil || a.Args.MCP.Server == "" || a.Args.MCP.Tool == "" {
			return a, errors.New("không gọi tool MCP được ở đây")
		}
		a.Target = a.Args.MCP.Server + "/" + a.Args.MCP.Tool
	} else if kind == "workflow_gate" { // a workflow waits on a person: approving runs nothing
		if sc.ConversationID == "" || a.Target == "" || a.Reason == "" {
			return a, errors.New("cổng quy trình cần cuộc chat, tên cổng và điều cần duyệt")
		}
	} else if kind == "create_project" || kind == "import_project" { // the office assistant (ADR-170), or an administrator under its project (ADR-191)
		if !(sc.Office || sc.Admin) || s.projects == nil || a.Args.NewProject == nil {
			return a, errors.New("chỉ trợ lý office hay agent chạy như administrator tạo hay nhập project")
		}
		if np := a.Args.NewProject; !sc.Office {
			if np.Parent == "" {
				np.Parent = sc.ProjectID
			}
			if np.Parent != sc.ProjectID {
				if _, err := perm.LinkedProject(ctx, s.store, sc.ProjectID, np.Parent); err != nil {
					return a, err
				}
			}
			np.Path = "" // a new folder (or a clone), never one of the machine's
		}
		if (kind == "import_project") != (a.Args.NewProject.Pack != "") {
			return a, errors.New("nhập project cần gói project (pack); tạo project thì không")
		}
		if err := s.projects.CheckNewProject(ctx, a.Args.NewProject); err != nil {
			return a, err
		}
		np := a.Args.NewProject
		a.Target = strings.TrimSpace(np.Name)
		if from := firstNonEmpty(np.URL, np.Path); from != "" {
			a.Target = strings.TrimSpace(a.Target + " ← " + from)
		}
	} else if kind == "send_message" { // Target: the chat it goes to, Args.Message: what it says
		if err := s.checkSend(ctx, sc, &a); err != nil {
			return a, err
		}
	} else if kind == "data_delete" { // a person always decides; the record's history keeps it
		r := a.Args.Record
		if r == nil || r.Collection == "" || r.ID == "" {
			return a, errors.New("cần collection và id của bản ghi")
		}
		c, err := s.store.Collections().Get(ctx, sc.ProjectID, r.Collection)
		if err != nil {
			return a, fmt.Errorf("không có collection %q", r.Collection)
		}
		x, err := s.store.Collections().Record(ctx, c.ID, r.ID)
		if err != nil {
			return a, fmt.Errorf("không có bản ghi %q trong %q", r.ID, r.Collection)
		}
		a.Target, a.TargetID = recordLabel(c, x), x.ID
	} else if kind == "run_automation" { // costs tokens: a person always decides
		if s.runner == nil {
			return a, errors.New("không giao việc được ở đây")
		}
		if err := s.checkRun(ctx, &a); err != nil {
			return a, err
		}
	} else if kind == "config_change" { // a person always decides
		if s.config == nil || a.Args.Change == nil {
			return a, errors.New("không đổi được cài đặt ở đây")
		}
		label, err := s.config.CheckChange(ctx, sc.ProjectID, sc.Office, a.Args.Change)
		if err != nil {
			return a, err
		}
		a.Target, a.TargetID = label, a.Args.Change.ID
	} else if isAutomation(kind) {
		spec, err := s.automationSpec(ctx, sc.ProjectID, kind, a.Args.Automation)
		if err != nil {
			return a, err
		}
		a.Target = spec.Name
		if kind == "update_automation" {
			a.TargetID = spec.AutomationID
		}
	} else if isGit(kind) {
		// a run in its own worktree: git works on the project, once what the
		// worktree changed is merged into it (its diffs decided)
		// a commit asked in the same turn as the edits waits for them (ADR-189)
		after := kind == "git_commit" && sc.Dir != "" && s.pendingTree(ctx, sc)
		if !after && sc.Dir != "" && s.unmerged(ctx, sc.ConversationID) {
			return a, errors.New("thay đổi trong worktree riêng của cuộc chat chưa được gộp vào project: nhờ người dùng duyệt diff đang chờ (trên Discord/Telegram: /pending rồi /approve <số>; hoặc Tổng quan → Cần xử lý), rồi đề xuất lại commit/nhánh/push")
		}
		if after {
			if err := s.checkAfterDiff(ctx, sc, &a); err != nil {
				return a, err
			}
		} else if err := s.checkGit(ctx, &a); err != nil {
			return a, err
		}
	} else if kind == "run_command" {
		args, err := perm.SplitCommand(target)
		if err != nil {
			return a, fmt.Errorf("%w: chạy không qua shell nên không dùng được | ; & > $ (gọi từng lệnh riêng)", err)
		}
		if _, err := s.projectPath(ctx, sc.ProjectID); err != nil {
			return a, err
		}
		a.Target, a.Args.Dir = perm.JoinCommand(args), sc.Dir
	} else if isProcess(kind) {
		procs, err := s.store.Processes().List(ctx, sc.ProjectID)
		if err != nil {
			return a, err
		}
		for _, p := range procs {
			if strings.EqualFold(p.Name, target) || p.ID == target {
				a.Target, a.TargetID = p.Name, p.ID
			}
		}
		if a.TargetID == "" {
			return a, fmt.Errorf("%w: tiến trình %q", ErrTarget, target)
		}
	} else {
		if s.ops == nil {
			return a, ErrTarget
		}
		v, err := s.ops.Compose(ctx, sc.ProjectID, "", false)
		if err != nil {
			return a, err
		}
		if !slices.ContainsFunc(v.Services, func(x ops.Service) bool { return x.Name == target }) {
			return a, fmt.Errorf("%w: service %q", ErrTarget, target)
		}
	}
	if (sc.ConversationID != "" || sc.TaskID != "") && kind != "mcp_call" && kind != "send_message" { // differs by its arguments
		if list, err := s.store.Actions().List(ctx, sc.ConversationID, sc.TaskID, ""); err == nil {
			for _, x := range list {
				if x.Status != "pending" || x.Kind != a.Kind || x.Target != a.Target {
					continue
				}
				if a.Kind == "git_commit" && !sameFiles(x.Args.Files, a.Args.Files) {
					continue
				}
				// the same command in another folder (an agent's worktree, a
				// workflow's check step in the project) is another card: else
				// the step waits on a card left on an agent's finished turn
				if a.Kind == "run_command" && x.Args.Dir != a.Args.Dir {
					continue
				}
				return x, nil
			}
		}
	}
	a, err := s.store.Actions().Create(ctx, a)
	if err != nil {
		return a, err
	}
	if a.Args.AfterDiff {
		return a, nil // decided once its turn's diff is merged (RunDeferred) or by a person
	}
	// within the agent's permission package and the project's allow lists,
	// office carries it out right away
	if sc.Admin && sc.AdminBy != "" {
		ctx = WithPrincipal(ctx, sc.AdminBy)
	}
	if !sc.Admin && a.Kind == "config_change" && PrincipalOf(ctx) == "" { // a change on the agent's own permission (ADR-194): in the name of the person its turn speaks for
		if j, err := s.store.Jobs().Get(ctx, a.JobID); err == nil {
			if as, ok := storage.SpeaksFor(ctx, s.store, j); ok {
				ctx = WithPrincipal(ctx, strings.TrimPrefix(as, "human:"))
			}
		}
	}
	if (sc.Admin && s.adminAllowed(ctx, a)) || s.autoAllowed(ctx, a, sc.Access) {
		done, err := s.Decide(ctx, a.ID, true, "auto:"+sc.Agent+" ("+perm.Label(sc.Access.Level)+")")
		s.auditAuto(ctx, sc, done, err)
		return done, err
	}
	// a bot's chat in direct mode: approved now, so the agent gets the result
	// in this turn and goes on (not after its answer, waiting to be asked again)
	if s.direct != nil && a.JobID != "" && a.Kind != "workflow_gate" { // a gate waits on a person, whatever the chat's way
		if by, ok := s.direct(ctx, a); ok {
			done, err := s.Decide(ctx, a.ID, true, by)
			if err != nil {
				return done, err
			}
			via, _, _ := strings.Cut(by, ":")
			actx := audit.With(ctx, audit.Who{Kind: "agent", Name: sc.Agent, ApprovedBy: by, Via: via, ConversationID: sc.ConversationID, JobID: sc.JobID, ActionID: a.ID})
			if done.Status == "failed" {
				err = errors.New(done.Detail)
			}
			_ = audit.Record(actx, s.store.Audit(), audit.Change{Action: "action.approve", Resource: "action", ResourceID: a.ID, ProjectID: sc.ProjectID,
				Detail: map[string]any{"kind": done.Kind, "target": done.Target, "status": done.Status, "direct": true}, Err: err})
			return done, nil
		}
	}
	return a, nil
}

// RunDeferred carries out a commit held for its turn's diff (ADR-189) when
// the run's permissions (or the chat's direct mode) allow it: now that the
// diff is merged, or queued (blocked) behind a diff still waiting for a
// person, to run once that is decided. Else it waits for a person.
func (s *Service) RunDeferred(ctx context.Context, sc Scope, id string, blocked bool) (storage.Action, error) {
	a, err := s.store.Actions().Get(ctx, id)
	if err != nil || a.Status != "pending" || !a.Args.AfterDiff {
		return a, err
	}
	if s.autoAllowed(ctx, a, sc.Access) {
		by := "auto:" + sc.Agent + " (" + perm.Label(sc.Access.Level) + ")"
		if blocked {
			return s.Queue(ctx, a.ID, by)
		}
		done, err := s.Decide(ctx, a.ID, true, by)
		s.auditAuto(ctx, sc, done, err)
		return done, err
	}
	if s.direct != nil && a.JobID != "" && !blocked {
		if by, ok := s.direct(ctx, a); ok {
			return s.Decide(ctx, a.ID, true, by)
		}
	}
	return a, nil
}

// auditAuto logs an action office carried out on the agent's own permission
// (no person approved it) as the agent's change (ADR-043).
func (s *Service) auditAuto(ctx context.Context, sc Scope, a storage.Action, err error) {
	via := "chat"
	if sc.ConversationID == "" && sc.TaskID != "" {
		via = "task"
	}
	if err == nil && a.Status == "failed" {
		err = errors.New(a.Detail)
	}
	actx := audit.With(ctx, audit.Who{Kind: "agent", Name: sc.Agent, Via: via, ConversationID: sc.ConversationID, JobID: sc.JobID, TaskID: sc.TaskID, ActionID: a.ID})
	_ = audit.Record(actx, s.store.Audit(), audit.Change{Action: "action.approve", Resource: "action", ResourceID: a.ID, ProjectID: sc.ProjectID,
		Detail: map[string]any{"kind": a.Kind, "target": a.Target, "status": a.Status, "auto": true}, Err: err})
}

// autoAllowed: the run's capabilities decide, within the project's allow
// lists (processes, containers, commands). Push always needs a person.
// adminAllowed: what an administrator's run carries out without asking
// (ADR-188). Loop guards on relayed messages still hold.
func (s *Service) adminAllowed(ctx context.Context, a storage.Action) bool {
	switch a.Kind {
	case "workflow_gate":
		return false // the point of the gate: a person decides
	case "git_push":
		return !s.PushesProtected(ctx, a)
	case "send_message":
		return s.autoAllowed(ctx, a, perm.Access{Caps: []string{perm.CapChatSend, perm.CapChatCross}})
	}
	return true
}

type principalKey struct{}

// WithPrincipal: the admin an administrator's run acts for (ADR-188); a
// settings change it approves is applied in their name.
func WithPrincipal(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, principalKey{}, email)
}

// PrincipalOf is the admin set by WithPrincipal ("" = none).
func PrincipalOf(ctx context.Context) string {
	v, _ := ctx.Value(principalKey{}).(string)
	return v
}

// ProtectedBranches never get a push without a person (ADR-188).
var ProtectedBranches = []string{"master", "main"}

// PushesProtected: the push would land on a protected branch, the current
// one or the one it tracks (unknown counts as protected).
func (s *Service) PushesProtected(ctx context.Context, a storage.Action) bool {
	root, err := s.projectPath(ctx, a.ProjectID)
	if err != nil {
		return true
	}
	st, err := gitops.ReadStatus(ctx, root)
	if err != nil {
		return true
	}
	_, upstream, _ := strings.Cut(st.Upstream, "/")
	return slices.Contains(ProtectedBranches, st.Branch) || slices.Contains(ProtectedBranches, upstream)
}

func (s *Service) autoAllowed(ctx context.Context, a storage.Action, acc perm.Access) bool {
	switch a.Kind {
	case "remember": // the project keeps its agents' notes on its own, or a person reads each
		return s.memory != nil && s.memory.Auto(ctx, a.ProjectID) && a.Reason != "" && s.keptThisTurn(ctx, a) < memory.AutoPerTurn
	case "forget": // its own notes, kept as a revision that can be put back (ADR-215)
		return s.memory != nil && s.memory.Auto(ctx, a.ProjectID) && a.Reason != ""
	case "create_automation", "update_automation": // within bounds, an agent that manages itself (ADR-194)
		return acc.Can(perm.CapSelf) && s.selfAutomation(ctx, a)
	case "config_change":
		return acc.Can(perm.CapSelf) && s.selfChange(ctx, a.ProjectID, a.Args.Change)
	case "run_automation":
		return false // code that runs unattended, or settings: a person always decides
	case "install_skill":
		return false // files written into the project: a person decides
	case "mcp_call":
		return false // the gateway already let through what the agent may call itself
	case "workflow_gate":
		return false // the point of the gate: a person decides
	case "create_project", "import_project":
		return false // a new folder and project on the machine: a person decides
	case "data_delete":
		return false // a person decides
	case "send_message": // never on and on: what a relayed message started asks a person
		if s.sender == nil {
			return false
		}
		if a.Args.Peer != "" || (a.Args.Project != "" && a.Args.Project != a.ProjectID) { // a linked project's chat (or another office's)
			return acc.Can(perm.CapChatCross)
		}
		return acc.Can(perm.CapChatSend) && !s.sender.Relayed(ctx, a.ConversationID)
	case "git_commit":
		return acc.Can(perm.CapCommit)
	case "git_branch":
		return acc.Can(perm.CapBranch)
	case "git_merge": // a Burn run's branch passed its review (ADR-196); anything else: a person decides
		return acc.Can(perm.CapCommit) && strings.HasPrefix(a.Args.Branch, "burn/")
	case "git_push":
		return false // publishing code always needs a person
	case "run_command":
		args, _ := perm.SplitCommand(a.Target)
		if _, safe := perm.MatchCommand(acc.Safe, args); safe {
			return true // checks and reads, from read only
		}
		_, ok := perm.MatchCommand(acc.Commands, args)
		return ok && (acc.Can(perm.CapCommands) || a.Args.Dir != "" && perm.AtLeast(acc.Level, perm.Propose))
	}
	if isProcess(a.Kind) {
		if !slices.Contains(acc.Processes, a.TargetID) {
			return false
		}
		if a.Kind == "run_process" && acc.Can(perm.CapCommands) {
			if p, err := s.store.Processes().Get(ctx, a.TargetID); err == nil && p.Kind == "job" {
				return true // a check command
			}
		}
		return acc.Can(perm.CapProcess)
	}
	return slices.Contains(acc.Containers, a.Target) && acc.Can(perm.CapContainer)
}

// Decide runs (approve) or rejects a pending action. Only one decision of an
// action runs; one made meanwhile gets ErrDecided.
func (s *Service) Decide(ctx context.Context, id string, approve bool, by string) (storage.Action, error) {
	// the dashboard and a bot deciding one at once must not both run it; while
	// it runs, it is shown as being decided (not as waiting for someone)
	if !deciding.Begin("actions", id, by) {
		a, err := s.store.Actions().Get(ctx, id)
		if err != nil {
			return a, err
		}
		return a, ErrDecided
	}
	defer deciding.End("actions", id)
	// once decided it runs to the end and is recorded, even when whoever
	// asked gave up waiting (an agent's tool call timing out on a long test):
	// else the command is killed and the card stays pending, approved
	ctx = context.WithoutCancel(ctx)
	a, err := s.store.Actions().Get(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Status != "pending" && a.Status != "queued" { // queued: approved, run once those before it are decided
		return a, ErrDecided
	}
	now := time.Now().UTC()
	a.DecidedBy, a.DecidedAt = by, &now
	if !approve {
		a.Status = "rejected"
		return a, s.store.Actions().Update(ctx, a)
	}
	if a.Kind == "run_command" {
		root, err := s.projectPath(ctx, a.ProjectID)
		if a.Args.Dir != "" {
			root = a.Args.Dir
			if _, serr := os.Stat(root); serr != nil {
				err = errors.New("worktree của lượt này đã được dọn nên không chạy được nữa")
			}
		}
		out := ""
		if err == nil {
			out, err = runCommand(ctx, root, a.Target)
		}
		a.Status, a.Detail = "done", "Thành công"
		if err != nil {
			a.Status, a.Detail = "failed", err.Error()
		}
		if out != "" {
			a.Detail += "\n" + out
		}
		return a, s.store.Actions().Update(ctx, a)
	}
	if a.Kind == "mcp_call" {
		a.Status, a.Detail = "done", ""
		if s.mcp == nil || a.Args.MCP == nil {
			a.Status, a.Detail = "failed", "office không gọi được tool MCP ở đây"
		} else if text, isErr, err := s.mcp.CallTool(ctx, a.Args.MCP.Server, a.Args.MCP.Tool, a.Args.MCP.Arguments); err != nil {
			a.Status, a.Detail = "failed", err.Error()
		} else {
			if isErr {
				a.Status = "failed"
			}
			if r := []rune(text); len(r) > mcpResultMax {
				text = string(r[:mcpResultMax]) + "\n… (đã cắt)"
			}
			a.Detail = text
		}
		return a, s.store.Actions().Update(ctx, a)
	}
	if a.Kind == "send_message" { // what it says back: where it went (a new chat's link)
		a.Status, a.Detail = "done", ""
		if s.sender == nil {
			a.Status, a.Detail = "failed", "không gửi tin được ở đây"
		} else if _, err := s.peerRecheck(ctx, &a); err != nil {
			a.Status, a.Detail = "failed", err.Error()
		} else if detail, to, err := s.sender.Relay(ctx, a); err != nil {
			a.Status, a.Detail = "failed", err.Error()
		} else {
			a.Detail, a.TargetID = detail, to // a new chat: now known
		}
		return a, s.store.Actions().Update(ctx, a)
	}
	if err := s.run(ctx, a); err != nil {
		a.Status, a.Detail = "failed", err.Error()
	} else {
		a.Status, a.Detail = "done", Kinds[a.Kind]+" "+a.Target+": đã thực hiện"
		if a.Kind == "git_commit" {
			a.Detail = fmt.Sprintf("Đã commit %d file: %s", len(a.Args.Files), a.Target)
		}
		if strings.HasSuffix(a.Kind, "_container") {
			a.Detail += " (docker compose chạy nền, xem mục Container)"
		}
	}
	return a, s.store.Actions().Update(ctx, a)
}

// Queue approves a pending action that waits behind an earlier proposal of
// its chat: it is "queued" and runs once those are decided (chat.Drain).
func (s *Service) Queue(ctx context.Context, id, by string) (storage.Action, error) {
	if !deciding.Begin("actions", id, by) {
		a, err := s.store.Actions().Get(ctx, id)
		if err != nil {
			return a, err
		}
		return a, ErrDecided
	}
	defer deciding.End("actions", id)
	a, err := s.store.Actions().Get(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Status != "pending" {
		return a, ErrDecided
	}
	a.Status, a.DecidedBy, a.Detail = "queued", by, ""
	return a, s.store.Actions().Update(ctx, a)
}

// DecideAlways approves a proposed command, then lets its agent run the like
// of it on its own from now on (perm.SuggestPattern, perm.AllowAlways). A
// wrapper (sudo, bash…) is refused before anything runs. An error means
// nothing was decided; once it ran, a permission that could not be saved is
// in Allowed.Error.
func (s *Service) DecideAlways(ctx context.Context, id, by string) (storage.Action, perm.Allowed, error) {
	a, err := s.store.Actions().Get(ctx, id)
	if err != nil {
		return a, perm.Allowed{}, err
	}
	if a.Status != "pending" {
		return a, perm.Allowed{}, ErrDecided
	}
	pattern, ok := perm.SuggestPattern(a.Target)
	if a.Kind != "run_command" || !ok {
		return a, perm.Allowed{}, perm.ErrNotAlways
	}
	agentID, err := s.proposer(ctx, a)
	if err != nil {
		return a, perm.Allowed{}, err
	}
	done, err := s.Decide(ctx, id, true, by)
	if err != nil {
		return done, perm.Allowed{}, err
	}
	allowed, err := perm.AllowAlways(ctx, s.store, a.ProjectID, agentID, pattern)
	via, _, _ := strings.Cut(by, ":")
	if via == by {
		via = "ui"
	}
	actx := audit.With(ctx, audit.Who{Kind: "human", Name: by, Via: via, ConversationID: a.ConversationID, JobID: a.JobID, TaskID: a.TaskID, ActionID: a.ID})
	_ = audit.Record(actx, s.store.Audit(), audit.Change{Action: "agent.update", Resource: "agent", ResourceID: agentID, ProjectID: a.ProjectID,
		Detail: map[string]any{"allow_always": allowed.Pattern, "pack": allowed.Pack, "new_pack": allowed.NewPack, "added_to_policy": allowed.Added, "approved_by": by, "command": a.Target}, Err: err})
	if err != nil {
		allowed.Pattern, allowed.Error = pattern, err.Error()
	}
	return done, allowed, nil
}

// AlwaysNote tells a person what "luôn cho phép" did.
func AlwaysNote(x perm.Allowed) string {
	switch {
	case x.Error != "":
		return "⚠️ Chưa lưu được quyền luôn cho phép: " + x.Error
	case !x.Auto:
		return "♾️ Đã thêm " + x.Pattern + " (gói " + x.Pack + "), nhưng mức hiện tại của agent chưa cho tự chạy lệnh ngoài danh sách an toàn (chỉ tự chạy trong worktree riêng)."
	}
	return "♾️ Lần sau agent tự chạy " + x.Pattern + " (gói " + x.Pack + ")."
}

// proposer is the project agent that proposed a: its run's agent, or the
// agent of that name (a lead run keeps no agent id).
func (s *Service) proposer(ctx context.Context, a storage.Action) (string, error) {
	if a.JobID != "" {
		if j, err := s.store.Jobs().Get(ctx, a.JobID); err == nil && j.AgentID != "" {
			if ag, err := s.store.Agents().Get(ctx, j.AgentID); err == nil && ag.Name == a.ProposedBy {
				return ag.ID, nil
			}
		}
	}
	return s.agentID(ctx, a.ProjectID, a.ProposedBy)
}

// checkGit validates a git action and fills its target: commit files must be
// changed and allowed by the policy (none given = every allowed change).
func (s *Service) checkGit(ctx context.Context, a *storage.Action) error {
	root, err := s.projectPath(ctx, a.ProjectID)
	if err != nil {
		return err
	}
	switch a.Kind {
	case "git_commit":
		a.Args.Message = strings.TrimSpace(firstNonEmpty(a.Args.Message, a.Target))
		if a.Args.Message == "" {
			return errors.New("commit cần message")
		}
		st, err := gitops.ReadStatus(ctx, root)
		if err != nil {
			return err
		}
		changed := map[string]bool{}
		for _, c := range st.Changes {
			changed[c.Path] = true
		}
		files := a.Args.Files
		if len(files) == 0 {
			for _, c := range st.Changes {
				files = append(files, c.Path)
			}
		}
		pol := perm.LoadPolicy(ctx, s.store, a.ProjectID)
		var keep []string
		for _, f := range files {
			if changed[strings.TrimPrefix(f, "./")] {
				keep = append(keep, strings.TrimPrefix(f, "./"))
			}
		}
		if denied := pol.Denied(keep); len(denied) > 0 {
			return fmt.Errorf("không được commit file cấm: %s", strings.Join(denied, ", "))
		}
		if len(keep) == 0 {
			return errors.New("không có thay đổi nào để commit")
		}
		a.Args.Files = keep
		a.Target = strings.SplitN(a.Args.Message, "\n", 2)[0]
	case "git_branch":
		a.Args.Branch = strings.TrimSpace(firstNonEmpty(a.Args.Branch, a.Target))
		if a.Args.Branch == "" {
			return errors.New("cần tên nhánh")
		}
		a.Target = a.Args.Branch
	case "git_merge":
		a.Args.Branch = strings.TrimSpace(firstNonEmpty(a.Args.Branch, a.Target))
		if a.Args.Branch == "" {
			return errors.New("cần tên nhánh để gộp")
		}
		a.Target = a.Args.Branch
	case "git_push":
		st, err := gitops.ReadStatus(ctx, root)
		if err != nil {
			return err
		}
		a.Target = st.Branch
	}
	return nil
}

// changedOf keeps of files those the project has changes in.
func changedOf(ctx context.Context, root string, files []string) ([]string, error) {
	st, err := gitops.ReadStatus(ctx, root)
	if err != nil {
		return nil, err
	}
	var keep []string
	for _, c := range st.Changes {
		if slices.Contains(files, c.Path) {
			keep = append(keep, c.Path)
		}
	}
	if len(keep) == 0 {
		return nil, errors.New("không có thay đổi nào của lượt này trong project để commit (diff bị bỏ qua hoặc chưa gộp)")
	}
	return keep, nil
}

func (s *Service) projectPath(ctx context.Context, projectID string) (string, error) {
	p, err := s.store.Repos().Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	if p.Path == "" {
		return "", errors.New("project không gắn thư mục")
	}
	return p.Path, nil
}

// sameFiles compares two commits' file lists as sets, ignoring order.
func sameFiles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// keptThisTurn counts the notes the turn of a already kept on its own.
func (s *Service) keptThisTurn(ctx context.Context, a storage.Action) int {
	if a.RunRef == "" {
		return 0
	}
	list, _ := s.store.Actions().List(ctx, "", "", a.RunRef)
	n := 0
	for _, x := range list {
		if x.Kind == "remember" && x.Status == "done" && x.ID != a.ID {
			n++
		}
	}
	return n
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (s *Service) run(ctx context.Context, a storage.Action) error {
	if a.Kind == "workflow_gate" {
		return nil // approved: the workflow goes on (its coordinator is called back, ADR-084)
	}
	if a.Kind == "remember" {
		m := storage.Memory{ProjectID: a.ProjectID, AgentID: a.TargetID, Text: a.Target, Source: "agent",
			CreatedBy: a.ProposedBy, Topic: a.Args.Topic, Summary: a.Args.Summary}
		if len(a.Args.Replaces) > 0 { // in the place of what it corrects (ADR-215)
			_, err := s.memory.Revise(ctx, a.ProjectID, a.TargetID, []storage.Memory{m}, nil, a.Args.Replaces, "ghi đè: "+a.Reason)
			return err
		}
		_, err := s.memory.Keep(ctx, m)
		return err
	}
	if a.Kind == "forget" {
		n, err := s.memory.Revise(ctx, a.ProjectID, a.TargetID, nil, nil, a.Args.Replaces, "bỏ: "+a.Reason)
		if err == nil && n == 0 {
			return errors.New("không có ghi nhớ nào của bạn với id đó (recall cho thấy id)")
		}
		return err
	}
	if a.Kind == "install_skill" {
		if s.skills == nil {
			return errors.New("không cài skill được ở đây")
		}
		_, err := s.skills.Install(ctx, a.ProjectID, a.TargetID, a.Target, a.Args.Summary)
		return err
	}
	if a.Kind == "run_automation" {
		if s.runner == nil {
			return errors.New("không chạy tự động hóa được ở đây")
		}
		_, err := s.runner.RunAutomation(ctx, a.TargetID)
		return err
	}
	if a.Kind == "create_project" || a.Kind == "import_project" {
		if s.projects == nil || a.Args.NewProject == nil {
			return errors.New("không tạo project được ở đây")
		}
		if by := s.owner(ctx, a); strings.Contains(by, "@") { // its automations get an owner (ADR-194)
			ctx = actor.With(ctx, "human:"+by)
		}
		r, err := s.projects.CreateProject(ctx, *a.Args.NewProject)
		if err != nil {
			return err
		}
		if by := PrincipalOf(ctx); by != "" { // an administrator's run made it (ADR-191)
			return s.inheritAdmin(ctx, r.ID, by, nil)
		}
		return nil
	}
	if a.Kind == "data_delete" {
		c, err := s.store.Collections().Get(ctx, a.ProjectID, a.Args.Record.Collection)
		if err != nil {
			return err
		}
		x, err := s.store.Collections().Record(ctx, c.ID, a.Args.Record.ID)
		if err != nil {
			return err
		}
		return s.store.Collections().Delete(ctx, x, "agent:"+a.ProposedBy)
	}
	if a.Kind == "config_change" {
		if s.config == nil {
			return errors.New("không đổi được cài đặt ở đây")
		}
		// an agent an administrator's run adds works as its makers do (ADR-207),
		// and so does one it makes a lead (ADR-214)
		by, had := PrincipalOf(ctx), map[string]bool{}
		agentChange := by != "" && a.Args.Change != nil && a.Args.Change.Resource == "agent"
		newAgent := agentChange && a.Args.Change.Op == "create"
		if newAgent {
			agents, _ := s.store.Agents().List(ctx, a.ProjectID)
			for _, x := range agents {
				had[x.ID] = true
			}
		}
		if _, err := s.config.ApplyChange(ctx, a); err != nil {
			return err
		}
		if newAgent {
			return s.inheritAdmin(ctx, a.ProjectID, by, had)
		}
		if agentChange && a.Args.Change.Op == "update" {
			return s.leadAdmin(ctx, a.Args.Change.ID, by)
		}
		return nil
	}
	if isAutomation(a.Kind) {
		return s.saveAutomation(ctx, a)
	}
	if isGit(a.Kind) {
		root, err := s.projectPath(ctx, a.ProjectID)
		if err != nil {
			return err
		}
		switch a.Kind {
		case "git_commit":
			files := a.Args.Files
			if a.Args.AfterDiff { // only what reached the project: a file the person left out of the diff is not there
				if files, err = changedOf(ctx, root, files); err != nil {
					return err
				}
			}
			_, err = gitops.Commit(ctx, root, a.Args.Message, files)
		case "git_branch":
			err = gitops.CreateBranch(ctx, root, a.Args.Branch)
		case "git_merge":
			_, _, err = gitops.Merge(ctx, root, a.Args.Branch)
		case "git_push":
			_, err = gitops.Push(ctx, root)
		}
		return err
	}
	if s.ops == nil {
		return errors.New("office không quản lý tiến trình/container")
	}
	switch a.Kind {
	case "run_process":
		return s.ops.Start(ctx, a.TargetID)
	case "restart_process":
		return s.ops.Restart(ctx, a.TargetID)
	case "stop_process":
		return s.ops.Stop(a.TargetID)
	case "start_container":
		return s.ops.ComposeAction(ctx, a.ProjectID, "", "up", a.Target)
	case "restart_container":
		return s.ops.ComposeAction(ctx, a.ProjectID, "", "restart", a.Target)
	case "stop_container":
		return s.ops.ComposeAction(ctx, a.ProjectID, "", "stop", a.Target)
	}
	return ErrKind
}

// checkSend checks a send_message proposal: Target is the chat (its id or its
// dashboard link), the text is not empty, and it comes from a chat's run (the
// person it speaks for is that run's). Target becomes the chat's title.
func (s *Service) checkSend(ctx context.Context, sc Scope, a *storage.Action) error {
	if s.sender == nil {
		return errors.New("không gửi tin sang chat khác được ở đây")
	}
	if sc.ConversationID == "" || sc.JobID == "" {
		return errors.New("chỉ gửi được từ một cuộc chat")
	}
	if j, err := s.store.Jobs().Get(ctx, sc.JobID); err != nil {
		return errors.New("không rõ lượt chat gửi tin này")
	} else if _, ok := storage.SpeaksFor(ctx, s.store, j); !ok {
		return errors.New("chỉ gửi thay được khi lượt chat do một người gửi, hoặc tự động hóa theo lịch có người chịu trách nhiệm (không phải bot hay webhook)")
	}
	if strings.TrimSpace(a.Args.Message) == "" {
		return errors.New("thiếu nội dung tin nhắn")
	}
	if a.Args.Peer != "" { // a project in another office, from the MCP other_office (ADR-169)
		return s.checkPeerSend(ctx, sc, a)
	}
	if s.peers.IsOtherOffice(ctx, a.Target) {
		return errors.New(otherHint)
	}
	// reach: a project the chat may send into: a linked one, or any for the
	// office assistant (ADR-046)
	reach := func(ref string) (storage.Repo, error) {
		if !sc.Office {
			return perm.LinkedProject(ctx, s.store, a.ProjectID, ref)
		}
		p, err := s.store.Repos().Get(ctx, ref)
		if err != nil {
			return p, fmt.Errorf("không có project %q", ref)
		}
		return p, nil
	}
	if a.Args.Project != "" && !sc.Office {
		if _, err := perm.LinkedProject(ctx, s.store, a.ProjectID, a.Args.Project); err != nil && s.peers != nil {
			if _, _, perr := peer.Resolve(ctx, s.store, s.peers, a.ProjectID, a.Args.Project); perr == nil {
				return errors.New(otherHint)
			}
		}
	}
	if a.Args.Project != "" { // a linked project's: id or name
		p, err := reach(a.Args.Project)
		if err != nil {
			return err
		}
		a.Args.Project = p.ID
		if a.Target == "" { // a new chat there
			a.Target, a.TargetID = p.Name+" · chat mới", ""
			return nil
		}
	}
	id := chatRef(a.Target)
	c, err := s.store.Chat().GetConversation(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: cuộc chat %q", ErrTarget, a.Target)
	}
	if c.ID == sc.ConversationID {
		return errors.New("đây là chính cuộc chat đang nói")
	}
	switch {
	case a.Args.Project != "" && c.ProjectID != a.Args.Project:
		return errors.New("cuộc chat đó không thuộc project đã ghi")
	case c.ProjectID != a.ProjectID: // only a linked project's
		p, err := reach(c.ProjectID)
		if err != nil {
			return errors.New("không có cuộc chat này trong project hay project liên kết")
		}
		a.Args.Project = p.ID
	}
	if c.Cleaned != "" {
		return errors.New("cuộc chat đó đã được dọn, không gửi tiếp được")
	}
	a.Target, a.TargetID = firstNonEmpty(c.Title, c.ID), c.ID
	return nil
}

// chatRef is the chat id in a target: the id, or a link …?tab=chat&c=<id>&m=….
func chatRef(target string) string {
	if _, q, ok := strings.Cut(target, "c="); ok {
		target, _, _ = strings.Cut(q, "&")
	}
	return strings.TrimSpace(target)
}

// otherHint points send_to_chat into another office to the MCP other_office.
const otherHint = "office khác: dùng mcp__other_office__send"

// peerRecheck checks again, as it is approved, a send to another office
// (nothing to check for one within this office).
func (s *Service) peerRecheck(ctx context.Context, a *storage.Action) (string, error) {
	if a.Args.Peer == "" {
		return "", nil
	}
	// Target is now its label; the run's administrator access, as it ran
	j, _ := s.store.Jobs().Get(ctx, a.JobID)
	return s.peerAccess(ctx, a, a.TargetID, a.ConversationID, a.ProjectID == assistant.ID(ctx, s.store), j.FullAccess)
}

// checkPeerSend checks a send to a project in another office (peerAccess);
// Target becomes "<office>/<project> · …".
func (s *Service) checkPeerSend(ctx context.Context, sc Scope, a *storage.Action) error {
	label, err := s.peerAccess(ctx, a, chatRef(a.Target), sc.ConversationID, sc.Office, sc.Admin)
	if err != nil {
		return err
	}
	id := chatRef(a.Target)
	a.Target, a.TargetID = label+" · chat mới", ""
	if id != "" {
		a.Target, a.TargetID = label+" · "+id, id
	}
	return nil
}

// peerAccess: a send to another office may go, when proposed and again when
// approved (a link removed meanwhile stops it): the office is still paired; a
// new chat goes to a project it opens to this one that the project links to
// (the assistant: any; an administrator's run: also the other office's
// assistant), a chat on to one this conversation handed work to (or of such a
// project). It fills the chat's project from that hand-over, and
// returns "<office>/<project>" (or the office's name).
func (s *Service) peerAccess(ctx context.Context, a *storage.Action, id, conversationID string, office, admin bool) (string, error) {
	if s.peers == nil {
		return "", errors.New("không có office liên kết")
	}
	p, err := s.peers.Get(ctx, a.Args.Peer)
	if err != nil {
		return "", errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	sent := false
	if id != "" { // "" (a new chat) is never one handed work to
		var prior storage.Action
		if prior, sent = peer.PriorSend(ctx, s.store, conversationID, p.ID, id); sent && a.Args.Project == "" {
			a.Args.Project = prior.Args.Project // the chat's project, as handed over
		}
	}
	reach := peer.Reach(ctx, s.store, s.peers, a.ProjectID, office, admin)
	i := slices.IndexFunc(reach, func(l peer.Link) bool { return l.Peer.ID == p.ID && l.Project.ID == a.Args.Project })
	switch {
	case i >= 0:
		return reach[i].Label(), nil
	case sent, office && id != "":
		return p.Name, nil
	}
	return "", fmt.Errorf("project %q ở office %s chưa liên kết với project này", a.Args.Project, p.Name)
}

// checkRun checks a run_automation proposal (Target = its id).
func (s *Service) checkRun(ctx context.Context, a *storage.Action) error {
	au, err := s.store.Automations().Get(ctx, a.Target)
	if err != nil || au.ProjectID != a.ProjectID {
		return errors.New("không có tự động hóa này trong project")
	}
	a.Target, a.TargetID = au.Name, au.ID
	return nil
}

// unmerged: the conversation (a chat in its own worktree) has diffs still
// waiting to be merged into the project. No conversation: nothing to check.
// pendingTree: the run's worktree has changes not in the project yet, made
// in this turn or waiting in a diff.
func (s *Service) pendingTree(ctx context.Context, sc Scope) bool {
	if files, err := worktree.Changed(ctx, sc.Dir); err == nil && len(files) > 0 {
		return true
	}
	return sc.ConversationID != "" && s.unmerged(ctx, sc.ConversationID)
}

// checkAfterDiff checks a commit held for its turn's diff: its files are the
// worktree's changes (those named, or all of them: never the project's other
// changes, another chat's).
func (s *Service) checkAfterDiff(ctx context.Context, sc Scope, a *storage.Action) error {
	if _, err := s.projectPath(ctx, a.ProjectID); err != nil {
		return err
	}
	a.Args.Message = strings.TrimSpace(firstNonEmpty(a.Args.Message, a.Target))
	if a.Args.Message == "" {
		return errors.New("commit cần message")
	}
	files := a.Args.Files
	if len(files) == 0 {
		files, _ = worktree.Changed(ctx, sc.Dir)
		if ps, err := s.store.Chat().ListPatches(ctx, sc.ConversationID); err == nil {
			for _, p := range ps {
				if p.Status == "pending" || p.Status == "queued" {
					files = append(files, p.Files...)
				}
			}
		}
	}
	var keep []string
	for _, f := range files {
		if f = strings.TrimPrefix(strings.TrimSpace(f), "./"); f != "" && !slices.Contains(keep, f) {
			keep = append(keep, f)
		}
	}
	if denied := perm.LoadPolicy(ctx, s.store, a.ProjectID).Denied(keep); len(denied) > 0 {
		return fmt.Errorf("không được commit file cấm: %s", strings.Join(denied, ", "))
	}
	if len(keep) == 0 {
		return errors.New("không có thay đổi nào để commit")
	}
	a.Args.Files, a.Args.AfterDiff = keep, true
	a.Target = strings.SplitN(a.Args.Message, "\n", 2)[0]
	return nil
}

func (s *Service) unmerged(ctx context.Context, conversationID string) bool {
	if conversationID == "" {
		return true // a run in a worktree of its own with nothing to show for it: keep git away
	}
	ps, err := s.store.Chat().ListPatches(ctx, conversationID)
	if err != nil {
		return true
	}
	for _, p := range ps {
		if p.Status == "pending" || p.Status == "queued" {
			return true
		}
	}
	return false
}

// recordLabel: a record as a card shows it (its collection, its first text field).
func recordLabel(c storage.Collection, x storage.Record) string {
	for _, f := range c.Fields {
		if v, ok := x.Data[f.Name].(string); ok && v != "" {
			if r := []rune(v); len(r) > 60 {
				v = string(r[:60]) + "…"
			}
			return c.Name + ": " + v
		}
	}
	return c.Name + ": " + x.ID
}
