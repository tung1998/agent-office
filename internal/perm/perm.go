// Package perm defines what agents may do, as nested packages: each level
// includes everything below it. What an agent may do in a run is the lowest
// of its own level, the mode chosen for the chat/task, and the project's cap.
package perm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// QuickCheck: after an agent edits a file, office checks it at once
	// (gofmt/vet, JSON, YAML) and tells the agent what to fix (ADR-133).
	QuickCheck bool `json:"quick_check"`
	// QuickChecks: the project's own quick checks by file ending, one a line
	// (".ts .vue: npx eslint {file}"), after the built-in ones (ADR-135).
	QuickChecks string `json:"quick_checks"`
	// MergeChecks: what office runs on a chat's diff before merging it on
	// its own, one a line ("dashboard/ .vue: cmd" for those files only);
	// empty = the build checks the project's files suggest (ADR-222).
	MergeChecks string `json:"merge_checks"`
	// LinkedProjects: ids of the projects this project's agents may open a
	// chat in, send to and read (send_to_chat, read_link, search_history).
	// One way: the other project links back on its own.
	LinkedProjects []string `json:"linked_projects"`
	// Experts: ids of this project's agents the projects linking to it may
	// ask as experts (ask_expert, ADR-177); nil = its default agent, empty =
	// none.
	Experts *[]string `json:"experts,omitempty"`

	// Worked out when loaded, not stored:
	Catalog []string `json:"-"` // every command of the project's packs
	Safe    []string `json:"-"` // the check/read ones: an agent's default, run on their own from read
	Jobs    []string `json:"-"` // ids of the project's check processes (kind job): an agent's default
	Build   bool     `json:"-"` // the project has build/test commands (HasBuild): the prompt asks to run them
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
		DenyPaths: []string{".env", "**/.env.*", "**/.env", "**/*.pem", "**/*.key"}, WorktreeLinks: []string{}, LinkedProjects: []string{}, QuickCheck: true}
}

func policyKey(projectID string) string { return "policy:" + projectID }

// LoadPolicy reads a project's policy (the default when none is saved).
func LoadPolicy(ctx context.Context, st storage.Store, projectID string) Policy {
	p, _ := LoadPolicyRaw(ctx, st, projectID)
	return p
}

// LoadPolicyRaw is LoadPolicy plus the raw JSON currently stored for the
// project (or "" when none is), to pass to SavePolicyIf for a real
// compare-and-swap save. A stored policy that is not valid JSON reads as the
// default but keeps its raw value, so a save can still overwrite it.
func LoadPolicyRaw(ctx context.Context, st storage.Store, projectID string) (Policy, string) {
	p := DefaultPolicy()
	raw, ok, err := st.Settings().GetRaw(ctx, policyKey(projectID))
	if err != nil || !ok {
		raw = ""
	} else if json.Unmarshal([]byte(raw), &p) != nil {
		p = DefaultPolicy()
	}
	p.MaxLevel = Operate // the mode picked per chat/task is the limit
	if p.Packs == nil {
		p.Packs = []Pack{}
	}
	if p.WorktreeLinks == nil {
		p.WorktreeLinks = []string{}
	}
	if p.LinkedProjects == nil {
		p.LinkedProjects = []string{}
	}
	for i, pat := range p.DenyPaths {
		if pat == ".env.*" {
			p.DenyPaths[i] = "**/.env.*"
		}
	}
	root := ""
	if r, err := st.Repos().Get(ctx, projectID); err == nil {
		root = r.Path
	}
	packs := ProjectPacks(root, p.Packs)
	p.Catalog, p.Safe = Catalog(packs)
	p.Build = HasBuild(packs)
	p.Jobs = []string{}
	if procs, err := st.Processes().List(ctx, projectID); err == nil {
		for _, x := range procs {
			if x.Kind == "job" {
				p.Jobs = append(p.Jobs, x.ID)
			}
		}
	}
	return p, raw
}

// ExpertIDs are the agents a project lends as experts (Policy.Experts),
// defaultAgent when it never chose.
func (p Policy) ExpertIDs(defaultAgent string) []string {
	if p.Experts == nil {
		if defaultAgent == "" {
			return nil
		}
		return []string{defaultAgent}
	}
	return *p.Experts
}

// SavePolicy stores a project's policy.
func SavePolicy(ctx context.Context, st storage.Store, projectID string, p Policy) error {
	return st.Settings().Set(ctx, policyKey(projectID), p)
}

// LinkedProject is the project ref (id or name, any case) among those
// projectID links to in its policy (Policy.LinkedProjects).
func LinkedProject(ctx context.Context, st storage.Store, projectID, ref string) (storage.Repo, error) {
	ref = strings.TrimSpace(ref)
	var names []string
	for _, id := range LoadPolicy(ctx, st, projectID).LinkedProjects {
		p, err := st.Repos().Get(ctx, id)
		if err != nil {
			continue
		}
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return storage.Repo{}, errors.New("project này chưa liên kết project nào (Cài đặt project → Quyền & lệnh → Project liên kết)")
	}
	return storage.Repo{}, fmt.Errorf("%q không phải project liên kết (có: %s)", ref, strings.Join(names, ", "))
}

// SavePolicyIf stores p only if the stored policy is still the raw JSON
// previously returned by LoadPolicyRaw, failing with storage.ErrConflict
// when another write landed in between (real compare-and-swap, unlike the
// advisory version-hash check the API layer also does for user-facing 409s).
func SavePolicyIf(ctx context.Context, st storage.Store, projectID, expectedRaw string, p Policy) error {
	return st.Settings().SetIf(ctx, policyKey(projectID), expectedRaw, p)
}

// policyRetries bounds UpdatePolicy's re-read-and-retry on a conflicting save.
const policyRetries = 5

// UpdatePolicy changes the stored policy with compare-and-swap: it re-reads
// and retries when another save lands in between, so a background write (an
// "always allow", project setup) never silently drops an admin's edit.
// change returns false to leave the policy as is.
func UpdatePolicy(ctx context.Context, st storage.Store, projectID string, change func(*Policy) (bool, error)) (Policy, error) {
	for i := 0; ; i++ {
		p, raw := LoadPolicyRaw(ctx, st, projectID)
		ok, err := change(&p)
		if err != nil || !ok {
			return p, err
		}
		err = SavePolicyIf(ctx, st, projectID, raw, p)
		if !errors.Is(err, storage.ErrConflict) || i == policyRetries-1 {
			return p, err
		}
	}
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
// "/" for a whole folder. Compared case-insensitively: a deny-listed file may
// have been deleted in the diff being checked, so it can't be resolved and
// stat'd to compare by inode like extra dirs are — and for a deny-list, a
// false-positive match is far cheaper than a missed one on a case-insensitive
// filesystem (e.g. "secret.PEM" vs "*.pem" on macOS APFS).
func matchPath(pat, file string) bool {
	if pat == "" {
		return false
	}
	pat = strings.ToLower(pat)
	file = strings.ToLower(file)
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
	if ok, _ := path.Match(pat, file); ok {
		return true
	}
	if !strings.Contains(pat, "/") {
		if ok, _ := path.Match(pat, path.Base(file)); ok {
			return true
		}
	}
	return false
}

// Effective is what an agent may do in one run.
func Effective(a storage.Agent, mode string, p Policy) string {
	return Min(Agent(a), mode, p.MaxLevel)
}
