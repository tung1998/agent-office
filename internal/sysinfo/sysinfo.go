// Package sysinfo reads the machine office runs on (CPU, memory, disk, load,
// network) and the processes office started, for the dashboard's overview.
package sysinfo

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Machine is the machine now.
type Machine struct {
	CPUPercent float64   `json:"cpu_percent"`
	Cores      []float64 `json:"cores"`
	MemTotal   uint64    `json:"mem_total"`
	MemUsed    uint64    `json:"mem_used"`
	DiskPath   string    `json:"disk_path"`
	DiskTotal  uint64    `json:"disk_total"`
	DiskUsed   uint64    `json:"disk_used"`
	Load       []float64 `json:"load"` // 1, 5, 15 minutes (none on Windows)
	UptimeS    uint64    `json:"uptime_s"`
	NetRx      float64   `json:"net_rx"` // bytes a second
	NetTx      float64   `json:"net_tx"`
	Host       string    `json:"host"`
	OS         string    `json:"os"`
}

// Proc is one process.
type Proc struct {
	PID       int32     `json:"pid"`
	PPID      int32     `json:"ppid"`
	Name      string    `json:"name"`
	Cmd       string    `json:"cmd"`
	CPU       float64   `json:"cpu"` // percent of one core, since the last look
	Mem       uint64    `json:"mem"` // resident
	StartedAt time.Time `json:"started_at"`
	Depth     int       `json:"depth"` // under the process it was listed from
}

// Sampler keeps what a rate needs between two looks (CPU, network).
type Sampler struct {
	dir string // the disk of this folder is shown

	mu      sync.Mutex
	procs   map[int32]*process.Process
	net     *net.IOCountersStat
	netAt   time.Time
	cpuWarm bool
}

// New reads the machine; dir's disk is the one shown.
func New(dir string) *Sampler { return &Sampler{dir: dir, procs: map[int32]*process.Process{}} }

// Machine reads the machine now (a rate is since the last call).
func (s *Sampler) Machine(ctx context.Context) Machine {
	s.mu.Lock()
	defer s.mu.Unlock()
	var m Machine
	if !s.cpuWarm { // the first look has nothing to compare with
		_, _ = cpu.PercentWithContext(ctx, 200*time.Millisecond, false)
		s.cpuWarm = true
	}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		m.CPUPercent = p[0]
	}
	if p, err := cpu.PercentWithContext(ctx, 0, true); err == nil {
		m.Cores = p
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemTotal, m.MemUsed = v.Total, v.Used
	}
	m.DiskPath = s.dir
	if d, err := disk.UsageWithContext(ctx, s.dir); err == nil {
		m.DiskTotal, m.DiskUsed = d.Total, d.Used
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		m.Load = []float64{l.Load1, l.Load5, l.Load15}
	}
	if h, err := host.InfoWithContext(ctx); err == nil {
		m.UptimeS, m.Host, m.OS = h.Uptime, h.Hostname, h.Platform+" "+h.PlatformVersion
	}
	if c, err := net.IOCountersWithContext(ctx, false); err == nil && len(c) > 0 {
		now := time.Now()
		if s.net != nil {
			if dt := now.Sub(s.netAt).Seconds(); dt > 0 && c[0].BytesRecv >= s.net.BytesRecv && c[0].BytesSent >= s.net.BytesSent {
				m.NetRx = float64(c[0].BytesRecv-s.net.BytesRecv) / dt
				m.NetTx = float64(c[0].BytesSent-s.net.BytesSent) / dt
			}
		}
		s.net, s.netAt = &c[0], now
	}
	return m
}

// Quick is CPU and memory only (the header's figures): cheap enough to ask often.
func (s *Sampler) Quick(ctx context.Context) (cpuPercent float64, memUsed, memTotal uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cpuWarm {
		_, _ = cpu.PercentWithContext(ctx, 200*time.Millisecond, false)
		s.cpuWarm = true
	}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		cpuPercent = p[0]
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		memUsed, memTotal = v.Used, v.Total
	}
	return
}

// Count is how many processes are under root.
func Count(ctx context.Context, root int32) int {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return 0
	}
	kids := map[int32][]int32{}
	for _, p := range all {
		if pp, err := p.PpidWithContext(ctx); err == nil {
			kids[pp] = append(kids[pp], p.Pid)
		}
	}
	n := 0
	var walk func(int32)
	walk = func(pid int32) {
		for _, k := range kids[pid] {
			n++
			walk(k)
		}
	}
	walk(root)
	return n
}

// Tree is root and every process under it (each child after its parent).
func (s *Sampler) Tree(ctx context.Context, root int32) (Proc, []Proc, error) {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return Proc{}, nil, err
	}
	kids := map[int32][]*process.Process{}
	byPID := map[int32]*process.Process{}
	for _, p := range all {
		byPID[p.Pid] = p
		if pp, err := p.PpidWithContext(ctx); err == nil {
			kids[pp] = append(kids[pp], p)
		}
	}
	rp, ok := byPID[root]
	if !ok {
		return Proc{}, nil, errors.New("không thấy tiến trình")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int32]bool{}
	var out []Proc
	var walk func(pid int32, depth int)
	walk = func(pid int32, depth int) {
		list := kids[pid]
		slices.SortFunc(list, func(a, b *process.Process) int { return int(a.Pid - b.Pid) })
		for _, p := range list {
			if seen[p.Pid] {
				continue
			}
			seen[p.Pid] = true
			out = append(out, s.read(ctx, p, pid, depth))
			walk(p.Pid, depth+1)
		}
	}
	self := s.read(ctx, rp, 0, 0)
	walk(root, 0)
	for pid := range s.procs { // what ended is forgotten
		if _, alive := byPID[pid]; !alive {
			delete(s.procs, pid)
		}
	}
	return self, out, nil
}

// read reads one process; its CPU is since the last look at it.
func (s *Sampler) read(ctx context.Context, p *process.Process, ppid int32, depth int) Proc {
	if kept, ok := s.procs[p.Pid]; ok {
		p = kept
	} else {
		s.procs[p.Pid] = p
	}
	out := Proc{PID: p.Pid, PPID: ppid, Depth: depth}
	out.Name, _ = p.NameWithContext(ctx)
	if c, err := p.CmdlineWithContext(ctx); err == nil {
		if len(c) > 300 {
			c = c[:300] + "…"
		}
		out.Cmd = c
	}
	out.CPU, _ = p.PercentWithContext(ctx, 0)
	if mi, err := p.MemoryInfoWithContext(ctx); err == nil {
		out.Mem = mi.RSS
	}
	if ms, err := p.CreateTimeWithContext(ctx); err == nil {
		out.StartedAt = time.UnixMilli(ms)
	}
	return out
}

// Stop asks pid to end (SIGTERM; Windows has no such signal: it ends).
func Stop(ctx context.Context, pid int32) error {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return err
	}
	return p.TerminateWithContext(ctx)
}

// KillTree ends pid and everything under it at once, the deepest first.
func KillTree(ctx context.Context, pid int32) error {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return err
	}
	var all []*process.Process
	var walk func(p *process.Process)
	walk = func(p *process.Process) {
		kids, _ := p.ChildrenWithContext(ctx)
		for _, k := range kids {
			walk(k)
		}
		all = append(all, p)
	}
	walk(p)
	var first error
	for _, x := range all {
		if err := x.KillWithContext(ctx); err != nil && first == nil && x.Pid == pid {
			first = err
		}
	}
	return first
}
