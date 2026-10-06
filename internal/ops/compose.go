package ops

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Docker Compose support: office drives the project's own compose file with
// the docker CLI, using compose's default project name (the folder name) so
// it sees the same containers as `docker compose` run by hand.

var (
	ErrNoCompose     = errors.New("project không có file docker compose")
	ErrUnknownFile   = errors.New("file compose không thuộc project")
	ErrUnknownSvc    = errors.New("service không có trong file compose")
	ErrBadAction     = errors.New("thao tác không hợp lệ")
	ErrDockerMissing = errors.New("không tìm thấy lệnh docker trên máy")
	ErrNoComposeCLI  = errors.New("chưa có Docker Compose v2 (lệnh `docker compose`); cài Docker Desktop hoặc plugin docker-compose")
)

// DockerError is a failed docker command; Msg is its first meaningful line.
type DockerError struct{ Msg string }

func (e *DockerError) Error() string { return e.Msg }

func dockerErr(stderr string) error {
	if strings.Contains(stderr, "unknown shorthand flag: 'f'") || strings.Contains(stderr, "'compose' is not a docker command") {
		return ErrNoComposeCLI
	}
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return &DockerError{Msg: l}
		}
	}
	return &DockerError{Msg: "lệnh docker bị lỗi"}
}

// Docker reports whether the docker daemon answers.
type Docker struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Port is a published port.
type Port struct {
	URL       string `json:"url"`
	Published int    `json:"published"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
}

// Stats is a container's resource usage.
type Stats struct {
	CPU     string `json:"cpu"`      // "0.52%"
	Mem     string `json:"mem"`      // "12.3MiB / 7.6GiB"
	MemPerc string `json:"mem_perc"` // "0.16%"
	Net     string `json:"net"`      // "1.2kB / 0B"
	Block   string `json:"block"`    // "0B / 0B"
	PIDs    string `json:"pids"`
}

// Container is the container of a compose service.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`  // running | exited | restarting | paused | created | dead
	Status string `json:"status"` // "Up 3 minutes (healthy)"
	Health string `json:"health,omitempty"`
	Ports  []Port `json:"ports"`
	Stats  *Stats `json:"stats,omitempty"`
}

// Service is a compose service and its container, if any.
type Service struct {
	Name      string     `json:"name"`
	Container *Container `json:"container,omitempty"`
}

// ComposeView is what the dashboard shows.
type ComposeView struct {
	Docker   Docker    `json:"docker"`
	Files    []string  `json:"files"`
	File     string    `json:"file"`
	Services []Service `json:"services"`
	Action   *State    `json:"action,omitempty"` // last up/stop/… run
}

func (m *Manager) docker(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = m.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return nil, ErrDockerMissing
	}
	if err != nil {
		if stderr.Len() == 0 {
			return out, &DockerError{Msg: err.Error()}
		}
		return out, dockerErr(stderr.String())
	}
	return out, nil
}

// composeTarget validates the project and file.
func (m *Manager) composeTarget(ctx context.Context, projectID, file string) (dir string, files []string, chosen string, err error) {
	project, err := m.store.Repos().Get(ctx, projectID)
	if err != nil {
		return "", nil, "", err
	}
	if project.Path == "" {
		return "", nil, "", ErrNoFolder
	}
	for _, c := range Detect(project.Path).Compose {
		files = append(files, c.File)
	}
	if len(files) == 0 {
		return project.Path, files, "", ErrNoCompose
	}
	if file == "" {
		file = files[0]
	}
	if !slices.Contains(files, file) {
		return "", nil, "", ErrUnknownFile
	}
	return project.Path, files, file, nil
}

func (m *Manager) composeServices(ctx context.Context, dir, file string) ([]string, error) {
	// --profile '*' includes services behind profiles (e.g. an optional app)
	out, err := m.docker(ctx, dir, "compose", "-f", file, "--profile", "*", "config", "--services")
	if err != nil {
		if out, err = m.docker(ctx, dir, "compose", "-f", file, "config", "--services"); err != nil {
			return nil, err
		}
	}
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	sort.Strings(names)
	return names, nil
}

