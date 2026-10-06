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
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/gitops"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
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
	"git_push":          "Push lên remote",
	"run_command":       "Chạy lệnh",
	"create_automation": "Tạo tự động hóa",
	"update_automation": "Sửa tự động hóa",
	"config_change":     "Đổi cài đặt",
	"run_automation":    "Chạy tự động hóa",
	"remember":          "Ghi nhớ",
	"mcp_call":          "Gọi tool MCP",
	"send_message":      "Gửi tin sang chat khác",
}

// Sender sends a message to another chat in the name of the person whose
// run proposed it (the chat engine, send_to_chat).
type Sender interface {
	Relay(ctx context.Context, a storage.Action) (string, error)
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
	Add(ctx context.Context, projectID, agentID, text, source, by string) (storage.Memory, error)
	Auto(ctx context.Context, projectID string) bool
}

// SetMemory turns on remember proposals.
func (s *Service) SetMemory(m Memory) { s.memory = m }

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
	Agent          string
	Level          string      // what the agent may do in this run (internal/perm)
	Access         perm.Access // its capabilities and commands in this run
	// Dir is the run's own worktree ("" = the project folder): commands run
	// there, and the project's allowed commands run without asking since
	// nothing there touches the project until a person merges it.
	Dir string
}

// Service proposes and decides actions.
type Service struct {
	store  storage.Store
	ops    *ops.Manager
	config ConfigApplier
	runner Runner
	memory Memory
	direct AutoApprover
	mcp    MCPCaller
	sender Sender
	// deciding holds the actions being decided now: the dashboard and a bot
	// deciding one at once must not both run it
	deciding sync.Map
}

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
	model, err := s.store.OrgModels().GetForRepo(ctx, projectID)
	if err != nil {
		return "", err
	}
	agents, err := s.store.Agents().List(ctx, model.ID)
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
	} else if kind == "send_message" { // Target: the chat it goes to, Args.Message: what it says
		if err := s.checkSend(ctx, sc, &a); err != nil {
			return a, err
		}
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
		if sc.Dir != "" && s.unmerged(ctx, sc.ConversationID) {
			return a, errors.New("thay đổi trong worktree riêng của cuộc chat chưa được gộp vào project: nhờ người dùng duyệt diff đang chờ (trên Discord/Telegram: /pending rồi /approve <số>; hoặc Tổng quan → Cần xử lý), rồi đề xuất lại commit/nhánh/push")
		}
		if err := s.checkGit(ctx, &a); err != nil {
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
				if x.Status == "pending" && x.Kind == a.Kind && x.Target == a.Target {
					return x, nil
				}
			}
		}
	}
	a, err := s.store.Actions().Create(ctx, a)
	if err != nil {
		return a, err
	}
	// within the agent's permission package and the project's allow lists,
	// office carries it out right away
	if s.autoAllowed(ctx, a, sc.Access) {
		done, err := s.Decide(ctx, a.ID, true, "auto:"+sc.Agent+" ("+perm.Label(sc.Access.Level)+")")
		s.auditAuto(ctx, sc, done, err)
		return done, err
	}
	// a bot's chat in direct mode: approved now, so the agent gets the result
	// in this turn and goes on (not after its answer, waiting to be asked again)
	if s.direct != nil && a.JobID != "" {
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
func (s *Service) autoAllowed(ctx context.Context, a storage.Action, acc perm.Access) bool {
	switch a.Kind {
	case "remember": // the project keeps its agents' notes on its own, or a person reads each
		return s.memory != nil && s.memory.Auto(ctx, a.ProjectID)
	case "create_automation", "update_automation", "config_change", "run_automation":
		return false // code that runs unattended, or settings: a person always decides
	case "mcp_call":
		return false // the gateway already let through what the agent may call itself
	case "send_message": // never on and on: what a relayed message started asks a person
		return acc.Can(perm.CapChatSend) && s.sender != nil && !s.sender.Relayed(ctx, a.ConversationID)
	case "git_commit":
		return acc.Can(perm.CapCommit)
	case "git_branch":
		return acc.Can(perm.CapBranch)
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
	if _, busy := s.deciding.LoadOrStore(id, struct{}{}); busy {
		a, err := s.store.Actions().Get(ctx, id)
		if err != nil {
			return a, err
		}
		return a, ErrDecided
	}
	defer s.deciding.Delete(id)
	a, err := s.store.Actions().Get(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Status != "pending" {
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

// DecideAlways approves a proposed command, then lets its agent run the like
// of it on its own from now on (perm.SuggestPattern, perm.AllowAlways). Risky
// commands and wrappers are refused before anything runs. An error means
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
	case "git_push":
		st, err := gitops.ReadStatus(ctx, root)
		if err != nil {
			return err
		}
		a.Target = st.Branch
	}
	return nil
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

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (s *Service) run(ctx context.Context, a storage.Action) error {
	if a.Kind == "remember" {
		_, err := s.memory.Add(ctx, a.ProjectID, a.TargetID, a.Target, "agent", a.ProposedBy)
		return err
	}
	if a.Kind == "send_message" {
		if s.sender == nil {
			return errors.New("không gửi tin được ở đây")
		}
		_, err := s.sender.Relay(ctx, a)
		return err
	}
	if a.Kind == "run_automation" {
		if s.runner == nil {
			return errors.New("không chạy tự động hóa được ở đây")
		}
		_, err := s.runner.RunAutomation(ctx, a.TargetID)
		return err
	}
	if a.Kind == "config_change" {
		if s.config == nil {
			return errors.New("không đổi được cài đặt ở đây")
		}
		_, err := s.config.ApplyChange(ctx, a)
		return err
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
			_, err = gitops.Commit(ctx, root, a.Args.Message, a.Args.Files)
		case "git_branch":
			err = gitops.CreateBranch(ctx, root, a.Args.Branch)
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
	if j, err := s.store.Jobs().Get(ctx, sc.JobID); err != nil || !strings.HasPrefix(j.CreatedBy, "human:") {
		return errors.New("chỉ gửi thay được khi lượt chat do một người gửi (không phải bot hay tự động hóa)")
	}
	if strings.TrimSpace(a.Args.Message) == "" {
		return errors.New("thiếu nội dung tin nhắn")
	}
	id := a.Target
	if _, q, ok := strings.Cut(id, "c="); ok { // a link: …?tab=chat&c=<id>&m=…
		id, _, _ = strings.Cut(q, "&")
	}
	c, err := s.store.Chat().GetConversation(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: cuộc chat %q", ErrTarget, a.Target)
	}
	if c.ID == sc.ConversationID {
		return errors.New("đây là chính cuộc chat đang nói")
	}
	if c.Cleaned != "" {
		return errors.New("cuộc chat đó đã được dọn, không gửi tiếp được")
	}
	a.Target, a.TargetID = firstNonEmpty(c.Title, c.ID), c.ID
	return nil
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
func (s *Service) unmerged(ctx context.Context, conversationID string) bool {
	if conversationID == "" {
		return true // a run in a worktree of its own with nothing to show for it: keep git away
	}
	ps, err := s.store.Chat().ListPatches(ctx, conversationID)
	if err != nil {
		return true
	}
	for _, p := range ps {
		if p.Status == "pending" {
			return true
		}
	}
	return false
}
