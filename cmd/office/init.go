package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/home"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/transfer"
	"github.com/tung1998/agent-office/templates/packs"
)

func initCmd() *cobra.Command {
	var (
		local  bool
		agents []string
		yes    bool
		from   string
		tmpl   string
	)
	cmd := &cobra.Command{
		Use:   "init [đường-dẫn]",
		Short: "Thêm thư mục làm project, với agent lấy từ thư viện",
		Long: `Thêm một thư mục làm project để office quản lý. Project mới có agent Trợ lý; thêm agent khác từ thư viện bằng --agent, hoặc để AI chọn ở trang Thiết lập của dashboard.

Hai chế độ cài đặt:
  --local   dữ liệu nằm trong <project>/.office, office chỉ quản lý project này
  mặc định  dữ liệu ở ~/.agent-office (hoặc .office của project gần nhất), quản lý nhiều project ở bất kỳ đâu`,
		Example: `  office init                       # thư mục hiện tại, agent Trợ lý
  office init --local
  office init ~/code/shop --agent office/team-lead --agent office/frontend-dev
  office init ~/code/acme                   # thư mục có ` + transfer.RepoPackFile + `: nhập gói (agent, quy trình, lịch, ghi nhớ)
  office init ~/code/acme --from ~/packs/company.pack.json
  office init ~/code/acme --template @agentoffice/startup        # công ty mới từ thư viện mẫu (ADR-199)
  office init ~/code/shop --template @agentoffice/startup/product`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			if tmpl != "" {
				if err := writeTemplate(ctx, tmpl, path); err != nil {
					return err
				}
			}
			info, err := repos.Detect(path)
			if err != nil {
				return fmt.Errorf("không đọc được thư mục %s: %w", path, err)
			}
			// read first: a bad pack leaves nothing behind (ADR-190)
			packFile, pack := filepath.Join(info.Path, transfer.RepoPackFile), (*transfer.Pack)(nil)
			if from != "" {
				p, err := transfer.ReadPackFile(from)
				if err != nil {
					return err
				}
				packFile, pack = from, &p
			} else if pack, err = transfer.RepoPack(info.Path); err != nil {
				return err
			}
			for _, id := range agents { // checked first: a wrong id leaves nothing behind
				if _, err := team.LibraryAgentByID(id); err != nil {
					return err
				}
			}

			var h home.Home
			if local {
				h, err = home.LocalHome(info.Path)
			} else {
				h, err = resolveHome()
			}
			if err != nil {
				return err
			}
			a, err := openApp(ctx, h)
			if err != nil {
				return err
			}
			defer a.Close()
			fmt.Fprintf(os.Stderr, "Dữ liệu office: %s (chế độ %s)\n", h.Dir, h.Mode)
			if h.Mode == home.Local {
				ensureGitignore(h.ProjectRoot)
			}

			repo, err := a.store.Repos().GetByPath(ctx, info.Path)
			created := errors.Is(err, storage.ErrNotFound)
			if created {
				repo, err = a.store.Repos().Create(ctx, storage.Repo{Name: info.Name, Path: info.Path, GitRemote: info.GitRemote, Description: info.Description})
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "✓ Đã thêm project %s (%s)\n", repo.Name, repo.Path)
			} else if err != nil {
				return err
			} else {
				fmt.Fprintf(os.Stderr, "• Project %s đã có\n", repo.Name)
			}

			current, err := a.store.Agents().List(ctx, repo.ID)
			if err != nil {
				return err
			}
			switch {
			case pack != nil && len(current) > 0 && from != "":
				return fmt.Errorf("project %s đã có agent: gói chỉ nhập vào project mới", repo.Name)
			case pack != nil && len(current) > 0:
				fmt.Fprintf(os.Stderr, "• Project đã có agent, bỏ qua %s\n", transfer.RepoPackFile)
			case pack != nil:
				res, err := newTransfers(a).ImportPack(ctx, *pack, repo, pack.Include)
				if err != nil {
					if created { // nothing half made stays behind
						_ = a.store.Repos().Delete(ctx, repo.ID)
					}
					return err
				}
				fmt.Fprintf(os.Stderr, "✓ Đã nhập gói %s (%s)\n", packFile, strings.Join(pack.Include, ", "))
				printChanges(os.Stderr, res.Changes)
				if current, err = a.store.Agents().List(ctx, repo.ID); err != nil {
					return err
				}
			}
			ids := agents
			if len(ids) == 0 && len(current) == 0 {
				ids = []string{team.StarterAgent}
			}
			if len(ids) > 0 {
				if err := a.team.AddFromLibrary(ctx, repo.ID, ids...); err != nil {
					return err
				}
				added, _ := a.store.Agents().List(ctx, repo.ID)
				fmt.Fprintf(os.Stderr, "✓ Project có %d agent\n", len(added))
				for _, ag := range added {
					fmt.Fprintf(os.Stderr, "    %-18s %s\n", ag.Key, ag.Role)
				}
			} else {
				fmt.Fprintf(os.Stderr, "• Project đã có %d agent\n", len(current))
			}

			if err := detectProviders(cmd, a, yes); err != nil {
				return err
			}

			if err := setupFirstAdmin(cmd, a, yes); err != nil {
				return err
			}
			_, pending := a.auth.PendingDefault(ctx)
			fmt.Fprintln(os.Stderr, "\nTiếp theo:")
			fmt.Fprintf(os.Stderr, "  1. %s run            # mở API cho dashboard\n", homeHint(h))
			if pending {
				fmt.Fprintf(os.Stderr, "  2. Mở dashboard, đăng nhập %s / %s rồi đặt email và mật khẩu của bạn\n", auth.DefaultAdminEmail, auth.DefaultAdminPassword)
			} else {
				fmt.Fprintln(os.Stderr, "  2. Mở dashboard và đăng nhập")
			}
			fmt.Fprintln(os.Stderr, "  3. Vào dashboard → Kết nối AI / Project / Agent / Quy trình để chỉnh chi tiết")
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "lưu dữ liệu trong <project>/.office (chỉ quản lý project này)")
	cmd.Flags().StringArrayVar(&agents, "agent", nil, "thêm agent từ thư viện theo id, vd office/devops (lặp lại được)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "tự đồng ý các bước (không hỏi)")
	cmd.Flags().StringVar(&tmpl, "template", "", "mẫu trong thư viện của office (@agentoffice/<bộ>[/<mẫu>], xem office packs): ghi vào thư mục (tạo thư mục và git repo nếu chưa có) rồi nhập gói của nó (ADR-199)")
	cmd.Flags().StringVar(&from, "from", "", "nhập gói project (.zip hoặc JSON) vào project mới; mặc định "+transfer.RepoPackFile+" trong thư mục nếu có")
	return cmd
}

