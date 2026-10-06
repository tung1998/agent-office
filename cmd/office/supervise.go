package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/selfupdate"
)

// runCmd is the supervisor: it runs the API server (`office serve`) and the
// dashboard as children, restarts them when they stop, applies a self-update
// when the server asks (exit code 75), and rolls back to the previous build if
// the new one does not come up.
func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Bật office: server API và dashboard, tự khởi động lại và cập nhật",
		Long: `Bật office. Supervisor chạy server API (office serve) và dashboard, tự bật lại khi
chúng dừng, và áp dụng "Cập nhật office" (tự quay về bản cũ nếu bản mới lỗi).

Tùy chọn của supervisor: --no-ui, --ui-port (mặc định 2704), --ui-dir.
Các tùy chọn khác được chuyển cho office serve (xem office serve --help).`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "-h" || a == "--help" {
					return cmd.Help()
				}
			}
			return supervise(args)
		},
	}
}

type child struct {
	name string
	cmd  *exec.Cmd
	done chan int // exit code
}

func startChild(name, dir string, env []string, argv ...string) (*child, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = env
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("không chạy được %s: %w", name, err)
	}
	ch := &child{name: name, cmd: c, done: make(chan int, 1)}
	go func() {
		err := c.Wait()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			code = -1
		}
		ch.done <- code
	}()
	return ch, nil
}

// stop asks a child to exit, then kills it.
func (c *child) stop() {
	if c == nil || c.cmd == nil {
		return
	}
	_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-c.done:
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		<-c.done
	}
}

func supervise(args []string) error {
	// supervisor flags; the rest goes to `office serve`
	var fwd []string
	uiPort, uiDir, noUI := "2704", "", false
	apiAddr := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() string {
			if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "--") {
				return v
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--no-ui":
			noUI = true
		case a == "--ui-port" || strings.HasPrefix(a, "--ui-port="):
			uiPort = val()
		case a == "--ui-dir" || strings.HasPrefix(a, "--ui-dir="):
			uiDir = val()
		case a == "--api" || strings.HasPrefix(a, "--api="):
			apiAddr = val()
			fwd = append(fwd, "--api", apiAddr)
		case a == "--home" || strings.HasPrefix(a, "--home="):
			homeFlag = val() // flag parsing is off here; the supervisor's own files follow it too
			fwd = append(fwd, "--home", homeFlag)
		case a == "--config" || a == "-c" || strings.HasPrefix(a, "--config="):
			configPath = val()
			fwd = append(fwd, "--config", configPath)
		default:
			fwd = append(fwd, a)
		}
	}
	if apiAddr == "" {
		apiAddr = "127.0.0.1:8787"
		if cfg, err := loadConfig(); err == nil && cfg.Server.APIAddr != "" {
			apiAddr = cfg.Server.APIAddr
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	src, hasSource := selfupdate.FindSource(exe)
	if uiDir == "" && hasSource && src.UIDir != "" {
		uiDir = src.UIDir
	}
	uiEntry := filepath.Join(uiDir, ".output", "server", "index.mjs")
	if !noUI && uiDir != "" {
		if _, err := os.Stat(uiEntry); err != nil {
			fmt.Fprintf(os.Stderr, "office: chưa build dashboard (%s), chỉ chạy API. Chạy `make ui-build` rồi khởi động lại.\n", uiEntry)
			noUI = true
		}
	}
	if uiDir == "" {
		noUI = true
	}
	// after an update the server binary at exe is the new build
	env := append(os.Environ(), selfupdate.EnvSupervised+"=1")
	uiEnv := append(os.Environ(), "PORT="+uiPort, "NUXT_OFFICE_API_BASE=http://"+loopback(apiAddr))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP) // closing the terminal stops cleanly too

	var api, ui *child
	startAPI := func() error {
		c, err := startChild("server", "", env, append([]string{exe, "serve"}, fwd...)...)
		api = c
		return err
	}
	startUI := func() {
		if noUI {
			return
		}
		c, err := startChild("dashboard", uiDir, uiEnv, "node", uiEntry)
		if err != nil {
			fmt.Fprintln(os.Stderr, "office:", err)
			return
		}
		ui = c
	}
	killOrphans() // children left by a supervisor that died without cleaning up
	if err := startAPI(); err != nil {
		return err
	}
	startUI()
	savePIDs(api, ui)
	if !noUI {
		fmt.Fprintf(os.Stderr, "office: dashboard http://localhost:%s\n", uiPort)
	}

	crashes := 0
	var lastStart = time.Now()
	var uiRestart, apiRestart <-chan time.Time // pending restarts (never block the loop)
	uiDone := func() <-chan int {
		if ui == nil {
			return nil
		}
		return ui.done
	}
	for {
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "office: đang tắt…")
			ui.stop()
			api.stop()
			_ = os.Remove(pidFile())
			return nil

		case code := <-uiDone():
			fmt.Fprintf(os.Stderr, "office: dashboard dừng (mã %d), bật lại sau 2 giây\n", code)
			ui = nil
			uiRestart = time.After(2 * time.Second)

		case <-uiRestart:
			uiRestart = nil
			startUI()
			savePIDs(api, ui)

		case <-apiRestart:
			apiRestart = nil
			if err := startAPI(); err != nil {
				return err
			}
			savePIDs(api, ui)
			lastStart = time.Now()

		case code := <-api.done:
			if code == selfupdate.RestartCode && hasSource {
				fmt.Fprintln(os.Stderr, "office: cập nhật, khởi động lại bằng bản mới…")
				ui.stop()
				ui = nil
				if err := startAPI(); err != nil {
					return err
				}
				startUI()
				savePIDs(api, ui)
				if healthy(apiAddr, 25*time.Second, api) {
					selfupdate.SaveResult(homeDir(), selfupdate.Result{State: "ok", Message: "Đã cập nhật và khởi động lại", At: time.Now().UTC()})
					crashes, lastStart = 0, time.Now()
					continue
				}
				fmt.Fprintln(os.Stderr, "office: bản mới không chạy được, quay về bản trước")
				ui.stop()
				api.stop()
				ui = nil
				if err := selfupdate.Rollback(src); err != nil {
					fmt.Fprintln(os.Stderr, "office: không quay về được:", err)
				}
				if err := startAPI(); err != nil {
					return err
				}
				startUI()
				savePIDs(api, ui)
				selfupdate.SaveResult(homeDir(), selfupdate.Result{State: "rolled_back", Message: "Bản mới không khởi động được (kiểm tra /healthz thất bại), đã quay về bản trước", At: time.Now().UTC()})
				lastStart = time.Now()
				continue
			}
			if code == 0 {
				ui.stop()
				return nil
			}
			// crashed: back off, and after repeated quick crashes give up
			if time.Since(lastStart) > time.Minute {
				crashes = 0
			}
			crashes++
			if crashes > 5 {
				ui.stop()
				return fmt.Errorf("server dừng liên tục (mã %d), supervisor thoát", code)
			}
			delay := time.Duration(1<<min(crashes, 5)) * time.Second
			fmt.Fprintf(os.Stderr, "office: server dừng (mã %d), bật lại sau %s\n", code, delay)
			api = &child{done: make(chan int)} // placeholder until the restart fires
			apiRestart = time.After(delay)
		}
	}
}

