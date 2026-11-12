package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// fakeNotifier records every text sent to a chat, for incident alert tests.
// It also satisfies ChannelReloader (cfg.Channels' declared type), the way
// the real bots manager satisfies both.
type fakeNotifier struct{ sent []string }

func (f *fakeNotifier) Notify(_ context.Context, channelID, chatID, text string) error {
	f.sent = append(f.sent, channelID+"|"+chatID+"|"+text)
	return nil
}
func (f *fakeNotifier) Reload(string) {}

func newIncidentTestServer(t *testing.T, n ChannelReloader) (*server, storage.Store, storage.Repo) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := st.Repos().Create(context.Background(), storage.Repo{Name: "shop", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return &server{cfg: Config{Store: st, Channels: n}}, st, p
}

// GET/PUT /api/incident-alert: saved settings round-trip, an unknown bot is
// rejected, min_severity always normalizes to "error" or "warning".
func TestIncidentAlertAPI(t *testing.T) {
	t.Parallel()
	s, st, p := newIncidentTestServer(t, &fakeNotifier{})
	ch, err := st.Channels().Create(context.Background(), storage.Channel{ProjectID: p.ID, Kind: "discord", Name: "Dev", TokenEnc: "x"})
	if err != nil {
		t.Fatal(err)
	}
	get := func() map[string]any {
		w := httptest.NewRecorder()
		s.getIncidentAlert(w, httptest.NewRequest("GET", "/api/incident-alert", nil))
		var out map[string]any
		mustDecode(t, w, &out)
		return out
	}
	if b := get(); b["min_severity"] != "error" || b["channel_id"] != "" {
		t.Fatalf("defaults = %v", b)
	}
	put := func(body string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", "/api/incident-alert", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), ctxUser, storage.User{ID: "u1", Email: "admin@x.io", Role: storage.RoleAdmin}))
		s.setIncidentAlert(w, r)
		return w.Code
	}
	if code := put(`{"channel_id":"chn_nope","chat_id":"c9"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown bot = %d", code)
	}
	if code := put(`{"channel_id":"` + ch.ID + `","chat_id":"c9","min_severity":"warning","base_url":"https://office.example.com/"}`); code != http.StatusOK {
		t.Fatalf("save = %d", code)
	}
	if b := get(); b["channel_id"] != ch.ID || b["min_severity"] != "warning" || b["base_url"] != "https://office.example.com" {
		t.Fatalf("saved = %v (base_url must lose its trailing slash)", b)
	}
}

// A monitor down tells once; polling again without a change does not repeat
// it; it goes down a second time (a fresh LastChangeAt) and tells again. The
// text carries the project name, the detail and a tappable absolute link.
func TestPollIncidentAlertsDedupAndResend(t *testing.T) {
	t.Parallel()
	n := &fakeNotifier{}
	s, st, p := newIncidentTestServer(t, n)
	ctx := context.Background()
	if err := st.Settings().Set(ctx, incidentAlertKey, IncidentAlertSettings{ChannelID: "chn_1", ChatID: "c9", BaseURL: "https://office.example.com"}); err != nil {
		t.Fatal(err)
	}
	m, err := st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "api", Type: "process", Target: "api", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	down := func(at time.Time) {
		m.Status, m.LastChangeAt, m.LastMessage = "down", &at, "connection refused"
		if err := st.Monitors().SaveStatus(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	first := time.Now().Add(-time.Minute)
	down(first)
	s.PollIncidentAlerts(ctx)
	if len(n.sent) != 1 {
		t.Fatalf("first time = %v", n.sent)
	}
	if !strings.Contains(n.sent[0], "shop: api") || !strings.Contains(n.sent[0], "connection refused") || !strings.Contains(n.sent[0], "https://office.example.com/projects/"+p.ID) {
		t.Fatalf("text = %q", n.sent[0])
	}
	s.PollIncidentAlerts(ctx) // same occurrence: not told again
	if len(n.sent) != 1 {
		t.Fatalf("repeated poll resent: %v", n.sent)
	}
	down(first.Add(10 * time.Minute)) // down again later: a new occurrence
	s.PollIncidentAlerts(ctx)
	if len(n.sent) != 2 {
		t.Fatalf("a later occurrence must tell again: %v", n.sent)
	}
}

// min_severity:"warning" must actually widen what is sent: a disabled
// automation (Severity "warning") is system-wide, with no signed-in user, so
// PollIncidentAlerts must find it even though it never calls incidentsFor.
func TestPollIncidentAlertsWarningSeverityIncludesAutomation(t *testing.T) {
	t.Parallel()
	n := &fakeNotifier{}
	s, st, p := newIncidentTestServer(t, n)
	ctx := context.Background()
	if _, err := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Báo cáo", Source: "schedule", Action: "script", DisabledCode: "failures", DisabledReason: "script lỗi"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().Set(ctx, incidentAlertKey, IncidentAlertSettings{ChannelID: "chn_1", ChatID: "c9", MinSeverity: "error"}); err != nil {
		t.Fatal(err)
	}
	s.PollIncidentAlerts(ctx)
	if len(n.sent) != 0 {
		t.Fatalf("min_severity error must not send a warning: %v", n.sent)
	}
	if err := st.Settings().Set(ctx, incidentAlertKey, IncidentAlertSettings{ChannelID: "chn_1", ChatID: "c9", MinSeverity: "warning"}); err != nil {
		t.Fatal(err)
	}
	s.PollIncidentAlerts(ctx)
	if len(n.sent) != 1 || !strings.Contains(n.sent[0], "Báo cáo") {
		t.Fatalf("min_severity warning must send the disabled automation: %v", n.sent)
	}
}

func mustDecode(t *testing.T, w *httptest.ResponseRecorder, out *map[string]any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
}
