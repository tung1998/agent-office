// Package usage records every model call (tokens, cost, duration), enforces
// daily budgets, and summarises spend for the dashboard.
package usage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/storage"
)

const settingsKey = "usage"

// Settings are the budgets and price overrides.
type Settings struct {
	DailyLimitUSD float64            `json:"daily_limit_usd"`          // 0 = no limit
	ProjectLimits map[string]float64 `json:"project_limits,omitempty"` // project id → daily USD
	Prices        map[string]Price   `json:"prices,omitempty"`         // overrides and extra models
	WarnRatio     float64            `json:"warn_ratio,omitempty"`     // 0: 0.8
	// A turn that died on a passing API failure (529, 5xx, a dropped
	// stream) goes on by itself (ADR-260): NoTransientRetry turns it off,
	// TransientRetries is how many times in a row (0: 3, at most 5).
	NoTransientRetry bool `json:"no_transient_retry,omitempty"`
	TransientRetries int  `json:"transient_retries,omitempty"`
	// An agent stuck on the same failure gets a second opinion from an agent
	// of another vendor (ADR-262): AutoAdvisor is off | repeat | repeat_done
	// ("" = repeat), AdvisorDailyUSD what its advices may cost a day (0: 2).
	AutoAdvisor     string  `json:"auto_advisor,omitempty"`
	AdvisorDailyUSD float64 `json:"advisor_daily_usd,omitempty"`
}

// DefaultAdvisorDailyUSD is what the advices may cost a day when the
// settings do not say (ADR-262).
const DefaultAdvisorDailyUSD = 2.0

// Advisor is when an advisor is asked: off | repeat | repeat_done.
func (s Settings) Advisor() string {
	switch s.AutoAdvisor {
	case "off", "repeat_done":
		return s.AutoAdvisor
	}
	return "repeat"
}

// AdvisorBudget is what the advices may cost a day.
func (s Settings) AdvisorBudget() float64 {
	if s.AdvisorDailyUSD <= 0 {
		return DefaultAdvisorDailyUSD
	}
	return s.AdvisorDailyUSD
}

// DefaultTransientRetries is how many times in a row a turn cut by a passing
// API failure goes on by itself when the settings do not say (ADR-260).
const DefaultTransientRetries = 3

// Retries is how many times in a row a turn cut by a passing API failure
// goes on by itself: 0 when turned off.
func (s Settings) Retries() int {
	switch {
	case s.NoTransientRetry:
		return 0
	case s.TransientRetries <= 0:
		return DefaultTransientRetries
	}
	return min(s.TransientRetries, 5)
}

// BudgetError is returned when a call would go over a daily limit.
type BudgetError struct {
	Scope string // "office" or a project name
	Limit float64
	Spent float64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("đã dùng hết ngân sách ngày của %s ($%.2f / $%.2f)", e.Scope, e.Spent, e.Limit)
}

// Service is the usage use-case layer.
type Service struct {
	store storage.Store
	loc   *time.Location
	now   func() time.Time

	mu sync.Mutex // serializes Settings read-modify-write (SetProjectLimit, SaveSettings)
	// onRecord hears each call recorded (the failed-prompt alerts), off the caller's path
	onRecord func(ctx context.Context, r storage.Run)
}

// SetOnRecord is told each call once it is recorded, in its own goroutine.
func (s *Service) SetOnRecord(fn func(ctx context.Context, r storage.Run)) { s.onRecord = fn }

// New builds a Service; days are counted in loc (nil: local time).
func New(store storage.Store, loc *time.Location) *Service {
	if loc == nil {
		loc = time.Local
	}
	return &Service{store: store, loc: loc, now: time.Now}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Settings returns the current settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	return s.loadSettings(ctx)
}

func (s *Service) loadSettings(ctx context.Context) (Settings, error) {
	var st Settings
	_, err := s.store.Settings().Get(ctx, settingsKey, &st)
	if st.WarnRatio == 0 {
		st.WarnRatio = 0.8
	}
	return st, err
}

