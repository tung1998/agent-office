package main

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/chat"
)

// hookCmd: what Claude Code calls back while an agent works (not for people).
func hookCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "hook", Hidden: true}
	var (
		userMCP bool
		gateway []string
		checks  string
	)
	guard := &cobra.Command{
		Use: "guard", Short: "PreToolUse: sửa file trong thư mục project, MCP theo quyền",
		RunE: func(*cobra.Command, []string) error {
			chat.Guard(os.Stdin, os.Stdout, userMCP, gateway)
			return nil
		},
	}
	guard.Flags().BoolVar(&userMCP, "user-mcp", false, "agent được dùng MCP của người dùng")
	guard.Flags().StringArrayVar(&gateway, "mcp", nil, "MCP của office mà lượt chạy được dùng (qua cổng)")
	check := &cobra.Command{
		Use: "check", Short: "PostToolUse: kiểm tra nhanh file vừa sửa",
		RunE: func(*cobra.Command, []string) error {
			chat.QuickCheck(os.Stdin, os.Stdout, chat.DecodeChecks(checks))
			return nil
		},
	}
	check.Flags().StringVar(&checks, "checks", "", "kiểm tra riêng của project (base64)")
	foreground := &cobra.Command{
		Use: "foreground", Short: "PreToolUse: không chạy nền (lượt chạy kết thúc là bị dừng)",
		RunE: func(*cobra.Command, []string) error {
			chat.Foreground(os.Stdin, os.Stdout)
			return nil
		},
	}
	foreground.Flags().StringVar(&chat.OfficeDir, "office-dir", "", "thư mục dữ liệu của office (DB, khóa): agent không đọc")
	cmd.AddCommand(guard, check, foreground)
	return cmd
}

// setGuardCommand points the chats' hooks at this binary.
func setGuardCommand() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	q := "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	chat.GuardCommand = q + " hook guard"
	chat.CheckCommand = q + " hook check"
	chat.ForegroundCommand = q + " hook foreground"
}
