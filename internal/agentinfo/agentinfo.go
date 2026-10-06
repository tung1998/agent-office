// Package agentinfo gathers what the agent page shows about one agent: its
// numbers over a window (runs, success, cost, speed, diffs merged), what it
// worked on (chats and tasks), and how its settings changed, with a way to go
// back to an earlier version.
package agentinfo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage"
)

// Service reads agent information from the store.
type Service struct {
	store storage.Store
	org   *orgmodel.Service
	loc   *time.Location
}

// New builds a Service; days are cut in loc.
func New(store storage.Store, org *orgmodel.Service, loc *time.Location) *Service {
	if loc == nil {
		loc = time.Local
	}
	return &Service{store: store, org: org, loc: loc}
}

// ---- stats ----

// Stats are an agent's numbers over the last Days days.
type Stats struct {
	Days         int         `json:"days"`
	Runs         int         `json:"runs"`
	OK           int         `json:"ok"`
	Errors       int         `json:"errors"`
	SuccessRate  float64     `json:"success_rate"` // 0..1; 0 without runs
	CostUSD      float64     `json:"cost_usd"`
	UnknownCost  int         `json:"unknown_cost"` // runs whose cost is unknown
	InputTokens  int         `json:"input_tokens"`
	OutputTokens int         `json:"output_tokens"`
	P50MS        int64       `json:"p50_ms"`
	P95MS        int64       `json:"p95_ms"`
	LastRunAt    *time.Time  `json:"last_run_at"`
	PerDay       []DayRow    `json:"per_day"`
	ByModel      []ModelRow  `json:"by_model"`
	TopErrors    []ErrorRow  `json:"top_errors"`
	Patches      PatchCounts `json:"patches"`
}

// DayRow is one day of runs.
type DayRow struct {
	Day     string  `json:"day"` // YYYY-MM-DD
	OK      int     `json:"ok"`
	Errors  int     `json:"errors"`
	CostUSD float64 `json:"cost_usd"`
}

// ModelRow is spend on one model.
type ModelRow struct {
	Model   string  `json:"model"`
	Runs    int     `json:"runs"`
	CostUSD float64 `json:"cost_usd"`
}

// ErrorRow is one kind of failure.
type ErrorRow struct {
	Error string    `json:"error"`
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

// PatchCounts are the code changes the agent proposed.
type PatchCounts struct {
	Total    int `json:"total"`
	Merged   int `json:"merged"`
	Pending  int `json:"pending"`
	Rejected int `json:"rejected"`
	Failed   int `json:"failed"`
}

// Stats works out an agent's numbers over the last days days.
func (s *Service) Stats(ctx context.Context, a storage.Agent, projectID string, days int) (Stats, error) {
	days = min(max(days, 1), 90)
	now := time.Now().In(s.loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc).AddDate(0, 0, -(days - 1))
	out := Stats{Days: days, PerDay: []DayRow{}, ByModel: []ModelRow{}, TopErrors: []ErrorRow{}}
	for d := 0; d < days; d++ {
		out.PerDay = append(out.PerDay, DayRow{Day: start.AddDate(0, 0, d).Format(time.DateOnly)})
	}
	runs, err := s.store.Runs().Since(ctx, start)
	if err != nil {
		return out, err
	}
	var durations []int64
	models := map[string]*ModelRow{}
	errs := map[string]*ErrorRow{}
	for _, r := range runs {
		if r.AgentID != a.ID {
			continue
		}
		out.Runs++
		if out.LastRunAt == nil || r.CreatedAt.After(*out.LastRunAt) {
			t := r.CreatedAt
			out.LastRunAt = &t
		}
		day := &out.PerDay[min(max(int(r.CreatedAt.In(s.loc).Sub(start).Hours()/24), 0), days-1)]
		if r.Status == "ok" {
			out.OK++
			day.OK++
		} else {
			out.Errors++
			day.Errors++
			key := errorKey(r.Error, r.Status)
			if e := errs[key]; e != nil {
				e.Count++
				if r.CreatedAt.After(e.Last) {
					e.Last = r.CreatedAt
				}
			} else {
				errs[key] = &ErrorRow{Error: key, Count: 1, Last: r.CreatedAt}
			}
		}
		cost := 0.0
		if r.CostUSD != nil {
			cost = *r.CostUSD
		} else {
			out.UnknownCost++
		}
		out.CostUSD += cost
		day.CostUSD += cost
		out.InputTokens += r.InputTokens
		out.OutputTokens += r.OutputTokens
		if r.DurationMS > 0 {
			durations = append(durations, r.DurationMS)
		}
		m := models[r.Model]
		if m == nil {
			m = &ModelRow{Model: r.Model}
			models[r.Model] = m
		}
		m.Runs++
		m.CostUSD += cost
	}
	if out.Runs > 0 {
		out.SuccessRate = float64(out.OK) / float64(out.Runs)
	}
	slices.Sort(durations)
	out.P50MS, out.P95MS = percentile(durations, 0.5), percentile(durations, 0.95)
	for _, m := range models {
		out.ByModel = append(out.ByModel, *m)
	}
	slices.SortFunc(out.ByModel, func(x, y ModelRow) int { return int((y.CostUSD - x.CostUSD) * 1e6) })
	for _, e := range errs {
		out.TopErrors = append(out.TopErrors, *e)
	}
	slices.SortFunc(out.TopErrors, func(x, y ErrorRow) int { return y.Count - x.Count })
	if len(out.TopErrors) > 5 {
		out.TopErrors = out.TopErrors[:5]
	}
	patches, err := s.patches(ctx, a, projectID, start)
	if err != nil {
		return out, err
	}
	for _, p := range patches {
		out.Patches.Total++
		switch p.Status {
		case "applied":
			out.Patches.Merged++
		case "pending":
			out.Patches.Pending++
		case "rejected":
			out.Patches.Rejected++
		default:
			out.Patches.Failed++
		}
	}
	return out, nil
}

// errorKey groups failures by their first line, without numbers that vary.
func errorKey(msg, status string) string {
	msg = strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	if msg == "" {
		return status
	}
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return msg
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(int(float64(len(sorted)-1)*p+0.5), len(sorted)-1)]
}