func (s *Service) saveSettings(ctx context.Context, st Settings) error {
	if st.DailyLimitUSD < 0 {
		return fmt.Errorf("ngân sách không được âm")
	}
	if st.TransientRetries < 0 || st.TransientRetries > 5 {
		return fmt.Errorf("số lần tự chạy tiếp phải từ 1 đến 5")
	}
	switch st.AutoAdvisor {
	case "", "off", "repeat", "repeat_done":
	default:
		return fmt.Errorf("cố vấn tự động: %q không hợp lệ (off, repeat, repeat_done)", st.AutoAdvisor)
	}
	if st.AdvisorDailyUSD < 0 {
		return fmt.Errorf("ngân sách cố vấn không được âm")
	}
	for k, v := range st.ProjectLimits {
		if v <= 0 {
			delete(st.ProjectLimits, k)
		}
	}
	return s.store.Settings().Set(ctx, settingsKey, st)
}

// SaveSettings replaces the settings.
func (s *Service) SaveSettings(ctx context.Context, st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveSettings(ctx, st)
}

// SetProjectLimit sets one project's daily budget (0 clears it): the
// read-modify-write is serialized by mu, so a concurrent change to another
// field is not lost (the caller used to do Settings()+SaveSettings() itself,
// unprotected).
func (s *Service) SetProjectLimit(ctx context.Context, projectID string, dailyLimitUSD float64) (old float64, st Settings, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err = s.loadSettings(ctx)
	if err != nil {
		return 0, st, err
	}
	old = st.ProjectLimits[projectID]
	if st.ProjectLimits == nil {
		st.ProjectLimits = map[string]float64{}
	}
	if dailyLimitUSD > 0 {
		st.ProjectLimits[projectID] = dailyLimitUSD
	} else {
		delete(st.ProjectLimits, projectID)
	}
	if err = s.saveSettings(ctx, st); err != nil {
		return old, st, err
	}
	return old, st, nil
}

// StartOfDay is midnight today in the office timezone.
func (s *Service) StartOfDay() time.Time {
	n := s.now().In(s.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.loc)
}

// Check fails with *BudgetError when today's spend has reached a limit.
func (s *Service) Check(ctx context.Context, projectID string) error {
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	since := s.StartOfDay()
	if st.DailyLimitUSD > 0 {
		spent, err := s.store.Runs().Spent(ctx, since, "")
		if err != nil {
			return err
		}
		if spent >= st.DailyLimitUSD {
			return &BudgetError{Scope: "office", Limit: st.DailyLimitUSD, Spent: spent}
		}
	}
	if limit := st.ProjectLimits[projectID]; projectID != "" && limit > 0 {
		spent, err := s.store.Runs().Spent(ctx, since, projectID)
		if err != nil {
			return err
		}
		if spent >= limit {
			name := projectID
			if r, err := s.store.Repos().Get(ctx, projectID); err == nil {
				name = "project " + r.Name
			}
			return &BudgetError{Scope: name, Limit: limit, Spent: spent}
		}
	}
	return nil
}

// Meta describes a call for the record.
type Meta struct {
	Kind      string // provider_test | setup_propose | chat …
	ProjectID string
	AgentID   string
	Effort    string // the effort the call was sent with ("" = the CLI's own)
}

// Record stores one call. The cost comes from the provider when it reports
// one (Claude Code), else is estimated from the price table.
func (s *Service) Record(ctx context.Context, m Meta, p storage.Provider, model string, res llm.Result, callErr error) (storage.Run, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return storage.Run{}, err
	}
	r := storage.Run{
		Kind: m.Kind, JobID: JobFrom(ctx), ProjectID: m.ProjectID, AgentID: m.AgentID, ProviderID: p.ID, ProviderName: p.Name,
		Model: firstNonEmpty(res.Model, model), Effort: m.Effort, Status: "ok", InputTokens: res.InputTokens, OutputTokens: res.OutputTokens,
		DurationMS: res.DurationMS, Actor: actor.From(ctx), CostSource: "unknown", CreatedAt: s.now().UTC(),
	}
	var be *BudgetError
	switch {
	case callErr != nil && asBudget(callErr, &be):
		r.Status, r.Error = "blocked", callErr.Error()
	case callErr != nil:
		r.Status, r.Error = "error", truncate(callErr.Error(), 500)
	}
	switch {
	case res.CostUSD > 0:
		c := res.CostUSD
		r.CostUSD, r.CostSource = &c, "provider"
	case res.InputTokens+res.OutputTokens > 0:
		if c, ok := Estimate(r.Model, res.InputTokens, res.OutputTokens, st.Prices); ok {
			r.CostUSD, r.CostSource = &c, "estimate"
		}
	}
	run, err := s.store.Runs().Create(ctx, r)
	if err == nil && s.onRecord != nil {
		go s.onRecord(context.WithoutCancel(ctx), run)
	}
	return run, err
}

