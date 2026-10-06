package actions

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

type fakeMCP struct{ got string }

func (f *fakeMCP) CallTool(_ context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	f.got = server + "/" + tool + " " + string(args)
	return "đã ghi", false, nil
}

// A call of an MCP tool that writes waits for a person even at the top
// level (the gateway already let through what the agent may call), each
// call is its own proposal, and approving it runs the call (ADR-093).
func TestMCPCall(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	svc := New(st, nil)
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "Dev", Level: perm.Operate, Access: perm.Access{Level: perm.Operate, Caps: perm.Preset(perm.Operate)}}
	call := func(x string) storage.ActionArgs {
		return storage.ActionArgs{MCP: &storage.MCPCallArgs{Server: "jira", Tool: "create_issue", Arguments: json.RawMessage(`{"title":"` + x + `"}`)}}
	}
	if _, err := svc.Propose(ctx, sc, "mcp_call", "", "", call("a")); err == nil {
		t.Fatal("mcp_call without the gateway")
	}
	f := &fakeMCP{}
	svc.SetMCP(f)
	a, err := svc.Propose(ctx, sc, "mcp_call", "", "Tham số: …", call("a"))
	if err != nil || a.Status != "pending" || a.Target != "jira/create_issue" {
		t.Fatalf("propose = %+v %v", a, err)
	}
	b, _ := svc.Propose(ctx, sc, "mcp_call", "", "", call("b"))
	if b.ID == a.ID {
		t.Fatal("two calls with other arguments became one proposal")
	}
	done, err := svc.Decide(ctx, a.ID, true, "human:a")
	if err != nil || done.Status != "done" || done.Detail != "đã ghi" || f.got != `jira/create_issue {"title":"a"}` {
		t.Fatalf("decide = %+v %v (%s)", done, err, f.got)
	}
	if _, err := svc.Decide(ctx, a.ID, true, "human:a"); !errors.Is(err, ErrDecided) {
		t.Fatalf("approved twice = %v", err)
	}
}

type slowMCP struct{ calls atomic.Int32 }

func (f *slowMCP) CallTool(context.Context, string, string, json.RawMessage) (string, bool, error) {
	f.calls.Add(1)
	time.Sleep(100 * time.Millisecond)
	return "ok", false, nil
}

// The dashboard and a bot approving one proposal at once: it runs once, the
// other gets ErrDecided (and so its agent is not sent on twice, ADR-084).
func TestDecideAtOnceRunsOnce(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	svc := New(st, nil)
	f := &slowMCP{}
	svc.SetMCP(f)
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "Dev", Level: perm.Operate, Access: perm.Access{Level: perm.Operate, Caps: perm.Preset(perm.Operate)}}
	a, err := svc.Propose(ctx, sc, "mcp_call", "", "", storage.ActionArgs{MCP: &storage.MCPCallArgs{Server: "jira", Tool: "create_issue"}})
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg      sync.WaitGroup
		ok, dup atomic.Int32
	)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(approve bool) {
			defer wg.Done()
			_, err := svc.Decide(ctx, a.ID, approve, "human:a")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrDecided):
				dup.Add(1)
			default:
				t.Error(err)
			}
		}(i != 3) // one of them rejects
	}
	wg.Wait()
	if ok.Load() != 1 || dup.Load() != 4 || f.calls.Load() > 1 {
		t.Fatalf("decided %d, refused %d, ran %d", ok.Load(), dup.Load(), f.calls.Load())
	}
}