// detectProviders offers to create connections for CLIs and API keys found on this machine.
func detectProviders(cmd *cobra.Command, a *app, yes bool) error {
	ctx := cmd.Context()
	existing, err := a.store.Providers().List(ctx)
	if err != nil {
		return err
	}
	have := map[storage.ProviderKind]bool{}
	for _, p := range existing {
		have[p.Kind] = true
	}
	type cand struct {
		label string
		in    provider.Input
	}
	var cands []cand
	if _, err := exec.LookPath("claude"); err == nil && !have[storage.ProviderClaudeCLI] {
		cands = append(cands, cand{"Claude Code CLI (lệnh claude trên máy)", provider.Input{Name: "Claude Code CLI", Kind: storage.ProviderClaudeCLI}})
	}
	if _, err := exec.LookPath("codex"); err == nil && !have[storage.ProviderCodexCLI] {
		cands = append(cands, cand{"Codex CLI (lệnh codex trên máy)", provider.Input{Name: "Codex CLI", Kind: storage.ProviderCodexCLI}})
	}
	if _, err := exec.LookPath("gemini"); err == nil && !have[storage.ProviderGeminiCLI] {
		cands = append(cands, cand{"Gemini CLI (lệnh gemini trên máy)", provider.Input{Name: "Gemini CLI", Kind: storage.ProviderGeminiCLI}})
	}
	if _, err := exec.LookPath("agy"); err == nil && !have[storage.ProviderAntigravityCLI] {
		cands = append(cands, cand{"Antigravity CLI (lệnh agy trên máy)", provider.Input{Name: "Antigravity CLI", Kind: storage.ProviderAntigravityCLI}})
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" && !have[storage.ProviderAnthropic] {
		cands = append(cands, cand{"Claude API (đọc ANTHROPIC_API_KEY)", provider.Input{Name: "Claude API", Kind: storage.ProviderAnthropic, APIKeyEnv: "ANTHROPIC_API_KEY"}})
	}
	if os.Getenv("OPENAI_API_KEY") != "" && !have[storage.ProviderOpenAI] {
		cands = append(cands, cand{"OpenAI API (đọc OPENAI_API_KEY)", provider.Input{Name: "OpenAI API", Kind: storage.ProviderOpenAI, APIKeyEnv: "OPENAI_API_KEY"}})
	}
	if len(cands) == 0 {
		if len(existing) == 0 {
			fmt.Fprintln(os.Stderr, "• Chưa có kết nối AI. Thêm trong dashboard hoặc: office provider add --kind anthropic --name \"Claude API\"")
		}
		return nil
	}
	for _, c := range cands {
		if !yes && (!interactive() || !confirm("Phát hiện "+c.label+". Thêm kết nối?", true)) {
			continue
		}
		p, err := a.providers.Create(ctx, c.in)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s: %v\n", c.label, err)
			continue
		}
		res, _ := a.providers.Test(ctx, p.ID, "", "")
		status := "✓"
		if !res.OK {
			status = "!"
		}
		fmt.Fprintf(os.Stderr, "%s Kết nối %s: %s\n", status, p.Name, res.Detail)
	}
	return nil
}

