// Package monitor runs a project's health checks on a schedule: HTTP, TCP,
// heartbeat (pushed by the service), office processes and compose
// containers. Checks are rule-based and free; a monitor with AI enabled asks
// the project's lead agent to analyse when it goes down, within a daily cap.
package monitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/storage"
)

const (
	downAfter    = 2 // consecutive failures before a monitor is down (flap guard)
	keepChecks   = 7 * 24 * time.Hour
	aiCooldown   = 30 * time.Minute
	maxParallel  = 8
	defaultTOut  = 10 * time.Second
	maxBodyBytes = 1 << 20
)

// Result is one check outcome.
type Result struct {
	OK        bool
	LatencyMS int
	Message   string
	Skip      bool // not enough data yet (heartbeat never pinged)
}

// Service schedules and runs monitors.
type Service struct {
	store  storage.Store
	ops    *ops.Manager
	engine *chat.Engine
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	inFlight map[string]bool
}

// New builds a Service. engine may be nil (no AI analysis).
func New(store storage.Store, o *ops.Manager, engine *chat.Engine) *Service {
	return &Service{
		store: store, ops: o, engine: engine, now: time.Now, inFlight: map[string]bool{},
		client: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("quá nhiều redirect")
			}
			return nil
		}},
	}
}

// NewToken makes a heartbeat token.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Run checks due monitors until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	sem := make(chan struct{}, maxParallel)
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			_ = s.store.Monitors().PruneChecks(ctx, s.now().Add(-keepChecks))
		case <-tick.C:
			list, err := s.store.Monitors().List(ctx, "")
			if err != nil {
				continue
			}
			for _, m := range list {
				if !m.Enabled || !s.due(m) || !s.claim(m.ID) {
					continue
				}
				sem <- struct{}{}
				go func(m storage.Monitor) {
					defer func() { <-sem; s.release(m.ID) }()
					s.CheckNow(ctx, m.ID)
				}(m)
			}
		}
	}
}

func (s *Service) due(m storage.Monitor) bool {
	iv := time.Duration(max(m.IntervalS, 10)) * time.Second
	return m.LastCheckedAt == nil || s.now().Sub(*m.LastCheckedAt) >= iv
}

func (s *Service) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[id] {
		return false
	}
	s.inFlight[id] = true
	return true
}

func (s *Service) release(id string) {
	s.mu.Lock()
	delete(s.inFlight, id)
	s.mu.Unlock()
}

// CheckNow runs one check, records it and handles status changes.
func (s *Service) CheckNow(ctx context.Context, id string) (storage.Monitor, error) {
	m, err := s.store.Monitors().Get(ctx, id)
	if err != nil {
		return m, err
	}
	res := s.check(ctx, m)
	now := s.now().UTC()
	m.LastCheckedAt = &now
	if res.Skip {
		m.LastMessage = res.Message
		return m, s.store.Monitors().SaveStatus(ctx, m)
	}
	_ = s.store.Monitors().AddCheck(ctx, storage.MonitorCheck{MonitorID: m.ID, At: now, OK: res.OK, LatencyMS: res.LatencyMS, Message: res.Message})
	m.LastLatencyMS, m.LastMessage = res.LatencyMS, res.Message
	prev := m.Status
	threshold := downAfter
	if m.Type == "heartbeat" || m.Type == "process" {
		threshold = 1 // already a sustained signal
	}
	if res.OK {
		m.Fails = 0
		m.Status = "up"
	} else {
		m.Fails++
		if m.Fails >= threshold {
			m.Status = "down"
		}
	}
	if m.Status != prev && m.Status != "pending" {
		m.LastChangeAt = &now
		// the first result of a new monitor is not an incident, unless it is down
		if prev != "pending" || m.Status == "down" {
			ev, err := s.store.Monitors().AddEvent(ctx, storage.MonitorEvent{MonitorID: m.ID, ProjectID: m.ProjectID, Kind: m.Status, Message: res.Message, At: now})
			if err == nil && m.Status == "down" {
				s.maybeAnalyse(m, ev)
			}
		}
	}
	return m, s.store.Monitors().SaveStatus(ctx, m)
}

