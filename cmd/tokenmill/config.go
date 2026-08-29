package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tokenmill/tokenmill/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage tokenmill config (like rtk config)",
	}
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigEditCmd())
	cmd.AddCommand(newConfigMigrateCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current config as jsonc (via stdout)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: load config (graceful): %v\n", err)
			}
			b, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Set config value via dot-path (e.g. techniques.jton.enabled false) and save atomically",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			val := args[1]
			// Load current config
			cfg, _ := loadConfig()
			// Save to the same source selected by the loader: explicit path, the
			// existing project source, or the canonical global source.
			targetPath := configTargetPath()
			if err := cfg.Set(path, val); err != nil {
				return fmt.Errorf("set %q: %w", path, err)
			}
			cfg.Validate()
			if err := cfg.Save(targetPath); err != nil {
				return fmt.Errorf("save %s: %w", targetPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ set %s=%s in %s\n", path, val, targetPath)
			// also show updated config
			b, _ := json.MarshalIndent(cfg, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open config in $EDITOR (like rtk config edit, atomically)",
		RunE: func(cmd *cobra.Command, args []string) error {
			targetPath := configTargetPath()
			// Ensure file exists with defaults if not
			if _, err := os.Stat(targetPath); os.IsNotExist(err) {
				def := config.DefaultConfig()
				if err := def.Save(targetPath); err != nil {
					return fmt.Errorf("create config: %w", err)
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = os.Getenv("VISUAL")
			}
			if editor == "" {
				// fallback to vi or nano
				if _, err := exec.LookPath("vi"); err == nil {
					editor = "vi"
				} else if _, err := exec.LookPath("nano"); err == nil {
					editor = "nano"
				} else {
					editor = "vi"
				}
			}
			// Split editor into command + args (handle "code --wait" style)
			parts := strings.Fields(editor)
			c := exec.Command(parts[0], append(parts[1:], targetPath)...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("editor %q failed: %w", editor, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ edited %s\n", targetPath)
			return nil
		},
	}
}

func newConfigMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Copy the legacy OpenCode config to the canonical TokenMill path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.MigrateLegacyConfig()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ migrated legacy config to %s\n", path)
			return nil
		},
	}
}

func configTargetPath() string {
	if cfgFile != "" {
		return cfgFile
	}

	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		projectPath := filepath.Join(cwd, ".opencode", "tokenmill.jsonc")
		if _, statErr := os.Stat(projectPath); statErr == nil || !os.IsNotExist(statErr) {
			return projectPath
		}
		rootPath := filepath.Join(cwd, "tokenmill.jsonc")
		if _, statErr := os.Stat(rootPath); statErr == nil {
			return rootPath
		}
		if _, statErr := os.Stat(filepath.Join(cwd, ".opencode")); statErr == nil {
			return projectPath
		}
	}

	if globalPath, err := config.GlobalConfigPath(); err == nil {
		return globalPath
	}
	if cwd != "" {
		return filepath.Join(cwd, ".opencode", "tokenmill.jsonc")
	}
	return filepath.Join(".opencode", "tokenmill.jsonc")
}
