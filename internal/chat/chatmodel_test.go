package chat_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// withModels gives the group's connection a model list (as a check would).
func (g group) withModels(t *testing.T, models ...string) {
	t.Helper()
	ps, _ := g.f.st.Providers().List(g.context)
	for _, p := range ps {
		if err := g.f.st.Providers().SetStatus(g.context, p.ID, "ok", "", models, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}

func (g group) clearCalls() {
	old, _ := filepath.Glob(filepath.Join(g.dir, "call*"))
	for _, f := range old {
		os.Remove(f)
	}
}

// callsOf: the args of each run, by who ran (lead | dev).
func (g group) callsOf(t *testing.T) map[string]string {
	t.Helper()
	calls, _ := filepath.Glob(filepath.Join(g.dir, "call*.args"))
	out := map[string]string{}
	for _, f := range calls {
		b, _ := os.ReadFile(f)
		who := "lead"
		if strings.Contains(string(b), "You are Dev") {
			who = "dev"
		}
		out[who] = string(b)
	}
	return out
}

// A chat's own model for one agent is what that agent runs on there; the
// other agents of the chat keep theirs, and the run records the effort.
func TestMemberModelOverride(t *testing.T) {
	g := newGroup(t)
	g.withModels(t, "m-chat", "m-other")
	if err := g.engine.SetMemberModel(g.context, g.conv.ID, g.lead, "m-chat"); err != nil {
		t.Fatal(err)
	}
	if err := g.engine.SetEffort(g.context, g.conv.ID, "high"); err != nil {
		t.Fatal(err)
	}
	g.clearCalls()
	if a := g.sendAll(t, "@"+g.leadNm+" @Dev xem"); len(a) < 2 {
		t.Fatalf("authors = %v", a)
	}
	calls := g.callsOf(t)
	if !strings.Contains(calls["lead"], "--model m-chat") {
		t.Fatalf("lead ran without the chat's model: %s", calls["lead"])
	}
	if strings.Contains(calls["dev"], "m-chat") {
		t.Fatalf("the lead's choice leaked to Dev: %s", calls["dev"])
	}
	runs, _ := g.f.st.Runs().List(g.context, storage.RunFilter{ProjectID: g.f.project.ID, AgentID: g.lead})
	if len(runs) == 0 || runs[0].Effort != "high" {
		t.Fatalf("run effort not recorded: %+v", runs)
	}
}

// Work handed over runs on the agent's own model, not the chat's choice.
func TestMemberModelIgnoredWhenDelegated(t *testing.T) {
	g := newGroup(t)
	g.withModels(t, "m-chat")
	if err := g.engine.SetMemberModel(g.context, g.conv.ID, g.dev.ID, "m-chat"); err != nil {
		t.Fatal(err)
	}
	g.clearCalls()
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("0.3"), 0o644)
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "@"+g.leadNm+" giao việc", nil); err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "sửa lỗi", "")
	g.waitAuthors(t, 2)
	if dev := g.callsOf(t)["dev"]; dev == "" || strings.Contains(dev, "m-chat") {
		t.Fatalf("delegated Dev ran on the chat's model: %q", dev)
	}
}

// A choice the connection no longer has falls back to the agent's model.
func TestMemberModelStaleFallsBack(t *testing.T) {
	g := newGroup(t)
	g.withModels(t, "m-chat")
	if err := g.engine.SetMemberModel(g.context, g.conv.ID, g.lead, "m-chat"); err != nil {
		t.Fatal(err)
	}
	g.withModels(t, "m-new")
	g.clearCalls()
	if a := g.sendAll(t, "@"+g.leadNm+" xem"); len(a) == 0 {
		t.Fatal("no answer")
	}
	if lead := g.callsOf(t)["lead"]; strings.Contains(lead, "m-chat") {
		t.Fatalf("ran on a model the connection lost: %s", lead)
	}
}

func TestSetMemberModelChecks(t *testing.T) {
	g := newGroup(t)
	g.withModels(t, "m-chat")
	if err := g.engine.SetMemberModel(g.context, g.conv.ID, g.lead, "not-there"); !errors.Is(err, chat.ErrUnknownModel) {
		t.Fatalf("foreign model err = %v", err)
	}
	// an agent not in the chat yet joins with the choice
	if err := g.engine.SetMemberModel(g.context, g.conv.ID, g.dev.ID, "m-chat"); err != nil {
		t.Fatal(err)
	}
	if err := g.engine.SetMemberModel(g.context, g.conv.ID, g.dev.ID, ""); err != nil {
		t.Fatalf("clearing = %v", err)
	}
}

// A person's turn is scored when it ends; their next message correcting it
// lowers its score (ADR-267).
func TestTurnIsScored(t *testing.T) {
	g := newGroup(t)
	if a := g.sendAll(t, "@"+g.leadNm+" sửa lỗi đăng nhập"); len(a) == 0 {
		t.Fatal("no answer")
	}
	list, err := g.f.st.Scores().List(g.context, storage.ScoreFilter{ProjectID: g.f.project.ID})
	if err != nil || len(list) != 1 {
		t.Fatalf("scores = %+v %v", list, err)
	}
	s := list[0]
	if s.TaskKind != "bugfix" || s.AgentID != g.lead || s.Model == "" || s.Q == nil || *s.Q != 100 {
		t.Fatalf("score = %+v", s)
	}
	g.sendAll(t, "@"+g.leadNm+" sai rồi, làm lại")
	got, _ := g.f.st.Scores().Get(g.context, s.JobID)
	if got.Q == nil || *got.Q != 0 {
		t.Fatalf("correction not counted: %+v", got)
	}
}
