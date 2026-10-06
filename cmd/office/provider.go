package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

func providerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "provider", Short: "Quản lý kết nối AI (Claude, GPT, CLI)"}

	var name, kind, baseURL, keyEnv string
	var keyStdin bool
	add := &cobra.Command{
		Use:   "add",
		Short: "Thêm kết nối AI",
		Example: `  office provider add --kind claude_cli --name "Claude Code"
  echo "$ANTHROPIC_API_KEY" | office provider add --kind anthropic --name "Claude API" --api-key-stdin
  office provider add --kind openai --name GPT --api-key-env OPENAI_API_KEY
  office provider add --kind openai_compatible --name Ollama --base-url http://localhost:11434/v1`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := provider.Input{Name: name, Kind: storage.ProviderKind(kind), BaseURL: baseURL, APIKeyEnv: keyEnv}
			if keyStdin {
				k, err := readPassword(true, "API key")
				if err != nil {
					return err
				}
				in.APIKey = &k
			}
			return withApp(cmd.Context(), func(a *app) error {
				p, err := a.providers.Create(cmd.Context(), in)
				if err != nil {
					return err
				}
				fmt.Printf("✓ Đã thêm %s (%s)\n", p.Name, p.Kind)
				res, err := a.providers.Test(cmd.Context(), p.ID, "", "")
				if err != nil {
					return err
				}
				printTest(res)
				return nil
			})
		},
	}
	add.Flags().StringVar(&name, "name", "", "tên hiển thị")
	add.Flags().StringVar(&kind, "kind", "", "anthropic | openai | openai_compatible | claude_cli | codex_cli")
	add.Flags().StringVar(&baseURL, "base-url", "", "URL API, hoặc đường dẫn binary với CLI")
	add.Flags().StringVar(&keyEnv, "api-key-env", "", "đọc key từ biến môi trường này")
	add.Flags().BoolVar(&keyStdin, "api-key-stdin", false, "đọc API key từ stdin (lưu mã hóa)")
	add.MarkFlagRequired("name")
	add.MarkFlagRequired("kind")

	list := &cobra.Command{
		Use:   "list",
		Short: "Liệt kê kết nối",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				ps, err := a.store.Providers().List(cmd.Context())
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(w, "TÊN\tLOẠI\tMẶC ĐỊNH\tTRẠNG THÁI\tSTRONG / BALANCED / FAST")
				for _, p := range ps {
					def := ""
					if p.IsDefault {
						def = "✓"
					}
					tiers := strings.Join([]string{orDash(p.TierModels["strong"]), orDash(p.TierModels["balanced"]), orDash(p.TierModels["fast"])}, " / ")
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Kind, def, p.Status, tiers)
				}
				return w.Flush()
			})
		},
	}

	var prompt string
	test := &cobra.Command{
		Use:   "test <tên>",
		Short: "Kiểm tra kết nối (và gửi thử prompt với --prompt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				ps, err := a.store.Providers().List(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range ps {
					if strings.EqualFold(p.Name, args[0]) || p.ID == args[0] {
						res, err := a.providers.Test(cmd.Context(), p.ID, prompt, "")
						if err != nil {
							return err
						}
						printTest(res)
						return nil
					}
				}
				return fmt.Errorf("không có kết nối %q", args[0])
			})
		},
	}
	test.Flags().StringVar(&prompt, "prompt", "", "gửi thử prompt này (tốn token)")
	cmd.AddCommand(add, list, test)
	return cmd
}

func printTest(res provider.TestResult) {
	if !res.OK {
		fmt.Printf("✗ %s\n", res.Detail)
		return
	}
	fmt.Printf("✓ %s\n", res.Detail)
	if res.Response != nil {
		fmt.Printf("  › %s\n", strings.TrimSpace(res.Response.Text))
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