// setupFirstAdmin makes the first account when the office has none: the email
// and password typed here, or on Enter (and without a terminal) the default
// admin / admin, which must set its own on the first login.
func setupFirstAdmin(cmd *cobra.Command, a *app, yes bool) error {
	ctx := cmd.Context()
	n, err := a.store.Users().Count(ctx)
	if err != nil || n > 0 {
		return err
	}
	if !yes && interactive() {
		fmt.Fprintf(os.Stderr, "\nTài khoản admin — email (Enter = %s / %s, đổi khi đăng nhập lần đầu): ", auth.DefaultAdminEmail, auth.DefaultAdminPassword)
		line, _ := stdin.ReadString('\n')
		if email := strings.TrimSpace(line); email != "" {
			for {
				pw, err := readPassword(false, "Mật khẩu")
				if err == nil {
					u, cerr := a.auth.CreateUser(ctx, auth.NewUser{Email: email, Name: "Admin", Role: storage.RoleAdmin, Password: pw}, cliActor)
					if cerr == nil {
						fmt.Fprintf(os.Stderr, "✓ Tạo tài khoản admin %s\n", u.Email)
						return nil
					}
					err = cerr
				}
				if errors.Is(err, auth.ErrInvalidEmail) {
					return fmt.Errorf("email %q không hợp lệ (chạy lại, hoặc Enter để dùng %s / %s)", email, auth.DefaultAdminEmail, auth.DefaultAdminPassword)
				}
				if !errors.Is(err, auth.ErrWeakPassword) && !errors.Is(err, errPasswordMismatch) {
					return err
				}
				fmt.Fprintln(os.Stderr, "  !", err)
			}
		}
	}
	if created, err := a.auth.EnsureDefaultAdmin(ctx); err != nil {
		return err
	} else if created {
		fmt.Fprintf(os.Stderr, "✓ Tạo tài khoản mặc định %s / %s (đổi email và mật khẩu khi đăng nhập lần đầu)\n", auth.DefaultAdminEmail, auth.DefaultAdminPassword)
	}
	return nil
}

func homeHint(h home.Home) string {
	if h.Mode == home.Local {
		return "office"
	}
	return "office"
}

// ensureGitignore keeps the local office data (DB, secret key) out of git.
func ensureGitignore(root string) {
	p := filepath.Join(root, ".gitignore")
	raw, _ := os.ReadFile(p)
	for _, line := range splitLines(string(raw)) {
		if line == ".office/" || line == ".office" || line == "/.office" || line == "/.office/" {
			return
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ không thêm được .office/ vào .gitignore:", err)
		return
	}
	prefix := ""
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		prefix = "\n"
	}
	_, writeErr := fmt.Fprintf(f, "%s# agent-office local data (database, secret key)\n.office/\n", prefix)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		fmt.Fprintln(os.Stderr, "✗ không thêm được .office/ vào .gitignore:", firstNonNil(writeErr, closeErr))
		return
	}
	fmt.Fprintln(os.Stderr, "✓ Thêm .office/ vào .gitignore")
}

func firstNonNil(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// writeTemplate puts library pack ref into path (ADR-199): a folder that is
// not there yet becomes a new git repo with the pack committed; an existing
// one gets the files, left for the person to commit.
func writeTemplate(ctx context.Context, ref, path string) error {
	fresh := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		fresh = true
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		if out, err := exec.CommandContext(ctx, "git", "-C", path, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %s", strings.TrimSpace(string(out)))
		}
		fresh = true
	}
	files, err := packs.Write(ctx, ref, path, fresh)
	if err != nil {
		return err
	}
	fmt.Printf("✓ Đã ghi mẫu %s (%d file) vào %s", ref, len(files), path)
	if !fresh {
		fmt.Print(": chưa commit, xem rồi commit vào repo của bạn")
	}
	fmt.Println()
	return nil
}
