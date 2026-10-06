package api

import (
	"context"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/sysinfo"
)

// procGroup is one thing office runs: an agent's answer, an automation's
// script, a project process, or anything else it started, with every process
// under it.
type procGroup struct {
	Kind           string         `json:"kind"` // agent | automation | project | other
	Label          string         `json:"label"`
	Sub            string         `json:"sub,omitempty"` // the chat's title, the project's name
	PID            int32          `json:"pid"`
	TurnID         string         `json:"turn_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	ProjectID      string         `json:"project_id,omitempty"`
	ProcessID      string         `json:"process_id,omitempty"`
	AutomationID   string         `json:"automation_id,omitempty"`
	CPU            float64        `json:"cpu"`
	Mem            uint64         `json:"mem"`
	StartedAt      time.Time      `json:"started_at"`
	Procs          []sysinfo.Proc `json:"procs"`
}

type officeProc struct {
	sysinfo.Proc
	Goroutines int `json:"goroutines"`
}

// systemStats: the machine, office itself and what it runs (admin).
func (s *server) systemStats(w http.ResponseWriter, r *http.Request) {
	full, err := s.fullStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, full)
}

// systemSummary: the header's figures (admin): CPU, memory, agents answering
// and processes office runs.
func (s *server) systemSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cpuPct, used, total := s.sys.Quick(ctx)
	writeJSON(w, http.StatusOK, s.summaryOf(cpuPct, used, total, sysinfo.Count(ctx, int32(os.Getpid()))))
}

func (s *server) summaryOf(cpuPct float64, used, total uint64, procs int) map[string]any {
	agents := 0
	if s.cfg.Chat != nil {
		agents = s.cfg.Chat.RunningCount()
	}
	// the project processes started from Vận hành (dev, build, terminal…)
	tasks := []string{}
	if s.cfg.Ops != nil {
		for _, p := range s.cfg.Ops.Running() {
			tasks = append(tasks, p.Name)
		}
	}
	sort.Strings(tasks)
	return map[string]any{"cpu_percent": cpuPct, "mem_used": used, "mem_total": total, "procs": procs, "agents": agents, "tasks": tasks}
}

func (s *server) officeGroups(ctx context.Context) (sysinfo.Proc, []procGroup, error) {
	self, list, err := s.sys.Tree(ctx, int32(os.Getpid()))
	if err != nil {
		return self, nil, err
	}
	running := map[int32]ops.Running{}
	if s.cfg.Ops != nil {
		for _, p := range s.cfg.Ops.Running() {
			running[int32(p.PID)] = p
		}
	}
	projects := map[string]string{}
	if rs, err := s.cfg.Store.Repos().List(ctx); err == nil {
		for _, p := range rs {
			projects[p.ID] = p.Name
		}
	}
	groups := []procGroup{}
	for _, p := range list {
		if p.Depth > 0 {
			g := &groups[len(groups)-1]
			g.Procs = append(g.Procs, p)
			g.CPU += p.CPU
			g.Mem += p.Mem
			continue
		}
		g := procGroup{Kind: "other", Label: p.Name, PID: p.PID, CPU: p.CPU, Mem: p.Mem, StartedAt: p.StartedAt, Procs: []sysinfo.Proc{p}}
		if info, ok := proctrack.Lookup(int(p.PID)); ok {
			g.Kind, g.Label, g.TurnID, g.ConversationID, g.ProjectID, g.AutomationID = info.Kind, info.Label, info.TurnID, info.ConversationID, info.ProjectID, info.AutomationID
			if c, err := s.cfg.Store.Chat().GetConversation(ctx, info.ConversationID); err == nil && info.ConversationID != "" {
				g.Sub = c.Title
			}
		} else if op, ok := running[p.PID]; ok {
			g.Kind, g.Label, g.ProcessID, g.ProjectID = "project", op.Name, op.ID, op.ProjectID
		}
		if g.Sub == "" {
			g.Sub = projects[g.ProjectID]
		}
		groups = append(groups, g)
	}
	order := map[string]int{"agent": 0, "automation": 1, "project": 2, "other": 3}
	slices.SortStableFunc(groups, func(a, b procGroup) int { return order[a.Kind] - order[b.Kind] })
	return self, groups, nil
}

// stopProcess ends one of office's processes: kill at once and with all it
// started, or stop (an agent's answer is cancelled, a project process
// stopped by its manager, anything else asked to end).
func (s *server) stopProcess(force bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pid64, err := strconv.ParseInt(r.PathValue("pid"), 10, 32)
		pid := int32(pid64)
		if err != nil || pid == int32(os.Getpid()) {
			writeError(w, http.StatusBadRequest, "Không dừng được tiến trình này.")
			return
		}
		_, groups, err := s.officeGroups(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var group *procGroup
		var target *sysinfo.Proc
		for i := range groups {
			for j := range groups[i].Procs {
				if groups[i].Procs[j].PID == pid {
					group, target = &groups[i], &groups[i].Procs[j]
				}
			}
		}
		if target == nil { // only what office started: never the machine's other processes
			writeError(w, http.StatusNotFound, "Tiến trình không còn chạy hoặc không do office mở.")
			return
		}
		root := group.PID == pid
		switch {
		case force:
			err = sysinfo.KillTree(ctx, pid)
		case root && group.TurnID != "":
			if t, ok := s.cfg.Chat.Turn(group.TurnID); ok {
				t.Cancel()
			} else {
				err = sysinfo.Stop(ctx, pid)
			}
		case root && group.ProcessID != "" && s.cfg.Ops != nil:
			err = s.cfg.Ops.Stop(group.ProcessID)
		default:
			err = sysinfo.Stop(ctx, pid)
		}
		action := "process.stop"
		if force {
			action = "process.kill"
		}
		s.audit(r, audit.Change{Action: action, Resource: "process", ResourceID: strconv.Itoa(int(pid)), ProjectID: group.ProjectID,
			Detail: map[string]any{"name": target.Name, "cmd": target.Cmd, "for": group.Label, "kind": group.Kind}, Err: err})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.kickStats() // the pages see it gone
		w.WriteHeader(http.StatusNoContent)
	}
}
