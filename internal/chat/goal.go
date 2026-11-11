package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/verdict"
)

// A chat's goal (/goal, ADR-273): ADR-132's "until the goal is reached" for a
// chat a person writes in. After each answer, once the chat is free, office
// runs the check (exit 0 = reached) and/or asks a read-only judge; not
// reached, it sends the chat what they said as the next round, until it is
// reached, the rounds run out, a turn fails, or the person stops the chat or
// clears the goal.

// ChatGoal is a chat's goal as it is kept (settings, "chat_goal/<chat>").
type ChatGoal struct {
	Text      string    `json:"text"`
	Check     string    `json:"check,omitempty"` // a shell command: exit 0 = reached (an admin's)
	MaxRounds int       `json:"max_rounds"`
	Round     int       `json:"round"`        // the round it is on (1 = the first answer)
	By        string    `json:"by,omitempty"` // who set it: its rounds are sent as them
	Since     time.Time `json:"since"`
	Last      string    `json:"last,omitempty"` // what the last check said, in short
}

const (
	goalKeyPrefix     = "chat_goal/"
	maxGoalText       = 4000
	goalFeedbackBytes = 4000
	goalCheckTime     = 5 * time.Minute
)

// GoalError: a /goal that cannot be set as typed (the person fixes it).
type GoalError struct{ msg string }

func (e *GoalError) Error() string { return e.msg }

func goalErr(format string, args ...any) error { return &GoalError{fmt.Sprintf(format, args...)} }

// Touch tells the dashboard the chat changed (its live data).
func (e *Engine) Touch(conversationID string) { e.running(conversationID) }

func goalKey(conversationID string) string { return goalKeyPrefix + conversationID }

// Goal is the chat's goal (nil = none).
func (e *Engine) Goal(ctx context.Context, conversationID string) *ChatGoal {
	var g ChatGoal
	if ok, err := e.store.Settings().Get(ctx, goalKey(conversationID), &g); err != nil || !ok || g.Text == "" {
		return nil
	}
	return &g
}

func (e *Engine) saveGoal(ctx context.Context, conversationID string, g *ChatGoal) error {
	if g == nil {
		g = &ChatGoal{} // an empty one is none
	}
	return e.store.Settings().Set(ctx, goalKey(conversationID), g)
}

// SetGoal gives the chat a goal from a /goal (it replaces one it had); check
// is allowed only when mayCheck (an admin: it runs a shell command).
func (e *Engine) SetGoal(ctx context.Context, conversationID string, s Slash, mayCheck bool) (*ChatGoal, error) {
	text, check := strings.TrimSpace(s.Arg), strings.TrimSpace(s.Check)
	switch {
	case text == "":
		return nil, goalErr("hãy viết mục tiêu sau /goal, ví dụ: /goal sửa cho test xanh")
	case utf8.RuneCountInString(text) > maxGoalText || len(check) > maxGoalText:
		return nil, goalErr("mục tiêu và lệnh kiểm tra tối đa 4000 ký tự")
	case check != "" && !mayCheck:
		return nil, goalErr("chỉ admin mới đặt được lệnh kiểm tra (--check) cho mục tiêu")
	case s.Rounds < 0 || s.Rounds > trigger.MaxGoalRounds:
		return nil, goalErr("số vòng (--rounds) từ 1 tới %d", trigger.MaxGoalRounds)
	}
	conv, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv.Cleaned != "" {
		return nil, ErrCleaned
	}
	if conv.Purpose != "" && conv.Purpose != "channel" {
		return nil, goalErr("chat này không đặt được mục tiêu")
	}
	rounds := s.Rounds
	if rounds == 0 {
		rounds = trigger.DefaultGoalRounds
	}
	g := &ChatGoal{Text: text, Check: check, MaxRounds: rounds, Round: 1, By: actor.From(ctx), Since: time.Now().UTC()}
	if err := e.saveGoal(ctx, conversationID, g); err != nil {
		return nil, err
	}
	e.running(conversationID) // the dashboard shows it
	return g, nil
}

// ClearGoal calls off the chat's goal; why, when given, is said in the chat.
// It reports whether there was one.
func (e *Engine) ClearGoal(ctx context.Context, conversationID, why string) bool {
	if e.Goal(ctx, conversationID) == nil {
		return false
	}
	if err := e.saveGoal(ctx, conversationID, nil); err != nil {
		slog.Warn("chat: goal not cleared", "conversation", conversationID, "err", err)
		return false
	}
	if why != "" {
		e.goalNote(conversationID, why)
	}
	e.running(conversationID)
	return true
}

