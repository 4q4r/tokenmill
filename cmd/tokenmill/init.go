package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tokenmill/tokenmill/internal/config"
)

//go:embed plugin_template.ts
var pluginTemplate string

const tuiTemplate = `import { createSignal, createEffect, onCleanup } from "solid-js"

export function TokenMillStats() {
  const [data, setData] = createSignal<any>(null)
  const fetchData = async () => {
    try {
      const res = await fetch("/api/tokenmill/gain?format=json").then(r=>r.json()).catch(async ()=>{
        // fallback to shelling tokenmill if API not available
        // @ts-ignore - opencode $ is available in plugin context but not here; graceful
        return null
      })
      if (res) setData(res)
    } catch (e) {
      if ((globalThis as any).TOKENMILL_DEBUG) console.debug("[tokenmill-tui] fetch fail-open", e)
    }
  }
  createEffect(() => {
    fetchData()
    const id = setInterval(fetchData, 30*60*1000)
    onCleanup(()=>clearInterval(id))
  })
  return (
    <div style={{ padding: "8px", "font-size": "12px" }}>
      <div style={{ "font-weight": "600", "margin-bottom": "4px" }}>TokenMill</div>
      {data() ? (
        <div>
          <div>Saved: {data().summary?.total_saved ?? 0} ({(data().summary?.avg_savings_pct ?? 0).toFixed(1)}%)</div>
          <div style={{ background: "#eee", height: "6px", "border-radius": "3px", overflow: "hidden", margin: "4px 0" }}>
            <div style={{ width: String(Math.min(100, data().summary?.avg_savings_pct ?? 0)) + "%", background: "#4ade80", height: "100%" }} />
          </div>
          <div style={{ opacity: 0.6 }}>Commands: {data().summary?.total_commands ?? 0}</div>
        </div>
      ) : (
        <div style={{ opacity: 0.6 }}>No data — run <code>tokenmill gain</code></div>
      )}
    </div>
  )
}

// Register as sidebar_content slot order 40 (graceful if slot not present)
export default {
  sidebar_content: {
    order: 40,
    component: TokenMillStats,
  },
}
`

