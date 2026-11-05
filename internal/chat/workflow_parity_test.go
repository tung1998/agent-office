package chat_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// The workflow_* tools work the same for an agent on an API connection as
// for one on Claude Code: offered only to the coordinator of the run (and on
// the office MCP server Codex and the CLIs use), delegate hidden, the role's
// answer calls it back once, its summary is the caller's answer.
func TestWorkflowToolsOverTheAPIAndMCP(t *testing.T) {
	var (
		mu       sync.Mutex
		coord    int
		problems []string
		check    func() // runs inside the coordinator's first request
	)
	fail := func(f string, a ...any) {
		mu.Lock()
		problems = append(problems, f)
		mu.Unlock()
		_ = a
	}
	reply := func(w http.ResponseWriter, content string, tool bool) {
		stop := "end_turn"
		if tool {
			stop = "tool_use"
		}
		w.Write([]byte(`{"model":"claude-sonnet-5","stop_reason":"` + stop + `","content":[` + content + `],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}
	text := func(s string) string { return `{"type":"text","text":"` + s + `"}` }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if !strings.Contains(body, "COORDINATOR agent") { // a role
			reply(w, text("Ý kiến cố vấn: nên làm A."), false)
			return
		}
		mu.Lock()
		coord++
		n := coord
		c := check
		mu.Unlock()
		switch n {
		case 1:
			if !strings.Contains(body, `"workflow_delegate"`) || !strings.Contains(body, `"workflow_ask"`) || !strings.Contains(body, `"workflow_done"`) {
				fail("coordinator lacks the workflow tools")
			}
			if strings.Contains(body, `"name":"delegate"`) {
				fail("delegate offered inside a run")
			}
			if c != nil {
				c()
			}
			reply(w, `{"type":"tool_use","id":"t1","name":"workflow_delegate","input":{"role":"co-van","brief":{"question":"A hay B?","context":"x","tried":"chưa"}}}`, true)
		case 2:
			if !strings.Contains(body, "Delegated role") {
				fail("delegate result not returned")
			}
			reply(w, text("Đã giao."), false)
		case 3:
			if !strings.Contains(body, "[office · workflow") {
				fail("not a call-back")
			}
			reply(w, `{"type":"tool_use","id":"t2","name":"workflow_done","input":{"summary":"Kết luận: làm A.","outputs":{"recommendation":"làm A"}}}`, true)
		default:
			reply(w, text("Xong."), false)
		}
	}))
	defer srv.Close()
	var prov storage.Provider
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, err := provs.Create(context.Background(), provider.Input{Name: "API", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		if err != nil {
			t.Fatal(err)
		}
		prov = p
		return p
	})
	ctx := context.Background()
	box := officetools.New(f.st, nil, actions.New(f.st, nil))
	mcp := mcpserver.New(box, "test")
	f.engine.SetOffice(box, mcp, "http://127.0.0.1:1/mcp")
	dev, err := f.st.Agents().Create(ctx, storage.Agent{ProjectID: f.project.ID, Key: "dev", Name: "Dev", ModelTier: "fast", ProviderID: prov.ID})
	if err != nil {
		t.Fatal(err)
	}
	lib := workflow.Library{Dir: filepath.Join(t.TempDir(), "workflows")}
	if _, err := (&workflow.Service{Store: f.st, Lib: lib}).Install(ctx, f.project.ID, "advisor", map[string]string{"co-van": dev.ID}); err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	// what Codex (and any CLI) sees: the office MCP server lists the same tools for the coordinator's scope
	check = func() {
		runs, _ := f.st.WorkflowRuns().List(ctx, f.project.ID, conv.ID, 1)
		if len(runs) == 0 {
			fail("no run")
			return
		}
		turn, ok := f.engine.Active(runs[0].ConversationID)
		if !ok {
			fail("no coordinator turn")
			return
		}
		tok, revoke := mcp.Grant(officetools.Scope{ProjectID: f.project.ID, ConversationID: runs[0].ConversationID, RunRef: turn.ID, Agent: conv.AgentName}, time.Minute)
		defer revoke()
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mcp.ServeHTTP(rec, req)
		if out := rec.Body.String(); !strings.Contains(out, "workflow_ask") || strings.Contains(out, `"name":"delegate"`) {
			fail("MCP tools/list for the coordinator: " + out)
		}
	}
	turn, _, err := f.engine.Send(ctx, conv.ID, "/advisor nên làm A hay B", nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, turn)
	last := evs[len(evs)-1]
	if last.Message == nil || !strings.Contains(last.Message.Content, "Kết luận: làm A.") {
		t.Fatalf("caller's answer = %+v", last)
	}
	// the caller's answer comes from workflow_done, while the coordinator's
	// own turn still sends that tool's result for its last word: wait for the
	// turn to end before counting its calls (under load it was read early)
	if runs, _ := f.st.WorkflowRuns().List(ctx, f.project.ID, conv.ID, 1); len(runs) == 1 {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if _, busy := f.engine.Active(runs[0].ConversationID); !busy {
				break
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(problems) > 0 || coord != 4 {
		t.Fatalf("coordinator calls = %d, problems = %v", coord, problems)
	}
}
