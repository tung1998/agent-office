package automation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memStash struct{ raw []byte }

func (m *memStash) Load(context.Context) (map[string]DisabledMCP, error) {
	out := map[string]DisabledMCP{}
	if m.raw == nil {
		return out, nil
	}
	return out, json.Unmarshal(m.raw, &out)
}

func (m *memStash) Save(_ context.Context, recs map[string]DisabledMCP) error {
	var err error
	m.raw, err = json.Marshal(recs)
	return err
}

// Turning a user, local or project MCP server off stops Claude Code loading
// it; turning it on gives every file back byte for byte (key order, layout,
// untouched values, no file or folder left behind).
func TestMCPToggleRoundTrip(t *testing.T) {
	ctx := context.Background()
	home, office := t.TempDir(), t.TempDir()
	proj := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Claude Code's layout: insertion order (not sorted), 2-space indent
	claudeJSON := `{
  "numStartups": 12,
  "tipsHistory": {
    "z": 1,
    "a": "<b>&</b> é"
  },
  "mcpServers": {
    "zeta": {
      "type": "stdio",
      "command": "npx",
      "args": [
        "-y",
        "zeta"
      ],
      "env": {
        "TOKEN": "very-secret"
      }
    },
    "beta": {
      "type": "http",
      "url": "https://b.example/mcp"
    },
    "alpha": {
      "command": "alpha"
    }
  },
  "projects": {
    "` + proj + `": {
      "allowedTools": [],
      "mcpServers": {
        "loc": {
          "command": "loc"
        }
      },
      "hasTrustDialogAccepted": true
    }
  },
  "userID": "abc"
}
`
	mcpJSON := `{"mcpServers":{"shared":{"command":"shared"}}}`
	cj := filepath.Join(home, ".claude.json")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(cj, claudeJSON)
	write(filepath.Join(proj, ".mcp.json"), mcpJSON)
	stash := &memStash{}
	s := &Service{Home: home, Installer: Installer{Home: home, Trash: filepath.Join(office, "trash")}, Stash: stash,
		Projects: func(context.Context) map[string]string { return map[string]string{proj: "prj_1"} }}

	find := func(typ, name string) Item {
		t.Helper()
		for _, it := range s.Scan(ctx).Items {
			if it.Kind == "mcp" && it.Location.Type == typ && it.Name == name {
				return it
			}
		}
		t.Fatalf("no %s %s in scan", typ, name)
		return Item{}
	}
	ref := func(it Item) Ref {
		return Ref{Kind: "mcp", Name: it.Name, Type: it.Location.Type, Path: it.Location.Path, ProjectPath: it.Location.ProjectPath}
	}
	read := func(p string) string { raw, _ := os.ReadFile(p); return string(raw) }

	cases := []struct {
		typ, name string
		off       func() // what Claude Code sees while off
	}{
		{"user", "zeta", func() {
			var doc map[string]any
			_ = json.Unmarshal([]byte(read(cj)), &doc)
			if _, ok := doc["mcpServers"].(map[string]any)["zeta"]; ok {
				t.Fatal("zeta still in ~/.claude.json while off")
			}
			if strings.Contains(string(stash.raw), "zeta") == false {
				t.Fatal("office did not keep zeta's config")
			}
		}},
		{"local", "loc", func() {
			if !strings.Contains(read(cj), `"disabledMcpServers": [`+"\n"+`        "loc"`) {
				t.Fatalf("local not in disabledMcpServers: %s", read(cj))
			}
		}},
		{"project", "shared", func() {
			if got := read(settingsLocal(proj)); !strings.Contains(got, `"disabledMcpjsonServers"`) || !strings.Contains(got, `"shared"`) {
				t.Fatalf("settings.local.json = %q", got)
			}
			if read(filepath.Join(proj, ".mcp.json")) != mcpJSON {
				t.Fatal(".mcp.json touched")
			}
		}},
	}
	for _, c := range cases {
		it := find(c.typ, c.name)
		if it.Disabled {
			t.Fatalf("%s starts off", c.name)
		}
		if err := s.SetMCPEnabled(ctx, ref(it), false); err != nil {
			t.Fatalf("off %s: %v", c.name, err)
		}
		it = find(c.typ, c.name) // still listed, marked off
		if !it.Disabled {
			t.Fatalf("%s not marked off", c.name)
		}
		c.off()
		if err := s.SetMCPEnabled(ctx, ref(it), false); err != nil { // again: nothing to do
			t.Fatalf("off again %s: %v", c.name, err)
		}
		if err := s.SetMCPEnabled(ctx, ref(it), true); err != nil {
			t.Fatalf("on %s: %v", c.name, err)
		}
		if find(c.typ, c.name).Disabled {
			t.Fatalf("%s still off", c.name)
		}
		if got := read(cj); got != claudeJSON {
			t.Fatalf("%s: ~/.claude.json not as it was:\n%s", c.name, got)
		}
		if read(filepath.Join(proj, ".mcp.json")) != mcpJSON {
			t.Fatalf("%s: .mcp.json changed", c.name)
		}
		if _, err := os.Stat(filepath.Join(proj, ".claude")); !os.IsNotExist(err) {
			t.Fatalf("%s: .claude left in the project", c.name)
		}
	}
	if recs, _ := stash.Load(ctx); len(recs) != 0 {
		t.Fatalf("records left: %v", recs)
	}

	// all three off together, then on in another order
	for _, c := range cases {
		if err := s.SetMCPEnabled(ctx, ref(find(c.typ, c.name)), false); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(cases) - 1; i >= 0; i-- {
		if err := s.SetMCPEnabled(ctx, ref(find(cases[i].typ, cases[i].name)), true); err != nil {
			t.Fatal(err)
		}
	}
	if read(cj) != claudeJSON {
		t.Fatalf("~/.claude.json after all three:\n%s", read(cj))
	}

	// a user server kept by office while off: its config is still readable,
	// and removing it just drops it
	zeta := find("user", "zeta")
	if err := s.SetMCPEnabled(ctx, ref(zeta), false); err != nil {
		t.Fatal(err)
	}
	if _, cfg, err := s.MCPConfig(ctx, ref(find("user", "zeta"))); err != nil || cfg["env"].(map[string]any)["TOKEN"] != "very-secret" {
		t.Fatalf("config of an off server = %v %v", cfg, err)
	}
	if _, err := s.Remove(ctx, ref(find("user", "zeta"))); err != nil {
		t.Fatal(err)
	}
	for _, it := range s.Scan(ctx).Items {
		if it.Name == "zeta" {
			t.Fatal("zeta still listed after remove")
		}
	}
	// a project server removed while off leaves no name in settings.local.json
	if err := s.SetMCPEnabled(ctx, ref(find("project", "shared")), false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove(ctx, ref(find("project", "shared"))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settingsLocal(proj)); !os.IsNotExist(err) {
		t.Fatalf("settings.local.json left: %s", read(settingsLocal(proj)))
	}
}

func TestMCPToggleOnlyEditable(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"c":{"command":"c"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Home: home, Installer: Installer{Home: home, Trash: t.TempDir()}, Stash: &memStash{}}
	r := Ref{Kind: "mcp", Name: "c", Type: "cursor", Path: filepath.Join(home, ".cursor", "mcp.json")}
	if err := s.SetMCPEnabled(ctx, r, false); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("cursor off = %v", err)
	}
	// a project already off in Claude Code's own list keeps the list when turned on
	proj := t.TempDir()
	cj := `{"projects":{"` + proj + `":{"mcpServers":{"l":{"command":"l"}},"disabledMcpServers":["l","other"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cj), 0o600); err != nil {
		t.Fatal(err)
	}
	lr := Ref{Kind: "mcp", Name: "l", Type: "local", Path: filepath.Join(home, ".claude.json"), ProjectPath: proj}
	if err := s.SetMCPEnabled(ctx, lr, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(raw), `"other"`) || strings.Contains(string(raw), `"l",`) {
		t.Fatalf("after on = %s", raw)
	}
}
