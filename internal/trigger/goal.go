package trigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/verdict"
)

// Goal mode (ADR-132): a chat or workflow run goes on until its goal is
// reached — the check command exits 0 and/or the judge agrees — or it has
// had MaxRounds turns.
const (
	DefaultGoalRounds = 5
	MaxGoalRounds     = 20
	maxGoalCheck      = 4000            // bytes of a check command
	goalFeedback      = 4000            // the last bytes of what the check printed, sent back
	goalCheckTimeout  = 5 * time.Minute // as a script's default
)

// Judger is an Executor that can ask, apart from the run's chat, whether a
// goal is met: a hidden chat with the agent, read only, under the job of ctx.
type Judger interface {
	Judge(ctx context.Context, projectID, agentID, prompt string) (string, error)
}

// WorkDirer is an Executor that knows where a chat's agent edits (its
// worktree; "" = the project folder): the check runs there.
type WorkDirer interface {
	WorkDir(ctx context.Context, projectID, conversationID string) string
}

// CleanGoal tidies g for an action: nil when it has nothing to check with or
// the action is not a chat or a workflow.
func CleanGoal(g *storage.AutomationGoal, action string) (*storage.AutomationGoal, error) {
	if g == nil || action != "chat" && action != "workflow" {
		return nil, nil
	}
	c := *g
	c.Text, c.Check = strings.TrimSpace(c.Text), strings.TrimSpace(c.Check)
	switch {
	case len(c.Check) > maxGoalCheck:
		return nil, errors.New("lệnh kiểm tra mục tiêu tối đa 4000 ký tự")
	case c.MaxRounds < 0 || c.MaxRounds > MaxGoalRounds:
		return nil, fmt.Errorf("số vòng tối đa từ 1 tới %d", MaxGoalRounds)
	case c.Judge && c.Text == "":
		return nil, errors.New("hãy mô tả mục tiêu để agent đánh giá")
	}
	if !c.On() {
		return nil, nil
	}
	return &c, nil
}

func goalRounds(g *storage.AutomationGoal) int {
	if g.MaxRounds < 1 {
		return DefaultGoalRounds
	}
	return min(g.MaxRounds, MaxGoalRounds)
}

// goalRun is how a goal went: the rounds (turns) it took, whether it was
// reached, why it stopped short, and what each round's checks said.
type goalRun struct {
	Rounds  int
	Met     bool
	Stopped string // the reason it stopped before its rounds ran out ("" = none)
	Log     []string
	Exit    int // the last check's exit code (-1: none ran)
}

// summary is the run's line on the goal (for the answer and the job).
func (g goalRun) summary() string {
	switch {
	case g.Met:
		return fmt.Sprintf("Mục tiêu: đạt sau %d vòng", g.Rounds)
	case g.Stopped != "":
		return fmt.Sprintf("Mục tiêu: chưa đạt sau %d vòng (%s)", g.Rounds, g.Stopped)
	}
	return fmt.Sprintf("Mục tiêu: chưa đạt sau %d vòng (hết số vòng)", g.Rounds)
}

// feedback is what the checks said of a round not reached, for the next turn.
type feedback struct {
	exit, output, judge string
}

// pursue checks the goal after the run's first turn (conv, reply) and, not
// reached, sends the chat what the checks said, again and again, until it is
// reached, or the rounds, the time or the day's cost run out, or the job is
// cancelled. It returns the chat, the last answer, how it went, and an error
// that ends the run (a turn that failed, a check that could not run).
func (r *Runner) pursue(ctx, actx context.Context, a storage.Automation, j storage.Job, conv, reply string, started time.Time, loc *time.Location) (string, string, goalRun, error) {
	g := a.Config.Goal
	most := goalRounds(g)
	run := goalRun{Exit: -1}
	// a run put back after a restart goes on with the round it was cut in:
	// its rounds are counted once, not again from 1 (ADR-258)
	for round := max(1, j.GoalRounds); ; round++ {
		run.Rounds = round
		if !r.goalRunning(ctx, j.ID) {
			run.Stopped = "job đã dừng"
			return conv, reply, run, nil
		}
		met, fb, err := r.goalCheck(ctx, actx, a, j, conv, reply, round, &run)
		if err != nil {
			return conv, reply, run, err
		}
		if met {
			run.Met = true
			return conv, reply, run, nil
		}
		if !r.goalRunning(ctx, j.ID) { // cancelled while it checked
			run.Stopped = "job đã dừng"
			return conv, reply, run, nil
		}
		if round >= most {
			return conv, reply, run, nil
		}
		if why := r.goalLimit(ctx, a, started, loc); why != "" {
			run.Stopped = why
			return conv, reply, run, nil
		}
		if err := r.store.Jobs().SetGoalRounds(ctx, j.ID, round+1); err != nil {
			return conv, reply, run, err
		}
		text := prompts.Render("trigger/goal-again", map[string]any{"Round": round + 1, "Max": most, "Goal": goalText(g),
			"Check": g.Check, "Exit": fb.exit, "Output": fb.output, "Judge": fb.judge})
		got, answer, err := r.exec.RunChat(actx, a.ProjectID, a.AgentID, conv, text, a.EditMode)
		if got != "" {
			conv = got
		}
		if errors.Is(err, ErrBusy) { // the run's own chat: busy is no reason to run it all again
			err = errors.New("chat của lượt chạy đang bận: dừng chạy theo mục tiêu")
		}
		if err != nil {
			run.Rounds = round + 1
			return conv, reply, run, err
		}
		reply = answer
	}
}

