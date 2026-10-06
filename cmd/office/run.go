package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/channels"
	"github.com/tung1998/agent-office/internal/cleanup"
	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/home"
	"github.com/tung1998/agent-office/internal/limitalert"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/monitor"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/selfupdate"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/worktree"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/api"
	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/setup"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/transfer"
)

// serveCmd is the API server. `office run` (the supervisor) starts it as a
// child; it can also be run directly.
func serveCmd() *cobra.Command {
	var (
		addr           string
		origins        []string
		secureCookies  bool
		trustedProxies []string
		cliSetup       bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Chạy server API (thường do `office run` khởi động)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if addr == "" {
				addr = cfg.Server.APIAddr
			}
			origins = append(origins, cfg.Server.CORSOrigins...)

			h, err := resolveHome()
			if err != nil {
				return err
			}
			a, err := openApp(ctx, h)
			if err != nil {
				return err
			}
			defer a.Close()
			st := a.store
			// what changed, whoever changed it, for the dashboard's open pages (ADR-072); set before anything writes in the background
			liveBus := events.New(300 * time.Millisecond)
			if sq, ok := a.store.(*sqlite.Store); ok {
				sq.OnWrite(liveBus.Wrote)
				sq.OnChat(liveBus.Chat) // a message or chat, pushed with its data (ADR-078)
			}

			proxies, err := parsePrefixes(trustedProxies)
			if err != nil {
				return err
			}
			var cliTools *clitools.Manager
			if cliSetup {
				cliTools = a.cli
			}
			// jobs cut off by the last shutdown do not run again (ADR-040)
			if n, err := a.store.Jobs().FailRunning(ctx, "restart", "office khởi động lại khi job đang chạy", time.Now().UTC()); err == nil && n > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "office: %d job đang chạy dở được đánh dấu lỗi (khởi động lại)\n", n)
			}
			// and their tasks, so the tasks list is right and "Chạy lại" works
			_, _ = a.store.Tasks().FailRunning(ctx, "Office khởi động lại khi Việc đang chạy", time.Now().UTC())
			chatEngine := chat.NewEngine(a.store, a.providers, a.usage)
			chatEngine.SetAttachments(attach.Store{Dir: filepath.Join(h.Dir, "attachments")})
			// agents edit and check in their own git worktrees (ADR-037)
			trees := worktree.New(filepath.Join(h.Dir, "worktrees"))
			chatEngine.SetWorktrees(trees)
			go chatEngine.SweepWorktrees(ctx, 14*24*time.Hour)
			procs := ops.NewManager(a.store, filepath.Join(h.Dir, "logs"), a.cli.Env())
			defer procs.Shutdown() // project processes stop with the office
			go procs.RunSampler(ctx, 3*time.Second)
			procs.Autostart(ctx)
			// agents read build/run/monitoring data through the office tools (MCP for Claude Code)
			acts := actions.New(a.store, procs) // agents propose, people approve
			// agents' long-term notes (ADR-068); too long, a fast model compacts them
			mem := memory.New(a.store, compactNotes(a.store, chatEngine))
			acts.SetMemory(mem)
			acts.SetSender(chatEngine) // send_to_chat: an agent writes to another chat in its person's name
			office := officetools.New(a.store, procs, acts)
			mcp := mcpserver.New(office, version)
			chatEngine.SetOffice(office, mcp, "http://"+loopback(addr)+"/mcp")
			monitors := monitor.New(a.store, procs, chatEngine)
			// schedules and webhooks start chats as jobs (ADR-040)
			runner := trigger.New(a.store, officeExecutor{chat: chatEngine})
			go runner.Run(ctx)
			// the office assistant: a hidden project whose chats span projects (ADR-046)
			if _, err := assistant.Ensure(ctx, a.store, a.org, filepath.Join(h.Dir, "assistant")); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "office: không dựng được trợ lý office: %v\n", err)
			}
			assistantID := func(ctx context.Context) string { return assistant.ID(ctx, a.store) }
			office.SetOffice(assistantID)
			chatEngine.SetAssistant(assistantID)
			acts.SetRunner(assistantRunner{store: a.store, trigger: runner})
			// Telegram / Discord bots: their messages are automations' triggers (ADR-048, ADR-049)
			if err := channels.MigrateRules(ctx, a.store); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "office: không chuyển được kênh sang quy tắc: %v\n", err)
			}
			bots := channels.NewManager(a.store, chatEngine, runner, func(ch storage.Channel) (channels.Adapter, error) {
				token, err := a.providers.Box().Open(ch.TokenEnc)
				if err != nil {
					return nil, fmt.Errorf("không mở được token: %w", err)
				}
				if ch.Kind == "discord" {
					return &channels.Discord{Token: token}, nil
				}
				return &channels.Telegram{Token: token}, nil
			})
			runner.SetOnReply(bots.Reply)
			runner.SetOnProgress(bots.Progress)
			acts.SetAutoApprover(bots.DirectApprover) // a chat in direct mode: approved in the turn, the agent goes on
			runner.SetOnNotify(func(ctx context.Context, channelID, chatID, text, convID string) {
				_ = bots.NotifyConversation(ctx, channelID, chatID, text, convID)
			})
			// an AI connection close to its limit: told in the chat the admin picked
			alerts := limitalert.New(a.store, bots.Notify)
			chatEngine.SetOnLimits(func(p storage.Provider, l chat.Limits) {
				for name, w := range l.Windows {
					alerts.Check(ctx, p.ID, p.Name, name, w.Utilization, w.ResetsAt)
				}
			})
			bots.SetDecider(chatDecider{store: a.store, chat: chatEngine, acts: acts}) // proposals decided from the chat (ADR-054)
			office.SetSendFile(bots.SendFileFor)                                       // an agent in a bot's chat posts images and files there (ADR-083)
			chatEngine.SetOnBotDecided(bots.DecidedOnDashboard)                        // a bot chat's card decided on the dashboard: the bot goes on (ADR-084)
			// a project's agent running on its own, finding work (spec 2026-10-01-burn-design)
			burner := burn.New(a.store, chatEngine, trees)
			office.SetBurn(func(ctx context.Context, sc officetools.Scope, name string, in officetools.BurnInput) (string, error) {
				return burner.Tool(ctx, sc, name, burn.ToolInput{Title: in.Title, Kind: in.Kind, Detail: in.Detail, Item: in.Item, Summary: in.Summary, Reason: in.Reason})
			})
			burner.Start(ctx)
			bots.Start(ctx)
			// data management: measuring, cleaning by hand and on its own (ADR-095)
			cleaner := cleanup.New(a.store, chatEngine, trees, filepath.Join(h.Dir, "attachments"), h.DB())
			go cleaner.RunAuto(ctx)

			// self-update: only under the supervisor and when the source is here
			supervised := os.Getenv(selfupdate.EnvSupervised) == "1"
			if supervised {
				go exitWithParent(ctx)
			}
			restart := make(chan struct{})
			var updater *selfupdate.Updater
			if supervised {
				if exe, err := os.Executable(); err == nil {
					if p, err := filepath.EvalSymlinks(exe); err == nil {
						exe = p
					}
					if src, ok := selfupdate.FindSource(exe); ok {
						var once sync.Once
						updater = selfupdate.New(src, h.Dir, a.cli.Env(), func() { once.Do(func() { close(restart) }) })
					}
				}
			}
			go monitors.Run(ctx)
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			// the MCP servers office manages, at /mcp/s/<name> with the runs' tokens (ADR-091);
			// who calls, and tools that write (ADR-093)
			gateway := &mcpgateway.Gateway{Store: a.store, Box: a.providers.Box(), Auth: mcp.Authorized, Log: log, Env: a.cli.Env(),
				Identify: gatewayIdentify(a.store, mcp), Propose: gatewayProposer(acts)}
			defer gateway.Close() // stdio servers office runs (ADR-092)
			// an approved mcp_call runs through it
			acts.SetMCP(gateway)
			chatEngine.SetGateway(gatewayFor(a.store, gateway))
			handler := api.New(api.Config{
				Office: office, Channels: bots, Memory: mem, Events: liveBus,
				Store: st, Auth: a.auth, AllowedOrigins: origins,
				SecureCookies: secureCookies, TrustedProxies: proxies, Logger: log, Version: version,
				Providers: a.providers, Org: a.org, Setup: setup.New(a.store, a.providers, a.org),
				Transfer:   transfer.New(a.store, a.providers, a.org),
				Usage:      a.usage,
				CLITools:   cliTools,
				Chat:       chatEngine,
				Burn:       burner,
				Cleanup:    cleaner,
				Trigger:    runner,
				Automation: newAutomation(a, h),
				Ops:        procs,
				Monitors:   monitors,
				MCP:        mcp,
				Gateway:    gateway,
				Actions:    acts,
				Updater:    updater,
				Supervised: supervised,
				Backup: func(ctx context.Context) (string, error) {
					return backupTo(ctx, a, filepath.Join(h.Dir, "backups", time.Now().Format("20060102-150405")))
				},
				System: api.SystemInfo{Mode: string(h.Mode), HomeDir: h.Dir, ProjectRoot: h.ProjectRoot},
			})

			// a first run gets admin / admin, to replace with a real email and password on first sign-in
			if created, err := a.auth.EnsureDefaultAdmin(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "office: không tạo được tài khoản mặc định: %v\n", err)
			} else if _, pending := a.auth.PendingDefault(ctx); created || pending {
				fmt.Fprintf(os.Stderr, "Tài khoản mặc định: %s / %s (lần đăng nhập đầu sẽ bắt đặt email và mật khẩu)\n",
					auth.DefaultAdminEmail, auth.DefaultAdminPassword)
			}

			srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
			go sweepSessions(ctx, st, log)

			errCh := make(chan error, 1)
			go func() { errCh <- srv.ListenAndServe() }()
			fmt.Fprintf(os.Stderr, "office %s · chế độ %s · dữ liệu %s · api http://%s\n", version, h.Mode, h.Dir, addr)

			select {
			case err := <-errCh:
				if !errors.Is(err, http.ErrServerClosed) {
					return err
				}
			case <-restart:
				// swapped in a new build: stop cleanly and let the supervisor restart us
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = srv.Shutdown(shutdownCtx)
				cancel()
				gateway.Close()
				procs.Shutdown()
				a.Close()
				os.Exit(selfupdate.RestartCode)
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return srv.Shutdown(shutdownCtx)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "api", "", "địa chỉ API (mặc định server.api_addr trong config, 127.0.0.1:8787)")
	cmd.Flags().StringSliceVar(&origins, "allowed-origin", []string{"http://localhost:2704", "http://127.0.0.1:2704"}, "origin của dashboard được phép gọi API")
	cmd.Flags().BoolVar(&secureCookies, "secure-cookies", false, "bật cờ Secure cho cookie (bắt buộc khi chạy sau HTTPS)")
	cmd.Flags().BoolVar(&cliSetup, "cli-setup", true, "cho phép cài và đăng nhập Claude Code/Codex từ dashboard (tắt khi chạy trong container)")
	cmd.Flags().StringSliceVar(&trustedProxies, "trusted-proxy", []string{"127.0.0.1/32", "::1/128"}, "dải IP của proxy (dashboard Nuxt) được tin header X-Forwarded-*")
	return cmd
}

