package chat

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/advice"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// An agent stuck on the same failure (the same command failing the same way
// twice in its run) gets a second opinion from an agent of another vendor,
// once, with the failure itself (ADR-262). The tracker counts; the engine
// picks the advisor, asks it in a plain read-only turn and keeps what it
// said for the chat.

// AdviceTimeout is how long an advisor may take to answer (tests make it short).
var AdviceTimeout = 3 * time.Minute

// adviceDiffMax is how much of the stuck run's diff goes to the advisor.
const adviceDiffMax = 6000

// AdviceDTO is an advice for the dashboard.
type AdviceDTO struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id,omitempty"`
	Trigger     string    `json:"trigger"`
	AgentName   string    `json:"agent_name"`
	AdvisorName string    `json:"advisor_name,omitempty"`
	Provider    string    `json:"provider,omitempty"`
	Model       string    `json:"model,omitempty"`
	Command     string    `json:"command,omitempty"`
	Error       string    `json:"error,omitempty"`
	Answer      string    `json:"answer,omitempty"`
	Objection   bool      `json:"objection,omitempty"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	CostUSD     *float64  `json:"cost_usd,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ToAdviceDTO is an advice as the dashboard shows it.
func ToAdviceDTO(a storage.Advice) AdviceDTO {
	return AdviceDTO{ID: a.ID, MessageID: a.MessageID, Trigger: a.Trigger, AgentName: a.AgentName, AdvisorName: a.AdvisorName,
		Provider: a.Provider, Model: a.Model, Command: a.Command, Error: a.Error, Answer: a.Answer, Objection: a.Objection,
		Status: a.Status, Reason: a.Reason, CostUSD: a.CostUSD, CreatedAt: a.CreatedAt}
}

// Advice is a chat's advices, oldest first.
func (e *Engine) Advice(ctx context.Context, conversationID string) ([]AdviceDTO, error) {
	list, err := e.store.Advice().Conversation(ctx, conversationID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]AdviceDTO, 0, len(list))
	for _, a := range list {
		out = append(out, ToAdviceDTO(a))
	}
	return out, nil
}

// adviceMode is when an advisor is asked (advice.Mode*).
func (e *Engine) adviceMode(ctx context.Context) string {
	if e.usage == nil {
		return advice.ModeOff
	}
	st, _ := e.usage.Settings(ctx)
	return st.Advisor()
}

// adviceAsk is one question to an advisor.
type adviceAsk struct {
	projectID, conversationID, jobID string
	agent                            storage.Agent // the stuck one
	dir                              string        // its worktree ("" = the project folder)
	trigger                          string
	command, errText                 string // repeat: what failed
	count                            int
	answer                           string   // done: the answer to review
	failures                         []string // done: what it repeated
}

// pickAdvisor is the agent of the project that answers for stuck: on, not
// stuck itself, of another vendor; one of a strong tier first. why says
// why there is none.
func (e *Engine) pickAdvisor(ctx context.Context, projectID string, stuck storage.Agent) (storage.Agent, string) {
	agents, err := e.Agents(ctx, projectID)
	if err != nil {
		return storage.Agent{}, err.Error()
	}
	mine, _ := e.agentFamily(ctx, stuck)
	var best storage.Agent
	rank := -1
	for _, a := range storage.OnAgents(agents) {
		if a.ID == stuck.ID {
			continue
		}
		fam, _ := e.agentFamily(ctx, a) // "" also when it cannot run now (a cap, no connection)
		if fam == "" || fam == mine {
			continue
		}
		r := 0
		if a.ModelTier == storage.TierStrong || a.ModelTier == "" {
			r = 1
		}
		if r > rank {
			best, rank = a, r
		}
	}
	if rank < 0 {
		return best, "no agent of another AI vendor in the project can answer now"
	}
	return best, ""
}

// agentNamed is the project's agent called name (key or name).
func (e *Engine) agentNamed(ctx context.Context, projectID, name string) (storage.Agent, bool) {
	agents, err := e.Agents(ctx, projectID)
	if err != nil {
		return storage.Agent{}, false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	i := slices.IndexFunc(agents, func(a storage.Agent) bool { return strings.ToLower(a.Name) == name || strings.ToLower(a.Key) == name })
	if i < 0 {
		return storage.Agent{}, false
	}
	return agents[i], true
}

// diffBrief is what dir changed (git diff against HEAD), cut.
func diffBrief(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	run := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout = &out
		if cmd.Run() != nil {
			return ""
		}
		return strings.TrimSpace(out.String())
	}
	stat := run("diff", "--stat", "HEAD")
	if stat == "" {
		return ""
	}
	return truncate(stat+"\n\n"+run("diff", "HEAD"), adviceDiffMax)
}