// Runs lists recent calls.
// before (a run id) gives the page after it.
func (s *Service) Runs(ctx context.Context, projectID string, days, limit int, before string) ([]storage.Run, error) {
	return s.store.Runs().List(ctx, storage.RunFilter{ProjectID: projectID, Since: s.StartOfDay().AddDate(0, 0, -days+1), Limit: limit, Before: before})
}

// Summary is what the Chi phí page shows.
type Summary struct {
	Today        float64            `json:"today"`
	DailyLimit   float64            `json:"daily_limit"`
	WarnRatio    float64            `json:"warn_ratio"`
	Period       float64            `json:"period"`
	Days         int                `json:"days"`
	ByDay        []storage.UsageRow `json:"by_day"`
	ByProject    []storage.UsageRow `json:"by_project"`
	ByModel      []storage.UsageRow `json:"by_model"`
	ByAgent      []storage.UsageRow `json:"by_agent"`
	ProjectToday map[string]float64 `json:"project_today"`
	Settings     Settings           `json:"settings"`
	Prices       map[string]Price   `json:"default_prices"`
}

// Summarize aggregates the last days (including today).
func (s *Service) Summarize(ctx context.Context, days int) (Summary, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return Summary{}, err
	}
	today := s.StartOfDay()
	since := today.AddDate(0, 0, -days+1)
	out := Summary{DailyLimit: st.DailyLimitUSD, WarnRatio: st.WarnRatio, Days: days, Settings: st, Prices: DefaultPrices(), ProjectToday: map[string]float64{}}
	if out.Today, err = s.store.Runs().Spent(ctx, today, ""); err != nil {
		return out, err
	}
	byDay, err := s.store.Runs().Aggregate(ctx, since, "day", s.loc)
	if err != nil {
		return out, err
	}
	// Fill every day so the chart has no gaps.
	have := map[string]storage.UsageRow{}
	for _, d := range byDay {
		have[d.Key] = d
		out.Period += d.CostUSD
	}
	for i := 0; i < days; i++ {
		day := since.AddDate(0, 0, i).Format("2006-01-02")
		row, ok := have[day]
		if !ok {
			row = storage.UsageRow{Key: day, Label: day}
		}
		out.ByDay = append(out.ByDay, row)
	}
	if out.ByProject, err = s.store.Runs().Aggregate(ctx, since, "project", s.loc); err != nil {
		return out, err
	}
	if out.ByModel, err = s.store.Runs().Aggregate(ctx, since, "model", s.loc); err != nil {
		return out, err
	}
	if out.ByAgent, err = s.store.Runs().Aggregate(ctx, since, "agent", s.loc); err != nil {
		return out, err
	}
	for id := range st.ProjectLimits {
		if v, err := s.store.Runs().Spent(ctx, today, id); err == nil {
			out.ProjectToday[id] = v
		}
	}
	sort.SliceStable(out.ByProject, func(i, j int) bool { return out.ByProject[i].CostUSD > out.ByProject[j].CostUSD })
	return out, nil
}

// Trend is the cost/latency chart data per day, broken down by "model" or
// "agent": spotting a prompt or model change driving cost or latency up
// before it trips a budget cap.
func (s *Service) Trend(ctx context.Context, days int, by string) ([]storage.UsageTrendRow, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	since := s.StartOfDay().AddDate(0, 0, -days+1)
	return s.store.Runs().Trend(ctx, since, by, s.loc)
}

func asBudget(err error, target **BudgetError) bool {
	return errors.As(err, target)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Back up to a rune boundary: a cut mid-rune leaves invalid UTF-8
	// (Vietnamese text), which some CLIs refuse to read.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
