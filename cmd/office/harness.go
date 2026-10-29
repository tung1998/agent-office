package main

import (
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/tung1998/agent-office/internal/harness"
	"github.com/tung1998/agent-office/internal/heartbeat"
	"github.com/tung1998/agent-office/internal/storage"
)

// office harness: the evidence for a project's harness review (ADR-181) as
// JSON, with no AI and no token; the better-harness workflow's first step.
func harnessCmd() *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:     "harness <project>",
		Short:   "Bằng chứng về vòng làm việc của agent trong project (JSON, cho quy trình better-harness)",
		Example: `  office harness shop --days 30`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				p, err := findProject(cmd, a, args[0])
				if err != nil {
					return err
				}
				ev, err := harness.Collect(cmd.Context(), a.store, p, days)
				if err != nil {
					return err
				}
				return printJSON(ev)
			})
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "số ngày gần nhất (1-90)")
	return cmd
}

// office heartbeat: the signs of stuck work the self-check would raise for a
// project at a time (ADR-230), with no AI and no turn opened.
func heartbeatCmd() *cobra.Command {
	var atFlag string
	cmd := &cobra.Command{
		Use:     "heartbeat <project>",
		Short:   "Dấu hiệu việc bị kẹt hay lặp mà vòng tự soát thấy trong project (JSON, không mở lượt)",
		Example: `  office heartbeat shop --at 2026-10-28T02:15:00Z`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now().UTC()
			if atFlag != "" {
				t, err := time.Parse(time.RFC3339, atFlag)
				if err != nil {
					return err
				}
				now = t.UTC()
			}
			return withApp(cmd.Context(), func(a *app) error {
				p, err := findProject(cmd, a, args[0])
				if err != nil {
					return err
				}
				f, err := a.store.Harness().Facts(cmd.Context(), p.ID, now.Add(-heartbeat.Window))
				if err != nil {
					return err
				}
				// facts after the moment asked for are not seen then
				f.Jobs = slices.DeleteFunc(f.Jobs, func(j storage.HarnessJob) bool { return j.CreatedAt.After(now) })
				f.Patches = slices.DeleteFunc(f.Patches, func(x storage.HarnessPatch) bool { return x.CreatedAt.After(now) })
				f.Actions = slices.DeleteFunc(f.Actions, func(x storage.HarnessAction) bool { return x.CreatedAt.After(now) })
				for i, x := range f.Actions {
					if x.Status != "pending" && x.DecidedAt.After(now) {
						f.Actions[i].Status = "pending" // decided later: it was still waiting then
					}
				}
				sigs := heartbeat.Detect(f, now)
				if sigs == nil {
					sigs = []heartbeat.Signal{}
				}
				return printJSON(sigs)
			})
		},
	}
	cmd.Flags().StringVar(&atFlag, "at", "", "thời điểm soát (RFC3339), mặc định bây giờ")
	return cmd
}
