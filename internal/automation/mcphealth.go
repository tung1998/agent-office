package automation

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// MCPState is one server's health as `claude mcp list` reports it.
type MCPState struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Status string `json:"status"` // connected | needs_auth | failed | unknown
	Detail string `json:"detail"`
}

// MCPCheck is the latest check of one folder ("" = machine-wide).
type MCPCheck struct {
	Path      string     `json:"path"`
	CheckedAt time.Time  `json:"checked_at"`
	Running   bool       `json:"running"`
	Error     string     `json:"error,omitempty"`
	Items     []MCPState `json:"items"`
}

// ParseMCPList reads lines like "name: target - ✔ Connected".
func ParseMCPList(out string) []MCPState {
	items := []MCPState{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		name, rest, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		// the target may contain " - " too: split at the first one followed by a status mark
		i := -1
		for j := 0; j < len(rest); {
			k := strings.Index(rest[j:], " - ")
			if k < 0 {
				break
			}
			if statusOf(rest[j+k+3:]) != "unknown" {
				i = j + k
				break
			}
			j += k + 3
		}
		if i < 0 {
			continue
		}
		mark := strings.TrimSpace(rest[i+3:])
		st := statusOf(mark)
		_, detail, _ := strings.Cut(mark, " ")
		items = append(items, MCPState{Name: strings.TrimSpace(name), Target: strings.TrimSpace(rest[:i]), Status: st, Detail: strings.TrimSpace(detail)})
	}
	return items
}

func statusOf(s string) string {
	switch {
	case strings.HasPrefix(s, "✔"), strings.HasPrefix(s, "✓"):
		return "connected"
	case strings.HasPrefix(s, "!"):
		return "needs_auth"
	case strings.HasPrefix(s, "✘"), strings.HasPrefix(s, "✗"):
		return "failed"
	}
	return "unknown"
}

// MCPHealth runs `claude mcp list` in the background (it takes ~30 s) and
// keeps the last result per folder.
type MCPHealth struct {
	Home   string
	Claude func() string
	OnDone func(MCPCheck)

	mu sync.Mutex
	m  map[string]*MCPCheck
}

func (h *MCPHealth) Get(path string) MCPCheck {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.m[path]; ok {
		return *c
	}
	return MCPCheck{Path: path, Items: []MCPState{}}
}

// Check starts a check unless one is running and returns the current state.
func (h *MCPHealth) Check(path string) MCPCheck {
	bin := ""
	if h.Claude != nil {
		bin = h.Claude()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m == nil {
		h.m = map[string]*MCPCheck{}
	}
	c, ok := h.m[path]
	if !ok {
		c = &MCPCheck{Path: path, Items: []MCPState{}}
		h.m[path] = c
	}
	if c.Running {
		return *c
	}
	if bin == "" {
		c.Error = ErrNeedsClaude.Error()
		return *c
	}
	c.Running = true
	go h.run(path, bin)
	return *c
}

func (h *MCPHealth) run(path, bin string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "mcp", "list")
	cmd.Dir = path
	if path == "" {
		cmd.Dir = h.Home
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	items := ParseMCPList(out.String())

	h.mu.Lock()
	c := h.m[path]
	c.Running, c.CheckedAt, c.Error = false, time.Now(), ""
	c.Items = items
	if err != nil && len(items) == 0 {
		c.Error = strings.TrimSpace(out.String())
		if c.Error == "" {
			c.Error = err.Error()
		}
	}
	res := *c
	h.mu.Unlock()
	if h.OnDone != nil {
		h.OnDone(res)
	}
}