func (s *Service) check(ctx context.Context, m storage.Monitor) Result {
	timeout := defaultTOut
	if m.Config.TimeoutMS > 0 {
		timeout = time.Duration(m.Config.TimeoutMS) * time.Millisecond
	}
	switch m.Type {
	case "http":
		return s.checkHTTP(ctx, m, timeout)
	case "tcp":
		start := s.now()
		c, err := net.DialTimeout("tcp", m.Target, timeout)
		ms := int(s.now().Sub(start).Milliseconds())
		if err != nil {
			return Result{LatencyMS: ms, Message: shortNetErr(err)}
		}
		c.Close()
		return Result{OK: true, LatencyMS: ms, Message: "mở cổng được"}
	case "heartbeat":
		iv := time.Duration(max(m.IntervalS, 10)) * time.Second
		if m.LastPingAt == nil {
			if s.now().Sub(m.CreatedAt) < iv+iv/2 {
				return Result{Skip: true, Message: "đang chờ heartbeat đầu tiên"}
			}
			return Result{Message: "chưa nhận heartbeat nào"}
		}
		ago := s.now().Sub(*m.LastPingAt)
		msg := fmt.Sprintf("heartbeat %s trước", ago.Round(time.Second))
		return Result{OK: ago <= iv+iv/2, Message: msg}
	case "process":
		if s.ops == nil {
			return Result{Message: "không quản lý tiến trình"}
		}
		st := s.ops.State(m.Target)
		if st.Status == "running" {
			return Result{OK: true, Message: fmt.Sprintf("đang chạy (PID %d)", st.PID)}
		}
		switch st.Status {
		case "stopped", "stopping":
			return Result{Message: "tiến trình đã bị dừng"}
		case "restarting":
			return Result{Message: fmt.Sprintf("tiến trình lỗi, đang chạy lại (lần %d)", st.Restarts)}
		}
		msg := "tiến trình " + st.Status
		if st.ExitCode != nil {
			msg += fmt.Sprintf(", mã thoát %d", *st.ExitCode)
		}
		return Result{Message: msg}
	case "container":
		if s.ops == nil {
			return Result{Message: "không quản lý container"}
		}
		start := s.now()
		c, err := s.ops.ServiceContainer(ctx, m.ProjectID, m.Config.File, m.Target)
		ms := int(s.now().Sub(start).Milliseconds())
		switch {
		case err != nil:
			return Result{LatencyMS: ms, Message: err.Error()}
		case c == nil:
			return Result{LatencyMS: ms, Message: "container chưa được tạo"}
		case c.State != "running":
			return Result{LatencyMS: ms, Message: c.Status}
		case c.Health == "unhealthy":
			return Result{LatencyMS: ms, Message: "unhealthy: " + c.Status}
		}
		return Result{OK: true, LatencyMS: ms, Message: c.Status}
	}
	return Result{Message: "loại giám sát không hỗ trợ"}
}

func (s *Service) checkHTTP(ctx context.Context, m storage.Monitor, timeout time.Duration) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Target, nil)
	if err != nil {
		return Result{Message: "URL không hợp lệ"}
	}
	req.Header.Set("User-Agent", "agent-office-monitor/1")
	start := s.now()
	resp, err := s.client.Do(req)
	if err != nil {
		return Result{LatencyMS: int(s.now().Sub(start).Milliseconds()), Message: shortNetErr(err)}
	}
	defer resp.Body.Close()
	var body []byte
	if m.Config.Keyword != "" {
		body, _ = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	}
	ms := int(s.now().Sub(start).Milliseconds())
	if !statusOK(resp.StatusCode, m.Config.ExpectStatus) {
		return Result{LatencyMS: ms, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	if m.Config.Keyword != "" && !strings.Contains(string(body), m.Config.Keyword) {
		return Result{LatencyMS: ms, Message: fmt.Sprintf("HTTP %d, không thấy %q", resp.StatusCode, m.Config.Keyword)}
	}
	return Result{OK: true, LatencyMS: ms, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

// statusOK matches "200-399" (default) or a list "200,204".
func statusOK(code int, expect string) bool {
	if expect == "" {
		expect = "200-399"
	}
	for _, part := range strings.Split(expect, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			l, e1 := strconv.Atoi(strings.TrimSpace(lo))
			h, e2 := strconv.Atoi(strings.TrimSpace(hi))
			if e1 == nil && e2 == nil && code >= l && code <= h {
				return true
			}
		} else if n, err := strconv.Atoi(part); err == nil && n == code {
			return true
		}
	}
	return false
}

func shortNetErr(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "quá thời gian chờ"
	case strings.Contains(err.Error(), "connection refused"):
		return "bị từ chối kết nối (dịch vụ không chạy?)"
	case strings.Contains(err.Error(), "no such host"):
		return "không phân giải được tên miền"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// Ping records a heartbeat and checks right away.
func (s *Service) Ping(ctx context.Context, token string) error {
	m, err := s.store.Monitors().GetByToken(ctx, token)
	if err != nil || m.Type != "heartbeat" {
		return storage.ErrNotFound
	}
	now := s.now().UTC()
	m.LastPingAt = &now
	if err := s.store.Monitors().SaveStatus(ctx, m); err != nil {
		return err
	}
	if m.Enabled && s.claim(m.ID) {
		defer s.release(m.ID)
		_, err = s.CheckNow(ctx, m.ID)
	}
	return err
}
