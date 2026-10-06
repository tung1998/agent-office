package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
)

const cliActor = "cli"

func userCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Quản lý tài khoản đăng nhập dashboard"}
	cmd.AddCommand(userCreateCmd(), userListCmd(), userPasswdCmd(), userDisableCmd(true), userDisableCmd(false))
	return cmd
}

// withAuth runs fn with the store and auth service of the resolved home.
func withAuth(ctx context.Context, fn func(storage.Store, *auth.Service) error) error {
	return withApp(ctx, func(a *app) error { return fn(a.store, a.auth) })
}

// userCreateCmd makes the first real account. The office starts with the
// default admin (admin / admin); this replaces it, which is how a box without a
// browser on it (Docker, a server) gets its admin. More accounts are made on
// the dashboard (Administration → Accounts), or here with --force.
func userCreateCmd() *cobra.Command {
	var email, name, role string
	var fromStdin, force bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Tạo tài khoản đầu tiên (thay tài khoản mặc định admin / admin)",
		Example: `  office user create --email admin@company.com --name "Admin" --role admin
  echo "$PW" | office user create --email bot@company.com --password-stdin --force`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAuth(cmd.Context(), func(st storage.Store, svc *auth.Service) error {
				ctx := cmd.Context()
				users, err := st.Users().List(ctx)
				if err != nil {
					return err
				}
				def, pending := svc.PendingDefault(ctx)
				onlyDefault := pending && len(users) == 1
				if len(users) > 0 && !onlyDefault && !force {
					return errors.New("office đã có tài khoản: tạo thêm trên dashboard (Quản trị → Tài khoản), hoặc thêm --force để tạo bằng lệnh")
				}
				pw, err := readPassword(fromStdin, "Mật khẩu")
				if err != nil {
					return err
				}
				if len(users) == 0 || (onlyDefault && !force) {
					role = string(storage.RoleAdmin) // the first account, or the default admin's place
				}
				u, err := svc.CreateUser(ctx, auth.NewUser{Email: email, Name: name, Role: storage.Role(role), Password: pw}, cliActor)
				if err != nil {
					return err
				}
				fmt.Printf("✓ Đã tạo %s (%s, %s)\n", u.Email, u.Role, u.ID)
				if onlyDefault && u.Role == storage.RoleAdmin {
					if err := st.Users().Delete(ctx, def.ID); err != nil {
						return fmt.Errorf("đã tạo %s nhưng chưa gỡ được tài khoản mặc định: %w", u.Email, err)
					}
					fmt.Printf("✓ Đã gỡ tài khoản mặc định %s\n", auth.DefaultAdminEmail)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email đăng nhập")
	cmd.Flags().StringVar(&name, "name", "", "tên hiển thị")
	cmd.Flags().StringVar(&role, "role", string(storage.RoleMember), "admin | member (tài khoản đầu tiên luôn là admin)")
	cmd.Flags().BoolVar(&fromStdin, "password-stdin", false, "đọc mật khẩu từ stdin")
	cmd.Flags().BoolVar(&force, "force", false, "vẫn tạo khi office đã có tài khoản")
	cmd.MarkFlagRequired("email")
	return cmd
}

func userListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Liệt kê tài khoản",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAuth(cmd.Context(), func(st storage.Store, _ *auth.Service) error {
				users, err := st.Users().List(cmd.Context())
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(w, "EMAIL\tTÊN\tROLE\tTRẠNG THÁI\tĐĂNG NHẬP GẦN NHẤT")
				for _, u := range users {
					status, last := "active", "-"
					if u.Disabled {
						status = "disabled"
					}
					if u.LastLoginAt != nil {
						last = u.LastLoginAt.Local().Format(time.DateTime)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", u.Email, u.Name, u.Role, status, last)
				}
				return w.Flush()
			})
		},
	}
}

func userPasswdCmd() *cobra.Command {
	var email string
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "passwd",
		Short: "Đặt lại mật khẩu (thu hồi mọi phiên đăng nhập của user)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw, err := readPassword(fromStdin, "Mật khẩu mới")
			if err != nil {
				return err
			}
			return withAuth(cmd.Context(), func(st storage.Store, svc *auth.Service) error {
				u, err := st.Users().GetByEmail(cmd.Context(), auth.NormalizeEmail(email))
				if errors.Is(err, storage.ErrNotFound) {
					return fmt.Errorf("không có tài khoản %s", email)
				}
				if err != nil {
					return err
				}
				if err := svc.ResetPassword(cmd.Context(), u.ID, pw, cliActor); err != nil {
					return err
				}
				fmt.Printf("✓ Đã đặt lại mật khẩu cho %s\n", u.Email)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email tài khoản")
	cmd.Flags().BoolVar(&fromStdin, "password-stdin", false, "đọc mật khẩu từ stdin")
	cmd.MarkFlagRequired("email")
	return cmd
}

func userDisableCmd(disable bool) *cobra.Command {
	use, short := "enable", "Mở lại tài khoản"
	if disable {
		use, short = "disable", "Vô hiệu hóa tài khoản (thu hồi mọi phiên)"
	}
	var email string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAuth(cmd.Context(), func(st storage.Store, svc *auth.Service) error {
				u, err := st.Users().GetByEmail(cmd.Context(), auth.NormalizeEmail(email))
				if errors.Is(err, storage.ErrNotFound) {
					return fmt.Errorf("không có tài khoản %s", email)
				}
				if err != nil {
					return err
				}
				if err := svc.SetDisabled(cmd.Context(), u.ID, disable, cliActor); err != nil {
					return err
				}
				fmt.Printf("✓ %s: %s\n", u.Email, use+"d")
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email tài khoản")
	cmd.MarkFlagRequired("email")
	return cmd
}

// readPassword reads from stdin (one line) or prompts twice on a terminal.
func readPassword(fromStdin bool, label string) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprintf(os.Stderr, "%s (tối thiểu %d ký tự): ", label, auth.MinPasswordLen)
	a, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Nhập lại: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errPasswordMismatch
	}
	return string(a), nil
}

var errPasswordMismatch = errors.New("hai lần nhập không khớp")
