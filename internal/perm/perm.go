// Package perm defines what agents may do, as nested packages: each level
// includes everything below it. What an agent may do in a run is the lowest
// of its own level, the mode chosen for the chat/task, and the project's cap.
package perm

import (
	"context"
	"path"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// Levels, lowest first.
const (
	Read    = "read"    // read code, logs, operations state
	Propose = "propose" // + propose diffs and operations, a person approves
	Check   = "check"   // + run the project's allowed check commands on its own
	Edit    = "edit"    // + apply clean diffs on its own (never denied files)
	Operate = "operate" // + run/restart allowed processes and containers on its own
)

// Info describes a level for the dashboard.
type Info struct {
	Level       string `json:"level"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// All lists the levels in order.
var All = []Info{
	{Read, "Chỉ đọc", "Đọc code, log, trạng thái vận hành"},
	{Propose, "Đề xuất", "Đề xuất sửa code và thao tác, người duyệt mới làm"},
	{Check, "Tự kiểm tra", "Tự chạy lệnh kiểm tra được phép (test, typecheck, lint, build)"},
	{Edit, "Tự sửa code", "Tự áp diff áp được sạch, trừ file cấm"},
	{Operate, "Vận hành", "Tự chạy lại tiến trình và container được phép"},
}

// Rank orders levels; unknown ones rank as Propose (the safe default).
func Rank(level string) int {
	for i, x := range All {
		if x.Level == level {
			return i
		}
	}
	return 1
}

// Valid reports a known level.
func Valid(level string) bool {
	for _, x := range All {
		if x.Level == level {
			return true
		}
	}
	return false
}

// Label is the Vietnamese name of a level.
func Label(level string) string { return All[Rank(level)].Label }

// Min returns the lowest of the given levels (empty ones are ignored).
func Min(levels ...string) string {
	out := Operate
	for _, l := range levels {
		if l != "" && Rank(l) < Rank(out) {
			out = l
		}
	}
	return out
}

// AtLeast reports whether level includes want.
func AtLeast(level, want string) bool { return Rank(level) >= Rank(want) }

// Agent returns an agent's own level: its package, or with its own picks of
// capabilities, the lowest level that includes all of them.
func Agent(a storage.Agent) string {
	if a.Permissions.Caps != nil {
		out := Read
		for _, id := range *a.Permissions.Caps {
			if c, ok := capInfo(id); ok && Rank(c.Min) > Rank(out) {
				out = c.Min
			}
		}
		return out
	}
	if Valid(a.Permissions.Level) {
		return a.Permissions.Level
	}
	if a.Permissions.ReadOnly {
		return Read
	}
	return Propose
}

// Policy is what a project offers its agents (which of it each agent may
// use is the agent's own permissions, see Resolve) and what no agent may do.
type Policy struct {
	MaxLevel  string   `json:"max_level"`  // no longer a cap: always Operate (the chat/task mode is the limit)
	Packs     []Pack   `json:"packs"`      // the project's own command packs
	DenyPaths []string `json:"deny_paths"` // files no agent may change, at any level
	// WorktreeLinks are more ignored folders to link into worktrees, besides
	// node_modules/.venv (relative to the project).
	WorktreeLinks []string `json:"worktree_links"`

	// Worked out when loaded, not stored:
	Catalog []string `json:"-"` // every command of the project's packs
	Safe    []string `json:"-"` // the check/read ones: an agent's default, run on their own from read
	Jobs    []string `json:"-"` // ids of the project's check processes (kind job): an agent's default
}

// Where a chat or task changes code: in its own git worktree, merged after a
// person approves (the default), or right in the project folder like the CLI.
const (
	EditWorktree = "worktree"
	EditDirect   = "direct"
)

// DefaultPolicy keeps agents at "propose" and protects secrets.
func DefaultPolicy() Policy {
	return Policy{MaxLevel: Operate, Packs: []Pack{},
		DenyPaths: []string{".env", ".env.*", "**/.env", "**/*.pem", "**/*.key"}, WorktreeLinks: []string{}}
}

func policyKey(projectID string) string { return "policy:" + projectID }

// LoadPolicy reads a project's policy (the default when none is saved).
func LoadPolicy(ctx context.Context, st storage.Store, projectID string) Policy {
	p := DefaultPolicy()
	if ok, err := st.Settings().Get(ctx, policyKey(projectID), &p); err != nil || !ok {
		p = DefaultPolicy()
	}
	p.MaxLevel = Operate // the mode picked per chat/task is the limit
	if p.Packs == nil {
		p.Packs = []Pack{}
	}
	if p.WorktreeLinks == nil {
		p.WorktreeLinks = []string{}
	}
	root := ""
	if r, err := st.Repos().Get(ctx, projectID); err == nil {
		root = r.Path
	}
	p.Catalog, p.Safe = Catalog(ProjectPacks(root, p.Packs))
	p.Jobs = []string{}
	if procs, err := st.Processes().List(ctx, projectID); err == nil {
		for _, x := range procs {
			if x.Kind == "job" {
				p.Jobs = append(p.Jobs, x.ID)
			}
		}
	}
	return p
}

// SavePolicy stores a project's policy.
func SavePolicy(ctx context.Context, st storage.Store, projectID string, p Policy) error {
	return st.Settings().Set(ctx, policyKey(projectID), p)
}

// Denied returns the files of a diff that the policy forbids.
func (p Policy) Denied(files []string) []string {
	var out []string
	for _, f := range files {
		f = strings.TrimPrefix(f, "./")
		for _, pat := range p.DenyPaths {
			if matchPath(strings.TrimSpace(pat), f) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// matchPath supports shell globs, "**/" for any folder depth, and a trailing
// "/" for a whole folder.
func matchPath(pat, file string) bool {
	if pat == "" {
		return false
	}
	if strings.HasSuffix(pat, "/") {
		return strings.HasPrefix(file, pat) || strings.Contains(file, "/"+pat)
	}
	if rest, ok := strings.CutPrefix(pat, "**/"); ok {
		if ok, _ := path.Match(rest, path.Base(file)); ok {
			return true
		}
		for i := range file {
			if file[i] == '/' {
				if ok, _ := path.Match(rest, file[i+1:]); ok {
					return true
				}
			}
		}
		return false
	}
	ok, _ := path.Match(pat, file)
	return ok
}

// Effective is what an agent may do in one run.
func Effective(a storage.Agent, mode string, p Policy) string {
	return Min(Agent(a), mode, p.MaxLevel)
}
