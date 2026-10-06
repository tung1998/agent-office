package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/config"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Thao tác với office.config.json"}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate config theo JSON Schema",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			fmt.Printf("✓ %s hợp lệ (project %s)\n", cfg.Path, cfg.Project.Name)
			return nil
		},
	})
	return cmd
}
