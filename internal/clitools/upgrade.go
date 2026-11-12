package clitools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/cliuse"
	"github.com/tung1998/agent-office/internal/llm"
)

// An update of a CLI that runs use (ADR-259): it waits until no run has the
// CLI running (when idle), holds new runs back while npm or Homebrew replace
// its files, checks the CLI starts after, and puts the version it had back
// when the new one does not start.

// UpgradeView is an update of a CLI as the dashboard and the bots see it.
type UpgradeView struct {
	Tool string `json:"tool"`
	// State: waiting (for its runs to end) | updating | checking |
	// rolling_back | done | rolled_back | failed | gave_up | cancelled
	State   string `json:"state"`
	When    string `json:"when"` // idle | now
	By      string `json:"by,omitempty"`
	Waiting int    `json:"waiting"` // runs still using the CLI
	// Draining: new runs are held back while those running end (a CLI
	// never idle on its own, UpgradeDrainAfter)
	Draining bool       `json:"draining,omitempty"`
	From     string     `json:"from,omitempty"`   // the version before
	Target   string     `json:"target,omitempty"` // the newest version known when it started
	To       string     `json:"to,omitempty"`     // the version after
	Note     string     `json:"note,omitempty"`
	JobID    string     `json:"job_id,omitempty"`
	Started  time.Time  `json:"started_at"`
	Ended    *time.Time `json:"ended_at,omitempty"`
}

// Active says whether the update has not ended yet.
func (u UpgradeView) Active() bool {
	switch u.State {
	case "waiting", "updating", "checking", "rolling_back":
		return true
	}
	return false
}