// GoalStatus is a line on how the chat's goal goes.
func GoalStatus(g *ChatGoal) string {
	if g == nil {
		return "Chat này chưa có mục tiêu. Đặt bằng /goal <mục tiêu>."
	}
	s := fmt.Sprintf("🎯 Mục tiêu: %s\nVòng %d/%d", g.Text, g.Round, g.MaxRounds)
	if g.Check != "" {
		s += " · kiểm tra: `" + g.Check + "`"
	}
	if g.Last != "" {
		s += "\nLần kiểm gần nhất: " + g.Last
	}
	return s + "\nGửi /goal clear để huỷ."
}

// goalInstructions tell the agent of the goal's first answer how it works.
func goalInstructions(g *ChatGoal) string {
	return prompts.Render("chat/goal-start", map[string]any{"Goal": g.Text, "Check": g.Check, "Max": g.MaxRounds})
}

// StartGoal sets the chat's goal and sends it as the next message (it waits
// in the chat while an answer runs: the goal goes on once it is sent).
func (e *Engine) StartGoal(ctx context.Context, conversationID string, s Slash, mayCheck bool, pageContext string, attachmentIDs []string) (*ChatGoal, *Turn, storage.Message, error) {
	g, err := e.SetGoal(ctx, conversationID, s, mayCheck)
	if err != nil {
		return nil, nil, storage.Message{}, err
	}
	turn, msg, err := e.SendWithContext(WithInstructions(ctx, goalInstructions(g)), conversationID, g.Text, pageContext, attachmentIDs)
	var off *OffError
	if err != nil && !errors.Is(err, ErrBusy) && !errors.As(err, &off) {
		e.ClearGoal(context.Background(), conversationID, "")
	}
	return g, turn, msg, err
}

// goalLocks: one look at a chat's goal at a time (several turns may end together).
var goalLocks sync.Map // conversation id → *sync.Mutex

// goalAfter runs once a turn of the chat ended (failed: why it failed): the
// chat free, its goal is checked and, not reached, the next round sent.
func (e *Engine) goalAfter(conversationID, failed string) {
	ctx := context.Background()
	g := e.Goal(ctx, conversationID)
	if g == nil || e.stopping.Load() {
		return
	}
	mu, _ := goalLocks.LoadOrStore(conversationID, &sync.Mutex{})
	if !mu.(*sync.Mutex).TryLock() { // being checked: that look goes on
		return
	}
	defer mu.(*sync.Mutex).Unlock()
	if failed != "" {
		e.ClearGoal(ctx, conversationID, fmt.Sprintf("🎯 Dừng theo mục tiêu ở vòng %d: lượt trả lời lỗi.", g.Round))
		return
	}
	if !e.chatIdle(ctx, conversationID) { // more to come (hand-offs, messages waiting): the last of them checks
		return
	}
	conv, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return
	}
	reply := lastAnswer(e, ctx, conversationID)
	met, fb, err := e.goalCheck(ctx, conv, g, reply)
	if e.Goal(ctx, conversationID) == nil { // cleared or the chat stopped meanwhile
		return
	}
	if err != nil {
		e.ClearGoal(ctx, conversationID, "🎯 Dừng theo mục tiêu: "+err.Error())
		return
	}
	if met {
		e.ClearGoal(ctx, conversationID, fmt.Sprintf("🎯 Mục tiêu đạt sau %d vòng: %s", g.Round, g.Text))
		return
	}
	if g.Round >= g.MaxRounds {
		e.ClearGoal(ctx, conversationID, fmt.Sprintf("🎯 Mục tiêu chưa đạt sau %d vòng (hết số vòng): %s\n\n%s", g.Round, g.Text, fb.short()))
		return
	}
	if !e.chatIdle(ctx, conversationID) { // the person wrote while it checked: theirs goes first
		return
	}
	g.Round++
	g.Last = fb.short()
	if err := e.saveGoal(ctx, conversationID, g); err != nil {
		return
	}
	text := prompts.Render("trigger/goal-again", map[string]any{"Round": g.Round, "Max": g.MaxRounds, "Goal": g.Text,
		"Check": g.Check, "Exit": fb.exit, "Output": fb.output, "Judge": fb.judge, "Note": ""})
	sctx := actor.With(ctx, g.By)
	if _, _, err := e.SendWithContext(sctx, conversationID, "[office] "+text, "", nil); err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrAgentBusy) {
		e.ClearGoal(ctx, conversationID, "🎯 Dừng theo mục tiêu: không gửi được vòng tiếp ("+err.Error()+")")
	}
}

// chatIdle: nothing answers in the chat, nothing waits there, no run goes on.
func (e *Engine) chatIdle(ctx context.Context, conversationID string) bool {
	e.mu.Lock()
	_, busy := e.active[conversationID]
	for k := range e.bg {
		if strings.HasPrefix(k, conversationID+"/") {
			busy = true
		}
	}
	e.mu.Unlock()
	return !busy && e.runOf(conversationID) == nil && !e.HasQueued(ctx, conversationID)
}