// sweepSessions deletes expired sessions hourly.
func sweepSessions(ctx context.Context, st storage.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := st.Sessions().DeleteExpired(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Warn("sweep sessions", "err", err)
		} else if n > 0 {
			log.Info("sweep sessions", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func parsePrefixes(in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, v := range in {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return nil, fmt.Errorf("--trusted-proxy %q: %w", v, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// newAutomation manages skills, agents and MCP servers on this machine. The
// library lives in the office data folder; removed items go to its trash.
func newAutomation(a *app, h home.Home) *automation.Service {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	trash := filepath.Join(h.Dir, "trash")
	claude := func() string { return a.cli.LookPath("claude") }
	return &automation.Service{
		Health:    &automation.MCPHealth{Home: userHome, Claude: claude},
		Home:      userHome,
		Library:   automation.Library{Dir: filepath.Join(h.Dir, "library"), Trash: trash},
		Installer: automation.Installer{Home: userHome, Trash: trash, Claude: claude},
		Projects: func(ctx context.Context) map[string]string {
			out := map[string]string{}
			if list, err := a.store.Repos().List(ctx); err == nil {
				for _, p := range list {
					if p.Path != "" {
						out[p.Path] = p.ID
					}
				}
			}
			return out
		},
	}
}

// loopback turns a listen address into one reachable from this machine
// (":8787" or "0.0.0.0:8787" → "127.0.0.1:8787").
func loopback(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// exitWithParent stops the server (as on Ctrl+C) when its supervisor dies, so
// no orphan keeps the port.
func exitWithParent(ctx context.Context) {
	parent := os.Getppid()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if os.Getppid() != parent {
				fmt.Fprintln(os.Stderr, "office: supervisor đã dừng, server tắt theo")
				_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
				return
			}
		}
	}
}
