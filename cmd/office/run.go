package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/activity"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/agentskills"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/channels"
	"github.com/tung1998/agent-office/internal/cleanup"
	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/heartbeat"
	"github.com/tung1998/agent-office/internal/home"
	"github.com/tung1998/agent-office/internal/limitalert"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/monitor"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/selfupdate"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/workflow"
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
	"github.com/tung1998/agent-office/internal/cognee"
	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/embed"
	"github.com/tung1998/agent-office/internal/setup"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/support"
)

// worktreeIdle: a chat's worktree untouched this long, with no change
// waiting for review, is removed (the chat stays).
const worktreeIdle = 3 * 24 * time.Hour

// serveCmd is the API server. `office run` (the supervisor) starts it as a
// child; it can also be run directly.
// turnStopWait is how long office going down waits for the answers it ends
// to save where they are (the supervisor kills it after 15s).
const turnStopWait = 6 * time.Second

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
			shareGoBuildCache() // before anything starts a child process

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
			// a proposal starting or ending its decision: cards and "Cần xử lý" show it at once
			deciding.OnChange(liveBus.Wrote)

			proxies, err := parsePrefixes(trustedProxies)
			if err != nil {
				return err
			}
			var cliTools *clitools.Manager
			if cliSetup {
				cliTools = a.cli
			}
			// chat answers cut off by the last shutdown go on (below); other jobs do not run again (ADR-040)
			interrupted, _ := a.store.Jobs().List(ctx, storage.JobFilter{Kind: "chat_turn", Status: "running", Limit: 200})
			if n, err := a.store.Jobs().FailRunning(ctx, "restart", "office khởi động lại khi job đang chạy", time.Now().UTC()); err == nil && n > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "office: %d job đang chạy dở được đánh dấu lỗi (khởi động lại)\n", n)
			}
			// and their tasks, so the tasks list is right and "Chạy lại" works
			_, _ = a.store.Tasks().FailRunning(ctx, "Office khởi động lại khi Việc đang chạy", time.Now().UTC())
			// and the workflows they were part of (their turns do not run again);
			// the chats that called them are told
			if n, err := a.workflows.EndInterrupted(ctx); err == nil && n > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "office: %d quy trình đang chạy dở được đánh dấu lỗi (khởi động lại)\n", n)
			}
			chatEngine := chat.NewEngine(a.store, a.providers, a.usage)
			chatEngine.SetAttachments(attach.Store{Dir: filepath.Join(h.Dir, "attachments")})
			// agents edit and check in their own git worktrees (ADR-037)
			trees := worktree.New(filepath.Join(h.Dir, "worktrees"))
			chatEngine.SetWorktrees(trees)
			// each is a copy of the project: idle ones go (made again when the
			// chat goes on), not only at start — an office up for weeks kept them all
			go func() {
				for t := time.NewTicker(6 * time.Hour); ; {
					chatEngine.SweepWorktrees(ctx, worktreeIdle)
					select {
					case <-ctx.Done():
						t.Stop()
						return
					case <-t.C:
					}
				}
			}()
			procs := ops.NewManager(a.store, filepath.Join(h.Dir, "logs"), a.cli.Env())
			defer procs.Detach() // project processes run on while office restarts
			// a status lives in memory: told like a table write
			procs.OnChange(func() { liveBus.Wrote("processes") })
			go procs.RunSampler(ctx, 3*time.Second)
			procs.Adopt() // those the last office left running
			procs.Autostart(ctx)
			// agents read build/run/monitoring data through the office tools (MCP for Claude Code)
			acts := actions.New(a.store, procs) // agents propose, people approve
			// agents' long-term notes (ADR-068); too long, a fast model compacts them
			mem := memory.New(a.store, compactNotes(a.store, chatEngine))
			mem.SetReviewer(reviewNotes(a.store, chatEngine))
			mem.SetLinkSaver(func(ctx context.Context, projectID, agentID string, o memory.Op) error { // ADR-218
				by := agentID
				if ag, err := a.store.Agents().Get(ctx, agentID); err == nil {
					by = ag.Name
				}
				return collections.New(a.store).SaveLink(ctx, projectID, collections.Link{Kind: o.Topic, Title: o.Summary, URL: o.Text, Note: o.ID}, "agent:"+by)
			})
			chatEngine.SetKeepFiles(func(ctx context.Context, projectID, convID string, files []attach.File) []string { // ADR-233
				svc := collections.New(a.store)
				var saved []string
				for _, f := range files {
					rel, err := svc.SaveFile(ctx, projectID, f.Path, f.Name, convID, actor.From(ctx))
					if err != nil {
						if !errors.Is(err, collections.ErrNoFolder) {
							slog.Warn("files: keep a sent file", "project", projectID, "file", f.Name, "err", err)
						}
						continue
					}
					saved = append(saved, rel)
				}
				return saved
			})
			chatEngine.SetOnAnswered(func(ctx context.Context, x chat.Answered) { // ADR-215
				if err := mem.Review(ctx, x.ProjectID, x.AgentID, x.Asked, x.Answer); err != nil {
					slog.Warn("memory: review", "agent", x.AgentID, "err", err)
				}
			})
			acts.SetMemory(mem)
			acts.SetSender(chatEngine) // send_to_chat: an agent writes to another chat in its person's name
			// linked projects in other offices (ADR-167)
			peers := peer.New(a.store, a.providers.Box())
			acts.SetPeers(peers)
			chatEngine.SetPeers(peers)
			chatEngine.SetActions(acts) // a workflow's gates: approval cards and check commands
			// a card approved while an earlier one waited runs in its turn, as approved (audited)
			chatEngine.SetQueueRunner(chatDecider{store: a.store, chat: chatEngine, acts: acts}.RunQueued)
			office := officetools.New(a.store, procs, acts)
			office.SetPeers(peers) // read_link/search_history in other offices
			// the notes mirrored into Cognee: recall finds them by meaning too (ADR-195)
			// (off until `office cognee up` or COGNEE_URL; read again each round)
			notes := cognee.Auto(a.store)
			go notes.Run(ctx, 2*time.Minute)
			// or embedded in SQLite by an AI connection, no service of its own (ADR-236);
			// it answers first when on (`office embed`; read again each round)
			vectors := embed.New(a.store, embed.Resolver(a.store, a.providers.APIKey))
			go vectors.Run(ctx, 2*time.Minute)
			office.SetRelated(func(ctx context.Context, projectID, query, skip string, n int) ([]string, error) {
				if vectors.On() {
					return vectors.Related(ctx, projectID, query, skip, n)
				}
				return notes.Related(ctx, projectID, query, skip, n)
			})
			// agents find skills in the office library and propose installing them (ADR-187)
			autoSvc := newAutomation(a, h)
			if autoSvc != nil {
				skills := agentskills.Installer{Store: a.store, Auto: autoSvc, Home: autoSvc.Home}
				office.SetSkills(skills)
				acts.SetSkills(skills)
			}
			// the office assistant creates, exports and imports projects (ADR-170)
			transfers := newTransfers(a)
			if h.ProjectRoot != "" {
				transfers.SetCloneRoot(filepath.Dir(h.ProjectRoot)) // as the dashboard clones
			}
			acts.SetProjects(transfers)
			office.SetProjects(transfers, h.Dir)
			mcp := mcpserver.New(office, version)
			chatEngine.SetOffice(office, mcp, "http://"+loopback(addr)+"/mcp")
			monitors := monitor.New(a.store, procs, chatEngine)
			// schedules and webhooks start chats as jobs (ADR-040)
			runner := trigger.New(a.store, officeExecutor{chat: chatEngine})
			go runner.Run(ctx)
			// stuck work (cards no one decides, loops, the same failure): the
			// coordinator looks into it while it happens (ADR-230)
			go heartbeat.New(a.store, officeExecutor{chat: chatEngine}).Run(ctx)
			// the office assistant: a hidden project whose chats span projects (ADR-046)
			if _, err := assistant.Ensure(ctx, a.store, filepath.Join(h.Dir, "assistant")); err != nil {
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
			// the support pack (ADR-171): its points do nothing where it is off
			supportPack := support.New(a.store)
			supportPack.SetRunner(runner)
			supportPack.SetBox(a.providers.Box()) // Crisp keys, SMTP passwords, hook secrets
			supportPack.SetBotSender(func(ctx context.Context, channelID, chatID, text string) error {
				return bots.NotifyConversation(ctx, channelID, chatID, text, "")
			})
			go supportPack.Run(ctx)            // replies and hooks out, SLAs, playbooks
			runner.AddOnDone(supportPack.Done) // a customer message's answer
			support.Who = api.Actor
			bots.SetTap(supportPack)
			office.AddProvider(supportPack)
			office.AddProvider(collections.NewTools(collections.New(a.store), acts)) // data_* (ADR-172); a delete is a card
			office.AddProvider(activity.NewTools(a.store))                           // project_report (ADR-210)
			chatEngine.SetOnEnded(func(ctx context.Context, x chat.Ended) {          // automations that follow it (ADR-243)
				runner.Emit(ctx, x.ProjectID, x.ID, trigger.EventPayload{Event: x.Event, From: x.From, FromName: x.FromName,
					ConversationID: x.ConversationID, Status: x.Status, Error: x.Error, Text: x.Text})
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
			a.usage.SetOnRecord(alerts.Run)                                            // prompts that keep failing: told in the same chat
			bots.SetDecider(chatDecider{store: a.store, chat: chatEngine, acts: acts}) // proposals decided from the chat (ADR-054)
			office.SetSendFile(bots.SendFileFor)                                       // an agent in a bot's chat posts images and files there (ADR-083)
			chatEngine.SetOnBotDecided(bots.DecidedOnDashboard)                        // a bot chat's card decided on the dashboard: the bot goes on (ADR-084)
			chatEngine.SetOnRunWait(bots.AnnounceRun)                                  // a workflow a bot's chat called waits on a person: the chat is told
			// an administrator's run decides others' cards (ADR-188)
			office.SetDecide(chatDecider{store: a.store, chat: chatEngine, acts: acts}.AdminDecide)
			// a project's agent running on its own, finding work (spec 2026-10-01-burn-design)
			burner := burn.New(a.store, chatEngine, trees)
			chatEngine.SetBoardHook(burner.OnBoard) // a task handed with delegate follows on the board (ADR-210)
			chatEngine.SetNextHook(burner.Next)     // a turn that ends takes on the agent's next task (ADR-221)
			chatEngine.SetBoardOpen(burner.Open)    // a hand-off waiting for its agent skips a task closed meanwhile
			office.SetBurn(func(ctx context.Context, sc officetools.Scope, name string, in officetools.BurnInput) (string, error) {
				return burner.Tool(ctx, sc, name, burn.ToolInput{Title: in.Title, Kind: in.Kind, Detail: in.Detail, Item: in.Item, Summary: in.Summary, Reason: in.Reason, What: in.What, Scanned: in.Scanned, Map: in.Map, Priority: in.Priority, Lessons: in.Lessons, Coverage: in.Coverage, Owner: in.Owner, From: in.From})
			})
			burner.SetNotify(bots.Notify) // a run's summary to the bot's chat its Burn names (ADR-120)
			// a script's @@quest lines feed the Burn, and may start it (ADR-132)
			runner.SetQuests(func(ctx context.Context, projectID string, qs []trigger.Quest, start bool, who string) (int, bool, error) {
				list := make([]burn.Quest, len(qs))
				for i, q := range qs {
					list[i] = burn.Quest{Title: q.Title, Detail: q.Detail}
				}
				return burner.AddQuests(ctx, projectID, list, start, who)
			})
			burner.Start(ctx)
			// questions to experts nobody answered in time fail, said in their chat (ADR-177)
			go func() {
				for t := time.NewTicker(5 * time.Minute); ; {
					chatEngine.SweepConsults(ctx)
					select {
					case <-ctx.Done():
						t.Stop()
						return
					case <-t.C:
					}
				}
			}()
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
			office.AddProvider(gatewayRest{st: a.store, gw: gateway}) // mcp_result: what office cut of a long result (ADR-183)
			office.SetMCPTools(gatewayDiscovery{st: a.store, gw: gateway})
			chatEngine.SetGateway(gatewayFor(a.store, gateway))
			handler := api.New(api.Config{
				Office: office, Channels: bots, Memory: mem, Events: liveBus,
				Packs: []api.PackRoutes{supportPack},
				Store: st, Auth: a.auth, AllowedOrigins: origins,
				SecureCookies: secureCookies, TrustedProxies: proxies, Logger: log, Version: version,
				Providers: a.providers, Team: a.team, Setup: setup.New(a.store, a.providers, a.team),
				Transfer:   transfers,
				Usage:      a.usage,
				CLITools:   cliTools,
				Chat:       chatEngine,
				Burn:       burner,
				Cleanup:    cleaner,
				Trigger:    runner,
				Automation: autoSvc,
				Workflows:  a.workflows,
				Peers:      peers,
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

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()
			// once it listens and the gateway is set (the turns reach their tools):
			// answers cut off by the last shutdown go on, then what waited in chats
			if n := chatEngine.ResumeInterrupted(ctx, interrupted); n > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "office: %d cuộc chat đang trả lời dở được làm tiếp (khởi động lại)\n", n)
			}
			chatEngine.SendAllQueued(ctx) // messages that waited in chats when office stopped
			fmt.Fprintf(os.Stderr, "office %s · chế độ %s · dữ liệu %s · api http://%s\n", version, h.Mode, h.Dir, addr)

			select {
			case err := <-errCh:
				if !errors.Is(err, http.ErrServerClosed) {
					return err
				}
			case <-restart:
				// swapped in a new build: stop cleanly and let the supervisor restart us
				chatEngine.Shutdown(turnStopWait) // before the server: a turn must not see its tools fail
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = srv.Shutdown(shutdownCtx)
				cancel()
				gateway.Close()
				procs.Detach()
				a.Close()
				os.Exit(selfupdate.RestartCode)
			case <-ctx.Done():
				chatEngine.Shutdown(turnStopWait)
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
	lib := automation.Library{Dir: filepath.Join(h.Dir, "library"), Trash: trash}
	if _, err := lib.SeedBuiltinSkills(); err != nil {
		fmt.Fprintf(os.Stderr, "office: không chép được skill có sẵn vào thư viện: %v\n", err)
	}
	return &automation.Service{
		Health:    &automation.MCPHealth{Home: userHome, Claude: claude},
		Home:      userHome,
		Library:   lib,
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

// workflowLibrary is the office's workflows (library/workflows/<key>.md).
func workflowLibrary(h home.Home) workflow.Library {
	return workflow.Library{Dir: filepath.Join(h.Dir, "library", "workflows")}
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

// shareGoBuildCache has the Go builds and tests office starts (agents,
// checks, scripts, processes) leave the folder out of the build cache's keys:
// otherwise every chat worktree caches its own copy of each package and test
// binary, and the cache grows by tens of GB. GOFLAGS set, even empty, is
// left as it is.
func shareGoBuildCache() {
	if _, set := os.LookupEnv("GOFLAGS"); !set {
		_ = os.Setenv("GOFLAGS", "-trimpath")
	}
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
