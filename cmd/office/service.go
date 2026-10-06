package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// serviceLabel names the launchd job / systemd unit that starts office at login.
const serviceLabel = "com.agent-office"

// serviceCmd registers `office run` with the OS so office comes back after a
// reboot: a LaunchAgent on macOS, a systemd user unit on Linux.
func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Tự bật office khi mở máy (LaunchAgent trên macOS, systemd user trên Linux)",
	}
	var now bool
	install := &cobra.Command{
		Use:   "install [-- tùy chọn của office run]",
		Short: "Đăng ký office run chạy khi đăng nhập máy",
		Long: `Đăng ký office run với hệ điều hành, chạy trong thư mục hiện tại với PATH hiện tại
(để tìm được node, git, claude...). Office tự bật lại nếu supervisor dừng bất thường.
Log ghi vào <home>/logs/office.log. Chạy lại lệnh này sau khi đổi thư mục hoặc PATH.`,
		Example: `  office service install          # bật từ lần đăng nhập sau
  office service install --now    # bật luôn (tắt office đang chạy tay trước)
  office service install -- --ui-port 3000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return serviceInstall(args, now)
		},
	}
	install.Flags().BoolVar(&now, "now", false, "bật ngay, không chờ lần đăng nhập sau")
	cmd.AddCommand(install,
		&cobra.Command{
			Use:   "uninstall",
			Short: "Bỏ đăng ký (dừng office do dịch vụ chạy)",
			RunE:  func(*cobra.Command, []string) error { return serviceUninstall() },
		},
		&cobra.Command{
			Use:   "status",
			Short: "Xem office đã đăng ký và đang chạy chưa",
			RunE:  func(*cobra.Command, []string) error { return serviceStatus() },
		})
	return cmd
}

type serviceSpec struct {
	exe, dir, log, path string
	args                []string // argv after the executable
}

func newServiceSpec(extra []string) (serviceSpec, error) {
	exe, err := os.Executable()
	if err != nil {
		return serviceSpec{}, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	dir, err := os.Getwd()
	if err != nil {
		return serviceSpec{}, err
	}
	h, err := resolveHome()
	if err != nil {
		return serviceSpec{}, err
	}
	logDir := filepath.Join(h.Dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return serviceSpec{}, err
	}
	// the global flags are relative to the folder the service runs in; pin them
	args := []string{"--home", h.Dir}
	if cfg, err := filepath.Abs(configPath); err == nil {
		args = append(args, "--config", cfg)
	}
	args = append(append(args, "run"), extra...)
	return serviceSpec{exe: exe, dir: dir, log: filepath.Join(logDir, "office.log"), path: os.Getenv("PATH"), args: args}, nil
}

func serviceInstall(extra []string, now bool) error {
	s, err := newServiceSpec(extra)
	if err != nil {
		return err
	}
	file, err := serviceFile()
	if err != nil {
		return err
	}
	var body []byte
	switch runtime.GOOS {
	case "darwin":
		body = launchdPlist(s)
	case "linux":
		body = systemdUnit(s)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, body, 0o644); err != nil {
		return err
	}
	fmt.Println("✓ đã ghi", file)
	switch runtime.GOOS {
	case "darwin":
		// LaunchAgents in ~/Library/LaunchAgents load by themselves at login
		if now {
			_ = launchctl("bootout", launchdDomain()+"/"+serviceLabel) // reinstall: drop the old job
			if err := launchctl("bootstrap", launchdDomain(), file); err != nil {
				return err
			}
		}
		fmt.Println("  office tự bật mỗi khi đăng nhập máy (bật tự đăng nhập trong Cài đặt nếu muốn chạy ngay khi mở máy)")
	case "linux":
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
		verb := []string{"enable"}
		if now {
			verb = []string{"enable", "--now"}
		}
		if err := systemctl(append(verb, serviceLabel+".service")...); err != nil {
			return err
		}
		fmt.Println("  office tự bật khi đăng nhập; chạy `loginctl enable-linger` để bật ngay khi mở máy, không cần đăng nhập")
	}
	if !now {
		fmt.Println("  thêm --now để bật luôn (tắt office đang chạy tay trước)")
	}
	fmt.Println("  log:", s.log)
	return nil
}

func serviceUninstall() error {
	file, err := serviceFile()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		_ = launchctl("bootout", launchdDomain()+"/"+serviceLabel)
	case "linux":
		_ = systemctl("disable", "--now", serviceLabel+".service")
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if runtime.GOOS == "linux" {
		_ = systemctl("daemon-reload")
	}
	fmt.Println("✓ đã bỏ đăng ký, office không tự bật nữa")
	return nil
}

func serviceStatus() error {
	file, err := serviceFile()
	if err != nil {
		return err
	}
	if _, err := os.Stat(file); err != nil {
		fmt.Println("chưa đăng ký (chạy `office service install`)")
		return nil
	}
	fmt.Println("đã đăng ký:", file)
	var c *exec.Cmd
	if runtime.GOOS == "darwin" {
		c = exec.Command("launchctl", "print", launchdDomain()+"/"+serviceLabel)
	} else {
		c = exec.Command("systemctl", "--user", "status", "--no-pager", serviceLabel+".service")
	}
	out, err := c.CombinedOutput()
	if runtime.GOOS == "darwin" {
		if err != nil {
			fmt.Println("chưa chạy (bật ở lần đăng nhập sau, hoặc `office service install --now`)")
			return nil
		}
		for _, line := range strings.Split(string(out), "\n") {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "state =") || strings.HasPrefix(t, "pid =") || strings.HasPrefix(t, "last exit code =") {
				fmt.Println(" ", t)
			}
		}
		return nil
	}
	os.Stdout.Write(out)
	return nil
}

func serviceFile() (string, error) {
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(u, "Library", "LaunchAgents", serviceLabel+".plist"), nil
	case "linux":
		return filepath.Join(u, ".config", "systemd", "user", serviceLabel+".service"), nil
	}
	return "", fmt.Errorf("chưa hỗ trợ tự bật trên %s", runtime.GOOS)
}

func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func launchctl(args ...string) error { return runQuiet("launchctl", args...) }

func systemctl(args ...string) error {
	return runQuiet("systemctl", append([]string{"--user"}, args...)...)
}

func runQuiet(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// launchdPlist restarts the supervisor only when it exits abnormally; a clean
// stop (SIGTERM from launchctl, logout) stays stopped.
func launchdPlist(s serviceSpec) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range append([]string{s.exe}, s.args...) {
		b.WriteString("\t\t<string>" + xmlText(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>WorkingDirectory</key><string>` + xmlText(s.dir) + `</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key><string>` + xmlText(s.path) + `</string>
	</dict>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key><false/>
	</dict>
	<key>ThrottleInterval</key><integer>10</integer>
	<key>ProcessType</key><string>Interactive</string>
	<key>StandardOutPath</key><string>` + xmlText(s.log) + `</string>
	<key>StandardErrorPath</key><string>` + xmlText(s.log) + `</string>
</dict>
</plist>
`)
	return []byte(b.String())
}

// systemdQuote quotes one argv word for ExecStart.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + s + `"`
}

func systemdUnit(s serviceSpec) []byte {
	argv := make([]string, 0, len(s.args)+1)
	for _, a := range append([]string{s.exe}, s.args...) {
		argv = append(argv, systemdQuote(a))
	}
	return []byte(`[Unit]
Description=agent-office
After=network-online.target

[Service]
ExecStart=` + strings.Join(argv, " ") + `
WorkingDirectory=` + s.dir + `
Environment=` + systemdQuote("PATH="+s.path) + `
Restart=on-failure
RestartSec=10
KillMode=mixed
TimeoutStopSec=30
StandardOutput=append:` + s.log + `
StandardError=append:` + s.log + `

[Install]
WantedBy=default.target
`)
}
