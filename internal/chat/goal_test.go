package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// scripted is a fake Anthropic API answering by what the last message says.
func scripted(t *testing.T, answer func(system, last string) string) fixture {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []json.RawMessage `json:"messages"`
			System   string            `json:"system"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		last := ""
		if n := len(body.Messages); n > 0 {
			last = string(body.Messages[n-1])
		}
		out, _ := json.Marshal(map[string]any{"model": "claude-sonnet-5", "stop_reason": "end_turn",
			"content": []map[string]any{{"type": "text", "text": answer(body.System, last)}}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, err := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		if err != nil {
			t.Fatal(err)
		}
		return p
	})
}

// waitUntil polls until ok (at most 15s).
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func hasMessage(f fixture, convID, part string) bool {
	msgs, _ := f.st.Chat().ListMessages(context.Background(), convID)
	for _, m := range msgs {
		if strings.Contains(m.Content, part) {
			return true
		}
	}
	return false
}

func TestParseSlash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		ok     bool
		want   chat.Slash
		goalOp string
	}{
		{"/btw file nào đang sửa?", true, chat.Slash{Name: "btw", Arg: "file nào đang sửa?"}, ""},
		{"/BTW@office_bot  hi", true, chat.Slash{Name: "btw", Arg: "hi"}, ""},
		{"/goal sửa cho test xanh", true, chat.Slash{Name: "goal", Arg: "sửa cho test xanh"}, "set"},
		{`/goal sửa test --check "go test ./..." --rounds 3`, true, chat.Slash{Name: "goal", Arg: "sửa test", Check: "go test ./...", Rounds: 3}, "set"},
		{"/goal --rounds 2 làm X --check make test", true, chat.Slash{Name: "goal", Arg: "làm X", Check: "make test", Rounds: 2}, "set"},
		{"/goal dùng cờ a--check x", true, chat.Slash{Name: "goal", Arg: "dùng cờ a--check x"}, "set"},
		{"/goal", true, chat.Slash{Name: "goal"}, "status"},
		{"/goal status", true, chat.Slash{Name: "goal", Arg: "status"}, "status"},
		{"/goal clear", true, chat.Slash{Name: "goal", Arg: "clear"}, "clear"},
		{"/goals x", false, chat.Slash{}, ""},
		{"/review x", false, chat.Slash{}, ""},
		{"btw x", false, chat.Slash{}, ""},
	}
	for _, c := range cases {
		got, ok := chat.ParseSlash(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseSlash(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
		if ok && got.Name == "goal" && got.GoalOp() != c.goalOp {
			t.Errorf("GoalOp(%q) = %q, want %q", c.in, got.GoalOp(), c.goalOp)
		}
	}
}

func TestGoalReachedWhenJudgeAgrees(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	sawGoalMode := false
	f := scripted(t, func(system, last string) string {
		if strings.Contains(last, "independent judge") {
			return "VERDICT: AGREE\nĐã xong."
		}
		mu.Lock()
		sawGoalMode = sawGoalMode || strings.Contains(system, "GOAL MODE")
		mu.Unlock()
		return "Đã làm xong việc."
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	g, turn, _, err := f.engine.StartGoal(ctx, conv.ID, chat.Slash{Name: "goal", Arg: "viết README"}, false, "", nil)
	if err != nil || g.Round != 1 || g.MaxRounds != 5 || g.By != "human:a@b.c" {
		t.Fatalf("StartGoal = %+v, %v", g, err)
	}
	collect(t, turn)
	waitUntil(t, "the goal reached", func() bool { return hasMessage(f, conv.ID, "Mục tiêu đạt sau 1 vòng") })
	if f.engine.Goal(ctx, conv.ID) != nil {
		t.Fatal("a reached goal is cleared")
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawGoalMode {
		t.Fatal("the first answer's system prompt says goal mode")
	}
}

func TestGoalStopsWhenRoundsRunOut(t *testing.T) {
	t.Parallel()
	f := scripted(t, func(_, last string) string {
		if strings.Contains(last, "independent judge") {
			return "VERDICT: DISAGREE\nCòn thiếu phần cài đặt."
		}
		return "Đã làm một phần."
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	_, turn, _, err := f.engine.StartGoal(ctx, conv.ID, chat.Slash{Name: "goal", Arg: "viết README", Rounds: 2}, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	waitUntil(t, "the rounds run out", func() bool { return hasMessage(f, conv.ID, "chưa đạt sau 2 vòng") })
	if !hasMessage(f, conv.ID, "[office] [Goal not reached yet · round 2 of 2]") || !hasMessage(f, conv.ID, "Còn thiếu phần cài đặt.") {
		t.Fatal("round 2 is sent with what the judge said")
	}
	if f.engine.Goal(ctx, conv.ID) != nil {
		t.Fatal("a goal whose rounds ran out is cleared")
	}
}

func TestGoalCheckCommand(t *testing.T) {
	t.Parallel()
	var dir string
	f := scripted(t, func(_, last string) string {
		if strings.Contains(last, "independent judge") {
			return "VERDICT: AGREE"
		}
		if strings.Contains(last, "round 2 of") { // told the check failed: it does the work
			if !strings.Contains(last, "failed (exit 1)") {
				return "no feedback"
			}
			_ = os.WriteFile(filepath.Join(dir, "done.txt"), []byte("ok"), 0o644)
		}
		return "Đã làm."
	})
	dir = f.dir
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	s := chat.Slash{Name: "goal", Arg: "tạo done.txt", Check: "test -f done.txt"}
	var ge *chat.GoalError
	if _, err := f.engine.SetGoal(ctx, conv.ID, s, false); !errors.As(err, &ge) {
		t.Fatalf("a check without admin = %v, want a GoalError", err)
	}
	_, turn, _, err := f.engine.StartGoal(ctx, conv.ID, s, true, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	waitUntil(t, "the goal reached", func() bool { return hasMessage(f, conv.ID, "Mục tiêu đạt sau 2 vòng") })
}

func TestGoalClearedWhenChatStops(t *testing.T) {
	t.Parallel()
	f := scripted(t, func(_, _ string) string { return "ok" })
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	if _, err := f.engine.SetGoal(ctx, conv.ID, chat.Slash{Name: "goal", Arg: "x"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chat.GoalStatus(f.engine.Goal(ctx, conv.ID)), "Vòng 1/5") {
		t.Fatal("status says the round")
	}
	f.engine.StopAll(conv.ID)
	if f.engine.Goal(ctx, conv.ID) != nil || !hasMessage(f, conv.ID, "Đã huỷ mục tiêu") {
		t.Fatal("stopping the chat calls its goal off")
	}
	if f.engine.ClearGoal(ctx, conv.ID, "") {
		t.Fatal("no goal left to clear")
	}
}

func TestBtwAnswersApart(t *testing.T) {
	t.Parallel()
	f := scripted(t, func(_, last string) string {
		if strings.Contains(last, "Side question") {
			if !strings.Contains(last, "Lần trước hỏi gì") {
				return "missing chat context"
			}
			return "Đang sửa hello.txt."
		}
		return "Lần trước hỏi gì đó."
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	m, err := f.engine.Btw(ctx, conv.ID, "file nào đang sửa?")
	if err != nil || m.Content != "Đang sửa hello.txt." || chat.BtwOf(m) != "file nào đang sửa?" {
		t.Fatalf("Btw = %+v, %v", m, err)
	}
	hist, _ := f.engine.History(ctx, conv.ID)
	if last := hist[len(hist)-1]; last.Btw != "file nào đang sửa?" {
		t.Fatalf("the card carries its question: %+v", last)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
	for _, h := range chat.HistoryFor(msgs, "Trợ lý") {
		if strings.Contains(h.Content, "Đang sửa hello.txt.") {
			t.Fatal("a /btw answer is not part of the agents' history")
		}
	}
	if _, err := f.engine.Btw(ctx, conv.ID, "  "); err == nil {
		t.Fatal("an empty question is refused")
	}
}
