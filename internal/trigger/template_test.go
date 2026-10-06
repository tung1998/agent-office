package trigger_test

import (
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/trigger"
)

func TestRender(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	v := trigger.Vars{Payload: map[string]any{"issue": map[string]any{"key": "SP-1", "labels": []any{"bug"}}}, RawPayload: `{"issue":{}}`,
		Source: "webhook", Automation: "Jira", Now: time.Date(2026, 9, 28, 17, 30, 0, 0, time.UTC), Loc: loc}
	got := trigger.Render("{{automation}}: {{payload.issue.key}} {{payload.issue.labels.0}} {{payload.nope}} {{today}} {{weird}}", v)
	if got != "Jira: SP-1 bug  2026-09-29 {{weird}}" {
		t.Fatalf("render = %q", got)
	}
}
