package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func templateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "template", Short: "Mô hình tổ chức mẫu"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Liệt kê mô hình mẫu",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				list, err := a.store.OrgModels().ListTemplates(cmd.Context())
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(w, "KEY\tTÊN\tLOẠI\tAGENT\tMÔ TẢ")
				for _, m := range list {
					agents, _ := a.store.Agents().List(cmd.Context(), m.ID)
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", m.Key, m.Name, m.Kind, len(agents), truncateRunes(m.Description, 60))
				}
				return w.Flush()
			})
		},
	})
	return cmd
}
