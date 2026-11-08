package api_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// failBriefOnce wraps a Store so the first CreateConversation call whose
// title looks like a role-change brief ("Cập nhật vai:") fails, simulating a
// transient error from Chat.Brief; later calls go through normally.
type failBriefOnce struct {
	storage.Store
	failed atomic.Bool
}

type failBriefOnceChat struct {
	storage.ChatRepo
	owner *failBriefOnce
}

func (s *failBriefOnce) Chat() storage.ChatRepo { return failBriefOnceChat{s.Store.Chat(), s} }

func (c failBriefOnceChat) CreateConversation(ctx context.Context, conv storage.Conversation) (storage.Conversation, error) {
	if strings.HasPrefix(conv.Title, "Cập nhật vai:") && c.owner.failed.CompareAndSwap(false, true) {
		return storage.Conversation{}, errors.New("transient failure")
	}
	return c.ChatRepo.CreateConversation(ctx, conv)
}

// ADR-194: an agent whose job changes (here its role and permission level)
// is told so in a chat of its own, to bring its notes and schedules up to
// date; an edit that changes nothing of its job tells it nothing.
func TestRoleChangeBriefsTheAgent(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	project := body["project"].(map[string]any)
	a := project["agents"].([]any)[0].(map[string]any)
	id := a["id"].(string)
	put := func(change map[string]any) {
		t.Helper()
		in := map[string]any{}
		for _, k := range []string{"key", "name", "role", "description", "provider_id", "model_tier", "llm_model", "instructions", "permissions"} {
			in[k] = a[k]
		}
		for k, v := range change {
			in[k] = v
		}
		if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id, in, nil); resp.StatusCode != 200 {
			t.Fatalf("PATCH = %d %v", resp.StatusCode, b)
		}
	}
	briefs := func() []string {
		var out []string
		convs, _ := e.st.Chat().ListConversationsFrom(context.Background(), project["id"].(string), "all", 50)
		for _, c := range convs {
			msgs, _ := e.st.Chat().ListMessages(context.Background(), c.ID)
			for _, m := range msgs {
				if strings.HasPrefix(m.Content, "[Office] Your job changed") {
					out = append(out, m.Content)
				}
			}
		}
		return out
	}
	put(map[string]any{"description": "chỉ đổi mô tả"})
	time.Sleep(200 * time.Millisecond)
	if n := len(briefs()); n != 0 {
		t.Fatalf("a description edit briefed the agent %d times", n)
	}
	put(map[string]any{"role": "Trưởng phòng kinh doanh", "permissions": map[string]any{"level": "read", "read_only": true}})
	var got []string
	for i := 0; i < 50 && len(got) == 0; i++ {
		time.Sleep(40 * time.Millisecond)
		got = briefs()
	}
	if len(got) != 1 || !strings.Contains(got[0], "Trưởng phòng kinh doanh") || !strings.Contains(got[0], "permission level") || !strings.Contains(got[0], "only_if") {
		t.Fatalf("brief: %q", got)
	}
}

