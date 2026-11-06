package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/clitools"
)

// office tools update: brings the AI CLIs to their latest version the way
// each was installed; a weekly script automation runs it, and a failure
// (non-zero exit) calls its agent. While office runs it goes through it
// (ADR-259, clirequest.go): updated once no run uses the CLI, checked, put
// back when broken; with no office running it updates here at once.
func toolsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tools", Short: "Công cụ AI CLI trên máy (Claude Code, Codex, Gemini, Antigravity)"}
	var now bool
	update := &cobra.Command{
		Use:     "update [tool...]",
		Short:   "Cập nhật các CLI đã cài lên bản mới nhất (mặc định: tất cả)",
		Example: "  office tools update\n  office tools update claude codex\n  office tools update codex --now",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, taken, err := updateThroughOffice(cmd.Context(), args, now)
			if err != nil {
				return err
			}
			if taken {
				failed := 0
				for _, r := range res {
					if !r.OK() {
						failed++
					}
				}
				if failed > 0 {
					return fmt.Errorf("%d công cụ cập nhật lỗi", failed)
				}
				return nil
			}
			fmt.Println("office không chạy: cập nhật ngay tại đây")
			m := clitools.NewManager()
			ids := args
			if len(ids) == 0 {
				ids = m.IDs()
			}
			failed := 0
			for _, id := range ids {
				installed, before, err := m.Detect(cmd.Context(), id)
				if err != nil {
					return err
				}
				if !installed {
					if len(args) > 0 {
						fmt.Printf("%s: chưa cài\n", id)
						failed++
					}
					continue
				}
				j, err := m.Update(id)
				if errors.Is(err, clitools.ErrUnknownInstall) && len(args) == 0 {
					fmt.Printf("%s: bỏ qua, %v\n", id, err)
					continue
				}
				if err != nil {
					fmt.Printf("%s: %v\n", id, err)
					failed++
					continue
				}
				j.Wait()
				v := j.Snapshot()
				if v.State != "succeeded" {
					fmt.Printf("%s: lỗi khi chạy %s (%s)\n%s\n", id, v.Command, v.Error, v.Output)
					failed++
					continue
				}
				_, after, _ := m.Detect(cmd.Context(), id)
				if after == before {
					fmt.Printf("%s: đã mới nhất (%s)\n", id, after)
				} else {
					fmt.Printf("%s: %s → %s\n", id, before, after)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d công cụ cập nhật lỗi", failed)
			}
			return nil
		},
	}
	update.Flags().BoolVar(&now, "now", false, "cập nhật ngay, không chờ hết lượt đang dùng CLI (lượt mới vẫn chờ)")
	cmd.AddCommand(update)
	return cmd
}

// updateThroughOffice asks the running office to update tools and prints
// how each update goes; taken false: no office runs (or its store cannot be
// opened), nothing was done.
func updateThroughOffice(ctx context.Context, tools []string, now bool) (res []cliResult, taken bool, err error) {
	err = withApp(ctx, func(a *app) error {
		res, taken, err = requestCLIUpdate(ctx, a.store, tools, "office tools update", now, 30*time.Second, 3*time.Second, func(r cliResult) {
			fmt.Println(r.Text)
		})
		return err
	})
	if err != nil && !taken {
		fmt.Printf("không gửi được yêu cầu cho office (%v)\n", err)
		return nil, false, nil
	}
	return res, taken, err
}
