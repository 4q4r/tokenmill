package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tokenmill/tokenmill/internal/stats"
)

func newGainCmd() *cobra.Command {
	var (
		format  string
		allFlag bool
		weekly  bool
		monthly bool
		daily   bool
		project bool
		history bool
		limit   int
		quota   bool
		graph   bool
		tier    string
	)

	cmd := &cobra.Command{
		Use:   "gain",
		Short: "Show token savings summary and history (like rtk gain)",
		Long:  `Reads ~/.local/share/tokenmill/tracking.db and shows summary + daily/weekly/monthly breakdowns.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// quota placeholder
			if quota {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Quota: not configured (placeholder — like rtk --quota)")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Tier:", tier)
				return nil
			}
			// Determine DB path: same as stats default
			store, err := stats.New("")
			if err != nil {
				return fmt.Errorf("open stats db: %w", err)
			}
			defer func() { _ = store.Close() }()

			// Project filtering: determine project_path for filtering (now persisted via SQLite column project_path)
			var projectPath string
			if project {
				if wd, err := os.Getwd(); err == nil {
					projectPath = filepath.Clean(wd)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: --project filter: cwd=%s (filtering by project_path)\n", projectPath)
				}
			}

			// Determine output format early for history handling
			f := strings.ToLower(format)
			if f == "" {
				f = "text"
			}
			if f != "json" && f != "csv" && f != "text" {
				return fmt.Errorf("invalid format %q, expected json|csv|text", format)
			}

			// History mode for json/csv remains early-return; for text we integrate into summary view like rtk
			if history {
				switch f {
				case "json":
					if limit <= 0 {
						limit = 20
					}
					var records []stats.CommandRecord
					if projectPath != "" {
						records, err = store.GetRecentForProject(limit, projectPath)
					} else {
						records, err = store.GetRecent(limit)
					}
					if err != nil {
						return fmt.Errorf("get recent: %w", err)
					}
					b, _ := json.MarshalIndent(records, "", "  ")
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
					return nil
				case "csv":
					if limit <= 0 {
						limit = 20
					}
					var records []stats.CommandRecord
					if projectPath != "" {
						records, err = store.GetRecentForProject(limit, projectPath)
					} else {
						records, err = store.GetRecent(limit)
					}
					if err != nil {
						return fmt.Errorf("get recent: %w", err)
					}
					w := csv.NewWriter(cmd.OutOrStdout())
					_ = w.Write([]string{"timestamp", "cmd", "input_tokens", "output_tokens", "saved_tokens", "savings_pct", "duration_ms"})
					for _, r := range records {
						_ = w.Write([]string{
							r.Timestamp.Format(time.RFC3339),
							r.Cmd,
							fmt.Sprintf("%d", r.InputTokens),
							fmt.Sprintf("%d", r.OutputTokens),
							fmt.Sprintf("%d", r.SavedTokens),
							fmt.Sprintf("%.2f", r.SavingsPct),
							fmt.Sprintf("%d", r.DurationMs),
						})
					}
					w.Flush()
					return nil
				default:
					// text: fall through to summary view and render Recent Commands section later
					if limit <= 0 {
						limit = 20
					}
					// don't return, will show recent after By Command
				}
			}

			if f == "json" {
				var data []byte
				if projectPath != "" {
					data, err = store.ExportJSONForProject(projectPath)
				} else {
					data, err = store.ExportJSON()
				}
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			if f == "csv" {
				var csvStr string
				if projectPath != "" {
					csvStr, err = store.ExportCSVForProject(projectPath)
				} else {
					csvStr, err = store.ExportCSV()
				}
				if err != nil {
					return err
				}
				_, _ = fmt.Fprint(cmd.OutOrStdout(), csvStr)
				return nil
			}
			// text mode (project-filtered if requested)
			var summary *stats.GainSummary
			var dailyData []stats.DayStats
			var weeklyData []stats.WeekStats
			var monthlyData []stats.MonthStats
			if projectPath != "" {
				summary, err = store.GetSummaryForProject(projectPath)
				if err != nil {
					return fmt.Errorf("get summary: %w", err)
				}
				dailyData, err = store.GetAllDaysForProject(projectPath)
				if err != nil {
					return fmt.Errorf("get daily stats: %w", err)
				}
				weeklyData, err = store.GetByWeekForProject(projectPath)
				if err != nil {
					return fmt.Errorf("get weekly stats: %w", err)
				}
				monthlyData, err = store.GetByMonthForProject(projectPath)
				if err != nil {
					return fmt.Errorf("get monthly stats: %w", err)
				}
			} else {
				summary, err = store.GetSummary()
				if err != nil {
					return fmt.Errorf("get summary: %w", err)
				}
				dailyData, err = store.GetAllDays()
				if err != nil {
					return fmt.Errorf("get daily stats: %w", err)
				}
				weeklyData, err = store.GetByWeek()
				if err != nil {
					return fmt.Errorf("get weekly stats: %w", err)
				}
				monthlyData, err = store.GetByMonth()
				if err != nil {
					return fmt.Errorf("get monthly stats: %w", err)
				}
			}

			// Determine which breakdowns to show based on flags
			showDaily := allFlag || daily
			showWeekly := allFlag || weekly
			showMonthly := allFlag || monthly
			if graph {
				showDaily = true
			}

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Header like rtk (show scope) — TokenMill branded but rtk pixel-perfect layout
			if projectPath != "" {
				_, _ = fmt.Fprintln(out, "TokenMill Token Savings (Project Scope)")
				_, _ = fmt.Fprintf(out, "Scope: %s\n", projectPath)
			} else {
				_, _ = fmt.Fprintln(out, "TokenMill Token Savings (Global Scope)")
			}
			_, _ = fmt.Fprintln(out, strings.Repeat("═", 60))
			_, _ = fmt.Fprintln(out, "")
			// KPI aligned like rtk print_kpi
			printKPI(out, "Total commands", fmt.Sprintf("%d", summary.TotalCommands))
			printKPI(out, "Input tokens", humanTokens(summary.TotalInput))
			printKPI(out, "Output tokens", humanTokens(summary.TotalOutput))
			printKPI(out, "Tokens saved", fmt.Sprintf("%s (%.1f%%)", humanTokens(summary.TotalSaved), summary.AvgSavingsPct))
			printKPI(out, "Total exec time", fmt.Sprintf("%s (avg %s)", humanDuration(summary.TotalTimeMs), humanDuration(summary.AvgTimeMs)))
			// efficiency meter 24 chars
			meter := efficiencyMeter(summary.AvgSavingsPct)
			_, _ = fmt.Fprintf(out, "Efficiency meter: %s %.1f%%\n", meter, summary.AvgSavingsPct)
			_, _ = fmt.Fprintln(out, "")
			// Warn about hook (stderr like rtk)
			// rtk shows "No tracking data yet" for an empty DB, but we keep only the
			// hook warning (and only when data exists) for compatibility.
			if summary.TotalCommands > 0 && !hasHookInstalled() {
				_, _ = fmt.Fprintln(errOut, "[warn] No hook installed — run `tokenmill init -g` for automatic token savings")
				_, _ = fmt.Fprintln(errOut, "")
			}
			// By command — rtk style with dynamic widths and impact bar 10 chars
			if len(summary.ByCommand) > 0 {
				_, _ = fmt.Fprintln(out, "By Command")
				// compute dynamic widths like rtk
				cmdWidth := 24
				impactWidth := 10
				// count width
				countWidth := 5
				for _, bc := range summary.ByCommand {
					cstr := fmt.Sprintf("%v", bc[1])
					if len(cstr) > countWidth {
						countWidth = len(cstr)
					}
				}
				if countWidth < 5 {
					countWidth = 5
				}
				// saved width using humanTokens
				savedWidth := 5
				for _, bc := range summary.ByCommand {
					savedVal := toInt(bc[2])
					s := humanTokens(savedVal)
					if len(s) > savedWidth {
						savedWidth = len(s)
					}
				}
				if savedWidth < 5 {
					savedWidth = 5
				}
				// time width using humanDuration
				timeWidth := 6
				for _, bc := range summary.ByCommand {
					tstr := humanDuration(toInt64(bc[4]))
					if len(tstr) > timeWidth {
						timeWidth = len(tstr)
					}
				}
				if timeWidth < 6 {
					timeWidth = 6
				}
				tableWidth := 3 + 2 + cmdWidth + 2 + countWidth + 2 + savedWidth + 2 + 6 + 2 + timeWidth + 2 + impactWidth
				_, _ = fmt.Fprintln(out, strings.Repeat("─", tableWidth))
				_, _ = fmt.Fprintf(out, "%3s %-*s %*s %*s %6s %*s %-*s\n", "#", cmdWidth, "Command", countWidth, "Count", savedWidth, "Saved", "Avg%", timeWidth, "Time", impactWidth, "Impact")
				_, _ = fmt.Fprintln(out, strings.Repeat("─", tableWidth))
				// max saved for bar
				maxSaved := 0
				for _, bc := range summary.ByCommand {
					v := toInt(bc[2])
					if v > maxSaved {
						maxSaved = v
					}
				}
				if maxSaved == 0 {
					maxSaved = 1
				}
				for i, bc := range summary.ByCommand {
					if i >= 10 {
						break
					}
					cmdStr := fmt.Sprintf("%v", bc[0])
					cmdCell := truncateForColumn(cmdStr, cmdWidth)
					cntStr := fmt.Sprintf("%v", bc[1])
					countCell := fmt.Sprintf("%*s", countWidth, cntStr)
					savedVal := toInt(bc[2])
					savedCell := fmt.Sprintf("%*s", savedWidth, humanTokens(savedVal))
					pctVal := toFloat64(bc[3])
					pctPlain := fmt.Sprintf("%.1f%%", pctVal)
					pctCell := fmt.Sprintf("%6s", pctPlain)
					timeCell := fmt.Sprintf("%*s", timeWidth, humanDuration(toInt64(bc[4])))
					impact := miniBar(savedVal, maxSaved, impactWidth)
					rowIdx := fmt.Sprintf("%2d.", i+1)
					_, _ = fmt.Fprintf(out, "%s %s %s %s %s %s %s\n", rowIdx, cmdCell, countCell, savedCell, pctCell, timeCell, impact)
				}
				_, _ = fmt.Fprintln(out, strings.Repeat("─", tableWidth))
				_, _ = fmt.Fprintln(out, "")
			} else {
				_, _ = fmt.Fprintln(out, "No commands yet.")
				_, _ = fmt.Fprintln(out, "")
			}

			// Recent Commands — only if --history text
			if history {
				// limit already set
				var records []stats.CommandRecord
				if projectPath != "" {
					records, _ = store.GetRecentForProject(limit, projectPath)
				} else {
					records, _ = store.GetRecent(limit)
				}
				if len(records) > 0 {
					_, _ = fmt.Fprintln(out, "Recent Commands")
					_, _ = fmt.Fprintln(out, "──────────────────────────────────────────────────────────")
					for _, r := range records {
						timeStr := r.Timestamp.Format("01-02 15:04")
						cmdShort := r.Cmd
						runes := []rune(cmdShort)
						if len(runes) > 25 {
							cmdShort = string(runes[:22]) + "..."
						}
						cmdPadded := fmt.Sprintf("%-25s", cmdShort)
						sign := "•"
						if r.SavingsPct >= 70 {
							sign = "▲"
						} else if r.SavingsPct >= 30 {
							sign = "■"
						}
						var pctStr string
						if r.SavingsPct < 0 {
							pctStr = fmt.Sprintf("%.0f%%", r.SavingsPct)
						} else {
							pctStr = fmt.Sprintf("-%.0f%%", r.SavingsPct)
						}
						tokensStr := humanTokens(r.SavedTokens)
						_, _ = fmt.Fprintf(out, "%s %s %s %s (%s)\n", timeStr, sign, cmdPadded, pctStr, tokensStr)
					}
					_, _ = fmt.Fprintln(out, "")
				}
			}

			// Daily breakdown
			if showDaily {
				if len(dailyData) > 0 {
					_, _ = fmt.Fprintln(out, "Daily breakdown")
					_, _ = fmt.Fprintln(out, strings.Repeat("─", 80))
					_, _ = fmt.Fprintf(out, "%-12s %6s %8s %8s %8s %6s %8s\n", "date", "cmds", "input", "output", "saved", "pct", "time")
					_, _ = fmt.Fprintln(out, strings.Repeat("─", 80))
					for _, d := range dailyData {
						_, _ = fmt.Fprintf(out, "%-12s %6d %8d %8d %8d %5.1f%% %8s\n", d.Date, d.Commands, d.InputTokens, d.OutputTokens, d.SavedTokens, d.SavingsPct, humanDuration(d.TotalTimeMs))
					}
					_, _ = fmt.Fprintln(out, "")
				}
			}
			if showWeekly {
				if len(weeklyData) > 0 {
					_, _ = fmt.Fprintln(out, "Weekly breakdown")
					_, _ = fmt.Fprintln(out, strings.Repeat("─", 80))
					for _, w := range weeklyData {
						_, _ = fmt.Fprintf(out, "%s to %s: %d cmds, saved %d (%.1f%%)\n", w.WeekStart, w.WeekEnd, w.Commands, w.SavedTokens, w.SavingsPct)
					}
					_, _ = fmt.Fprintln(out, "")
				}
			}
			if showMonthly {
				if len(monthlyData) > 0 {
					_, _ = fmt.Fprintln(out, "Monthly breakdown")
					_, _ = fmt.Fprintln(out, strings.Repeat("─", 80))
					for _, m := range monthlyData {
						_, _ = fmt.Fprintf(out, "%s: %d cmds, saved %d (%.1f%%)\n", m.Month, m.Commands, m.SavedTokens, m.SavingsPct)
					}
					_, _ = fmt.Fprintln(out, "")
				}
			}
			if graph && len(dailyData) > 0 {
				_, _ = fmt.Fprintln(out, "Graph (daily saved tokens):")
				maxSaved := 0
				for _, d := range dailyData {
					if d.SavedTokens > maxSaved {
						maxSaved = d.SavedTokens
					}
				}
				if maxSaved == 0 {
					maxSaved = 1
				}
				for _, d := range dailyData {
					barLen := int(float64(d.SavedTokens) / float64(maxSaved) * 40)
					if barLen < 1 && d.SavedTokens > 0 {
						barLen = 1
					}
					bar := strings.Repeat("█", barLen)
					_, _ = fmt.Fprintf(out, "%s %s %d\n", d.Date, bar, d.SavedTokens)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "text", "output format: text, json, csv")
	cmd.Flags().BoolVarP(&allFlag, "all", "a", false, "show all breakdowns (daily + weekly + monthly)")
	cmd.Flags().BoolVarP(&weekly, "weekly", "w", false, "show weekly breakdown")
	cmd.Flags().BoolVarP(&monthly, "monthly", "m", false, "show monthly breakdown")
	cmd.Flags().BoolVarP(&daily, "daily", "d", false, "show daily breakdown")
	cmd.Flags().BoolVarP(&project, "project", "p", false, "filter to current project (persisted via SQLite project_path)")
	cmd.Flags().BoolVarP(&history, "history", "H", false, "show recent command history")
	cmd.Flags().IntVar(&limit, "limit", 20, "limit for history")
	cmd.Flags().BoolVarP(&quota, "quota", "q", false, "show quota estimate (placeholder)")
	cmd.Flags().StringVar(&tier, "tier", "20x", "subscription tier for quota calculation")
	cmd.Flags().BoolVarP(&graph, "graph", "g", false, "show ASCII graph of daily savings")
	// Also support --tui hint? stats will alias.

	return cmd
}

func hasHookInstalled() bool {
	home, _ := os.UserHomeDir()
	if home != "" {
		if _, err := os.Stat(fmt.Sprintf("%s/.config/opencode/plugins/tokenmill.ts", home)); err == nil {
			return true
		}
	}
	if _, err := os.Stat(".opencode/plugins/tokenmill.ts"); err == nil {
		return true
	}
	return false
}

func humanTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func humanDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	m := ms / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%dm%ds", m, s)
}

func efficiencyMeter(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	width := 24
	filled := int(math.Round(pct / 100 * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	empty := width - filled
	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}

func printKPI(w interface{ Write([]byte) (int, error) }, label, value string) {
	// mimic rtk print_kpi: println!("{:<18} {}", format!("{label}:"), value)
	_, _ = fmt.Fprintf(w, "%-18s %s\n", label+":", value)
}

func truncateForColumn(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		// pad to width left-aligned
		return fmt.Sprintf("%-*s", width, text)
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func miniBar(value, max, width int) string {
	if max <= 0 || width <= 0 {
		if width <= 0 {
			return ""
		}
		return strings.Repeat("░", width)
	}
	filled := int(math.Round(float64(value) / float64(max) * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func toInt(v interface{}) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case int32:
		return int(x)
	case uint:
		return int(x)
	default:
		return 0
	}
}

func toInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case int32:
		return int64(x)
	default:
		return 0
	}
}

func toFloat64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	default:
		return 0
	}
}

// Ensure sorting helpers not needed elsewhere
var _ = sort.StringSlice{}
