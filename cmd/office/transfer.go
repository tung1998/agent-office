package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/transfer"
)

func exportCmd() *cobra.Command {
	var file string
	var commit bool
	cmd := &cobra.Command{
		Use:   "export [thư-mục]",
		Short: "Xuất config (kết nối AI không kèm key, mô hình mẫu, project) ra thư mục JSON",
		Long: `Xuất cấu hình ra thư mục (mỗi mẫu, mỗi project một file) để review và đưa lên git.
Không xuất API key, tài khoản, phiên đăng nhập. Chạy lại sẽ cập nhật và xóa file thừa.`,
		Example: `  office export ./office-config
  office export ./office-config --commit     # tự git commit nếu thư mục nằm trong repo git
  office export --file office-bundle.json    # một file duy nhất`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				b, err := transfer.New(a.store, a.providers, a.org).Export(cmd.Context())
				if err != nil {
					return err
				}
				summary := fmt.Sprintf("%d kết nối, %d mẫu, %d project", len(b.Providers), len(b.Templates), len(b.Projects))
				if file != "" {
					now := time.Now().UTC()
					b.ExportedAt = &now
					raw, _ := json.MarshalIndent(b, "", "  ")
					if err := os.WriteFile(file, append(raw, '\n'), 0o644); err != nil {
						return err
					}
					fmt.Printf("✓ Đã xuất %s → %s\n", summary, file)
					return nil
				}
				dir := "office-config"
				if len(args) == 1 {
					dir = args[0]
				}
				if err := transfer.WriteDir(dir, b); err != nil {
					return err
				}
				fmt.Printf("✓ Đã xuất %s → %s\n", summary, dir)
				if commit {
					return gitCommit(dir)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "xuất ra một file JSON thay vì thư mục")
	cmd.Flags().BoolVar(&commit, "commit", false, "git add + commit thư mục sau khi xuất")
	return cmd
}

func gitCommit(dir string) error {
	run := func(args ...string) ([]byte, error) {
		return exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	}
	if _, err := run("rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("%s không nằm trong repo git", dir)
	}
	if out, err := run("add", "-A", "."); err != nil {
		return fmt.Errorf("git add: %s", out)
	}
	if err := exec.Command("git", "-C", dir, "diff", "--cached", "--quiet", "--", ".").Run(); err == nil {
		fmt.Println("• Không có thay đổi để commit")
		return nil
	}
	if out, err := run("commit", "-m", "office: cập nhật config export", "--", "."); err != nil {
		return fmt.Errorf("git commit: %s", out)
	}
	fmt.Println("✓ Đã git commit (chưa push)")
	return nil
}

func importCmd() *cobra.Command {
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "import <thư-mục|file.json>",
		Short: "Nhập config đã export (xem trước với --dry-run)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := transfer.ReadAny(args[0])
			if err != nil {
				return err
			}
			return withApp(cmd.Context(), func(a *app) error {
				svc := transfer.New(a.store, a.providers, a.org)
				plan, err := svc.Import(cmd.Context(), b, true)
				if err != nil {
					return err
				}
				printChanges(os.Stdout, plan.Changes)
				if dryRun {
					return nil
				}
				if !yes && (!interactive() || !confirm("Áp dụng các thay đổi trên?", false)) {
					fmt.Println("Đã hủy.")
					return nil
				}
				res, err := svc.Import(cmd.Context(), b, false)
				if err != nil {
					return err
				}
				n := 0
				for _, c := range res.Changes {
					if c.Op == "create" || c.Op == "update" {
						n++
					}
				}
				fmt.Printf("✓ Đã áp dụng %d thay đổi (bản cũ của mô hình được lưu trong lịch sử)\n", n)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "chỉ xem trước, không ghi")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "không hỏi xác nhận")
	return cmd
}

func printChanges(w io.Writer, changes []transfer.Change) {
	label := map[string]string{"create": "+ tạo", "update": "~ cập nhật", "unchanged": "  giữ nguyên", "skip": "! bỏ qua"}
	kind := map[string]string{"provider": "kết nối", "template": "mẫu", "project": "project"}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, c := range changes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", label[c.Op], kind[c.Kind], c.Name, c.Detail)
	}
	tw.Flush()
}

func backupCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Sao lưu toàn bộ dữ liệu office (database + khóa mã hóa)",
		Long: `Tạo bản sao nhất quán của database (chạy được khi server đang chạy) và khóa mã hóa API key.
Bản backup chứa key đã mã hóa và khóa giải mã: giữ ở nơi an toàn, không đưa lên git.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				dir := out
				if dir == "" {
					dir = filepath.Join(a.home.Dir, "backups", time.Now().Format("20060102-150405"))
				}
				if _, err := backupTo(cmd.Context(), a, dir); err != nil {
					return err
				}
				fmt.Printf("✓ Đã sao lưu vào %s\n", dir)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "thư mục đích (mặc định <dữ liệu>/backups/<thời-gian>)")
	return cmd
}

// backupTo copies the database (consistently) and the encryption key into dir.
func backupTo(ctx context.Context, a *app, dir string) (string, error) {
	b, ok := a.store.(interface {
		Backup(ctx context.Context, dest string) error
	})
	if !ok {
		return "", fmt.Errorf("storage hiện tại không hỗ trợ backup")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := b.Backup(ctx, filepath.Join(dir, "office.db")); err != nil {
		return "", err
	}
	if key, err := os.ReadFile(a.home.SecretKey()); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "secret.key"), key, 0o600); err != nil {
			return "", err
		}
	}
	note := "Khôi phục: dừng office, chép office.db và secret.key vào thư mục dữ liệu (" + a.home.Dir + "), rồi chạy lại.\n"
	_ = os.WriteFile(filepath.Join(dir, "RESTORE.txt"), []byte(note), 0o600)
	return dir, nil
}
