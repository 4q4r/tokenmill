package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig_Show_TableDriven(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{"default", ""},
		{"custom", `{"logLevel":"debug","minSavingsPercent":25}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var cfgPath string
			var args []string
			if tc.config != "" {
				cfgPath = filepath.Join(dir, "c.jsonc")
				os.WriteFile(cfgPath, []byte(tc.config), 0644)
				args = []string{"--config", cfgPath, "config", "show"}
			} else {
				// isolate HOME to empty
				t.Setenv("HOME", dir)
				t.Setenv("USERPROFILE", dir)
				t.Setenv("XDG_DATA_HOME", dir)
				// also ensure no project file
				args = []string{"config", "show"}
			}
			root := newRootCmd()
			root.SetArgs(args)
			w := &testWriter{}
			root.SetOut(w)
			root.SetErr(w)
			// Use --log-level error via flag to avoid needing global
			// But we can also set via args: prepend --log-level error
			// For show test, we already have args; we can add --log-level via root flag? Instead set via env
			// Simpler: set logLevel via flag in args if needed
			if err := root.Execute(); err != nil {
				t.Fatalf("config show: %v stderr=%s", err, w.String())
			}
			out := w.String()
			if out == "" {
				t.Fatal("empty show output")
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("show not valid json: %v out=%s", err, out)
			}
			if _, ok := m["logLevel"]; !ok {
				t.Fatalf("missing logLevel in show")
			}
			if _, ok := m["techniques"]; !ok {
				t.Fatalf("missing techniques")
			}
		})
	}
}

func TestConfig_SetShow_Atomic(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tokenmill.jsonc")

	// first set logLevel debug
	root := newRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "config", "set", "logLevel", "debug"})
	w := &testWriter{}
	ew := &testWriter{}
	root.SetOut(w)
	root.SetErr(ew)
	if err := root.Execute(); err != nil {
		t.Fatalf("set: %v stderr=%s", err, ew.String())
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("tmp leftover %s", e.Name())
		}
	}
	data, _ := os.ReadFile(cfgPath)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("saved not json: %v", err)
	}
	if m["logLevel"] != "debug" {
		t.Fatalf("expected debug got %v", m["logLevel"])
	}
	// Show should reflect it
	root2 := newRootCmd()
	root2.SetArgs([]string{"--config", cfgPath, "config", "show"})
	w2 := &testWriter{}
	root2.SetOut(w2)
	root2.SetErr(w2)
	if err := root2.Execute(); err != nil {
		t.Fatalf("show after set: %v stderr=%s", err, w2.String())
	}
	var m2 map[string]interface{}
	if err := json.Unmarshal(w2.Bytes(), &m2); err != nil {
		t.Fatalf("show json: %v", err)
	}
	if m2["logLevel"] != "debug" {
		t.Fatalf("show after set got %v", m2["logLevel"])
	}
	// Second set should update
	root3 := newRootCmd()
	root3.SetArgs([]string{"--config", cfgPath, "config", "set", "techniques.jton.enabled", "false"})
	w3 := &testWriter{}
	ew3 := &testWriter{}
	root3.SetOut(w3)
	root3.SetErr(ew3)
	if err := root3.Execute(); err != nil {
		t.Fatalf("second set: %v stderr=%s", err, ew3.String())
	}
	data3, _ := os.ReadFile(cfgPath)
	var m3 map[string]interface{}
	json.Unmarshal(data3, &m3)
	techs, ok := m3["techniques"].(map[string]interface{})
	if !ok {
		t.Fatalf("techniques missing after second set file=%s", string(data3))
	}
	jton, ok := techs["jton"].(map[string]interface{})
	if !ok {
		t.Fatalf("jton missing file=%s", string(data3))
	}
	if jton["enabled"] != false {
		t.Fatalf("jton enabled expected false got %v file=%s stdout=%s", jton["enabled"], string(data3), w3.String())
	}
	entries2, _ := os.ReadDir(dir)
	for _, e := range entries2 {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("tmp leftover after second %s", e.Name())
		}
	}
}

func TestConfig_Set_TextAndBoolVariants(t *testing.T) {
	tests := []struct {
		path  string
		value string
		check func(m map[string]interface{}) bool
	}{
		{"enabled", "false", func(m map[string]interface{}) bool { return m["enabled"] == false }},
		{"logLevel", "warn", func(m map[string]interface{}) bool { return m["logLevel"] == "warn" }},
		{"minSavingsPercent", "42", func(m map[string]interface{}) bool {
			if v, ok := m["minSavingsPercent"].(float64); ok {
				return v == 42
			}
			return false
		}},
		{"techniques.dedup", "false", func(m map[string]interface{}) bool {
			techs := m["techniques"].(map[string]interface{})
			return techs["dedup"] == false
		}},
	}
	for _, tc := range tests {
		t.Run(tc.path+"="+tc.value, func(t *testing.T) {
			subPath := filepath.Join(t.TempDir(), "c.jsonc")
			root := newRootCmd()
			root.SetArgs([]string{"--config", subPath, "config", "set", tc.path, tc.value})
			w := &testWriter{}
			root.SetOut(w)
			root.SetErr(w)
			if err := root.Execute(); err != nil {
				t.Fatalf("set %s %s: %v stderr=%s", tc.path, tc.value, err, w.String())
			}
			data, _ := os.ReadFile(subPath)
			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !tc.check(m) {
				t.Fatalf("check failed for %s=%s got %v", tc.path, tc.value, m)
			}
		})
	}
}

func TestConfig_SetInvalidPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.jsonc")
	root := newRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "config", "set", "nonexistent.field", "123"})
	w := &testWriter{}
	root.SetOut(w)
	root.SetErr(w)
	if err := root.Execute(); err == nil {
		t.Fatalf("expected error for invalid path")
	}
}

func TestConfigMigrateLegacy(t *testing.T) {
	configHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TOKENMILL_ENABLED", "")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	legacyPath := filepath.Join(home, ".config", "opencode", "tokenmill.jsonc")
	legacyData := []byte(`{"enabled":false,"logLevel":"debug"}`)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyData, 0644); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"config", "migrate"})
	out := &testWriter{}
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatalf("config migrate: %v output=%s", err, out.String())
	}

	canonicalPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	got, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("read canonical config: %v", err)
	}
	if string(got) != string(legacyData) {
		t.Fatalf("migrated config = %q, want %q", got, legacyData)
	}
}

func TestConfigSetUsesCanonicalGlobalPath(t *testing.T) {
	configHome := t.TempDir()
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TOKENMILL_LOG_LEVEL", "")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	root := newRootCmd()
	root.SetArgs([]string{"config", "set", "logLevel", "debug"})
	out := &testWriter{}
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatalf("config set: %v output=%s", err, out.String())
	}

	canonicalPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	data, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("read canonical config: %v", err)
	}
	if !strings.Contains(string(data), `"logLevel": "debug"`) {
		t.Fatalf("canonical config missing updated log level: %s", data)
	}
	legacyPath := filepath.Join(home, ".config", "opencode", "tokenmill.jsonc")
	if _, err := os.Stat(legacyPath); err == nil {
		t.Fatalf("config set wrote the legacy OpenCode config path: %s", legacyPath)
	}
}

func TestExplicitConfigDatabasePathUsedByGain(t *testing.T) {
	configHome := t.TempDir()
	dataHome := t.TempDir()
	home := t.TempDir()
	project := t.TempDir()
	customPath := filepath.Join(t.TempDir(), "custom", "tracking.db")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TOKENMILL_DATABASE_PATH", "")
	t.Setenv("TOKENMILL_TRACKING_DATABASE_PATH", "")
	t.Setenv("TOKENMILL_LOG_LEVEL", "")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	customPathJSON, err := json.Marshal(customPath)
	if err != nil {
		t.Fatal(err)
	}
	explicitPath := filepath.Join(t.TempDir(), "explicit.jsonc")
	if err := os.WriteFile(explicitPath, []byte(`{"tracking":{"database_path":`+string(customPathJSON)+`}}`), 0644); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"--config", explicitPath, "gain", "--format", "json"})
	out := &testWriter{}
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatalf("gain with explicit config: %v output=%s", err, out.String())
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("explicit config database was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataHome, "tokenmill", "tracking.db")); err == nil {
		t.Fatal("gain created the default database instead of the explicit config database")
	}
}
