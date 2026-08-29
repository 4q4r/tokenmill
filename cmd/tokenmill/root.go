package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/tokenmill/tokenmill/internal/config"
	"github.com/tokenmill/tokenmill/internal/stats"
)

var (
	cfgFile  string
	logLevel string
	version  = "0.1.0"
)

// newRootCmd creates root cobra command with persistent flags --config, --log-level and subcommands.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tokenmill",
		Short: "Lossless token optimization for OpenCode — cache-safe tournament codecs",
		Long: `tokenmill is a lossless input-token optimizer for OpenCode.
It runs a tournament of codecs (JTON, compact JSON, RLE, table, etc.) and picks the smallest
lossless representation per block, with cache-safe SHA256 dedup and provider prompt-cache awareness.`,
		Version: version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Setup slog level from --log-level flag or config.
			// Precedence for log level: --log-level > --config > TOKENMILL_* > project > global > default.
			cfg, err := loadConfig()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("load config: %w", err)
			}
			levelStr := logLevel
			if levelStr == "" {
				levelStr = string(cfg.LogLevel)
				if levelStr == "" {
					levelStr = "info"
				}
			}
			lvl := parseLogLevel(levelStr)
			h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
			slog.SetDefault(slog.New(h))
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			stats.ResetConfiguredDatabasePath()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $XDG_CONFIG_HOME/tokenmill/config.jsonc or platform config dir/tokenmill/config.jsonc)")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "log level: debug, info, warn, error")

	// also bind --version already provided by cobra via Version, but ensure flag
	root.SetVersionTemplate(`{{printf "tokenmill version %s\n" .Version}}`)

	// Add subcommands
	root.AddCommand(newInitCmd())
	root.AddCommand(newGainCmd())
	root.AddCommand(newRewriteCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newStatsCmd())

	// Hidden alias for gain --quota etc? Gain handles quota flag.
	return root
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "info", "INFO", "":
		return slog.LevelInfo
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// loadConfig loads defaults < global < project < TOKENMILL_* and overlays --config last.
func loadConfig() (config.Config, error) {
	if cfgFile != "" {
		cfg, err := config.LoadFrom(cfgFile)
		stats.SetConfiguredDatabasePath(cfg.DatabasePathOverride())
		return cfg, err
	}
	cfg, err := config.Load()
	stats.SetConfiguredDatabasePath(cfg.DatabasePathOverride())
	return cfg, err
}

// Execute entry for main.
func Execute() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