// Summary says in a line how it went (for the bots and the alert chat).
func (u UpgradeView) Summary(name string) string {
	switch u.State {
	case "waiting":
		if u.Draining {
			return fmt.Sprintf("%s: lượt mới tạm chờ, đợi %d lượt đang chạy xong rồi cập nhật", name, u.Waiting)
		}
		return fmt.Sprintf("%s: chờ %d lượt đang dùng chạy xong rồi cập nhật", name, u.Waiting)
	case "updating", "checking", "rolling_back":
		return name + ": đang cập nhật"
	case "done":
		if u.From != "" && u.To != "" && u.From != u.To {
			return fmt.Sprintf("%s: đã cập nhật %s → %s", name, u.From, u.To)
		}
		return fmt.Sprintf("%s: đã cập nhật (bản %s)", name, cmpOr(u.To, u.From, "?"))
	case "rolled_back":
		return fmt.Sprintf("%s: bản mới không chạy được, đã quay về bản %s. %s", name, u.From, u.Note)
	case "gave_up":
		return fmt.Sprintf("%s: chờ quá lâu mà vẫn có lượt dùng, bỏ cập nhật", name)
	case "cancelled":
		return name + ": đã hủy cập nhật"
	}
	return fmt.Sprintf("%s: cập nhật lỗi: %s", name, u.Note)
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// upgrade is one update in progress.
type upgrade struct {
	view   UpgradeView
	cancel context.CancelFunc
}

// UpgradeMaxWait is how long an update when idle waits for its runs.
const UpgradeMaxWait = 12 * time.Hour

// A CLI whose runs follow each other is never idle: an update when idle that
// waited UpgradeDrainAfter holds new runs back and waits only for those
// running, at most UpgradeDrainMax (then the runs go again and it waits
// UpgradeDrainAfter more before the next try).
const (
	UpgradeDrainAfter = 30 * time.Minute
	UpgradeDrainMax   = 10 * time.Minute
)

// ErrUpgrading: an update of the tool is already on its way.
var ErrUpgrading = errors.New("công cụ này đang được cập nhật")

// SetAfterUpdate is called once a tool's files changed and runs may use it
// again: it checks the connections on the tool (their broken mark goes, as
// Kết nối AI's Kiểm tra) and says why the tool's install is broken ("" =
// it is not; a signed-out account or a network error is not the install's).
// A broken install is put back to the version it had (ADR-259).
func (m *Manager) SetAfterUpdate(fn func(ctx context.Context, tool string) string) {
	m.afterUpdate = fn
}

// SetOnUpgradeEnd is told how each update ended.
func (m *Manager) SetOnUpgradeEnd(fn func(UpgradeView)) { m.onUpgradeEnd = fn }

// UpdateWhenIdle updates tool id once no run has its CLI running (at most
// UpgradeMaxWait); new runs wait meanwhile only while its files change.
func (m *Manager) UpdateWhenIdle(id, by string) (UpgradeView, error) {
	return m.beginUpgrade(id, by, "idle")
}

// UpdateNow updates tool id now: runs using it go on (they may fail if the
// files go under them), new ones wait while it is replaced.
func (m *Manager) UpdateNow(id, by string) (UpgradeView, error) {
	return m.beginUpgrade(id, by, "now")
}

// Upgrade is tool id's current or last update.
func (m *Manager) Upgrade(id string) (UpgradeView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.upgrades[id]
	if !ok {
		return UpgradeView{}, false
	}
	return u.view, true
}

// CancelUpgrade drops tool id's update while it still waits for its runs.
func (m *Manager) CancelUpgrade(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.upgrades[id]
	if !ok || u.view.State != "waiting" {
		return errors.New("không có cập nhật nào đang chờ cho công cụ này")
	}
	u.cancel()
	return nil
}

func (m *Manager) beginUpgrade(id, by, when string) (UpgradeView, error) {
	t, ok := m.find(id)
	if !ok {
		return UpgradeView{}, ErrUnknownTool
	}
	targets, unknown := m.targets(context.Background(), t)
	if len(targets) == 0 {
		if unknown > 0 {
			return UpgradeView{}, ErrUnknownInstall
		}
		return UpgradeView{}, ErrNotInstalled
	}
	m.mu.Lock()
	if m.upgrades == nil {
		m.upgrades = map[string]*upgrade{}
	}
	if u := m.upgrades[id]; u != nil && u.view.Active() {
		m.mu.Unlock()
		return u.view, ErrUpgrading
	}
	if j := m.last[id]; j != nil && j.Snapshot().State == "running" {
		m.mu.Unlock()
		return UpgradeView{}, ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &upgrade{view: UpgradeView{Tool: id, State: "waiting", When: when, By: by, Waiting: cliuse.InUse(id), Started: m.clock().UTC()}, cancel: cancel}
	if when == "now" {
		u.view.State = "updating"
	}
	m.upgrades[id] = u
	v := u.view
	m.mu.Unlock()
	go m.runUpgrade(ctx, t, targets, u)
	return v, nil
}

func (m *Manager) set(u *upgrade, fn func(v *UpgradeView)) {
	m.mu.Lock()
	fn(&u.view)
	m.mu.Unlock()
}

func (m *Manager) runUpgrade(ctx context.Context, t Tool, targets []target, u *upgrade) {
	defer u.cancel()
	poll := m.upPoll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	maxWait := m.upMaxWait
	if maxWait <= 0 {
		maxWait = UpgradeMaxWait
	}
	// release lets the CLI's runs go; it is called before the update says
	// it ended, so a next update (now) never shares a hold that is about to
	// go while this one's end is told
	release := func() {}
	end := func(state, note string) {
		release()
		now := m.clock().UTC()
		m.set(u, func(v *UpgradeView) { v.State, v.Note, v.Ended = state, note, &now })
		m.forgetLatest(t.ID)
		if m.onUpgradeEnd != nil {
			m.mu.Lock()
			v := u.view
			m.mu.Unlock()
			m.onUpgradeEnd(v)
		}
	}
	if u.view.When == "idle" {
		grace, drainMax := m.upGrace, m.upDrainMax
		if grace <= 0 {
			grace = UpgradeDrainAfter
		}
		if drainMax <= 0 {
			drainMax = UpgradeDrainMax
		}
		// the CLI is taken only when no run has it running, in one step:
		// a run never starts between the count and the hold
		deadline := m.clock().Add(maxWait)
		drainAt, drainUntil, draining := m.clock().Add(grace), time.Time{}, false
		for {
			// taking the CLI and leaving "waiting" happen under m.mu, where a
			// cancel is decided too: one cancelled never takes it
			m.mu.Lock()
			if ctx.Err() != nil {
				m.mu.Unlock()
				end("cancelled", "")
				return
			}
			var ok bool
			if draining {
				// new runs are held already: the CLI is the update's once the
				// runs that were going ended
				ok = cliuse.InUse(t.ID) == 0
			} else {
				var r func()
				if r, ok = cliuse.TryHold(t.ID); ok {
					release = r
				}
			}
			if ok {
				u.view.State, u.view.Waiting, u.view.Draining = "updating", 0, false
			}
			m.mu.Unlock()
			if ok {
				break
			}
			now := m.clock()
			switch {
			case draining && !now.Before(drainUntil): // the runs going did not end: let new ones go again
				release()
				release, draining, drainAt = func() {}, false, now.Add(grace)
			case !draining && !now.Before(drainAt) && now.Before(deadline):
				release, draining, drainUntil = cliuse.Hold(t.ID), true, now.Add(drainMax)
			}
			n := cliuse.InUse(t.ID)
			m.set(u, func(v *UpgradeView) { v.Waiting, v.Draining = n, draining })
			if !now.Before(deadline) {
				end("gave_up", fmt.Sprintf("vẫn còn %d lượt dùng sau %s", n, maxWait))
				return
			}
			select {
			case <-ctx.Done():
				end("cancelled", "")
				return
			case <-time.After(poll):
			}
		}
	} else {
		release = cliuse.Hold(t.ID) // runs already on it go on; new ones wait
	}
	// the hold stays on until the update ends (rollback included): the
	// connections' check runs the CLI exempt from it, every other run waits
	defer func() { release() }()
	m.set(u, func(v *UpgradeView) { v.State, v.Waiting = "updating", 0 })
	target := m.latestKnown(t.ID)
	m.set(u, func(v *UpgradeView) { v.Target = target })
	// every copy the connections run, one after the other under the same
	// hold (ADR-279); the update says how the worst of them went
	rank := map[string]int{"done": 0, "rolled_back": 1, "failed": 2}
	state, notes := "done", []string(nil)
	for i, tg := range targets {
		st, note := m.upgradeOne(t, tg, u, i == 0)
		if rank[st] > rank[state] {
			state = st
		}
		if note != "" {
			if len(targets) > 1 {
				note = tg.path + ": " + note
			}
			notes = append(notes, note)
		}
	}
	end(state, strings.Join(notes, "; "))
}

// upgradeOne updates the copy at tg and says how it went (done |
// rolled_back | failed) and why; the first copy gives the update its
// versions before and after.
func (m *Manager) upgradeOne(t Tool, tg target, u *upgrade, first bool) (state, note string) {
	m.set(u, func(v *UpgradeView) { v.State = "updating" })
	from := m.versionAt(tg.path)
	if first {
		m.set(u, func(v *UpgradeView) { v.From = from })
	}
	j, err := m.start(t.ID, t.ID, "update", tg.argv, 15*time.Minute, term{}, "HOMEBREW_NO_ASK=1", "HOMEBREW_ASK=")
	if err != nil {
		return "failed", err.Error()
	}
	m.set(u, func(v *UpgradeView) { v.JobID = j.ID })
	j.Wait()
	jv := j.Snapshot()
	m.set(u, func(v *UpgradeView) { v.State = "checking" })
	why := m.brokenAt(t, tg.path) // the new version does not start
	if why == "" && tg.dflt {
		why = m.recheck(t.ID)
	}
	if why != "" {
		return m.rollback(t, tg, u, from, why, first)
	}
	to := m.versionAt(tg.path)
	if first {
		m.set(u, func(v *UpgradeView) { v.To = to })
	}
	m.mu.Lock()
	if to != "" && m.bad[t.ID] == to { // the version put back now starts: no longer bad
		delete(m.bad, t.ID)
	}
	m.mu.Unlock()
	// the update command ran and the CLI still starts, but on the same
	// version as before: another install of the same command, earlier on
	// PATH, likely shadows the one just updated (npm reaching a different
	// global prefix than the one lookPath finds, a stray Homebrew copy
	// ahead of a native install, …) — not "done", the broken copy may still
	// be the one every run actually uses
	if tg.dflt && jv.State == "succeeded" && to != "" && to == from {
		if dups := m.Duplicates(tg.path); len(dups) > 0 {
			return "failed", fmt.Sprintf("đã chạy lệnh cập nhật nhưng %s vẫn ở bản %s: có bản khác đứng trước trên PATH, có thể che bản vừa cập nhật: %s",
				t.Bin, to, strings.Join(dups, ", "))
		}
	}
	if jv.State != "succeeded" { // the command failed but the CLI still starts: the old one is kept
		return "failed", cmpOr(jv.Error, "lệnh cập nhật lỗi") + " (CLI vẫn chạy được)"
	}
	return "done", ""
}

// recheck checks the connections on the tool (SetAfterUpdate): why its
// install is broken, "" when it is not.
func (m *Manager) recheck(tool string) string {
	if m.afterUpdate == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(cliuse.Exempt(context.Background()), 3*time.Minute)
	defer cancel()
	return m.afterUpdate(ctx, tool)
}

// latestKnown is the newest version kept for id ("" = none read yet).
func (m *Manager) latestKnown(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latest[id].version
}

// rollback puts version from back after an update that left the copy at
// tg broken: npm installs that exact version (into the copy's own prefix),
// under the update's hold (no run started since the files changed);
// Homebrew and the official installer cannot pick a version, so the person
// is told how to fix it.
func (m *Manager) rollback(t Tool, tg target, u *upgrade, from, why string, first bool) (state, note string) {
	m.mu.Lock()
	target := u.view.Target
	if from != "" && target != "" {
		// a broken version: Watch does not update to it again on its own.
		// An install that did not start before (from "") was not broken by
		// the new version: it is not marked
		if m.bad == nil {
			m.bad = map[string]string{}
		}
		m.bad[t.ID] = target
	}
	m.mu.Unlock()
	p := pkgs[t.ID]
	if tg.meth.ID != "npm" || p.npm == "" || from == "" {
		return "failed", fmt.Sprintf("bản mới không chạy được (%s); hãy cài lại: %s", why, cmpOr(manualFix(t), "cài lại CLI"))
	}
	m.set(u, func(v *UpgradeView) { v.State = "rolling_back" })
	argv := []string{"npm", "install", "-g", p.npm + "@" + from}
	if !tg.dflt {
		if x := npmAt(tg.path, p.npm, from); x != nil {
			argv = x
		}
	}
	j, err := m.start(t.ID, t.ID, "update", argv, 15*time.Minute, term{})
	if err != nil {
		return "failed", fmt.Sprintf("bản mới không chạy được (%s), quay về bản %s lỗi: %v", why, from, err)
	}
	j.Wait()
	still := m.brokenAt(t, tg.path)
	if still == "" && tg.dflt {
		still = m.recheck(t.ID)
	}
	if still != "" {
		return "failed", fmt.Sprintf("bản mới không chạy được (%s), quay về bản %s vẫn lỗi: %s", why, from, still)
	}
	if first {
		to := m.versionAt(tg.path)
		m.set(u, func(v *UpgradeView) { v.To = to })
	}
	return "rolled_back", "Lỗi của bản mới: " + why
}

func manualFix(t Tool) string {
	for _, meth := range t.methods {
		if meth.Recommended {
			return meth.Command
		}
	}
	return ""
}

// InstallFixFor is the command that reinstalls the CLI whose command is
// bin's last path element, the way bin itself is really installed —
// updateMethod, read from its own path, the same check an update uses — so
// an npm-installed binary gets the npm command and a Homebrew one gets the
// brew command, never a guess from a static "recommended" flag; it falls
// back to the Method marked Recommended only when bin's path does not say
// (bin not found, or installed some other way). Wired into internal/llm
// (SetInstallFix) so a broken CLI's error names the fix that reaches the
// actual broken binary: naming the wrong install method installs an unused
// second copy and leaves the real one still broken.
func (m *Manager) InstallFixFor(bin string) string {
	t, ok := m.findByBin(filepath.Base(bin))
	if !ok {
		return ""
	}
	if meth, ok := updateMethod(t, bin); ok {
		return meth.Command
	}
	return manualFix(t)
}

// version is the x.y.z the installed tool says ("" when it does not start).
func (m *Manager) version(t Tool) string { return m.versionAt(m.lookPath(t.Bin)) }

// versionAt is the x.y.z the copy at path says ("" when it does not start).
func (m *Manager) versionAt(path string) string {
	if path == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := runOut(ctx, m.Env(), path, "--version")
	if err != nil {
		return ""
	}
	return VersionOf(out)
}

// broken says why the installed tool cannot start ("" = it starts): gone
// from PATH, its --version failing, or npm's wrapper finding no binary.
func (m *Manager) broken(t Tool) string { return m.brokenAt(t, m.lookPath(t.Bin)) }

// brokenAt is broken for the copy at path.
func (m *Manager) brokenAt(t Tool, path string) string {
	if path == "" {
		return "không còn lệnh " + t.Bin
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runOut(ctx, m.Env(), path, "--version")
	switch {
	case err != nil && llm.CLIBroken(out):
		return firstLine(out)
	case err != nil:
		return strings.TrimSpace(err.Error() + ": " + firstLine(out))
	case VersionOf(out) == "":
		return "không đọc được phiên bản: " + firstLine(out)
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}
