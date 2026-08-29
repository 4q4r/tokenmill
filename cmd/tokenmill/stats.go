package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var tui bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Alias to gain + TUI hint (like rtk stats --tui)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tui {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "TUI mode: polling `tokenmill gain -f json` every 30m. Ensure plugin installed:")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  tokenmill init -g --opencode")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "TUI component: ~/.config/opencode/tui-plugins/tokenmill-stats.tsx (SolidJS, graceful if not installed)")
			}
			// Alias to gain
			// Create gain command and execute it with same flags that don't conflict
			// For simplicity, delegate to newGainCmd with text output and all flag
			gain := newGainCmd()
			// Pass through args? If no args and not --tui, default gain text
			// We need to propagate --project, --format etc? For alias, just show text full
			gain.SetArgs([]string{"--all"})
			// Capture output to forward
			gain.SetOut(cmd.OutOrStdout())
			gain.SetErr(os.Stderr)
			if err := gain.Execute(); err != nil {
				return err
			}
			if tui {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nHint: TUI sidebar slot order 40, efficiency meter visible in OpenCode TUI if installed.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&tui, "tui", false, "show TUI hint and run gain")
	return cmd
}
