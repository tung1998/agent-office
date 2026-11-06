package clitools

import (
	"context"
	"strings"
	"time"
)

// AutoUpdateKey stores how office keeps the AI CLIs up to date (ADR-259).
const AutoUpdateKey = "system.cli_auto_update"

// Auto-update modes.
const (
	AutoOff    = "off"    // nothing on its own
	AutoNotify = "notify" // a new version (or a CLI that no longer starts) is told, once
	AutoUpdate = "auto"   // and updated when idle
)

// AutoSetting is that setting: Mode for every CLI, Tools a CLI's own.
type AutoSetting struct {
	Mode  string            `json:"mode"`
	Tools map[string]string `json:"tools,omitempty"`
	// Told: the version (or "broken") already told per CLI, so a
	// restart does not tell it again
	Told map[string]string `json:"told,omitempty"`
}

// CleanMode keeps a known mode ("" otherwise).
func CleanMode(m string) string {
	switch m = strings.TrimSpace(m); m {
	case AutoOff, AutoNotify, AutoUpdate:
		return m
	}
	return ""
}

// ModeOf is tool's mode (notify when nothing is set).
func (s AutoSetting) ModeOf(tool string) string {
	if m := CleanMode(s.Tools[tool]); m != "" {
		return m
	}
	if m := CleanMode(s.Mode); m != "" {
		return m
	}
	return AutoNotify
}

// AutoBy is who an update office starts on its own is by.
const AutoBy = "tự cập nhật"

// WatchHooks is what Watch reads and tells.
type WatchHooks struct {
	Load   func(ctx context.Context) AutoSetting
	Save   func(ctx context.Context, s AutoSetting)
	Notify func(ctx context.Context, text string)
}

// Notice is what one look at tool found to tell or do: a newer version, or
// an install that no longer starts. Key says it once (in AutoSetting.Told).
type Notice struct {
	Tool, Name     string
	Version        string // installed
	Latest         string
	Broken         string // why it does not start ("" = it does)
	Key            string
	Tell           bool // not told yet (AutoSetting.Told)
	UpdateWhenIdle bool // auto mode: updated whether it was told or not
}

// Check looks at every installed CLI once: what to tell or update under s.
// A CLI told broken that starts again loses that mark (changed says so), so
// the next break is told too.
func (m *Manager) Check(ctx context.Context, s *AutoSetting) (out []Notice, changed bool) {
	for _, t := range m.tools {
		mode := s.ModeOf(t.ID)
		if mode == AutoOff {
			continue
		}
		path := m.lookPath(t.Bin)
		if path == "" {
			continue
		}
		if u, ok := m.Upgrade(t.ID); ok && u.Active() {
			continue
		}
		n := Notice{Tool: t.ID, Name: t.Name}
		if why := m.broken(t); why != "" { // an install broken outside office (ADR-259)
			n.Broken, n.Key = why, "broken"
			n.UpdateWhenIdle = mode == AutoUpdate
		} else {
			if s.Told[t.ID] == "broken" {
				delete(s.Told, t.ID)
				changed = true
			}
			n.Version = m.version(t)
			latest, err := m.LatestNow(ctx, t.ID)
			if err != nil || !Newer(n.Version, latest) {
				continue
			}
			n.Latest, n.Key = latest, latest
			m.mu.Lock()
			bad := m.bad[t.ID] == latest // its update was put back: not again on its own
			m.mu.Unlock()
			n.UpdateWhenIdle = mode == AutoUpdate && !bad
		}
		// being told once and being updated are apart: a version told under
		// notify is still updated once the mode is auto, and an update that
		// failed or gave up is tried again on the next look
		n.Tell = s.Told[t.ID] != n.Key
		if !n.Tell && !n.UpdateWhenIdle {
			continue
		}
		out = append(out, n)
	}
	return out, changed
}

// Text is how a notice is told.
func (n Notice) Text(updating bool) string {
	if n.Broken != "" {
		if updating {
			return "⚠️ " + n.Name + " không chạy được (" + n.Broken + "): office cài lại bản mới nhất khi không còn lượt dùng."
		}
		return "⚠️ " + n.Name + " không chạy được (" + n.Broken + "). Vào Kết nối AI → Công cụ CLI để cập nhật hoặc cài lại."
	}
	if updating {
		return "⬆️ " + n.Name + " có bản " + n.Latest + " (đang dùng " + n.Version + "): office cập nhật khi không còn lượt dùng."
	}
	return "⬆️ " + n.Name + " có bản " + n.Latest + " (đang dùng " + n.Version + "). Cập nhật ở Kết nối AI → Công cụ CLI, hoặc /update cli " + n.Tool + "."
}

// Watch looks at the CLIs every interval (and once soon after start): a new
// version or a broken install is told once; in auto mode it is updated when
// idle (ADR-259).
func (m *Manager) Watch(ctx context.Context, every time.Duration, h WatchHooks) {
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		}
		m.WatchOnce(ctx, h)
	}
}

// WatchOnce is one look of Watch.
func (m *Manager) WatchOnce(ctx context.Context, h WatchHooks) {
	s := h.Load(ctx)
	notices, changed := m.Check(ctx, &s)
	if len(notices) == 0 && !changed {
		return
	}
	if s.Told == nil {
		s.Told = map[string]string{}
	}
	for _, n := range notices {
		updating := false
		if n.UpdateWhenIdle {
			_, err := m.UpdateWhenIdle(n.Tool, AutoBy)
			updating = err == nil
		}
		if n.Tell && h.Notify != nil {
			h.Notify(ctx, n.Text(updating))
		}
		s.Told[n.Tool] = n.Key
	}
	if h.Save != nil {
		h.Save(ctx, s)
	}
}