// healthy polls /healthz until it answers 200, the timeout passes, or the
// server exits (a build that crashes at start fails fast).
func healthy(addr string, timeout time.Duration, c *child) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	url := "http://" + loopback(addr) + "/healthz"
	for ctx.Err() == nil {
		select {
		case code := <-c.done:
			c.done <- code // keep it for stop()
			return false
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// homeDir is the office data folder (for the update result file).
func homeDir() string {
	if h, err := resolveHome(); err == nil {
		return h.Dir
	}
	return "."
}

func pidFile() string { return filepath.Join(homeDir(), "supervisor.pids") }

// savePIDs records the children so a later supervisor can clean them up.
func savePIDs(cs ...*child) {
	var b strings.Builder
	for _, c := range cs {
		if c != nil && c.cmd != nil && c.cmd.Process != nil {
			fmt.Fprintf(&b, "%d\n", c.cmd.Process.Pid)
		}
	}
	_ = os.WriteFile(pidFile(), []byte(b.String()), 0o600)
}

// killOrphans stops children recorded by a previous supervisor that are still
// running (only processes that look like office's server or dashboard).
func killOrphans() {
	raw, err := os.ReadFile(pidFile())
	if err != nil {
		return
	}
	for _, f := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(f)
		if err != nil || pid <= 1 {
			continue
		}
		out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		cmdline := string(out)
		if err != nil || !(strings.Contains(cmdline, "office serve") || strings.Contains(cmdline, ".output/server/index.mjs")) {
			continue
		}
		fmt.Fprintf(os.Stderr, "office: dọn tiến trình còn sót của lần chạy trước (PID %d)\n", pid)
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		_ = syscall.Kill(pid, syscall.SIGTERM)
		for i := 0; i < 30 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(200 * time.Millisecond)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = os.Remove(pidFile())
}