// recordGoal puts how the goal went on the job (its output: the rounds and
// what each check said). Not reached, with nothing else wrong, the job fails
// as goal_not_met: a result, as a script's non-zero exit, not a breakdown.
func (r *Runner) recordGoal(ctx context.Context, j storage.Job, g goalRun, err error) {
	exit := g.Exit
	if exit < 0 { // the judge alone
		exit = 1
		if g.Met {
			exit = 0
		}
	}
	_ = r.store.Jobs().SetOutput(ctx, j.ID, truncateTail(g.summary()+"\n\n"+strings.Join(g.Log, "\n"), maxOutput), exit)
	if g.Met || err != nil {
		return
	}
	if cur, gerr := r.store.Jobs().Get(ctx, j.ID); gerr == nil && cur.Status == "running" {
		_, _ = r.store.Jobs().Finish(ctx, j.ID, "failed", "goal_not_met", g.summary(), r.now().UTC())
	}
}

// goalText is the goal as the agents read it.
func goalText(g *storage.AutomationGoal) string {
	if g.Text != "" {
		return g.Text
	}
	return "the check command `" + g.Check + "` exits 0"
}

// goalRunning: the job goes on (a turn finished it: it runs again while the
// goal is checked); false when it was cancelled or ended meanwhile.
func (r *Runner) goalRunning(ctx context.Context, jobID string) bool {
	cur, err := r.store.Jobs().Get(ctx, jobID)
	if err != nil {
		return false
	}
	switch cur.Status {
	case "running":
		return true
	case "done":
		cur.Status = "running"
		return r.store.Jobs().Update(ctx, cur) == nil
	}
	return false
}

// goalLimit says why no more round may start: the run's time (MaxMinutes, all
// rounds together) or the day's cost is used up ("" = it may).
func (r *Runner) goalLimit(ctx context.Context, a storage.Automation, started time.Time, loc *time.Location) string {
	now := r.now()
	if a.Limits.MaxMinutes > 0 && now.Sub(started) >= time.Duration(a.Limits.MaxMinutes)*time.Minute {
		return "hết thời gian cho phép"
	}
	if a.Limits.DailyCostUSD > 0 {
		local := now.In(loc)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		if spent, _ := r.store.Jobs().CostSince(ctx, "automation", a.ID, day); spent >= a.Limits.DailyCostUSD {
			return "chạm trần chi phí trong ngày"
		}
	}
	return ""
}

