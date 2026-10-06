package chat_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// anthropicProvider answers every turn at once, with token usage.
func anthropicProvider(t *testing.T) func(provs *provider.Service) storage.Provider {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"Xong."}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	t.Cleanup(srv.Close)
	return func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, _ := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		return p
	}
}

func TestChatTurnIsAJob(t *testing.T) {
	f := setup(t, anthropicProvider(t))
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	jobs, _ := f.st.Jobs().List(context.Background(), storage.JobFilter{ProjectID: f.project.ID})
	if len(jobs) != 1 || jobs[0].Kind != "chat_turn" || jobs[0].Origin != "user" || jobs[0].Status != "done" ||
		jobs[0].MessageID == "" || jobs[0].ConversationID != conv.ID || jobs[0].InputTokens != 10 || jobs[0].CreatedBy != "human:a@b.c" {
		t.Fatalf("jobs = %+v", jobs)
	}
	// busy: no orphan job
	turn, _, _ = f.engine.Send(ctx, conv.ID, "1", nil)
	if _, _, err := f.engine.Send(ctx, conv.ID, "2", nil); err != chat.ErrBusy {
		t.Fatalf("err = %v", err)
	}
	collect(t, turn)
	if jobs, _ := f.st.Jobs().List(context.Background(), storage.JobFilter{ProjectID: f.project.ID}); len(jobs) != 2 {
		t.Fatalf("busy made a job: %d", len(jobs))
	}
}
