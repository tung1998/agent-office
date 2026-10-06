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
	cmd.AddCommand(guard)
	return cmd
}

// setGuardCommand points the chats' hook at this binary.
func setGuardCommand() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	chat.GuardCommand = "'" + strings.ReplaceAll(exe, "'", `'\''`) + "' hook guard"
}