// askAdvisor asks an advisor q and keeps what came of it (answered,
// failed, or skipped with why). It is not asked when the settings turn it
// off: the zero Advice comes back then.
func (e *Engine) askAdvisor(ctx context.Context, q adviceAsk) storage.Advice {
	rec := storage.Advice{ProjectID: q.projectID, ConversationID: q.conversationID, JobID: q.jobID, AgentID: q.agent.ID, AgentName: q.agent.Name,
		Trigger: q.trigger, Command: truncate(q.command, 500), Error: truncate(q.errText, 2000)}
	if e.adviceMode(ctx) == advice.ModeOff {
		return storage.Advice{}
	}
	save := func() storage.Advice {
		if q.jobID != "" { // the run already ended: its answer carries it
			if j, err := e.store.Jobs().Get(ctx, q.jobID); err == nil && j.MessageID != "" {
				rec.MessageID = j.MessageID
			}
		}
		saved, err := e.store.Advice().Create(context.WithoutCancel(ctx), rec)
		if err != nil {
			slog.Warn("advice: save", "err", err)
			return rec
		}
		if t, ok := e.TurnByJob(q.jobID); ok {
			d := ToAdviceDTO(saved)
			t.emit(Event{Type: "advice", Advice: &d})
		}
		return saved
	}
	skip := func(why string) storage.Advice {
		rec.Status, rec.Reason = storage.AdviceSkipped, why
		return save()
	}
	if e.usage != nil {
		st, _ := e.usage.Settings(ctx)
		spent, _ := e.store.Advice().SpentSince(ctx, e.usage.StartOfDay())
		if budget := st.AdvisorBudget(); spent >= budget {
			return skip(fmt.Sprintf("the advisors' budget for today is used up ($%.2f of $%.2f)", spent, budget))
		}
	}
	project, err := e.store.Repos().Get(ctx, q.projectID)
	if err != nil {
		return skip(err.Error())
	}
	adv, why := e.pickAdvisor(ctx, q.projectID, q.agent)
	if why != "" {
		return skip(why)
	}
	rec.AdvisorID, rec.AdvisorName = adv.ID, adv.Name
	dir := q.dir
	if dir == "" {
		dir = project.Path
	}
	data := map[string]any{"Agent": q.agent.Name, "Project": project.Name, "Count": q.count, "Command": truncate(q.command, 500),
		"Error": truncate(q.errText, 3000), "Diff": diffBrief(ctx, dir), "Answer": truncate(q.answer, 4000), "Failures": q.failures}
	prompt := prompts.Render("advice/"+q.trigger, data)
	if t, ok := e.TurnByJob(q.jobID); ok {
		t.emit(Event{Type: "status", Text: fmt.Sprintf("Cố vấn %s đang xem lỗi lặp lại của %s…", adv.Name, q.agent.Name)})
	}
	// a plain answer from the advisor's strong model: no tools, nothing run;
	// what it costs counts on the stuck run's job
	actx, cancel := context.WithTimeout(WithNoTools(WithModelTier(usage.WithJob(context.WithoutCancel(ctx), q.jobID), storage.TierStrong)), AdviceTimeout)
	defer cancel()
	out, err := e.Invoke(actx, project, adv, prompt, nil, "advice", nil)
	rec.Provider, rec.Model, rec.CostUSD = out.Provider, out.Model, out.CostUSD
	if err == nil && strings.TrimSpace(out.Text) == "" {
		err = fmt.Errorf("the advisor said nothing")
	}
	if err != nil {
		rec.Status, rec.Reason = storage.AdviceFailed, truncate(err.Error(), 300)
		return save()
	}
	rec.Status, rec.Answer = storage.AdviceAnswered, truncate(strings.TrimSpace(out.Text), 4000)
	rec.Objection = q.trigger == advice.TriggerDone && advice.Objects(rec.Answer)
	return save()
}

// stuckAgent is the agent of sc, the run that failed.
func (e *Engine) stuckAgent(ctx context.Context, sc officetools.Scope) storage.Agent {
	if a, ok := e.agentNamed(ctx, sc.ProjectID, sc.Agent); ok {
		return a
	}
	return storage.Agent{Name: sc.Agent}
}