type psRow struct {
	ID         string `json:"ID"`
	Name       string `json:"Name"`
	Image      string `json:"Image"`
	Service    string `json:"Service"`
	State      string `json:"State"`
	Status     string `json:"Status"`
	Health     string `json:"Health"`
	Publishers []struct {
		URL           string `json:"URL"`
		TargetPort    int    `json:"TargetPort"`
		PublishedPort int    `json:"PublishedPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

// parsePS accepts both formats compose has used: a JSON array or one object per line.
func parsePS(out []byte) []psRow {
	out = bytes.TrimSpace(out)
	var rows []psRow
	if len(out) > 0 && out[0] == '[' {
		_ = json.Unmarshal(out, &rows)
		return rows
	}
	for _, l := range bytes.Split(out, []byte("\n")) {
		var r psRow
		if json.Unmarshal(l, &r) == nil && r.ID != "" {
			rows = append(rows, r)
		}
	}
	return rows
}

// Compose returns the compose view of a project. withStats adds CPU/RAM/net
// from `docker stats`, which is the slow part (it samples for a moment).
func (m *Manager) Compose(ctx context.Context, projectID, file string, withStats bool) (ComposeView, error) {
	v := ComposeView{Files: []string{}, Services: []Service{}}
	dir, files, file, err := m.composeTarget(ctx, projectID, file)
	v.Files, v.File = append(v.Files, files...), file
	if err != nil {
		return v, err
	}
	if st := m.State(actionKey(projectID)); st.Status != "stopped" || st.StartedAt != nil {
		v.Action = &st
	}
	ver, err := m.docker(ctx, dir, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		v.Docker.Error = err.Error()
		var de *DockerError
		if errors.As(err, &de) {
			v.Docker.Error = "Docker chưa chạy hoặc không kết nối được: " + de.Msg
		}
		return v, nil
	}
	v.Docker = Docker{Available: true, Version: strings.TrimSpace(string(ver))}

	names, err := m.composeServices(ctx, dir, file)
	if err != nil {
		v.Docker.Error = err.Error() // compose missing, or the file does not parse
		return v, nil
	}
	out, err := m.docker(ctx, dir, "compose", "-f", file, "--profile", "*", "ps", "--all", "--format", "json")
	if err != nil {
		out, _ = m.docker(ctx, dir, "compose", "-f", file, "ps", "--all", "--format", "json")
	}
	byService := map[string]*Container{}
	var running []string
	for _, r := range parsePS(out) {
		c := &Container{ID: r.ID, Name: r.Name, Image: r.Image, State: r.State, Status: r.Status, Health: r.Health, Ports: []Port{}}
		seen := map[int]bool{}
		for _, p := range r.Publishers {
			if p.PublishedPort == 0 || seen[p.PublishedPort] {
				continue
			}
			seen[p.PublishedPort] = true
			c.Ports = append(c.Ports, Port{URL: p.URL, Published: p.PublishedPort, Target: p.TargetPort, Protocol: p.Protocol})
		}
		byService[r.Service] = c
		if r.State == "running" {
			running = append(running, r.ID)
		}
	}
	if withStats && len(running) > 0 {
		for id, s := range m.dockerStats(ctx, dir, running) {
			for _, c := range byService {
				if strings.HasPrefix(c.ID, id) || strings.HasPrefix(id, c.ID) {
					s := s
					c.Stats = &s
				}
			}
		}
	}
	for _, n := range names {
		v.Services = append(v.Services, Service{Name: n, Container: byService[n]})
	}
	return v, nil
}

func (m *Manager) dockerStats(ctx context.Context, dir string, ids []string) map[string]Stats {
	out, err := m.docker(ctx, dir, append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, ids...)...)
	res := map[string]Stats{}
	if err != nil {
		return res
	}
	for _, l := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		var r struct {
			ID       string `json:"ID"`
			CPUPerc  string `json:"CPUPerc"`
			MemUsage string `json:"MemUsage"`
			MemPerc  string `json:"MemPerc"`
			NetIO    string `json:"NetIO"`
			BlockIO  string `json:"BlockIO"`
			PIDs     string `json:"PIDs"`
		}
		if json.Unmarshal(l, &r) == nil && r.ID != "" {
			res[r.ID] = Stats{CPU: r.CPUPerc, Mem: r.MemUsage, MemPerc: r.MemPerc, Net: r.NetIO, Block: r.BlockIO, PIDs: r.PIDs}
		}
	}
	return res
}

func actionKey(projectID string) string { return "compose:" + projectID }

var composeActions = map[string]bool{"up": true, "stop": true, "restart": true, "down": true, "pull": true, "build": true}

// ComposeAction runs `docker compose <action> [service]` in the background;
// its output streams like a process (ActionKey).
func (m *Manager) ComposeAction(ctx context.Context, projectID, file, action, service string) error {
	if !composeActions[action] {
		return ErrBadAction
	}
	dir, _, file, err := m.composeTarget(ctx, projectID, file)
	if err != nil {
		return err
	}
	if service != "" {
		names, err := m.composeServices(ctx, dir, file)
		if err != nil {
			return err
		}
		if !slices.Contains(names, service) {
			return ErrUnknownSvc
		}
		if action == "down" {
			action = "rm" // down is for the whole stack; one service is stopped and removed
		}
	}
	args := []string{"compose", "-f", file}
	if service == "" && action != "up" && action != "pull" && action != "build" {
		// stopping/removing the whole stack must include services behind profiles;
		// `up` keeps compose's default (optional profiles stay off)
		args = append(args, "--profile", "*")
	}
	args = append(args, action)
	switch action {
	case "up":
		args = append(args, "-d")
	case "rm":
		args = append(args, "-s", "-f")
	}
	if service != "" {
		args = append(args, service) // naming a service also enables its profile
	}
	p := m.get(actionKey(projectID))
	p.mu.Lock()
	if p.state.Status == "running" || p.state.Status == "stopping" {
		p.mu.Unlock()
		return ErrRunning
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = a
		if strings.ContainsAny(a, " *\"'$") {
			quoted[i] = strconv.Quote(a)
		}
	}
	p.def = storage.Process{ID: actionKey(projectID), ProjectID: projectID, Name: "docker compose " + action, Kind: "job",
		Command: "docker " + strings.Join(quoted, " ")}
	p.mu.Unlock()
	return m.launch(p, dir)
}

// ActionKey is the id to stream an action's output with Since.
func ActionKey(projectID string) string { return actionKey(projectID) }

// ComposeLogs follows a service's logs, calling emit per line until ctx ends.
func (m *Manager) ComposeLogs(ctx context.Context, projectID, file, service string, emit func(string)) error {
	dir, _, file, err := m.composeTarget(ctx, projectID, file)
	if err != nil {
		return err
	}
	names, err := m.composeServices(ctx, dir, file)
	if err != nil {
		return err
	}
	if !slices.Contains(names, service) {
		return ErrUnknownSvc
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", file, "--profile", "*", "logs", "--follow", "--tail", "300", "--no-color", "--no-log-prefix", service)
	cmd.Dir = dir
	cmd.Env = m.env
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không chạy được docker: %w", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			emit(ansiRe.ReplaceAllString(sc.Text(), ""))
		}
	}()
	err = cmd.Wait()
	pw.Close()
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// ComposeTail returns the last n log lines of a service.
func (m *Manager) ComposeTail(ctx context.Context, projectID, file, service string, n int) (string, error) {
	dir, _, file, err := m.composeTarget(ctx, projectID, file)
	if err != nil {
		return "", err
	}
	names, err := m.composeServices(ctx, dir, file)
	if err != nil {
		return "", err
	}
	if !slices.Contains(names, service) {
		return "", ErrUnknownSvc
	}
	out, err := m.docker(ctx, dir, "compose", "-f", file, "--profile", "*", "logs", "--tail", strconv.Itoa(n), "--no-color", "--no-log-prefix", service)
	if err != nil {
		return "", err
	}
	return ansiRe.ReplaceAllString(string(out), ""), nil
}

// ServiceContainer returns the container of one compose service (nil if none).
func (m *Manager) ServiceContainer(ctx context.Context, projectID, file, service string) (*Container, error) {
	dir, _, file, err := m.composeTarget(ctx, projectID, file)
	if err != nil {
		return nil, err
	}
	out, err := m.docker(ctx, dir, "compose", "-f", file, "--profile", "*", "ps", "--all", "--format", "json", service)
	if err != nil {
		return nil, err
	}
	for _, r := range parsePS(out) {
		if r.Service == service {
			return &Container{ID: r.ID, Name: r.Name, Image: r.Image, State: r.State, Status: r.Status, Health: r.Health}, nil
		}
	}
	return nil, nil
}