// goalCheck runs the round's check, then (the check passed, or none) the
// judge; met when both say so. It notes each in run.Log.
func (r *Runner) goalCheck(ctx, actx context.Context, a storage.Automation, j storage.Job, conv, reply string, round int, run *goalRun) (bool, feedback, error) {
	g := a.Config.Goal
	var fb feedback
	if g.Check != "" {
		out, code, timedOut, err := RunScript(ctx, r.goalDir(ctx, a.ProjectID, conv), storage.AutomationScript{Lang: "bash", Body: g.Check, TimeoutS: int(goalCheckTimeout / time.Second)},
			[]string{"OFFICE_JOB_ID=" + j.ID, "OFFICE_AUTOMATION=" + a.Name, "OFFICE_ROUND=" + fmt.Sprint(round)}, "")
		if err != nil {
			run.Log = append(run.Log, fmt.Sprintf("Vòng %d: không chạy được lệnh kiểm tra: %v", round, err))
			return false, fb, fmt.Errorf("không chạy được lệnh kiểm tra mục tiêu: %w", err)
		}
		run.Exit = code
		out = strings.TrimSpace(out)
		fb.output = truncateTail(out, goalFeedback)
		switch {
		case timedOut:
			fb.exit = "timed out"
			run.Log = append(run.Log, fmt.Sprintf("Vòng %d: lệnh kiểm tra quá thời gian", round))
		case code != 0:
			fb.exit = fmt.Sprintf("exit %d", code)
			run.Log = append(run.Log, fmt.Sprintf("Vòng %d: kiểm tra chưa đạt (mã %d)", round, code))
		default:
			run.Log = append(run.Log, fmt.Sprintf("Vòng %d: kiểm tra đạt", round))
		}
		if out != "" {
			run.Log = append(run.Log, indent(truncateTail(out, 1500)))
		}
		if timedOut || code != 0 {
			return false, fb, nil
		}
	}
	if !g.Judge {
		return true, fb, nil
	}
	said, err := r.goalJudge(actx, a, j, reply)
	if err != nil {
		run.Log = append(run.Log, fmt.Sprintf("Vòng %d: agent đánh giá lỗi: %v", round, err))
		return false, fb, fmt.Errorf("agent đánh giá mục tiêu lỗi: %w", err)
	}
	met := verdict.Of(said) == "yes" // unclear counts as not yet
	label := "chưa đạt"
	if met {
		label = "đạt"
	}
	run.Log = append(run.Log, fmt.Sprintf("Vòng %d: agent đánh giá: %s", round, label), indent(truncateTail(strings.TrimSpace(said), 1500)))
	if !met {
		fb.judge = truncateTail(strings.TrimSpace(said), goalFeedback)
	}
	return met, fb, nil
}

// goalDir is where the check runs: the chat's worktree when its agent edits
// in one, else the project folder (as a script).
func (r *Runner) goalDir(ctx context.Context, projectID, conv string) string {
	if w, ok := r.exec.(WorkDirer); ok {
		if dir := w.WorkDir(ctx, projectID, conv); dir != "" {
			return dir
		}
	}
	dir, _ := os.UserHomeDir()
	if p, err := r.store.Repos().Get(ctx, projectID); err == nil && p.Path != "" {
		dir = p.Path
	}
	return dir
}

// goalJudge asks the judge under a job of its own within the run (its cost
// counts toward the automation's day), read only.
func (r *Runner) goalJudge(actx context.Context, a storage.Automation, j storage.Job, reply string) (string, error) {
	jd, ok := r.exec.(Judger)
	if !ok {
		return "", errors.New("office này không có agent đánh giá")
	}
	g := a.Config.Goal
	now := r.now().UTC()
	child, err := r.store.Jobs().Create(actx, storage.Job{ProjectID: a.ProjectID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: j.Trigger,
		Status: "running", StartedAt: &now, ParentJobID: j.ID, Title: a.Name + " · đánh giá mục tiêu", AgentID: a.AgentID, ExtraDirs: j.ExtraDirs})
	if err != nil {
		return "", err
	}
	text := prompts.Render("trigger/goal-judge", map[string]any{"Automation": a.Name, "Goal": goalText(g), "Check": g.Check,
		"Reply": truncateTail(strings.TrimSpace(reply), goalFeedback), "Agree": verdict.Agree, "Disagree": verdict.Disagree})
	// its own job; nothing of it goes to a bot's chat, no tags, read only
	jctx := WithTags(WithFollowUp(WithProgress(usage.WithJob(actx, child.ID), nil), nil), nil)
	said, err := jd.Judge(WithCeiling(jctx, perm.Read), a.ProjectID, a.AgentID, text)
	if cur, gerr := r.store.Jobs().Get(actx, child.ID); gerr == nil && (cur.Status == "running" || cur.Status == "pending") {
		status, code, msg := "done", "", ""
		if err != nil {
			status, code, msg = "failed", "agent_error", err.Error()
		}
		_, _ = r.store.Jobs().Finish(actx, child.ID, status, code, msg, r.now().UTC())
	}
	return said, err
}

// truncateTail keeps the last n bytes of s (a cut rune at the start dropped).
func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for s != "" && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "…" + s
}

func indent(s string) string { return "    " + strings.ReplaceAll(s, "\n", "\n    ") }
