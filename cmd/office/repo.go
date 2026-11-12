package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/transfer"
)

func repoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"repo", "projects"},
		Short:   "Quản lý project (thư mục trên máy, hoặc helper toàn máy)",
	}

	var (
		name      string
		agents    []string
		workflows []string
	)
	add := &cobra.Command{
		Use:   "add [đường-dẫn]",
		Short: "Thêm project; bỏ trống đường dẫn để tạo helper cho toàn bộ máy",
		Example: `  office project add ~/code/shop --agent office/team-lead --agent office/frontend-dev --workflow review-pr
  office project add --name "Trợ lý máy"   # không gắn thư mục`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var info repos.Info
			if len(args) == 1 {
				var err error
				if info, err = repos.Detect(args[0]); err != nil {
					return err
				}
			} else if strings.TrimSpace(name) == "" {
				return errors.New("project không có thư mục cần --name")
			}
			if name != "" {
				info.Name = name
			}
			for _, id := range agents { // checked first: a wrong id leaves no project behind
				if _, err := team.LibraryAgentByID(id); err != nil {
					return err
				}
			}
			return withApp(cmd.Context(), func(a *app) error {
				r, err := a.store.Repos().Create(cmd.Context(), storage.Repo{Name: info.Name, Path: info.Path, GitRemote: info.GitRemote, Description: info.Description})
				if errors.Is(err, storage.ErrConflict) {
					return fmt.Errorf("thư mục %s đã là project", info.Path)
				}
				if err != nil {
					return err
				}
				fmt.Printf("✓ Đã thêm project %s · %s\n", r.Name, projectScope(r))
				ids := agents
				if len(ids) == 0 {
					ids = []string{team.StarterAgent}
				}
				if err := a.team.AddFromLibrary(cmd.Context(), r.ID, ids...); err != nil {
					return err
				}
				fmt.Printf("✓ Agent từ thư viện: %s\n", strings.Join(ids, ", "))
				if len(workflows) > 0 {
					if err := a.team.InstallWorkflows(cmd.Context(), r.ID, workflows); err != nil {
						return err
					}
					fmt.Printf("✓ Quy trình: %s\n", strings.Join(workflows, ", "))
				}
				return nil
			})
		},
	}
	add.Flags().StringArrayVar(&agents, "agent", nil, "agent từ thư viện theo id, vd office/devops (lặp lại được; mặc định office/assistant)")
	add.Flags().StringSliceVar(&workflows, "workflow", nil, "cài quy trình từ thư viện theo key, vd review-pr,fix-tests")
	add.Flags().StringVar(&name, "name", "", "tên project (bắt buộc khi không có đường dẫn)")

	list := &cobra.Command{
		Use:   "list",
		Short: "Liệt kê project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				rs, err := a.store.Repos().List(cmd.Context())
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(w, "TÊN\tMẶC ĐỊNH\tAGENT\tPHẠM VI")
				for _, r := range rs {
					agents, _ := a.store.Agents().List(cmd.Context(), r.ID)
					def := "—"
					if d, ok := storage.DefaultAgent(r, agents); ok {
						def = d.Name
					}
					fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", r.Name, def, len(agents), projectScope(r))
				}
				return w.Flush()
			})
		},
	}

	rm := &cobra.Command{
		Use:   "rm <đường-dẫn|tên>",
		Short: "Bỏ quản lý project (không xóa file)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				r, err := findProject(cmd, a, args[0])
				if err != nil {
					return err
				}
				if err := a.store.Repos().Delete(cmd.Context(), r.ID); err != nil {
					return err
				}
				fmt.Printf("✓ Đã bỏ quản lý %s\n", r.Name)
				return nil
			})
		},
	}
	var (
		out     string
		include []string
	)
	pack := &cobra.Command{
		Use:   "pack <đường-dẫn|tên>",
		Short: "Ghi gói project (JSON) để repo mang theo: clone về là có sẵn agent, quy trình, lịch, ghi nhớ",
		Long: `Ghi gói project dạng JSON (ADR-190). Mặc định ghi vào <thư mục project>/` + transfer.RepoPackFile + `, gồm config (agent, quy trình, tự động hóa theo lịch, quyền, bảng dữ liệu) và ghi nhớ.
Thêm một thư mục, hoặc clone một repo, có file này làm project (office init, Thêm project) là nhập gói.`,
		Example: `  office project pack ~/code/acme
  office project pack acme --include config --out /tmp/acme.pack.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				r, err := findProject(cmd, a, args[0])
				if err != nil {
					return err
				}
				file := out
				if file == "" {
					if r.Path == "" {
						return errors.New("project không có thư mục: cần --out")
					}
					file = filepath.Join(r.Path, transfer.RepoPackFile)
				}
				p, err := newTransfers(a).ExportPack(cmd.Context(), r.ID, include, nil)
				if err != nil {
					return err
				}
				raw, err := json.MarshalIndent(p, "", "  ")
				if err != nil {
					return err
				}
				if err := os.WriteFile(file, append(raw, '\n'), 0o644); err != nil {
					return err
				}
				burn := ""
				if p.Burn != nil {
					burn = fmt.Sprintf(", Burn (%d hồ sơ review)", len(p.Burn.ReviewProfiles))
				}
				fmt.Printf("✓ Đã ghi gói %s (%s): %d tự động hóa, %d bảng dữ liệu, %d ghi nhớ%s\n", file, strings.Join(p.Include, ", "),
					len(p.Automations), len(p.Collections), len(p.Memories), burn)
				return nil
			})
		},
	}
	pack.Flags().StringVar(&out, "out", "", "file đích (mặc định <thư mục project>/"+transfer.RepoPackFile+")")
	pack.Flags().StringSliceVar(&include, "include", []string{transfer.IncludeConfig, transfer.IncludeMemory}, "phần mang theo: config, memory, chats")
	var force bool
	move := &cobra.Command{
		Use:   "move <đường-dẫn|tên> <thư-mục-mới>",
		Short: "Chuyển project sang thư mục khác (giữ agent, chat, lịch sử)",
		Long: `Đổi thư mục của project, vd. khi có hai bản clone của cùng repo trên máy.
Agent, chat, quy trình, ghi nhớ vẫn giữ. Thư mục mới phải cùng git remote với project (--force để bỏ qua).
Worktree của chat tạo từ thư mục cũ vẫn trỏ về bản cũ: xoá chúng (khi không còn việc dở) để lượt sau tạo lại từ thư mục mới.`,
		Example: `  office project move agent-office ~/Desktop/ai-manager-agents`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := repos.Detect(args[1])
			if err != nil {
				return fmt.Errorf("thư mục mới: %w", err)
			}
			return withApp(cmd.Context(), func(a *app) error {
				r, err := findProject(cmd, a, args[0])
				if err != nil {
					return err
				}
				if r.Path == info.Path {
					fmt.Printf("Project %s đã ở %s\n", r.Name, info.Path)
					return nil
				}
				if err := sameRemote(r.GitRemote, info.GitRemote); err != nil && !force {
					return err
				}
				remote := info.GitRemote
				if remote == "" {
					remote = r.GitRemote
				}
				if err := a.store.Repos().SetPath(cmd.Context(), r.ID, info.Path, remote); err != nil {
					if errors.Is(err, storage.ErrConflict) {
						return fmt.Errorf("thư mục %s đã là của project khác", info.Path)
					}
					return err
				}
				fmt.Printf("✓ Đã chuyển %s: %s → %s\n", r.Name, projectScope(r), info.Path)
				if wts := oldWorktrees(r.Path); len(wts) > 0 {
					fmt.Printf("! %d worktree chat còn gắn với thư mục cũ (xoá khi không còn việc dở để tạo lại từ thư mục mới):\n", len(wts))
					for _, w := range wts {
						fmt.Println("  " + w)
					}
				}
				return nil
			})
		},
	}
	move.Flags().BoolVar(&force, "force", false, "cho chuyển dù git remote khác nhau")
	cmd.AddCommand(add, list, rm, pack, move)
	return cmd
}

// sameRemote refuses moving a project onto a clone of a different repo; a
// side with no remote is not a mismatch.
func sameRemote(old, new string) error {
	norm := func(s string) string {
		s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "/"), ".git")
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
		}
		if i := strings.LastIndex(s, "@"); i >= 0 {
			s = s[i+1:]
		}
		return strings.ToLower(strings.Replace(s, ":", "/", 1))
	}
	if old == "" || new == "" || norm(old) == norm(new) {
		return nil
	}
	return fmt.Errorf("git remote khác nhau (project: %s, thư mục mới: %s); dùng --force nếu chắc chắn", old, new)
}

// oldWorktrees lists the linked worktrees of dir (not dir itself).
func oldWorktrees(dir string) []string {
	if dir == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var wts []string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && filepath.Clean(p) != filepath.Clean(dir) {
			wts = append(wts, p)
		}
	}
	return wts
}

// findProject matches a folder path first, then an exact project name.
func findProject(cmd *cobra.Command, a *app, ref string) (storage.Repo, error) {
	if info, err := repos.Detect(ref); err == nil {
		if r, err := a.store.Repos().GetByPath(cmd.Context(), info.Path); err == nil {
			return r, nil
		}
	}
	rs, err := a.store.Repos().List(cmd.Context())
	if err != nil {
		return storage.Repo{}, err
	}
	for _, r := range rs {
		if strings.EqualFold(r.Name, ref) || r.ID == ref {
			return r, nil
		}
	}
	return storage.Repo{}, fmt.Errorf("không có project %q", ref)
}

func projectScope(r storage.Repo) string {
	if r.Path == "" {
		return "toàn máy (không gắn thư mục)"
	}
	return r.Path
}
