package monitor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func setup(t *testing.T) (*Service, storage.Store, storage.Repo) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(context.Background(), storage.Repo{Name: "p", Path: t.TempDir()})
	return New(st, nil, nil), st, p
}

func TestStatusOK(t *testing.T) {
	for _, c := range []struct {
		code int
		exp  string
		ok   bool
	}{{200, "", true}, {302, "", true}, {404, "", false}, {204, "200,204", true}, {500, "200-299", false}} {
		if statusOK(c.code, c.exp) != c.ok {
			t.Errorf("%d %q", c.code, c.exp)
		}
	}
}

func TestHTTPFlapGuardAndEvents(t *testing.T) {
	s, st, p := setup(t)
	ctx := context.Background()
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte("hello storefront"))
	}))
	defer srv.Close()
	m, _ := st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "web", Type: "http", Target: srv.URL, IntervalS: 30, Enabled: true,
		Config: storage.MonitorConfig{Keyword: "storefront"}})

	m, _ = s.CheckNow(ctx, m.ID)
	if m.Status != "up" {
		t.Fatalf("first: %+v", m)
	}
	fail.Store(true)
	m, _ = s.CheckNow(ctx, m.ID)
	if m.Status != "up" || m.Fails != 1 {
		t.Fatalf("one failure must not flip: %+v", m)
	}
	m, _ = s.CheckNow(ctx, m.ID)
	if m.Status != "down" || m.LastMessage != "HTTP 500" {
		t.Fatalf("two failures: %+v", m)
	}
	fail.Store(false)
	m, _ = s.CheckNow(ctx, m.ID)
	evs, _ := st.Monitors().Events(ctx, p.ID, 10)
	if m.Status != "up" || len(evs) != 2 || evs[0].Kind != "up" || evs[1].Kind != "down" {
		t.Fatalf("events: %+v %+v", m, evs)
	}
	checks, _ := st.Monitors().Checks(ctx, m.ID, time.Now().Add(-time.Hour))
	if len(checks) != 4 {
		t.Fatalf("checks: %d", len(checks))
	}

	kw, _ := st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "kw", Type: "http", Target: srv.URL, IntervalS: 30, Enabled: true,
		Config: storage.MonitorConfig{Keyword: "nope"}})
	kw, _ = s.CheckNow(ctx, kw.ID)
	if kw.Fails != 1 {
		t.Fatalf("keyword missing must fail: %+v", kw)
	}
}

func TestTCPAndHeartbeat(t *testing.T) {
	s, st, p := setup(t)
	ctx := context.Background()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	m, _ := st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "redis", Type: "tcp", Target: addr, IntervalS: 30, Enabled: true})
	if m, _ = s.CheckNow(ctx, m.ID); m.Status != "up" {
		t.Fatalf("tcp up: %+v", m)
	}
	ln.Close()
	s.CheckNow(ctx, m.ID)
	if m, _ = s.CheckNow(ctx, m.ID); m.Status != "down" {
		t.Fatalf("tcp down: %+v", m)
	}

	hb, _ := st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "cron", Type: "heartbeat", Token: NewToken(), IntervalS: 60, Enabled: true})
	if hb, _ = s.CheckNow(ctx, hb.ID); hb.Status != "pending" {
		t.Fatalf("waiting for first ping: %+v", hb)
	}
	if err := s.Ping(ctx, hb.Token); err != nil {
		t.Fatal(err)
	}
	if hb, _ = st.Monitors().Get(ctx, hb.ID); hb.Status != "up" {
		t.Fatalf("after ping: %+v", hb)
	}
	s.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	if hb, _ = s.CheckNow(ctx, hb.ID); hb.Status != "down" {
		t.Fatalf("missed heartbeat: %+v", hb)
	}
	if err := s.Ping(ctx, "wrong"); err != storage.ErrNotFound {
		t.Fatal(err)
	}
}
