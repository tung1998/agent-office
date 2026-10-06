package api

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/sysinfo"
)

// The machine's figures, pushed (ADR-078): while an admin has a page open the
// server looks every 3 seconds and sends the header's summary; the whole
// picture (processes) only to the pages showing the Máy tab. An answer that
// starts or ends is sent at once. Nobody looking: nothing is measured.
const statsEvery = 3 * time.Second

func admins(v events.Viewer, _ map[string]bool) bool { return v.Admin }
func watchingMachine(v events.Viewer, t map[string]bool) bool {
	return v.Admin && t["machine"]
}

type liveStats struct {
	mu      sync.Mutex
	running bool
	kick    chan struct{}
}

// startStats wires the pushes: a new admin page gets the figures now, the
// Máy tab its picture now, an answer starting/ending a fresh summary.
func (s *server) startStats() {
	if s.cfg.Events == nil {
		return
	}
	s.live.kick = make(chan struct{}, 1)
	s.cfg.Events.OnSubscribe(func(sub *events.Sub) {
		go s.pushIncidentsTo(context.Background(), sub.Viewer(), true) // what needs them, without asking
		if !sub.Viewer().Admin {
			return
		}
		cpu, used, total := s.sys.Quick(context.Background())
		s.cfg.Events.SendTo(sub, events.Event{Name: "stats", Data: s.summaryOf(cpu, used, total, sysinfo.Count(context.Background(), int32(os.Getpid())))})
		s.ensureStats()
	}, func(sub *events.Sub, topic string) {
		if topic == "machine" && sub.Viewer().Admin {
			if full, err := s.fullStats(context.Background()); err == nil {
				s.cfg.Events.SendTo(sub, events.Event{Name: "machine", Data: full})
			}
		}
	})
	if s.cfg.Chat != nil {
		s.cfg.Chat.SetOnRunning(func(conversationID string) {
			s.kickStats()
			s.pushConversation(conversationID) // its row: answering now, or not
		})
	}
}

// kickStats sends a fresh look now (an answer started or ended, a process stopped).
func (s *server) kickStats() {
	select {
	case s.live.kick <- struct{}{}:
	default:
	}
}

// ensureStats runs the loop while an admin looks.
func (s *server) ensureStats() {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.live.running {
		return
	}
	s.live.running = true
	go s.statsLoop()
}

func (s *server) statsLoop() {
	tick := time.NewTicker(statsEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
		case <-s.live.kick:
			time.Sleep(150 * time.Millisecond) // the turn's process has started (or gone)
		}
		s.live.mu.Lock()
		if s.cfg.Events.Count(admins) == 0 {
			s.live.running = false
			s.live.mu.Unlock()
			return
		}
		s.live.mu.Unlock()
		s.pushStats(context.Background())
	}
}

// pushStats sends one look: the picture to the Máy tabs (its summary to the
// rest), else the summary alone. CPU is read once: two reads in a row would
// measure nothing between them.
func (s *server) pushStats(ctx context.Context) {
	if s.cfg.Events.Count(watchingMachine) > 0 {
		full, err := s.fullStats(ctx)
		if err == nil {
			s.cfg.Events.Send(events.Event{Name: "machine", Data: full}, watchingMachine)
			s.cfg.Events.Send(events.Event{Name: "stats", Data: full["summary"]}, func(v events.Viewer, t map[string]bool) bool { return v.Admin && !t["machine"] })
			return
		}
	}
	cpu, used, total := s.sys.Quick(ctx)
	s.cfg.Events.Send(events.Event{Name: "stats", Data: s.summaryOf(cpu, used, total, sysinfo.Count(ctx, int32(os.Getpid())))}, admins)
}

// fullStats is what GET /api/system/stats answers.
func (s *server) fullStats(ctx context.Context) (map[string]any, error) {
	machine := s.sys.Machine(ctx)
	self, groups, err := s.officeGroups(ctx)
	if err != nil {
		return nil, err
	}
	procs := 0
	for _, g := range groups {
		procs += len(g.Procs)
	}
	return map[string]any{"machine": machine, "office": officeProc{self, runtime.NumGoroutine()}, "groups": groups,
		"summary": s.summaryOf(machine.CPUPercent, machine.MemUsed, machine.MemTotal, procs)}, nil
}
