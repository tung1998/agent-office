package clitools

import (
	"context"
	"errors"
	"fmt"
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
	State   string     `json:"state"`
	When    string     `json:"when"` // idle | now
	By      string     `json:"by,omitempty"`
	Waiting int        `json:"waiting"`        // runs still using the CLI
	From    string     `json:"from,omitempty"` // the version before
	To      string     `json:"to,omitempty"`   // the version after
	Note    string     `json:"note,omitempty"`
	JobID   string     `json:"job_id,omitempty"`
	Started time.Time  `json:"started_at"`
	Ended   *time.Time `json:"ended_at,omitempty"`
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

// Defaults of an update (Manager fields override them in tests).
const (
	// UpgradeMaxWait is how long an update when idle waits for its runs.
	UpgradeMaxWait = 12 * time.Hour
	// upgradeHold bounds how long new runs wait for an update.
	upgradeHold = 20 * time.Minute
)

// ErrUpgrading: an update of the tool is already on its way.
var ErrUpgrading = errors.New("công cụ này đang được cập nhật")

// SetAfterUpdate is called once a tool's update ended with it starting
// (checks its connections again: a broken mark goes, ADR-259).
func (m *Manager) SetAfterUpdate(fn func(ctx context.Context, tool string)) { m.afterUpdate = fn }

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
	path := m.lookPath(t.Bin)
	if path == "" {
		return UpgradeView{}, ErrNotInstalled
	}
	meth, ok := updateMethod(t, path)
	if !ok {
		return UpgradeView{}, ErrUnknownInstall
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
	go m.runUpgrade(ctx, t, meth, u)
	return v, nil
}

func (m *Manager) set(u *upgrade, fn func(v *UpgradeView)) {
	m.mu.Lock()
	fn(&u.view)
	m.mu.Unlock()
}

func (m *Manager) runUpgrade(ctx context.Context, t Tool, meth Method, u *upgrade) {
	defer u.cancel()
	poll := m.upPoll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	maxWait := m.upMaxWait
	if maxWait <= 0 {
		maxWait = UpgradeMaxWait
	}
	end := func(state, note string) {
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
	// when idle: until no run has the CLI running
	if u.view.When == "idle" {
		deadline := m.clock().Add(maxWait)
		for {
			n := cliuse.InUse(t.ID)
			m.set(u, func(v *UpgradeView) { v.Waiting = n })
			if n == 0 {
				break
			}
			if !m.clock().Before(deadline) {
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
	}
	hold := m.holdMax
	if hold <= 0 {
		hold = upgradeHold
	}
	release := cliuse.Hold(t.ID, hold) // new runs wait while its files change
	released := false
	free := func() {
		if !released {
			released = true
			release()
		}
	}
	defer free()
	if u.view.When == "idle" { // a run that started just before the hold: let it end (briefly)
		for until := m.clock().Add(2 * time.Minute); cliuse.InUse(t.ID) > 0 && m.clock().Before(until); {
			time.Sleep(min(poll, time.Second))
		}
	}
	m.set(u, func(v *UpgradeView) { v.State, v.Waiting = "updating", 0 })
	from := m.version(t)
	m.set(u, func(v *UpgradeView) { v.From = from })
	j, err := m.start(t.ID, t.ID, "update", meth.update, 15*time.Minute, term{}, "HOMEBREW_NO_ASK=1", "HOMEBREW_ASK=")
	if err != nil {
		end("failed", err.Error())
		return
	}
	m.set(u, func(v *UpgradeView) { v.JobID = j.ID })
	j.Wait()
	jv := j.Snapshot()
	m.set(u, func(v *UpgradeView) { v.State = "checking" })
	if why := m.broken(t); why != "" { // the new version does not start
		m.rollback(t, meth, u, from, why, end)
		free()
		return
	}
	to := m.version(t)
	m.set(u, func(v *UpgradeView) { v.To = to })
	free()
	if m.afterUpdate != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		m.afterUpdate(ctx, t.ID)
		cancel()
	}
	if jv.State != "succeeded" { // the command failed but the CLI still starts: the old one is kept
		end("failed", cmpOr(jv.Error, "lệnh cập nhật lỗi")+" (CLI vẫn chạy được)")
		return
	}
	end("done", "")
}

// rollback puts version from back after an update that left the CLI unable
// to start: npm installs that exact version; Homebrew and the official
// installer cannot pick a version, so the person is told how to fix it.
func (m *Manager) rollback(t Tool, meth Method, u *upgrade, from, why string, end func(state, note string)) {
	p := pkgs[t.ID]
	if meth.ID != "npm" || p.npm == "" || from == "" {
		end("failed", fmt.Sprintf("bản mới không chạy được (%s); hãy cài lại: %s", why, cmpOr(manualFix(t), "cài lại CLI")))
		return
	}
	m.set(u, func(v *UpgradeView) { v.State = "rolling_back" })
	j, err := m.start(t.ID, t.ID, "update", []string{"npm", "install", "-g", p.npm + "@" + from}, 15*time.Minute, term{})
	if err != nil {
		end("failed", fmt.Sprintf("bản mới không chạy được (%s), quay về bản %s lỗi: %v", why, from, err))
		return
	}
	j.Wait()
	if still := m.broken(t); still != "" {
		end("failed", fmt.Sprintf("bản mới không chạy được (%s), quay về bản %s vẫn lỗi: %s", why, from, still))
		return
	}
	to := m.version(t)
	m.set(u, func(v *UpgradeView) { v.To = to })
	if m.afterUpdate != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		m.afterUpdate(ctx, t.ID)
		cancel()
	}
	end("rolled_back", "Lỗi của bản mới: "+why)
}

func manualFix(t Tool) string {
	for _, meth := range t.methods {
		if meth.Recommended {
			return meth.Command
		}
	}
	return ""
}

// version is the x.y.z the installed tool says ("" when it does not start).
func (m *Manager) version(t Tool) string {
	path := m.lookPath(t.Bin)
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
func (m *Manager) broken(t Tool) string {
	path := m.lookPath(t.Bin)
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
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
