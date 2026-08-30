package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInit_PluginTemplateMatchesCheckedInPlugin(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve init test path")
	}

	path := filepath.Join(filepath.Dir(testFile), "..", "..", "plugin", "tokenmill.ts")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in plugin: %v", err)
	}
	if string(content) != pluginTemplate {
		t.Fatalf("generated plugin template has drifted from plugin/tokenmill.ts")
	}
}

func TestInit_Global_IdempotentTempRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows fallback

	root := newRootCmd()
	root.SetArgs([]string{"init", "-g", "--opencode"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("first init: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "tokenmill.ts")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("plugin not created: %v", err)
	}
	content1, _ := os.ReadFile(pluginPath)
	opencodeJSON := filepath.Join(home, ".config", "opencode", "opencode.json")
	if _, err := os.Stat(opencodeJSON); err != nil {
		t.Fatalf("opencode.json not created: %v", err)
	}
	// Check no temp files leftover
	dir := filepath.Dir(pluginPath)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	// Second run idempotent
	root2 := newRootCmd()
	root2.SetArgs([]string{"init", "-g", "--opencode"})
	root2.SetOut(&testWriter{})
	root2.SetErr(&testWriter{})
	if err := root2.Execute(); err != nil {
		t.Fatalf("second init: %v", err)
	}
	content2, _ := os.ReadFile(pluginPath)
	if string(content1) != string(content2) {
		t.Fatalf("idempotent failed: content changed between runs")
	}
	// opencode.json should not duplicate entry
	data, _ := os.ReadFile(opencodeJSON)
	count := strings.Count(string(data), "opencode-tokenmill")
	if count != 1 {
		t.Fatalf("opencode.json should have single tokenmill entry, got %d count in %s", count, string(data))
	}
	// Check tui plugin exists
	tuiPath := filepath.Join(home, ".config", "opencode", "tui-plugins", "tokenmill-stats.tsx")
	if _, err := os.Stat(tuiPath); err != nil {
		t.Fatalf("tui plugin not created: %v", err)
	}
	// Ensure tui leftover no tmp
	if entries, _ := os.ReadDir(filepath.Dir(tuiPath)); len(entries) > 0 {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Fatalf("tui tmp leftover: %s", e.Name())
			}
		}
	}
}

func TestInit_Local_Opencode(t *testing.T) {
	proj := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(proj)
	defer os.Chdir(oldWd)

	root := newRootCmd()
	root.SetArgs([]string{"init", "--opencode"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("local init: %v", err)
	}
	pluginPath := filepath.Join(proj, ".opencode", "plugins", "tokenmill.ts")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("local plugin not created at %s: %v", pluginPath, err)
	}
	// Check no tmp leftover
	dir := filepath.Dir(pluginPath)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("tmp leftover %s", e.Name())
		}
	}
}

func TestInit_HookOnlySkipsTUI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := newRootCmd()
	root.SetArgs([]string{"init", "-g", "--opencode", "--hook-only"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("hook-only init: %v", err)
	}
	tuiPath := filepath.Join(home, ".config", "opencode", "tui-plugins", "tokenmill-stats.tsx")
	if _, err := os.Stat(tuiPath); err == nil {
		t.Fatalf("hook-only should skip tui plugin but file exists")
	}
}

func TestInit_AtomicTempRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := newRootCmd()
	root.SetArgs([]string{"init", "-g", "--opencode"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Ensure files were created via temp+rename: we already checked no .tmp leftover, but also ensure opencode.json is valid json and contains plugin entry
	opencodeJSON := filepath.Join(home, ".config", "opencode", "opencode.json")
	data, err := os.ReadFile(opencodeJSON)
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	if !strings.Contains(string(data), "opencode-tokenmill") && !strings.Contains(string(data), "tokenmill") {
		t.Fatalf("opencode.json missing tokenmill entry: %s", string(data))
	}
	// Verify plugin file has expected export
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "tokenmill.ts")
	pdata, _ := os.ReadFile(pluginPath)
	if !strings.Contains(string(pdata), "TokenMillPlugin") {
		t.Fatalf("plugin missing TokenMillPlugin export")
	}
	if strings.Contains(string(pdata), `tool.execute.after`) {
		t.Fatalf("plugin must not register tool.execute.after (cache metadata removed)")
	}
	if !strings.Contains(string(pdata), `experimental.chat.system.transform`) {
		t.Fatalf("plugin missing system.transform")
	}
	// Critical: system transforms must mutate OpenCode's live array in place.
	if !strings.Contains(string(pdata), "splice(0, output.system.length") {
		t.Fatalf("plugin must use splice in-place")
	}
	if strings.Contains(string(pdata), "tool.execute.before") {
		t.Fatalf("plugin must not rewrite executable tool arguments before execution")
	}
}

