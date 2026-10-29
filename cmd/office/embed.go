package main

import (
	"cmp"
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/embed"
)

// office embed: an AI connection embeds the agents' notes into SQLite, so
// recall finds them by meaning with no service of its own (ADR-236). A
// running office picks the change up within 2 minutes.
func embedCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "embed", Short: "Tìm ghi chú của agent theo nghĩa bằng embedding của một kết nối AI (không cần Docker)"}
	var model string
	use := &cobra.Command{
		Use:   "use <kết nối>",
		Short: "Dùng kết nối này (OpenAI hoặc tương thích OpenAI, vd Ollama) để embed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				return embed.Save(cmd.Context(), a.store, embed.Settings{Provider: args[0], Model: model})
			})
		},
	}
	use.Flags().StringVar(&model, "model", "", "model embedding (OpenAI mặc định "+embed.DefaultModel+"; kết nối tương thích phải ghi, vd nomic-embed-text)")
	cmd.AddCommand(use, &cobra.Command{
		Use:   "auto",
		Short: "Tự chọn: kết nối OpenAI đầu tiên có khóa",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error { return embed.Save(cmd.Context(), a.store, embed.Settings{}) })
		},
	}, &cobra.Command{
		Use:   "off",
		Short: "Tắt (recall chỉ còn từ khóa, hoặc Cognee nếu bật)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error { return embed.Save(cmd.Context(), a.store, embed.Settings{Off: true}) })
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Kết nối đang dùng và số ghi chú đã embed mỗi project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error { return embedStatus(cmd, a, false) })
		},
	}, &cobra.Command{
		Use:   "sync",
		Short: "Embed ngay ghi chú còn thiếu (office đang chạy tự làm 2 phút một lần)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error { return embedStatus(cmd, a, true) })
		},
	})
	return cmd
}

func embedStatus(cmd *cobra.Command, a *app, sync bool) error {
	ctx := cmd.Context()
	c := embed.Resolver(a.store, a.providers.APIKey)(ctx)
	if c == nil {
		s := embed.Load(ctx, a.store)
		fmt.Printf("tắt (cài đặt: tắt=%v, kết nối=%s): cần một kết nối OpenAI có khóa, `office embed use <kết nối> --model …`, hoặc %s\n", s.Off, cmp.Or(s.Provider, "tự chọn"), embed.EnvURL)
		return nil
	}
	fmt.Printf("bật: %s, model %s\n", c.URL, c.Model)
	ix := embed.New(a.store, func(context.Context) *embed.Client { return c })
	repos, err := a.store.Repos().List(ctx)
	if err != nil {
		return err
	}
	if sync {
		ix.All(ctx)
	}
	for _, r := range repos {
		fmt.Printf("- %s: %d ghi chú\n", r.Name, ix.Count(ctx, r.ID, c.Model))
	}
	return nil
}