// patches are the diffs of the agent's answers and task steps since t.
func (s *Service) patches(ctx context.Context, a storage.Agent, projectID string, since time.Time) ([]storage.Patch, error) {
	var out []storage.Patch
	convs, err := s.store.Chat().ListConversationsFrom(ctx, projectID, "all", 200) // with the bots' chats
	if err != nil {
		return nil, err
	}
	for _, c := range convs {
		if c.AgentID != a.ID || c.UpdatedAt.Before(since) {
			continue
		}
		ps, err := s.store.Chat().ListPatches(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if !p.CreatedAt.Before(since) && p.TaskID == "" {
				out = append(out, p)
			}
		}
	}
	tasks, err := s.store.Tasks().List(ctx, projectID, 100)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.CreatedAt.Before(since) {
			continue
		}
		steps, err := s.store.Tasks().ListSteps(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		mine := map[string]bool{}
		for _, st := range steps {
			if st.AgentID == a.ID {
				mine[st.ID] = true
			}
		}
		if len(mine) == 0 {
			continue
		}
		ps, err := s.store.Tasks().ListPatches(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if mine[p.StepID] {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// ---- activity ----

// Item is one thing the agent worked on.
type Item struct {
	Kind           string    `json:"kind"` // chat | task
	At             time.Time `json:"at"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`           // task status; "" for chats
	Phases         []string  `json:"phases,omitempty"` // task: what the agent did (plan, work, review…)
	Steps          int       `json:"steps,omitempty"`
	CostUSD        float64   `json:"cost_usd"`
	ConversationID string    `json:"conversation_id,omitempty"`
	TaskID         string    `json:"task_id,omitempty"`
	Source         string    `json:"source,omitempty"`  // chat: web | discord | telegram | auto
	Answers        int       `json:"answers,omitempty"` // chat: how many times it answered there
}

// Activity lists the chats the agent took part in, newest first.
func (s *Service) Activity(ctx context.Context, a storage.Agent, projectID string, limit int) ([]Item, error) {
	limit = min(max(limit, 1), 100)
	out := []Item{}
	convs, err := s.store.Chat().ListConversationsFrom(ctx, projectID, "all", 200) // with the bots' chats
	if err != nil {
		return nil, err
	}
	for _, c := range convs {
		// a place it took part in: its own chat, or one it was pulled into (a member)
		in := c.AgentID == a.ID
		if !in {
			if ms, err := s.store.Chat().Members(ctx, c.ID); err == nil {
				in = slices.ContainsFunc(ms, func(m storage.ChatMember) bool { return m.AgentID == a.ID })
			}
		}
		if !in {
			continue
		}
		it := Item{Kind: "chat", At: c.UpdatedAt, Title: c.Title, ConversationID: c.ID, Source: chatSource(c)}
		if said, err := s.answers(ctx, a, c.ID); err == nil {
			it.Answers = len(said)
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(x, y Item) int { return y.At.Compare(x.At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- history ----

// Change is one setting that changed.
type Change struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// Entry is one change to the agent's settings.
type Entry struct {
	RevisionID string    `json:"revision_id"` // the snapshot holding the state before it
	At         time.Time `json:"at"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	Created    bool      `json:"created"` // the agent was added here
	Changes    []Change  `json:"changes"`
	Restorable bool      `json:"restorable"` // there is a before state to go back to
}

// History lists the changes to agent a, newest first. Each snapshot holds
// the model just before a change; the state after it is the next newer
// snapshot, or the model as it is now.
func (s *Service) History(ctx context.Context, a storage.Agent) ([]Entry, error) {
	revs, err := s.store.Revisions().List(ctx, a.OrgModelID, orgmodel.KeepRevisions)
	if err != nil {
		return nil, err
	}
	current, err := s.store.Agents().List(ctx, a.OrgModelID)
	if err != nil {
		return nil, err
	}
	afterKeys := map[string]bool{}
	for _, x := range current {
		afterKeys[x.Key] = true
	}
	after, ok := orgmodel.AgentSpecOf(a), true
	key := a.Key
	out := []Entry{}
	for _, rev := range revs {
		t, err := orgmodel.RevisionTemplate(rev)
		if err != nil {
			continue
		}
		before, found := specByKey(t, key, rev.Action, afterKeys)
		afterKeys = map[string]bool{}
		for _, x := range t.Agents {
			afterKeys[x.Key] = true
		}
		e := Entry{RevisionID: rev.ID, At: rev.CreatedAt, Actor: rev.Actor, Action: rev.Action}
		switch {
		case !found && ok:
			e.Created, e.Changes = true, []Change{}
		case found && ok:
			e.Changes, e.Restorable = diff(before, after), true
		}
		if e.Created || len(e.Changes) > 0 {
			out = append(out, e)
		}
		after, ok, key = before, found, firstNonEmpty(before.Key, key)
		if !found {
			break // older snapshots are from before the agent existed
		}
	}
	return out, nil
}

// specByKey finds an agent in a snapshot. When the change renamed its key
// ("agent.update:<new key>" while the snapshot has the old one), it is the
// one agent of the snapshot whose key is gone afterwards (afterKeys).
func specByKey(t orgmodel.Template, key, action string, afterKeys map[string]bool) (orgmodel.AgentSpec, bool) {
	for _, x := range t.Agents {
		if x.Key == key {
			return x, true
		}
	}
	if action != "agent.update:"+key {
		return orgmodel.AgentSpec{}, false
	}
	var gone []orgmodel.AgentSpec
	for _, x := range t.Agents {
		if !afterKeys[x.Key] {
			gone = append(gone, x)
		}
	}
	if len(gone) == 1 {
		return gone[0], true
	}
	return orgmodel.AgentSpec{}, false
}

var fields = []struct {
	name string
	get  func(orgmodel.AgentSpec) any
}{
	{"name", func(x orgmodel.AgentSpec) any { return x.Name }},
	{"key", func(x orgmodel.AgentSpec) any { return x.Key }},
	{"tier", func(x orgmodel.AgentSpec) any { return x.Tier }},
	{"role", func(x orgmodel.AgentSpec) any { return x.Role }},
	{"description", func(x orgmodel.AgentSpec) any { return x.Description }},
	{"reports_to", func(x orgmodel.AgentSpec) any { return nonNil(x.ReportsTo) }},
	{"provider_id", func(x orgmodel.AgentSpec) any { return x.ProviderID }},
	{"fallback_provider_ids", func(x orgmodel.AgentSpec) any { return nonNil(x.Fallbacks) }},
	{"model_tier", func(x orgmodel.AgentSpec) any { return x.ModelTier }},
	{"llm_model", func(x orgmodel.AgentSpec) any { return x.LLMModel }},
	{"effort", func(x orgmodel.AgentSpec) any { return x.Effort }},
	{"instructions", func(x orgmodel.AgentSpec) any { return x.Instructions }},
	{"permissions", func(x orgmodel.AgentSpec) any { return permView(x.Permissions) }},
}

func diff(before, after orgmodel.AgentSpec) []Change {
	out := []Change{}
	for _, f := range fields {
		b, a := f.get(before), f.get(after)
		if !reflect.DeepEqual(b, a) {
			out = append(out, Change{Field: f.name, Before: b, After: a})
		}
	}
	return out
}

// permView is the permissions as compared and shown (legacy flags left out).
func permView(p storage.Permissions) map[string]any {
	v := map[string]any{"level": p.Level}
	if p.Caps != nil {
		v["caps"] = nonNil(*p.Caps)
	}
	if p.Commands != nil {
		v["commands"] = nonNil(*p.Commands)
	}
	if p.Processes != nil {
		v["processes"] = nonNil(*p.Processes)
	}
	if p.Containers != nil {
		v["containers"] = nonNil(*p.Containers)
	}
	return v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ErrNoBefore: the change added the agent, there is nothing to go back to.
var ErrNoBefore = errors.New("lần này tạo agent nên không có bản trước để quay về")

// Restore puts agent a back to how it was just before the change stored in
// revisionID (its id and key stay). Saving snapshots first, so it can be undone.
func (s *Service) Restore(ctx context.Context, a storage.Agent, revisionID string) (storage.Agent, error) {
	rev, err := s.store.Revisions().Get(ctx, revisionID)
	if err != nil {
		return a, err
	}
	if rev.OrgModelID != a.OrgModelID {
		return a, storage.ErrNotFound
	}
	hist, err := s.History(ctx, a)
	if err != nil {
		return a, err
	}
	// the key the agent had in that snapshot (it may have been renamed since)
	key, known := a.Key, false
	for _, e := range hist {
		for _, c := range e.Changes {
			if c.Field == "key" {
				key = fmt.Sprint(c.Before)
			}
		}
		if e.RevisionID == revisionID {
			known = e.Restorable
			break
		}
	}
	if !known {
		return a, ErrNoBefore
	}
	t, err := orgmodel.RevisionTemplate(rev)
	if err != nil {
		return a, err
	}
	var spec orgmodel.AgentSpec
	found := false
	for _, x := range t.Agents {
		if x.Key == key {
			spec, found = x, true
		}
	}
	if !found {
		return a, ErrNoBefore
	}
	a.Name, a.Tier, a.Role, a.Description, a.ReportsTo = spec.Name, spec.Tier, spec.Role, spec.Description, spec.ReportsTo
	a.ModelTier, a.LLMModel, a.Instructions, a.Permissions = spec.ModelTier, spec.LLMModel, spec.Instructions, spec.Permissions
	if storage.ValidEffort(spec.Effort) {
		a.Effort = spec.Effort
	}
	// ADR-074 security: restoring a snapshot never turns full access back on
	// — only internal/api/org.go's applyAgent (admin only) may.
	a.Permissions.FullAccess, a.Permissions.FullAccessBy, a.Permissions.ExtraDirs = false, "", nil
	if spec.ProviderID == "" {
		a.ProviderID = ""
	} else if _, err := s.store.Providers().Get(ctx, spec.ProviderID); err == nil {
		a.ProviderID = spec.ProviderID
	}
	a.FallbackProviderIDs = nil // a deleted connection in the list is skipped when it runs
	for _, id := range spec.Fallbacks {
		if _, err := s.store.Providers().Get(ctx, id); err == nil {
			a.FallbackProviderIDs = append(a.FallbackProviderIDs, id)
		}
	}
	return s.org.SaveAgent(ctx, a)
}

// chatSource is where a chat started: a bot (discord, telegram), an automation, or the web.
func chatSource(c storage.Conversation) string {
	kind, _, _ := strings.Cut(c.CreatedBy, ":")
	switch {
	case c.Purpose == "channel" && (kind == "discord" || kind == "telegram"):
		return kind
	case kind == "auto":
		return "auto"
	}
	return "web"
}

// answers are the agent's messages in a chat, oldest first.
func (s *Service) answers(ctx context.Context, a storage.Agent, conversationID string) ([]storage.Message, error) {
	msgs, err := s.store.Chat().ListMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	var out []storage.Message
	for _, m := range msgs {
		if m.Role == "assistant" && m.Author == a.Name {
			out = append(out, m)
		}
	}
	return out, nil
}