func TestInit_PatchOpencodeJSON_Compatible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Pre-create opencode.json with existing plugin entry "superpowers..."
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0755)
	opencodeJSON := filepath.Join(home, ".config", "opencode", "opencode.json")
	os.WriteFile(opencodeJSON, []byte(`{"plugin":["superpowers@git+https://github.com/obra/superpowers.git"]}`), 0644)

	root := newRootCmd()
	root.SetArgs([]string{"init", "-g", "--opencode"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("init with existing: %v", err)
	}
	data, _ := os.ReadFile(opencodeJSON)
	s := string(data)
	if !strings.Contains(s, "superpowers") {
		t.Fatalf("existing plugin lost: %s", s)
	}
	if !strings.Contains(s, "opencode-tokenmill") {
		t.Fatalf("tokenmill not added: %s", s)
	}
	// Count tokenmill occurrences should be 1
	c := strings.Count(s, "tokenmill")
	if c != 1 {
		t.Fatalf("expected 1 tokenmill entry, got %d in %s", c, s)
	}
	// Second init should not duplicate
	root2 := newRootCmd()
	root2.SetArgs([]string{"init", "-g", "--opencode"})
	root2.SetOut(&testWriter{})
	root2.SetErr(&testWriter{})
	root2.Execute()
	data2, _ := os.ReadFile(opencodeJSON)
	if strings.Count(string(data2), "tokenmill") != 1 {
		t.Fatalf("idempotent duplicate after second run: %s", string(data2))
	}
}

func TestInit_GlobalFlagAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, args := range [][]string{
		{"init", "-g", "--opencode"},
		{"init", "--global", "--opencode"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// clean
			os.RemoveAll(filepath.Join(home, ".config"))
			root := newRootCmd()
			root.SetArgs(args)
			root.SetOut(&testWriter{})
			root.SetErr(&testWriter{})
			if err := root.Execute(); err != nil {
				t.Fatalf("args %v: %v", args, err)
			}
			if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugins", "tokenmill.ts")); err != nil {
				t.Fatalf("plugin not created for %v", args)
			}
		})
	}
}

func TestInit_CreatesCanonicalConfigDir(t *testing.T) {
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	root := newRootCmd()
	root.SetArgs([]string{"init", "-g", "--opencode", "--hook-only"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	canonicalDir := filepath.Join(configHome, "tokenmill")
	info, err := os.Stat(canonicalDir)
	if err != nil {
		t.Fatalf("canonical config directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("canonical config path is not a directory: %s", canonicalDir)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugins", "tokenmill.ts")); err != nil {
		t.Fatalf("OpenCode plugin path changed unexpectedly: %v", err)
	}
}

func TestInit_PatchMalformedOpenCodePreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	original := []byte(`{"provider":{"limit":9007199254740993},`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := patchOpencodeJSON(path); err == nil {
		t.Fatal("malformed existing config must return an error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("malformed config changed from %q to %q", original, got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode changed to %o", info.Mode().Perm())
	}
}

func TestInit_PatchOpenCodePreservesLargeNumbersAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	original := []byte(`{"provider":{"limit":9007199254740993},"plugin":["existing"]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := patchOpencodeJSON(path); err != nil {
		t.Fatalf("patchOpencodeJSON: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`9007199254740993`)) {
		t.Fatalf("large number changed: %s", got)
	}
	var patched map[string]json.RawMessage
	if err := json.Unmarshal(got, &patched); err != nil {
		t.Fatalf("patched config is invalid JSON: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode changed to %o", info.Mode().Perm())
	}
}

func TestInit_PatchOpenCodeJSONCBlockCommentsPreservesFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	original := []byte(`{
  /* provider credentials must survive plugin installation */
  "provider": {"apiKey": "keep-me"},
  "permission": {"edit": "deny"}, // preserve unknown OpenCode settings
  "plugin": [],
}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}

	if err := patchOpencodeJSON(path); err != nil {
		t.Fatalf("patchOpencodeJSON JSONC: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var patched map[string]json.RawMessage
	if err := json.Unmarshal(got, &patched); err != nil {
		t.Fatalf("patched config is invalid JSON: %v", err)
	}
	var provider map[string]string
	if err := json.Unmarshal(patched["provider"], &provider); err != nil || provider["apiKey"] != "keep-me" {
		t.Fatalf("provider field was lost: %s", got)
	}
	var permission map[string]string
	if err := json.Unmarshal(patched["permission"], &permission); err != nil || permission["edit"] != "deny" {
		t.Fatalf("permission field was lost: %s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode changed to %o", info.Mode().Perm())
	}
}
