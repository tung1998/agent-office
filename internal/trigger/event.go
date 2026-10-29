package trigger

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Events an automation of source "event" can follow (ADR-243): something
// in the project ended, so the next piece of work starts on its own.
var Events = []string{
	"turn.done", "turn.failed", // a person's chat turn: the agent answered, or it failed
	"automation.done", "automation.failed", // a run of another automation
	"workflow.done", "workflow.failed", // a workflow run (one a chat started, not a sub-workflow)
}

// ValidEvent: e is one of Events.
func ValidEvent(e string) bool { return slices.Contains(Events, e) }

const (
	maxEventDepth      = 5    // automation → automation → …: a chain this long stops
	maxEventRunsPerHr  = 30   // an event automation runs at most this often (a loop the depth misses)
	maxEventText       = 8192 // the answer or result a payload carries
	eventTriggerSource = "event"
)

// EventPayload is what an event run gets as its payload ({{payload.text}}…).
type EventPayload struct {
	Event          string `json:"event"`
	From           string `json:"from,omitempty"` // the agent, automation id or workflow key
	FromName       string `json:"from_name,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	Text           string `json:"text,omitempty"` // the answer, the run's output or the workflow's result
	Depth          int    `json:"depth,omitempty"`
	// Untrusted: it comes (maybe a few links back) from a webhook or a bot's
	// message, so the run never gets full access.
	Untrusted bool `json:"untrusted,omitempty"`
}

// Emit starts the project's event automations that follow ev; id is what
// ended (a job or run id), so the same end never starts one twice.
func (r *Runner) Emit(ctx context.Context, projectID, id string, ev EventPayload) {
	if !ValidEvent(ev.Event) || ev.Depth > maxEventDepth {
		return
	}
	ev.Text = truncateBytes(ev.Text, maxEventText)
	list, err := r.store.Automations().List(ctx, projectID)
	if err != nil {
		return
	}
	body, _ := json.Marshal(ev)
	queued := false
	for _, a := range list {
		c := a.Config
		self := strings.HasPrefix(ev.Event, "automation.") && ev.From == a.ID
		from := c.EventFrom
		if from == storage.EventSelf {
			from = a.ID
		}
		if !a.Enabled || a.Source != eventTriggerSource || c.Event != ev.Event || (from != "" && from != ev.From) {
			continue
		}
		if self && !c.CanRepeat() {
			continue // on its own end only with a stop condition (ADR-244)
		}
		if end := c.EndsAt; end != nil && !r.now().Before(*end) { // its stop time, as a schedule's
			_ = r.store.Automations().Disable(ctx, a.ID, "ended", "đã tới giờ dừng "+end.In(time.Local).Format("15:04 02/01/2006"), nil)
			continue
		}
		if r.eventRunsLastHour(ctx, a.ID) >= maxEventRunsPerHr {
			slog.Warn("trigger: event automation over its hourly runs", "automation", a.ID, "event", ev.Event)
			continue
		}
		if _, status, err := r.enqueueAt(ctx, r.now().UTC(), a, eventTriggerSource, string(body), "event:"+ev.Event+":"+id, "", ev.Untrusted); err == nil && status == "queued" {
			queued = true
		}
	}
	if queued {
		r.StartReady(ctx, r.now().UTC())
	}
}

func (r *Runner) eventRunsLastHour(ctx context.Context, automationID string) int {
	recent, _ := r.store.Jobs().List(ctx, storage.JobFilter{Origin: "automation", OriginID: automationID, Since: r.now().Add(-time.Hour), Limit: 200})
	n := 0
	for _, j := range recent {
		if j.Trigger == eventTriggerSource {
			n++
		}
	}
	return n
}

// eventOf reads the event a job of trigger "event" ran for (zero when not one).
func eventOf(j storage.Job) (ev EventPayload) {
	if j.Trigger == eventTriggerSource {
		_ = json.Unmarshal([]byte(j.Payload), &ev)
	}
	return ev
}

// emitDone tells the project an automation's run ended (automation.*): the
// next link of a chain starts, one deeper, as untrusted as this one was.
func (r *Runner) emitDone(ctx context.Context, a storage.Automation, j storage.Job, text string) {
	var name string
	switch j.Status {
	case "done":
		name = "automation.done"
	case "failed":
		name = "automation.failed"
	default:
		return // skipped, cancelled, waiting: nothing ended
	}
	out := firstNonEmpty(text, j.Output)
	if stop := a.Config.StopWhen; stop != "" && strings.Contains(out, stop) { // its stop condition met: no more runs (ADR-244)
		_ = r.store.Automations().Disable(ctx, a.ID, "ended", "đã gặp điều kiện dừng: "+stop, nil)
	}
	prev := eventOf(j)
	depth := prev.Depth + 1
	if prev.From == a.ID {
		depth = prev.Depth // repeating itself is not a deeper chain: its stop condition ends it (ADR-244)
	}
	r.Emit(ctx, a.ProjectID, j.ID, EventPayload{Event: name, From: a.ID, FromName: a.Name, ConversationID: j.ConversationID,
		Status: j.Status, Error: j.Error, Text: out, Depth: depth,
		Untrusted: prev.Untrusted || j.Trigger == "webhook" || IsChannel(j.Trigger)})
}
