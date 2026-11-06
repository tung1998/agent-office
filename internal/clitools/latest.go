package clitools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// What a tool's newest version is read from (ADR-259): its npm package (the
// official installer of Claude Code ships the same versions), else its
// Homebrew formula or cask.
type pkgRef struct {
	npm  string
	brew string
	cask bool
}

var pkgs = map[string]pkgRef{
	"claude":      {npm: "@anthropic-ai/claude-code", brew: "claude-code", cask: true},
	"codex":       {npm: "@openai/codex", brew: "codex", cask: true},
	"gemini":      {npm: "@google/gemini-cli", brew: "gemini-cli"},
	"antigravity": {brew: "antigravity-cli", cask: true},
	"dsh":         {npm: "@deepseek-ai/dsh"},
}

// LatestTTL is how long a newest version read stays good.
const LatestTTL = 6 * time.Hour

// latestEntry is a newest version read, or why it could not be.
type latestEntry struct {
	version string
	err     string
	at      time.Time
	busy    bool // a read is on its way
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// parseVersion reads the first x.y.z of what a CLI prints ("codex-cli
// 0.166.0", "2.1.318 (Claude Code)").
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	m := versionRe.FindString(s)
	if m == "" {
		return v, false
	}
	for i, p := range strings.SplitN(m, ".", 3) {
		v[i], _ = strconv.Atoi(p)
	}
	return v, true
}

// VersionOf is the x.y.z in what a CLI prints ("" when there is none).
func VersionOf(s string) string { return versionRe.FindString(s) }

// Newer says whether latest is a newer version than installed; false when
// either cannot be read.
func Newer(installed, latest string) bool {
	a, ok1 := parseVersion(installed)
	b, ok2 := parseVersion(latest)
	return ok1 && ok2 && semverCmp(b, a) > 0
}

// source is where tool's newest version is read for the way it is installed
// at path ("" = not installed).
func (m *Manager) source(t Tool, path string) (kind, name string, cask bool) {
	p, ok := pkgs[t.ID]
	if !ok {
		return "", "", false
	}
	how := ""
	if path != "" {
		if meth, ok := updateMethod(t, path); ok {
			how = meth.ID
		}
	}
	switch {
	case how == "brew" && p.brew != "":
		return "brew", p.brew, p.cask
	case p.npm != "":
		return "npm", p.npm, false
	case p.brew != "":
		return "brew", p.brew, p.cask
	}
	return "", "", false
}

// LatestNow reads tool id's newest version now (and keeps it LatestTTL).
func (m *Manager) LatestNow(ctx context.Context, id string) (string, error) {
	t, ok := m.find(id)
	if !ok {
		return "", ErrUnknownTool
	}
	kind, name, cask := m.source(t, m.lookPath(t.Bin))
	var v string
	var err error
	switch kind {
	case "npm":
		v, err = m.npmLatest(ctx, name)
	case "brew":
		v, err = m.brewLatest(ctx, name, cask)
	default:
		err = errors.New("không rõ bản mới nhất của công cụ này")
	}
	e := latestEntry{version: v, at: m.clock()}
	if err != nil {
		e.err = err.Error()
	}
	m.mu.Lock()
	if m.latest == nil {
		m.latest = map[string]latestEntry{}
	}
	m.latest[id] = e
	m.mu.Unlock()
	return v, err
}

// latestCached is the newest version kept for id; one older than LatestTTL
// (or none) is read again in the background, so a slow or broken network
// never holds the status up.
func (m *Manager) latestCached(id string) string {
	v, _ := m.latestState(id)
	return v
}

// latestState is latestCached, with whether a read of it is on its way (the
// dashboard looks again soon then: a stale version is replaced shortly).
func (m *Manager) latestState(id string) (string, bool) {
	m.mu.Lock()
	if m.latest == nil {
		m.latest = map[string]latestEntry{}
	}
	e, ok := m.latest[id]
	stale := !ok || m.clock().Sub(e.at) >= LatestTTL
	if stale && !e.busy && !m.noFetch {
		e.busy = true
		m.latest[id] = e
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := m.LatestNow(ctx, id); err != nil {
				m.mu.Lock()
				x := m.latest[id]
				x.busy = false
				m.latest[id] = x
				m.mu.Unlock()
			}
		}()
	}
	busy := m.latest[id].busy
	m.mu.Unlock()
	return e.version, busy
}

// forgetLatest drops what is kept of id's newest version (after an update).
func (m *Manager) forgetLatest(id string) {
	m.mu.Lock()
	delete(m.latest, id)
	m.mu.Unlock()
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Manager) npmLatest(ctx context.Context, pkg string) (string, error) {
	base := m.registry
	if base == "" {
		base = "https://registry.npmjs.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+url.PathEscape(pkg)+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	c := m.httpc
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("npm: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm: %s", resp.Status)
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Version == "" {
		return "", errors.New("npm: không đọc được phiên bản")
	}
	return out.Version, nil
}

func (m *Manager) brewLatest(ctx context.Context, name string, cask bool) (string, error) {
	brew := m.lookPath("brew")
	if brew == "" {
		return "", errors.New("máy không có brew")
	}
	args := []string{"info", "--json=v2"}
	if cask {
		args = append(args, "--cask")
	}
	out, err := runOut(ctx, m.Env(), brew, append(args, name)...)
	if err != nil {
		return "", fmt.Errorf("brew info: %v", err)
	}
	var info struct {
		Formulae []struct {
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
		Casks []struct {
			Version string `json:"version"`
		} `json:"casks"`
	}
	if err := json.Unmarshal([]byte(firstJSON(out)), &info); err != nil {
		return "", errors.New("brew: không đọc được phiên bản")
	}
	switch {
	case len(info.Casks) > 0 && info.Casks[0].Version != "":
		return VersionOf(info.Casks[0].Version), nil
	case len(info.Formulae) > 0 && info.Formulae[0].Versions.Stable != "":
		return info.Formulae[0].Versions.Stable, nil
	}
	return "", errors.New("brew: không có phiên bản")
}
