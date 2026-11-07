package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func scoreJob(t *testing.T, st storage.Store, projectID string) storage.Job {
	t.Helper()
	j, err := st.Jobs().Create(context.Background(), storage.Job{ProjectID: projectID, Kind: "chat_turn", Origin: "user", Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestScoresUpsertGet(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	j := scoreJob(t, st, p.ID)
	yes := true
	fin := time.Now().UTC().Truncate(time.Second)
	s := storage.JobScore{JobID: j.ID, ProjectID: p.ID, ConversationID: "cnv_1", MessageID: "msg_1", AgentID: "agt_1", ProviderID: "prv_1",
		Model: "claude-opus-5-5", Effort: "medium", TaskKind: "bugfix", Difficulty: "medium", Status: "done",
		Parts: storage.ScoreParts{NoCorrection: &yes}, CostUSD: 0.5, DurationMS: 1200, FinishedAt: fin}
	if err := st.Scores().Upsert(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := st.Scores().Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Q != nil || got.Parts.Merged != nil || got.Parts.NoCorrection == nil || !*got.Parts.NoCorrection {
		t.Fatalf("nil-ness lost: %+v", got)
	}
	if got.Model != s.Model || got.Effort != "medium" || got.TaskKind != "bugfix" || !got.FinishedAt.Equal(fin) || got.CostUSD != 0.5 {
		t.Fatalf("round trip = %+v", got)
	}
	q := 80
	s.Q, s.Feedback = &q, 1
	if err := st.Scores().Upsert(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Scores().ByMessage(ctx, "msg_1")
	if got.Q == nil || *got.Q != 80 || got.Feedback != 1 {
		t.Fatalf("update = %+v", got)
	}
	if _, err := st.Scores().Get(ctx, "job_none"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	list, err := st.Scores().List(ctx, storage.ScoreFilter{ProjectID: p.ID, Since: fin.Add(-time.Hour)})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
	if list, _ := st.Scores().List(ctx, storage.ScoreFilter{ProjectID: p.ID, AgentID: "agt_2"}); len(list) != 0 {
		t.Fatalf("agent filter = %v", list)
	}
}

func TestScoresSetLabelsManual(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	j := scoreJob(t, st, p.ID)
	_ = st.Scores().Upsert(ctx, storage.JobScore{JobID: j.ID, ProjectID: p.ID, TaskKind: "ask", Difficulty: "light", FinishedAt: time.Now().UTC()})
	if err := st.Scores().SetLabels(ctx, j.ID, "feature", "heavy"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Scores().Get(ctx, j.ID)
	if got.TaskKind != "feature" || got.Difficulty != "heavy" || !got.LabelManual {
		t.Fatalf("labels = %+v", got)
	}
	// a later re-score keeps the person's labels
	got.TaskKind, got.Difficulty, got.LabelManual = "ask", "light", false
	_ = st.Scores().Upsert(ctx, got)
	got, _ = st.Scores().Get(ctx, j.ID)
	if got.TaskKind != "feature" || !got.LabelManual {
		t.Fatalf("manual labels overwritten: %+v", got)
	}
	if err := st.Scores().SetLabels(ctx, "job_none", "ask", "light"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestScoresOpenSkipsFrozen(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	a, b := scoreJob(t, st, p.ID), scoreJob(t, st, p.ID)
	_ = st.Scores().Upsert(ctx, storage.JobScore{JobID: a.ID, ProjectID: p.ID, FinishedAt: old})
	_ = st.Scores().Upsert(ctx, storage.JobScore{JobID: b.ID, ProjectID: p.ID, FinishedAt: old, Frozen: true})
	open, err := st.Scores().Open(ctx, time.Now().UTC().Add(-7*24*time.Hour))
	if err != nil || len(open) != 1 || open[0].JobID != a.ID {
		t.Fatalf("open = %v %v", open, err)
	}
}

func TestScoresCascadeOnJobDelete(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	j := scoreJob(t, st, p.ID)
	_ = st.Scores().Upsert(ctx, storage.JobScore{JobID: j.ID, ProjectID: p.ID, FinishedAt: time.Now().UTC()})
	if err := st.Repos().Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Scores().Get(ctx, j.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("score kept after its job went: %v", err)
	}
}

func TestRunEffortAndMemberModel(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	if _, err := st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: p.ID, Status: "ok", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	runs, _ := st.Runs().List(ctx, storage.RunFilter{ProjectID: p.ID})
	if len(runs) != 1 || runs[0].Effort != "high" {
		t.Fatalf("runs = %+v", runs)
	}
	c, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "agt_1", AgentName: "A"})
	_ = st.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "agt_2", AgentName: "B"})
	if err := st.Chat().SetMemberModel(ctx, c.ID, "agt_1", "claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	// a session update does not wipe the choice
	_ = st.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "agt_1", AgentName: "A", SessionID: "s1"})
	ms, _ := st.Chat().Members(ctx, c.ID)
	got := map[string]string{}
	for _, m := range ms {
		got[m.AgentID] = m.LLMModel
	}
	if got["agt_1"] != "claude-sonnet-5-5" || got["agt_2"] != "" {
		t.Fatalf("member models = %v", got)
	}
	if err := st.Chat().SetMemberModel(ctx, c.ID, "agt_9", "x"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("non-member err = %v", err)
	}
}