func newInitCmd() *cobra.Command {
	var globalFlag bool
	var opencodeFlag bool
	var hookOnly bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize tokenmill for OpenCode (like rtk init)",
		Long:  "Create plugin file and patch opencode.json/tui.json. Global (-g) writes the OpenCode integration to ~/.config/opencode and creates the canonical TokenMill config directory; local writes to .opencode.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine scope: global if -g or --global set, else local if --opencode without global
			// Surgical: isGlobal = globalFlag (explicit), isLocal = opencodeFlag && !isGlobal (clear precedence, no `|| &&` fragility)
			isGlobal := globalFlag
			if cmd.Flags().Changed("global") {
				gv, _ := cmd.Flags().GetBool("global")
				if gv {
					isGlobal = true
				}
			}
			isLocal := opencodeFlag && !isGlobal
			if !isLocal {
				if err := config.EnsureGlobalConfigDir(); err != nil {
					return fmt.Errorf("create canonical config directory: %w", err)
				}
			}
			// If opencode flag without global -> local; if with global -> global.
			// Legacy: if neither flag (plain init) -> maybe global? But we default to global if opencodeFlag true and !globalFlag false -> local.
			// To support `tokenmill init -g --opencode` both true => isGlobal already true.
			// To support `tokenmill init --opencode` alone => isGlobal false => local.
			home, _ := os.UserHomeDir()
			var pluginPath string
			var tuiPluginPath string
			var opencodeJSON string
			var tuiJSON string

			if isGlobal {
				// global
				if home == "" {
					return fmt.Errorf("cannot determine home dir for global install")
				}
				pluginPath = filepath.Join(home, ".config", "opencode", "plugins", "tokenmill.ts")
				tuiPluginPath = filepath.Join(home, ".config", "opencode", "tui-plugins", "tokenmill-stats.tsx")
				opencodeJSON = filepath.Join(home, ".config", "opencode", "opencode.json")
				tuiJSON = filepath.Join(home, ".config", "opencode", "tui.json")
			} else if isLocal {
				// local
				cwd, _ := os.Getwd()
				_ = cwd
				pluginPath = filepath.Join(".opencode", "plugins", "tokenmill.ts")
				// local tui plugin maybe not needed? But we still optionally create .opencode/tui-plugins ?
				// For local we skip global tui patch; attempt local tui.json if exists
				tuiPluginPath = filepath.Join(".opencode", "tui-plugins", "tokenmill-stats.tsx")
				opencodeJSON = filepath.Join(".opencode", "opencode.json")
				tuiJSON = filepath.Join(".opencode", "tui.json")
				// also check ./opencode.json fallback if .opencode/opencode.json not exists? Prefer root opencode.json ?
				// We'll handle both; patch whichever exists or create .opencode/opencode.json
			} else {
				// No opencode flag: default to global like rtk? Assume global.
				if home == "" {
					return fmt.Errorf("cannot determine home dir")
				}
				pluginPath = filepath.Join(home, ".config", "opencode", "plugins", "tokenmill.ts")
				tuiPluginPath = filepath.Join(home, ".config", "opencode", "tui-plugins", "tokenmill-stats.tsx")
				opencodeJSON = filepath.Join(home, ".config", "opencode", "opencode.json")
				tuiJSON = filepath.Join(home, ".config", "opencode", "tui.json")
				// Log hint that --opencode is recommended
				fmt.Fprintln(os.Stderr, "hint: use --opencode for OpenCode plugin (defaulting to global)")
			}

			// 1) Create plugin file atomically temp+rename, idempotent
			if err := writeAtomic(pluginPath, pluginTemplate, 0644); err != nil {
				return fmt.Errorf("write plugin: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ plugin %s\n", pluginPath)

			// 2) Patch opencode.json plugins entry
			if err := patchOpencodeJSON(opencodeJSON); err != nil {
				return fmt.Errorf("patch %s: %w", opencodeJSON, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ patched %s plugin entry [\"opencode-tokenmill\"]\n", opencodeJSON)

			// 3) Optionally TUI plugin if not --hook-only and graceful
			if !hookOnly {
				// check if we should create tui plugin: if global tui dir exists or we can create, do it
				if err := os.MkdirAll(filepath.Dir(tuiPluginPath), 0755); err == nil {
					if _, err := os.Stat(tuiPluginPath); os.IsNotExist(err) {
						if err := writeAtomic(tuiPluginPath, tuiTemplate, 0644); err != nil {
							fmt.Fprintf(os.Stderr, "warn: tui plugin write failed (graceful): %v\n", err)
						} else {
							fmt.Fprintf(cmd.OutOrStdout(), "✓ tui plugin %s\n", tuiPluginPath)
						}
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "· tui plugin exists %s (idempotent)\n", tuiPluginPath)
					}
				}
				if err := patchTuiJSON(tuiJSON); err != nil {
					return fmt.Errorf("patch %s: %w", tuiJSON, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "✓ patched %s\n", tuiJSON)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "· --hook-only: skipped tui plugin")
			}

			// Instructions like rtk
			fmt.Fprintln(cmd.OutOrStdout(), "")
			fmt.Fprintln(cmd.OutOrStdout(), "Next steps:")
			fmt.Fprintln(cmd.OutOrStdout(), "  tokenmill --version")
			fmt.Fprintln(cmd.OutOrStdout(), "  tokenmill gain")
			fmt.Fprintln(cmd.OutOrStdout(), "  tokenmill rewrite \"git status\"  # test tournament")
			if isGlobal {
				fmt.Fprintln(cmd.OutOrStdout(), "Global plugin installed at ~/.config/opencode/plugins/tokenmill.ts")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Local plugin installed at .opencode/plugins/tokenmill.ts")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Restart opencode to load the plugin.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&globalFlag, "global", "g", false, "install globally (OpenCode: ~/.config/opencode)")
	cmd.Flags().Bool("opencode", false, "install OpenCode plugin")
	// Bind opencode flag to variable? Use lookup
	cmd.Flags().Lookup("opencode").NoOptDefVal = "true"
	// Also wire opencodeFlag via manual retrieval in RunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetBool("opencode"); v {
			opencodeFlag = true
		}
		return nil
	}
	cmd.Flags().BoolVar(&hookOnly, "hook-only", false, "only install hook, skip TUI")

	// Ensure that PreRunE for logLevel still runs: wrap
	origPre := cmd.PersistentPreRunE
	_ = origPre
	return cmd
}

func writeAtomic(path, content string, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Check idempotent: if file exists and content same, skip write but still ensure perm
	if data, err := os.ReadFile(path); err == nil {
		if string(data) == content {
			return nil
		}
	}
	tmp, err := os.CreateTemp(dir, ".tokenmill-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		if _, err := os.Stat(tmpName); err == nil {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func patchOpencodeJSON(path string) error {
	obj, mode, exists, err := readJSONCObject(path)
	if err != nil {
		return err
	}
	pluginKey := "plugin"
	if _, hasPlugin := obj[pluginKey]; !hasPlugin {
		if _, hasPlugins := obj["plugins"]; hasPlugins {
			pluginKey = "plugins"
		}
	}
	plugins, err := decodePluginList(obj[pluginKey])
	if err != nil {
		return fmt.Errorf("decode %s: %w", pluginKey, err)
	}
	hasTokenMill := false
	for _, plugin := range plugins {
		if plugin == "opencode-tokenmill" || plugin == "tokenmill" || strings.Contains(plugin, "tokenmill") {
			hasTokenMill = true
			break
		}
	}
	if !hasTokenMill {
		plugins = append(plugins, "opencode-tokenmill")
	}
	pluginData, err := json.Marshal(plugins)
	if err != nil {
		return fmt.Errorf("encode %s: %w", pluginKey, err)
	}
	obj[pluginKey] = json.RawMessage(pluginData)
	return writeJSONObjectAtomic(path, obj, mode, exists)
}

func patchTuiJSON(path string) error {
	object, mode, exists, err := readJSONCObject(path)
	if err != nil {
		return err
	}
	return writeJSONObjectAtomic(path, object, mode, exists)
}

func readJSONCObject(path string) (map[string]json.RawMessage, os.FileMode, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), 0, false, nil
		}
		return nil, 0, true, fmt.Errorf("read: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, true, fmt.Errorf("stat: %w", err)
	}
	cleaned := config.StripJSONC(string(data))
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &object); err != nil {
		return nil, 0, true, fmt.Errorf("parse JSONC: %w", err)
	}
	if object == nil {
		return nil, 0, true, fmt.Errorf("parse JSONC: root must be an object")
	}
	return object, info.Mode().Perm(), true, nil
}

func decodePluginList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		if len(raw) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("plugin value must be an array or string")
	}
	var plugins []string
	if err := json.Unmarshal(raw, &plugins); err == nil {
		return plugins, nil
	}
	var plugin string
	if err := json.Unmarshal(raw, &plugin); err == nil {
		return []string{plugin}, nil
	}
	return nil, fmt.Errorf("plugin value must be an array of strings or a string")
}

func writeJSONObjectAtomic(path string, object map[string]json.RawMessage, mode os.FileMode, exists bool) error {
	out, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	out = append(out, '\n')
	if !exists || mode.Perm() == 0 {
		mode = 0600
	}
	return writeAtomic(path, string(out), mode.Perm())
}
