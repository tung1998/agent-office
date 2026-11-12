package chat

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-254: a turn has no time limit, so the office token a run gets must
// not die after 30 minutes (a daily-upgrade run lost its commit to that).
func TestOfficeTokenOutlivesHalfAnHour(t *testing.T) {
	t.Parallel()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	srv := mcpserver.New(officetools.New(st, nil, nil), "test")
	srv.Now = func() time.Time { return now }
	e := &Engine{}
	e.SetOffice(officetools.New(st, nil, nil), srv, "http://127.0.0.1:1/mcp")

	oa, revoke := e.officeAccess(ctx, officetools.Scope{ProjectID: "p"})
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+oa.Token)
	for _, d := range []time.Duration{31 * time.Minute, 3 * time.Hour, 10 * time.Hour} {
		now = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC).Add(d)
		if !srv.Authorized(req) {
			t.Fatalf("token refused %s into the run: %s", d, srv.Why(req))
		}
	}
	revoke()
	if srv.Authorized(req) || !strings.Contains(srv.Why(req), "thu hồi") {
		t.Fatalf("after the run: %q", srv.Why(req))
	}
}
