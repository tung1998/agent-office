package api_test

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/proctrack"
)

// The overview reads the machine and what office runs (an agent's process
// named for its agent); kill ends it; a process office did not start is refused.
func TestSystemStats(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	defer proctrack.Track(proctrack.With(context.Background(), proctrack.Info{Kind: "agent", TurnID: "t1", Label: "Dev"}), cmd.Process.Pid)()

	code, body := do(t, admin, "GET", e.srv.URL+"/api/system/stats", nil, nil)
	if code.StatusCode != 200 {
		t.Fatalf("stats = %d %v", code.StatusCode, body)
	}
	if m := body["machine"].(map[string]any); m["mem_total"].(float64) == 0 {
		t.Fatalf("machine = %v", m)
	}
	found := false
	for _, g := range body["groups"].([]any) {
		g := g.(map[string]any)
		if int(g["pid"].(float64)) == cmd.Process.Pid {
			found = g["kind"] == "agent" && g["label"] == "Dev"
		}
	}
	if !found {
		t.Fatalf("the agent's process is not listed as its: %v", body["groups"])
	}
	if code, _ := do(t, admin, "POST", e.srv.URL+"/api/system/processes/1/kill", nil, nil); code.StatusCode != 404 {
		t.Fatalf("killing a process office did not start = %d", code.StatusCode)
	}
	if code, b := do(t, admin, "POST", fmt.Sprintf("%s/api/system/processes/%d/kill", e.srv.URL, cmd.Process.Pid), nil, nil); code.StatusCode != 204 {
		t.Fatalf("kill = %d %v", code.StatusCode, b)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("not killed")
	}
}