// AdviseFailure counts a failure of command in sc's run (an office tool
// that failed: run_command, a workflow's gate) and, the second time it
// fails the same way, asks an advisor: what comes back goes with the
// failure to the agent ("" = nothing to add).
func (e *Engine) AdviseFailure(ctx context.Context, sc officetools.Scope, command, errText string) string {
	if sc.Consult || e.adviceMode(ctx) == advice.ModeOff {
		return ""
	}
	f := e.advisor.Failed(sc.JobID, command, errText)
	if !f.Ask {
		return ""
	}
	rec := e.askAdvisor(ctx, adviceAsk{projectID: sc.ProjectID, conversationID: sc.ConversationID, jobID: sc.JobID, agent: e.stuckAgent(ctx, sc),
		dir: sc.Dir, trigger: advice.TriggerRepeat, command: command, errText: errText, count: f.Count})
	return adviceNote(rec, f.Count, "")
}

// adviceNote is what the stuck agent reads of rec (later: the command it
// is about, when it comes after the failure).
func adviceNote(rec storage.Advice, count int, later string) string {
	switch rec.Status {
	case storage.AdviceAnswered:
		data := map[string]any{"Count": count, "Advisor": rec.AdvisorName, "Model": cmp.Or(rec.Model, rec.Provider), "Answer": rec.Answer, "Command": later}
		if later != "" {
			return prompts.Render("advice/later", data)
		}
		return prompts.Render("advice/note", data)
	case storage.AdviceSkipped, storage.AdviceFailed:
		if later != "" {
			return ""
		}
		return prompts.Render("advice/no-advisor", map[string]any{"Count": count, "Reason": rec.Reason})
	}
	return ""
}

// TakeAdvice hands over the advices kept for sc's run (a CLI's own tool
// failed twice: the advice comes with its next office tool call).
func (e *Engine) TakeAdvice(sc officetools.Scope) string {
	notes := e.advisor.Take(sc.JobID)
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "\n\n")
}

// watchFails reads the failures of the CLI's own tools in a turn (its
// Bash, Edit…: event tool_failed, never shown) and, the second time one
// fails the same way, asks an advisor in the background; its answer waits
// for the run's next office tool call, and is kept for the chat.
func (e *Engine) watchFails(ctx context.Context, turn *Turn, conv storage.Conversation, agent storage.Agent, dir string, emit func(Event)) func(Event) {
	return func(ev Event) {
		if ev.Type != "tool_failed" {
			emit(ev)
			return
		}
		if ev.Tool == nil || turn.JobID == "" || e.adviceMode(ctx) == advice.ModeOff {
			return
		}
		command := ev.Tool.Name + ": " + ev.Tool.Summary
		f := e.advisor.Failed(turn.JobID, command, ev.Text)
		if !f.Ask {
			return
		}
		q := adviceAsk{projectID: conv.ProjectID, conversationID: conv.ID, jobID: turn.JobID, agent: agent, dir: dir,
			trigger: advice.TriggerRepeat, command: command, errText: ev.Text, count: f.Count}
		go func() {
			rec := e.askAdvisor(context.WithoutCancel(ctx), q)
			e.advisor.Push(turn.JobID, adviceNote(rec, f.Count, command))
		}()
	}
}

// reviewBeforeDone: a run that repeated a failure ends; under repeat_done
// an advisor reads its answer first. One that objects adds a warning to
// the answer; nothing is held back.
func (e *Engine) reviewBeforeDone(ctx context.Context, turn *Turn, conv storage.Conversation, agent storage.Agent, dir, answer string) string {
	if turn.JobID == "" || strings.TrimSpace(answer) == "" || e.adviceMode(ctx) != advice.ModeRepeatDone || !e.advisor.Repeated(turn.JobID) {
		return answer
	}
	rec := e.askAdvisor(ctx, adviceAsk{projectID: conv.ProjectID, conversationID: conv.ID, jobID: turn.JobID, agent: agent, dir: dir,
		trigger: advice.TriggerDone, answer: answer, failures: e.advisor.Failures(turn.JobID)})
	if !rec.Objection {
		return answer
	}
	_, why, _ := strings.Cut(strings.TrimSpace(rec.Answer), "\n")
	return answer + "\n\n> ⚠️ **Cố vấn " + rec.AdvisorName + " chưa đồng ý:** " + strings.ReplaceAll(strings.TrimSpace(why), "\n", "\n> ")
}

// adviceOf is the advices of a chat by the answer they went to.
func (e *Engine) adviceOf(ctx context.Context, conversationID string) map[string][]AdviceDTO {
	list, err := e.store.Advice().Conversation(ctx, conversationID, 0)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := map[string][]AdviceDTO{}
	for _, a := range list {
		if a.MessageID != "" {
			out[a.MessageID] = append(out[a.MessageID], ToAdviceDTO(a))
		}
	}
	return out
}
