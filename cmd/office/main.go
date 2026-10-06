// Command office is the agent-office CLI and server.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/config"
	"github.com/tung1998/agent-office/internal/home"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/usage"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

var (
	configPath string
	homeFlag   string
)

func main() {
	root := &cobra.Command{
		Use:           "office",
		Short:         "agent-office: phòng ban AI agent giám sát hệ thống",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "office.config.json", "đường dẫn office.config.json")
	root.PersistentFlags().StringVar(&homeFlag, "home", "", "thư mục dữ liệu (mặc định: .office của project gần nhất, hoặc ~/.agent-office)")
	root.AddCommand(runCmd(), serveCmd(), initCmd(), userCmd(), repoCmd(), providerCmd(), templateCmd(), exportCmd(), importCmd(), backupCmd(), configCmd(), hookCmd(), serviceCmd())
	setGuardCommand()

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "lỗi:", err)
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// loadConfig returns the config file, or defaults when it does not exist yet.
func loadConfig() (config.Config, error) {
	cfg, err := config.Load(configPath)
	if errors.Is(err, config.ErrNotFound) {
		return config.Defaults(), nil
	}
	return cfg, err
}

// resolveHome picks the data directory for this invocation.
func resolveHome() (home.Home, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return home.Home{}, err
	}
	return home.Resolve(homeFlag, cwd)
}

// app bundles what most commands need.
type app struct {
	home      home.Home
	store     storage.Store
	auth      *auth.Service
	providers *provider.Service
	org       *orgmodel.Service
	usage     *usage.Service
	cli       *clitools.Manager
}

func (a *app) Close() { a.store.Close() }

// openApp opens the store in h (migrated, built-in templates seeded).
func openApp(ctx context.Context, h home.Home) (*app, error) {
	st, err := sqlite.Open(h.DB())
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	box, err := secrets.Load(h.SecretKey())
	if err != nil {
		st.Close()
		return nil, err
	}
	org := orgmodel.NewService(st)
	if _, err := org.SeedBuiltins(ctx); err != nil {
		st.Close()
		return nil, err
	}
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.Local)
	provs.SetUsage(u)
	// OFFICE_CLI_PATH pins where Claude Code / Codex are looked up.
	cli := clitools.NewManager()
	if p := os.Getenv("OFFICE_CLI_PATH"); p != "" {
		cli = clitools.NewManagerWithPath(p)
	}
	provs.SetBinResolver(cli.LookPath)
	return &app{home: h, store: st, auth: auth.NewService(st, auth.Options{}), providers: provs, org: org, usage: u, cli: cli}, nil
}

// withApp resolves the home and runs fn with an open app.
func withApp(ctx context.Context, fn func(*app) error) error {
	h, err := resolveHome()
	if err != nil {
		return err
	}
	a, err := openApp(ctx, h)
	if err != nil {
		return err
	}
	defer a.Close()
	return fn(a)
}
