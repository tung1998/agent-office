package scoring_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/scoring"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

type env struct {
	st   storage.Store
	p    storage.Repo
	conv storage.Conversation
	rec  scoring.Recorder
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	conv, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, p: p, conv: conv, now: time.Now().UTC().Truncate(time.Second)}
	e.rec = scoring.Recorder{St: st, Now: func() time.Time { return e.now }}
	return e
}

// job makes a finished chat turn with one run and its answer message.
func (e *env) job(t *testing.T, status string, tools []storage.ToolCall) storage.Job {
	t.Helper()
	ctx := context.Background()
	msg, err := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: e.conv.ID, Role: "assistant", Content: "ok", Tools: tools, AgentID: "agt_1"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.st.Jobs().Create(ctx, storage.Job{ProjectID: e.p.ID, Kind: "chat_turn", Origin: "user", Status: status, ConversationID: e.conv.ID, MessageID: msg.ID, AgentID: "agt_1"})
	if err != nil {
		t.Fatal(err)
	}
	c := 0.42
	if _, err := e.st.Runs().Create(ctx, storage.Run{Kind: "chat", JobID: j.ID, ProjectID: e.p.ID, Model: "claude-sonnet-5-5", Effort: "medium", Status: "ok", CostUSD: &c, DurationMS: 900}); err != nil {
		t.Fatal(err)
	}
	j.FinishedAt = &e.now
	return j
}

func (e *env) score(t *testing.T, jobID string) storage.JobScore {
	t.Helper()
	s, err := e.st.Scores().Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func q(s storage.JobScore) int {
	if s.Q == nil {
		return -1
	}
	return *s.Q
}

func TestJobEndedCreatesScore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "done", []storage.ToolCall{{Name: "Bash", Summary: "go test ./internal/..."}})
	if err := e.rec.JobEnded(ctx, j, "Sửa lỗi đăng nhập", scoring.Source{}); err != nil {
		t.Fatal(err)
	}
	s := e.score(t, j.ID)
	if s.Model != "claude-sonnet-5-5" || s.Effort != "medium" || s.TaskKind != "bugfix" || s.Difficulty != "medium" || s.CostUSD != 0.42 {
		t.Fatalf("score = %+v", s)
	}
	if s.Parts.ChecksGreen == nil || !*s.Parts.ChecksGreen || s.Parts.Merged != nil || q(s) != 100 {
		t.Fatalf("parts = %+v q=%d", s.Parts, q(s))
	}
}

func TestRefreshOnPatchReject(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "done", nil)
	_ = e.rec.JobEnded(ctx, j, "thêm tính năng", scoring.Source{})
	p, err := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: e.conv.ID, MessageID: j.MessageID, Diff: "x", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.rec.Refresh(ctx, e.p.ID)
	if s := e.score(t, j.ID); s.Parts.Merged != nil {
		t.Fatalf("pending patch decided merged: %+v", s.Parts)
	}
	if err := e.st.Chat().DecidePatch(ctx, p.ID, "rejected", "", "me", e.now); err != nil {
		t.Fatal(err)
	}
	_ = e.rec.Refresh(ctx, e.p.ID)
	s := e.score(t, j.ID)
	if s.Parts.Merged == nil || *s.Parts.Merged || q(s) != 33 { // 20 of 60
		t.Fatalf("after reject: %+v q=%d", s.Parts, q(s))
	}
}

func TestCorrectionAndFeedback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "done", nil)
	_ = e.rec.JobEnded(ctx, j, "ADR là gì", scoring.Source{})
	if err := e.rec.MarkCorrection(ctx, e.conv.ID); err != nil {
		t.Fatal(err)
	}
	if s := e.score(t, j.ID); q(s) != 0 {
		t.Fatalf("corrected q = %d", q(s))
	}
	// the person's 👍 outweighs the guess
	if err := e.rec.SetFeedback(ctx, j.MessageID, 1); err != nil {
		t.Fatal(err)
	}
	if s := e.score(t, j.ID); q(s) != 100 || s.Feedback != 1 {
		t.Fatalf("feedback: %+v", s)
	}
	_ = e.rec.MarkCorrection(ctx, e.conv.ID)
	if s := e.score(t, j.ID); q(s) != 100 {
		t.Fatalf("a guess overrode the person's 👍: %d", q(s))
	}
	if err := e.rec.SetFeedback(ctx, "msg_none", 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown message err = %v", err)
	}
}

func TestFailedJobHasNilQ(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "failed", nil)
	_ = e.rec.JobEnded(ctx, j, "x", scoring.Source{})
	if s := e.score(t, j.ID); s.Q != nil || s.Status != "failed" {
		t.Fatalf("failed job scored: %+v", s)
	}
}

func TestFreezeAfter7Days(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "done", nil)
	_ = e.rec.JobEnded(ctx, j, "x", scoring.Source{})
	e.now = e.now.Add(8 * 24 * time.Hour)
	if err := e.rec.Refresh(ctx, e.p.ID); err != nil {
		t.Fatal(err)
	}
	if s := e.score(t, j.ID); !s.Frozen {
		t.Fatalf("not frozen: %+v", s)
	}
	if err := e.rec.SetFeedback(ctx, j.MessageID, -1); !errors.Is(err, storage.ErrStale) {
		t.Fatalf("feedback on frozen err = %v", err)
	}
	_ = e.rec.MarkCorrection(ctx, e.conv.ID)
	if s := e.score(t, j.ID); q(s) != 100 {
		t.Fatalf("frozen score changed: %d", q(s))
	}
}

// A refresh that read a score before the person's 👎 must not write it back
// over the 👎 (review finding 1).
func TestStaleRefreshKeepsFeedback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "done", nil)
	_ = e.rec.JobEnded(ctx, j, "x", scoring.Source{})
	old := e.score(t, j.ID) // what a refresh read
	e.now = e.now.Add(time.Second)
	if err := e.rec.SetFeedback(ctx, j.MessageID, -1); err != nil {
		t.Fatal(err)
	}
	q := 100
	old.Q = &q
	if err := e.st.Scores().Update(ctx, old, old.UpdatedAt); !errors.Is(err, storage.ErrStale) {
		t.Fatalf("stale write err = %v", err)
	}
	if s := e.score(t, j.ID); s.Feedback != -1 {
		t.Fatalf("👎 lost: %+v", s)
	}
}

// Past the window a score is final even if nothing froze it yet (finding 2).
func TestFeedbackAfterWindowWithoutRefresh(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.job(t, "done", nil)
	_ = e.rec.JobEnded(ctx, j, "x", scoring.Source{})
	e.now = e.now.Add(8 * 24 * time.Hour)
	if err := e.rec.SetFeedback(ctx, j.MessageID, 1); !errors.Is(err, storage.ErrStale) {
		t.Fatalf("feedback past window err = %v", err)
	}
	_ = e.rec.MarkCorrection(ctx, e.conv.ID)
	if s := e.score(t, j.ID); q(s) != 100 {
		t.Fatalf("correction past window changed Q: %d", q(s))
	}
}
