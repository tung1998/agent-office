package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestAdviceRepo(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	done, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", Status: "running"})
	_, _ = st.Jobs().Finish(ctx, done.ID, "done", "", "", time.Now())
	_, _ = st.Jobs().Finish(ctx, failed.ID, "failed", "", "boom", time.Now())
	c1, c2 := 0.25, 0.5
	for _, a := range []storage.Advice{
		{ProjectID: p.ID, ConversationID: "c1", JobID: done.ID, Trigger: "repeat", Command: "go test", Answer: "fix x", Status: storage.AdviceAnswered, CostUSD: &c1},
		{ProjectID: p.ID, ConversationID: "c1", JobID: done.ID, Trigger: "done", Answer: "OBJECT\nno", Objection: true, Status: storage.AdviceAnswered, CostUSD: &c2},
		{ProjectID: p.ID, ConversationID: "c1", JobID: failed.ID, Trigger: "repeat", Answer: "y", Status: storage.AdviceAnswered},
		{ProjectID: p.ID, ConversationID: "c2", JobID: failed.ID, Trigger: "repeat", Status: storage.AdviceSkipped, Reason: "no advisor"},
	} {
		if _, err := st.Advice().Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Advice().SetMessage(ctx, done.ID, "msg1"); err != nil {
		t.Fatal(err)
	}
	list, err := st.Advice().Conversation(ctx, "c1", 0)
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %d %v", len(list), err)
	}
	if list[0].MessageID != "msg1" || list[2].MessageID != "" || !list[1].Objection || list[0].CostUSD == nil || *list[0].CostUSD != 0.25 {
		t.Fatalf("rows wrong: %+v", list)
	}
	since := time.Now().Add(-time.Hour)
	if v, err := st.Advice().SpentSince(ctx, since); err != nil || v != 0.75 {
		t.Fatalf("spent = %v %v", v, err)
	}
	s, err := st.Advice().Stats(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.AdviceStats{Asked: 3, Skipped: 1, Objected: 1, Runs: 2, RunsDone: 1, CostUSD: 0.75}
	if s != want {
		t.Fatalf("stats = %+v, want %+v", s, want)
	}
	if v, _ := st.Advice().SpentSince(ctx, time.Now().Add(time.Hour)); v != 0 {
		t.Fatal("spent counted a future window")
	}
}
