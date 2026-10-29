package trigger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A script starts a workflow of the project by printing "@@workflow <key>
// <input>" (ADR-237): the script checks for free, the workflow (code, wait,
// condition, an agent only where it needs one) does the rest in a chat of
// its own. Only a scheduled or hand run starts one; a webhook's or a bot's
// script does not (its input is an outsider's).

// maxFlows bounds the workflows one script run starts.
const maxFlows = 3

// FlowCall is one "@@workflow" line: the workflow's key and its input.
type FlowCall struct{ Key, Input string }

// Flows are the "@@workflow <key> <input>" lines of a script's output, one per key.
func Flows(out string) []FlowCall {
	var calls []FlowCall
	for _, l := range strings.Split(out, "\n") {
		rest, ok := flowLine(l)
		if !ok {
			continue
		}
		key, input, _ := strings.Cut(strings.TrimSpace(rest), " ")
		if !workflow.ValidKey(key) || len(calls) >= maxFlows || containsFlow(calls, key) {
			continue
		}
		calls = append(calls, FlowCall{Key: key, Input: strings.TrimSpace(input)})
	}
	return calls
}

func containsFlow(calls []FlowCall, key string) bool {
	for _, c := range calls {
		if c.Key == key {
			return true
		}
	}
	return false
}

func flowLine(l string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(l), "@@workflow")
	if !ok || rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != ':' {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, ":")), true
}

// startFlows starts the workflows a script's output names, each under a job
// of the run (its cost counts toward the automation's day), and says so.
func (r *Runner) startFlows(ctx context.Context, a storage.Automation, j storage.Job, out string) string {
	calls := Flows(out)
	if len(calls) == 0 {
		return ""
	}
	if j.Trigger != "schedule" && !(j.Trigger == "manual" && j.Payload == "") {
		return fmt.Sprintf("Bỏ qua %d quy trình: chỉ lượt chạy theo lịch hoặc chạy tay mới mở quy trình", len(calls))
	}
	ag, ok := r.answerer(ctx, a, j.Trigger, j.Payload)
	if !ok || ag.Disabled {
		return "Bỏ qua quy trình: project không có agent đang bật để chạy"
	}
	var started []string
	for _, c := range calls {
		now := r.now().UTC()
		child, err := r.store.Jobs().Create(ctx, storage.Job{ProjectID: a.ProjectID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: j.Trigger,
			Status: "running", StartedAt: &now, ParentJobID: j.ID, Title: a.Name + " · " + workflow.Prefix + c.Key, AgentID: ag.ID})
		if err != nil {
			slog.Warn("automation: workflow job", "automation", a.ID, "workflow", c.Key, "err", err)
			continue
		}
		started = append(started, workflow.Prefix+c.Key)
		fctx := context.WithoutCancel(ctx)
		fctx = actor.With(fctx, "auto:"+a.Name)
		fctx = WithModelTier(fctx, a.ModelTier)
		fctx = WithTags(fctx, a.Config.Tags)
		fctx = WithSkill(fctx, c.Key)
		fctx = proctrack.With(fctx, proctrack.Info{Kind: "automation", ProjectID: a.ProjectID, AutomationID: a.ID, Label: a.Name})
		fctx = usage.WithJob(fctx, child.ID)
		r.wg.Add(1)
		go func(c FlowCall, childID string) {
			defer r.wg.Done()
			conv, _, err := r.exec.RunChat(fctx, a.ProjectID, ag.ID, "", workflow.Call(c.Key, c.Input), a.EditMode)
			cur, gerr := r.store.Jobs().Get(fctx, childID)
			if gerr != nil || cur.Status != "running" && cur.Status != "pending" {
				return
			}
			if conv != "" && cur.ConversationID == "" {
				cur.ConversationID = conv
				_ = r.store.Jobs().Update(fctx, cur)
			}
			status, code, msg := "done", "", ""
			if err != nil {
				status, code, msg = "failed", "agent_error", err.Error()
			}
			_, _ = r.store.Jobs().Finish(fctx, childID, status, code, msg, r.now().UTC())
		}(c, child.ID)
	}
	if len(started) == 0 {
		return ""
	}
	return "Đã mở quy trình " + strings.Join(started, ", ")
}