// A brief that fails to send (transient error) must not be marked "told": the
// next real change to the same agent should still brief it, instead of being
// folded into the failed attempt for the next briefGap.
func TestRoleChangeBriefRetriesAfterTransientFailure(t *testing.T) {
	var wrapped *failBriefOnce
	e := setupWithStore(t, nil, func(st storage.Store) storage.Store {
		wrapped = &failBriefOnce{Store: st}
		return wrapped
	})
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	project := body["project"].(map[string]any)
	a := project["agents"].([]any)[0].(map[string]any)
	id := a["id"].(string)
	put := func(change map[string]any) {
		t.Helper()
		in := map[string]any{}
		for _, k := range []string{"key", "name", "role", "description", "provider_id", "model_tier", "llm_model", "instructions", "permissions"} {
			in[k] = a[k]
		}
		for k, v := range change {
			in[k] = v
		}
		if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id, in, nil); resp.StatusCode != 200 {
			t.Fatalf("PATCH = %d %v", resp.StatusCode, b)
		}
	}
	briefs := func() []string {
		var out []string
		convs, _ := e.st.Chat().ListConversationsFrom(context.Background(), project["id"].(string), "all", 50)
		for _, c := range convs {
			msgs, _ := e.st.Chat().ListMessages(context.Background(), c.ID)
			for _, m := range msgs {
				if strings.HasPrefix(m.Content, "[Office] Your job changed") {
					out = append(out, m.Content)
				}
			}
		}
		return out
	}
	// first real change: the brief's CreateConversation fails (transient).
	put(map[string]any{"role": "Trưởng phòng kinh doanh"})
	for i := 0; i < 50; i++ {
		time.Sleep(40 * time.Millisecond)
		if wrapped.failed.Load() {
			break
		}
	}
	if !wrapped.failed.Load() {
		t.Fatalf("the brief's CreateConversation never ran")
	}
	time.Sleep(100 * time.Millisecond) // let the failed goroutine finish
	if n := len(briefs()); n != 0 {
		t.Fatalf("a failed brief still left a message: %d", n)
	}
	// second real change right after: should still brief, not be folded away.
	put(map[string]any{"role": "Giám đốc kinh doanh"})
	var got []string
	for i := 0; i < 50 && len(got) == 0; i++ {
		time.Sleep(40 * time.Millisecond)
		got = briefs()
	}
	if len(got) != 1 || !strings.Contains(got[0], "Giám đốc kinh doanh") {
		t.Fatalf("agent was not re-briefed after the transient failure: %q", got)
	}
}

// blockingBrief wraps a Store so every CreateConversation call whose title
// looks like a role-change brief ("Cập nhật vai:") blocks until released,
// letting a test force two briefRole calls to overlap.
type blockingBrief struct {
	storage.Store
	release chan struct{}
	calls   atomic.Int32
}

type blockingBriefChatRepo struct {
	storage.ChatRepo
	owner *blockingBrief
}

func (s *blockingBrief) Chat() storage.ChatRepo { return blockingBriefChatRepo{s.Store.Chat(), s} }

func (c blockingBriefChatRepo) CreateConversation(ctx context.Context, conv storage.Conversation) (storage.Conversation, error) {
	if strings.HasPrefix(conv.Title, "Cập nhật vai:") {
		c.owner.calls.Add(1)
		<-c.owner.release
	}
	return c.ChatRepo.CreateConversation(ctx, conv)
}

// Two role edits on the same agent in quick succession must brief it only
// once: the dedup slot has to be reserved synchronously before Chat.Brief
// runs, not only after it succeeds, or both edits race past the check and
// each sends its own "your job changed" brief.
func TestRoleChangeBriefDoesNotRaceOnConcurrentEdits(t *testing.T) {
	var wrapped *blockingBrief
	e := setupWithStore(t, nil, func(st storage.Store) storage.Store {
		wrapped = &blockingBrief{Store: st, release: make(chan struct{})}
		return wrapped
	})
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	project := body["project"].(map[string]any)
	a := project["agents"].([]any)[0].(map[string]any)
	id := a["id"].(string)
	put := func(change map[string]any) {
		t.Helper()
		in := map[string]any{}
		for _, k := range []string{"key", "name", "role", "description", "provider_id", "model_tier", "llm_model", "instructions", "permissions"} {
			in[k] = a[k]
		}
		for k, v := range change {
			in[k] = v
		}
		if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id, in, nil); resp.StatusCode != 200 {
			t.Fatalf("PATCH = %d %v", resp.StatusCode, b)
		}
	}
	put(map[string]any{"role": "Trưởng phòng kinh doanh"})
	put(map[string]any{"role": "Giám đốc kinh doanh"})
	close(wrapped.release)
	time.Sleep(200 * time.Millisecond)
	if n := wrapped.calls.Load(); n != 1 {
		t.Fatalf("Brief invoked %d times for two quick edits, want 1", n)
	}
}