// lastAnswer is the chat's last answer ("" = none).
func lastAnswer(e *Engine, ctx context.Context, conversationID string) string {
	msgs, err := e.store.Chat().ListMessages(ctx, conversationID)
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && !IsBtw(msgs[i]) {
			return msgs[i].Content
		}
	}
	return ""
}

// goalFb is what a round's checks said.
type goalFb struct{ exit, output, judge string }

func (f goalFb) short() string {
	switch {
	case f.judge != "":
		return truncate(strings.Join(strings.Fields(f.judge), " "), 300)
	case f.exit != "":
		return "lệnh kiểm tra chưa đạt (" + f.exit + ")"
	}
	return ""
}

// goalCheck runs the goal's check, then (passed, or none) the judge.
func (e *Engine) goalCheck(ctx context.Context, conv storage.Conversation, g *ChatGoal, reply string) (bool, goalFb, error) {
	var fb goalFb
	if g.Check != "" {
		out, code, timedOut, err := trigger.RunScript(ctx, e.goalDir(ctx, conv), storage.AutomationScript{Lang: "bash", Body: g.Check, TimeoutS: int(goalCheckTime / time.Second)},
			[]string{"OFFICE_CONVERSATION=" + conv.ID, "OFFICE_ROUND=" + fmt.Sprint(g.Round)}, "")
		if err != nil {
			return false, fb, fmt.Errorf("không chạy được lệnh kiểm tra: %w", err)
		}
		fb.output = tailBytes(strings.TrimSpace(out), goalFeedbackBytes)
		switch {
		case timedOut:
			fb.exit = "timed out"
			return false, fb, nil
		case code != 0:
			fb.exit = fmt.Sprintf("exit %d", code)
			return false, fb, nil
		}
		if g.Text == "" {
			return true, fb, nil
		}
	}
	said, err := e.goalJudge(ctx, conv, g, reply)
	if err != nil {
		return false, fb, fmt.Errorf("agent đánh giá lỗi: %w", err)
	}
	if verdict.Of(said) == "yes" { // unclear counts as not yet
		return true, fb, nil
	}
	fb.judge = tailBytes(strings.TrimSpace(said), goalFeedbackBytes)
	return false, fb, nil
}

// goalDir is where the check runs: the chat's worktree, else the project folder.
func (e *Engine) goalDir(ctx context.Context, conv storage.Conversation) string {
	if e.trees != nil && e.trees.Exists(conv.ProjectID, ChatTree(conv.ID)) {
		return e.trees.Path(conv.ProjectID, ChatTree(conv.ID))
	}
	dir, _ := os.UserHomeDir()
	if p, err := e.store.Repos().Get(ctx, conv.ProjectID); err == nil && p.Path != "" {
		dir = p.Path
	}
	return dir
}

// goalJudge asks the chat's agent, in a hidden read-only chat, whether the
// goal is met now.
func (e *Engine) goalJudge(ctx context.Context, conv storage.Conversation, g *ChatGoal, reply string) (string, error) {
	agent, err := e.agentFor(ctx, conv)
	if err != nil {
		return "", err
	}
	text := prompts.Render("trigger/goal-judge", map[string]any{"Automation": firstNonEmpty(conv.Title, "chat"), "Goal": g.Text, "Check": g.Check,
		"Reply": tailBytes(strings.TrimSpace(reply), goalFeedbackBytes), "Agree": verdict.Agree, "Disagree": verdict.Disagree})
	return e.askAside(actor.With(ctx, g.By), conv.ProjectID, agent.ID, GoalJudgePurpose, text, 15*time.Minute)
}

// askAside asks an agent in a hidden chat of its own, read only, and waits
// for its answer (at most limit).
func (e *Engine) askAside(ctx context.Context, projectID, agentID, purpose, prompt string, limit time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	conv, err := e.StartConversationPurpose(ctx, projectID, agentID, purpose)
	if err != nil {
		return "", err
	}
	if err := e.SetMode(ctx, conv.ID, perm.Read); err != nil {
		return "", err
	}
	turn, _, err := e.Send(WithCeiling(ctx, perm.Read), conv.ID, prompt, nil)
	if err != nil {
		return "", err
	}
	reply := ""
	for seq := 0; ; {
		evs, done, wake := turn.Since(seq)
		seq += len(evs)
		for _, ev := range evs {
			if ev.Type == "error" {
				return "", errors.New(ev.Text)
			}
			if ev.Type == "done" && ev.Message != nil {
				reply = ev.Message.Content
			}
		}
		if done {
			return reply, nil
		}
		select {
		case <-wake:
		case <-ctx.Done():
			turn.Cancel()
			return "", errors.New("quá thời gian chờ câu trả lời")
		}
	}
}

// tailBytes keeps the last n bytes of s, cut on a character.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for s != "" && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "…" + s
}

// goalNote leaves office's line on the goal in the chat.
func (e *Engine) goalNote(conversationID, text string) {
	_, _ = e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: conversationID, Role: "assistant", Author: "office", Content: text})
}
