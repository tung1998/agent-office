package officetools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/advice"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// A command failing the same way twice in a run: the second result carries
// what the advisor said; any later tool call carries an advice kept for the
// run (ADR-262).
func TestAdviceAppendedToToolResult(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi\n"), 0o644)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "c", Path: root})
	box := New(st, nil, actions.New(st, nil))
	var tr advice.Tracker
	var asked []string
	kept := ""
	box.SetAdvice(func(_ context.Context, sc Scope, command, errText string) string {
		if f := tr.Failed(sc.JobID, command, errText); f.Ask {
			asked = append(asked, command)
			return "ADVISOR: look at the path"
		}
		return ""
	}, func(Scope) string { k := kept; kept = ""; return k })
	acc := perm.Access{Level: perm.Check, Caps: []string{perm.CapPropose, perm.CapCommands}, Commands: []string{"cat *"}}
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", JobID: "job1", Agent: "a", Level: perm.Check, Access: acc}
	call := func(cmd string) (string, bool) {
		raw, _ := json.Marshal(map[string]string{"command": cmd, "reason": "x"})
		return box.Call(ctx, sc, "run_command", raw)
	}
	out, failed := call("cat missing.txt")
	if !failed || strings.Contains(out, "ADVISOR") {
		t.Fatalf("first failure: %v %q", failed, out)
	}
	out, failed = call("cat missing.txt")
	if !failed || !strings.Contains(out, "ADVISOR: look at the path") || len(asked) != 1 {
		t.Fatalf("second failure: %v %q asked=%v", failed, out, asked)
	}
	if out, _ = call("cat hello.txt"); strings.Contains(out, "ADVISOR") {
		t.Fatalf("a command that works carries no advice: %q", out)
	}
	kept = "KEPT ADVICE"
	if out, _ = call("cat hello.txt"); !strings.Contains(out, "KEPT ADVICE") {
		t.Fatalf("kept advice not handed over: %q", out)
	}
	if out, _ = call("cat hello.txt"); strings.Contains(out, "KEPT ADVICE") {
		t.Fatal("kept advice handed over twice")
	}
}
