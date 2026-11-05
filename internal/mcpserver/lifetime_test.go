package mcpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/officetools"
)

// fakeClock is a clock a test moves.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func bearer(tok string) *http.Request {
	r := httptest.NewRequest("POST", "/mcp", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return r
}

// ADR-254: a run's token lives until the run revokes it (at most MaxRun);
// a refused one says why: revoked, expired or unknown.
func TestRunTokenLifetime(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	s := New(nil, "test")
	s.Now = clock.now

	tok, revoke := s.Grant(officetools.Scope{ProjectID: "p"}, 0) // 0: MaxRun
	clock.add(8 * time.Hour)                                     // a daily-upgrade run
	if !s.Authorized(bearer(tok)) || !s.Live(tok) || s.Why(bearer(tok)) != "" {
		t.Fatal("token of a live run refused after 8h")
	}
	revoke()
	if s.Authorized(bearer(tok)) || s.Live(tok) {
		t.Fatal("revoked token still accepted")
	}
	if why := s.Why(bearer(tok)); !strings.Contains(why, "thu hồi") {
		t.Fatalf("revoked: %q", why)
	}

	short, _ := s.Grant(officetools.Scope{ProjectID: "p"}, time.Minute)
	clock.add(2 * time.Minute)
	if s.Authorized(bearer(short)) {
		t.Fatal("token past its ttl accepted")
	}
	if why := s.Why(bearer(short)); !strings.Contains(why, "hết hạn") || !strings.Contains(why, "1m0s") {
		t.Fatalf("expired: %q", why)
	}
	capped, _ := s.Grant(officetools.Scope{}, 48*time.Hour) // capped at MaxRun
	clock.add(MaxRun + time.Minute)
	if s.Authorized(bearer(capped)) {
		t.Fatal("token outlived MaxRun")
	}
	if why := s.Why(bearer("nope")); !strings.Contains(why, "không hợp lệ") {
		t.Fatalf("unknown: %q", why)
	}
	if why := s.Why(bearer("")); !strings.Contains(why, "thiếu token") {
		t.Fatalf("missing: %q", why)
	}

	// the endpoint's 401 carries the reason
	w := httptest.NewRecorder()
	req := bearer(tok)
	req.Body = io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	s.ServeHTTP(w, req)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "thu hồi") {
		t.Fatalf("401 = %d %q", w.Code, w.Body.String())
	}
}

// Sweep drops expired tokens nobody asks about again, and forgets ended
// ones after keepEnded.
func TestRunTokenSweep(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	s := New(nil, "test")
	s.Now = clock.now
	s.Grant(officetools.Scope{}, time.Minute)
	clock.add(2 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Sweep(ctx, 5*time.Millisecond); close(done) }()
	count := func() (int, int) { s.mu.Lock(); defer s.mu.Unlock(); return len(s.grants), len(s.ended) }
	deadline := time.Now().Add(2 * time.Second)
	for g, _ := count(); g != 0 && time.Now().Before(deadline); g, _ = count() {
		time.Sleep(5 * time.Millisecond)
	}
	if g, e := count(); g != 0 || e != 1 {
		t.Fatalf("after sweep: grants %d ended %d", g, e)
	}
	clock.add(keepEnded + time.Minute)
	for _, e := count(); e != 0 && time.Now().Before(deadline); _, e = count() {
		time.Sleep(5 * time.Millisecond)
	}
	if _, e := count(); e != 0 {
		t.Fatal("ended token never forgotten")
	}
	cancel()
	<-done
}
