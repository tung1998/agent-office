package automation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleList = `Checking MCP server health…

claude.ai Notion: https://mcp.notion.com/mcp - ! Needs authentication
claude.ai Helium 10 : https://mcp.helium10.com/mcp - ! Needs authentication
plugin:browser-use:browser-use: uvx --python 3.12 browser-use@latest --cli-mcp - ✘ Failed to connect — ENOENT: Executable not found in $PATH: "uvx"
context7: npx -y @upstash/context7-mcp - ✔ Connected
atlassian: https://mcp.atlassian.com/v1/mcp (HTTP) - ✓ Connected
weird line without status
`

func TestParseMCPList(t *testing.T) {
	got := ParseMCPList(sampleList)
	want := []MCPState{
		{Name: "claude.ai Notion", Target: "https://mcp.notion.com/mcp", Status: "needs_auth", Detail: "Needs authentication"},
		{Name: "claude.ai Helium 10", Target: "https://mcp.helium10.com/mcp", Status: "needs_auth", Detail: "Needs authentication"},
		{Name: "plugin:browser-use:browser-use", Target: "uvx --python 3.12 browser-use@latest --cli-mcp", Status: "failed", Detail: `Failed to connect — ENOENT: Executable not found in $PATH: "uvx"`},
		{Name: "context7", Target: "npx -y @upstash/context7-mcp", Status: "connected", Detail: "Connected"},
		{Name: "atlassian", Target: "https://mcp.atlassian.com/v1/mcp (HTTP)", Status: "connected", Detail: "Connected"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestMCPHealthCheck(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho 'Checking MCP server health…'\necho \"x: $(pwd) - ✔ Connected\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan MCPCheck, 1)
	h := &MCPHealth{Home: dir, Claude: func() string { return bin }, OnDone: func(c MCPCheck) { done <- c }}
	if c := h.Get(""); !c.CheckedAt.IsZero() || c.Running {
		t.Fatalf("fresh: %+v", c)
	}
	if c := h.Check(""); !c.Running {
		t.Fatalf("check should be running: %+v", c)
	}
	select {
	case c := <-done:
		if c.Running || c.Error != "" || len(c.Items) != 1 || c.Items[0].Status != "connected" {
			t.Fatalf("done: %+v", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no result")
	}
	if c := h.Get(""); c.CheckedAt.IsZero() || len(c.Items) != 1 {
		t.Fatalf("cached: %+v", c)
	}
}

func TestMCPHealthNoClaude(t *testing.T) {
	h := &MCPHealth{Home: t.TempDir(), Claude: func() string { return "" }}
	if c := h.Check(""); c.Running || c.Error == "" {
		t.Fatalf("want error: %+v", c)
	}
}
