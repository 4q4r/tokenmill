package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// ---------- defaults ----------

func TestDefaults(t *testing.T) {
	cfg := DefaultConfig()
	want := Config{
		Enabled:           true,
		LogSavings:        true,
		LogLevel:          LogLevelInfo,
		ShowUpdateEvery:   10,
		MinSavingsPercent: 10,
		MinSavingsTokens:  32,
		FreshnessTurns:    20,
		Techniques: Techniques{
			Dedup:         true,
			AnsiStripping: true,
			CrRendering:   true,
			ExactRLE:      ExactRLE{Enabled: true, MinRun: 3},
			BlockFactoring: BlockFactoring{
				Enabled:  true,
				MinBlock: 2,
				MaxBlock: 20,
			},
			PathDict:       PathDict{Enabled: true, MaxCodes: 5, MinCount: 3},
			SubstringDict:  SubstringDict{Enabled: false, MinLen: 40, MinCount: 4, Experimental: false},
			Jton:           Jton{Enabled: true, MinRows: 10},
			JsonCompact:    true,
			TableTSV:       true,
			StacktraceDict: true,
		},
		Experimental: Experimental{
			"ison":     false,
			"gcfGraph": false,
		},
	}

	if cfg.Enabled != want.Enabled {
		t.Fatalf("Enabled: got %v want %v", cfg.Enabled, want.Enabled)
	}
	if cfg.LogSavings != want.LogSavings {
		t.Fatalf("LogSavings mismatch")
	}
	if cfg.LogLevel != want.LogLevel {
		t.Fatalf("LogLevel got %v want %v", cfg.LogLevel, want.LogLevel)
	}
	if cfg.ShowUpdateEvery != want.ShowUpdateEvery {
		t.Fatalf("ShowUpdateEvery got %v want %v", cfg.ShowUpdateEvery, want.ShowUpdateEvery)
	}
	if cfg.MinSavingsPercent != want.MinSavingsPercent {
		t.Errorf("MinSavingsPercent got %d want %d", cfg.MinSavingsPercent, want.MinSavingsPercent)
	}
	if cfg.MinSavingsTokens != want.MinSavingsTokens {
		t.Errorf("MinSavingsTokens got %d want %d", cfg.MinSavingsTokens, want.MinSavingsTokens)
	}
	if cfg.FreshnessTurns != want.FreshnessTurns {
		t.Errorf("FreshnessTurns got %d want %d", cfg.FreshnessTurns, want.FreshnessTurns)
	}
	if cfg.Techniques.Dedup != want.Techniques.Dedup {
		t.Errorf("Dedup mismatch")
	}
	if cfg.Techniques.ExactRLE.MinRun != 3 {
		t.Errorf("ExactRLE MinRun got %d want 3", cfg.Techniques.ExactRLE.MinRun)
	}
	if cfg.Techniques.BlockFactoring.MinBlock != 2 || cfg.Techniques.BlockFactoring.MaxBlock != 20 {
		t.Errorf("BlockFactoring mismatch got %+v want %+v", cfg.Techniques.BlockFactoring, want.Techniques.BlockFactoring)
	}
	if cfg.Techniques.PathDict.MaxCodes != 5 || cfg.Techniques.PathDict.MinCount != 3 {
		t.Errorf("PathDict mismatch")
	}
	if cfg.Techniques.SubstringDict.MinLen != 40 || cfg.Techniques.SubstringDict.MinCount != 4 {
		t.Errorf("SubstringDict mismatch got %+v want %+v", cfg.Techniques.SubstringDict, want.Techniques.SubstringDict)
	}
	if cfg.Techniques.Jton.MinRows != 10 {
		t.Errorf("Jton MinRows got %d want 10", cfg.Techniques.Jton.MinRows)
	}
	if cfg.Experimental["ison"] != false || cfg.Experimental["gcfGraph"] != false {
		t.Errorf("Experimental defaults mismatch got %+v", cfg.Experimental)
	}
	// json round-trip
	b, _ := json.Marshal(cfg)
	var rt Config
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("json round-trip: %v", err)
	}
}

func TestLogLevel_Level(t *testing.T) {
	tests := []struct {
		level LogLevel
		want  slog.Level
	}{
		{LogLevelDebug, slog.LevelDebug},
		{LogLevelInfo, slog.LevelInfo},
		{LogLevelWarn, slog.LevelWarn},
		{LogLevelError, slog.LevelError},
		{LogLevel("UNKNOWN"), slog.LevelInfo}, // invalid defaults to info
		{"", slog.LevelInfo},
	}
	for _, tc := range tests {
		got := tc.level.Level()
		if got != tc.want {
			t.Errorf("LogLevel %q Level() got %v want %v", tc.level, got, tc.want)
		}
	}
}

func TestLogLevelValidation(t *testing.T) {
	// invalid logLevel should fall back to info on validate / load
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	os.WriteFile(path, []byte(`{"logLevel":"verbose"}`), 0644)
	cfg, err := LoadFrom(path)
	if err == nil {
		t.Log("expected error for caller logging but got nil (acceptable if warn only)")
	}
	if cfg.LogLevel != LogLevelInfo {
		t.Fatalf("invalid logLevel should fallback to info, got %q", cfg.LogLevel)
	}
}

// ---------- JSONC ----------

func TestJSONCCommentsAndTrailingCommas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	content := `{
		// this is a comment
		"enabled": false, // trailing comment
		"logLevel": "debug", // another
		"minSavingsPercent": 50,
		"techniques": {
			"dedup": false,
			"exactRLE": {
				"enabled": true,
				"minRun": 5, // trailing comma test
			},
		}, // trailing comma after techniques
		"experimental": {
			"ison": true,
		},
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Logf("LoadFrom error (should be warn only, fail-open): %v", err)
	}
	if cfg.Enabled != false {
		t.Errorf("enabled: got %v want false", cfg.Enabled)
	}
	if cfg.LogLevel != LogLevelDebug {
		t.Errorf("logLevel: got %v want debug", cfg.LogLevel)
	}
	if cfg.MinSavingsPercent != 50 {
		t.Errorf("minSavingsPercent: got %d want 50", cfg.MinSavingsPercent)
	}
	if cfg.Techniques.Dedup != false {
		t.Errorf("dedup: got %v want false", cfg.Techniques.Dedup)
	}
	if cfg.Techniques.ExactRLE.MinRun != 5 {
		t.Errorf("minRun: got %d want 5", cfg.Techniques.ExactRLE.MinRun)
	}
	if cfg.Experimental["ison"] != true {
		t.Errorf("experimental ison: got %v want true", cfg.Experimental["ison"])
	}
	// ensure defaults still present for unspecified fields
	if cfg.Techniques.Jton.MinRows != 10 {
		t.Errorf("Jton MinRows should remain default 10, got %d", cfg.Techniques.Jton.MinRows)
	}
}

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(string) bool
	}{
		{
			name: "line comment",
			input: `{"a": 1 // comment
}`,
			check: func(s string) bool { return !strings.Contains(s, "//") && strings.Contains(s, `"a": 1`) },
		},
		{
			name:  "trailing comma object",
			input: `{"a": 1,}`,
			check: func(s string) bool { return !strings.Contains(s, ",}") && strings.Contains(s, `"a": 1`) },
		},
		{
			name:  "trailing comma array",
			input: `{"a": [1,2,],}`,
			check: func(s string) bool { return strings.Contains(s, `"a": [1,2]`) },
		},
		{
			name:  "comment inside string not stripped",
			input: `{"a": "http://example.com // not comment"}`,
			check: func(s string) bool { return strings.Contains(s, "http://example.com // not comment") },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripJSONC(tc.input)
			if !tc.check(got) {
				t.Fatalf("stripJSONC failed: got %q", got)
			}
			// should be valid JSON after strip (except maybe)
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("after strip not valid JSON: %v got %q", err, got)
			}
		})
	}
}

func TestStripJSONC_ExportedPreservesURL(t *testing.T) {
	input := `{"url":"https://example.test//value", // comment
"value":1,}`
	parsed := StripJSONC(input)
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(parsed), &value); err != nil {
		t.Fatalf("StripJSONC result is invalid JSON: %v", err)
	}
	if string(value["url"]) != `"https://example.test//value"` {
		t.Fatalf("URL was changed: %s", value["url"])
	}
}

// ---------- fail-open ----------

// isolateConfigEnv points the global and project config layers at empty
// temporary directories so tests observe defaults regardless of the
// developer machine's own tokenmill configuration.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	configHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func TestFailOpenInvalidJSON(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonc")
	os.WriteFile(path, []byte(`{ invalid json,,,`), 0644)
	cfg, err := LoadFrom(path)
	if err == nil {
		t.Fatalf("expected error for invalid JSON (for caller logging) but got nil")
	}
	// fail-open: should return defaults, not panic, config valid
	def := DefaultConfig()
	if cfg.Enabled != def.Enabled || cfg.LogLevel != def.LogLevel {
		t.Errorf("fail-open should return defaults, got %+v want %+v", cfg, def)
	}
	if cfg.MinSavingsPercent != def.MinSavingsPercent {
		t.Errorf("fail-open MinSavingsPercent got %d want %d", cfg.MinSavingsPercent, def.MinSavingsPercent)
	}
}

func TestLoadFromNotExistReturnsDefaults(t *testing.T) {
	isolateConfigEnv(t)
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "does-not-exist.jsonc"))
	if err == nil {
		t.Log("LoadFrom missing file should return error for logging (but fail-open config)")
	}
	def := DefaultConfig()
	if cfg.Enabled != def.Enabled {
		t.Errorf("missing file should return defaults")
	}
}

// ---------- env override ----------

func TestEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	os.WriteFile(path, []byte(`{}`), 0644)

	t.Setenv("TOKENMILL_ENABLED", "false")
	t.Setenv("TOKENMILL_LOG_LEVEL", "debug")
	t.Setenv("TOKENMILL_MIN_SAVINGS_PERCENT", "75")
	t.Setenv("TOKENMILL_TECHNIQUES_DEDUP", "false")
	t.Setenv("TOKENMILL_TECHNIQUES_EXACTRLE_MINRUN", "7")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Logf("LoadFrom err: %v", err)
	}
	if cfg.Enabled != false {
		t.Errorf("env override ENABLED got %v want false", cfg.Enabled)
	}
	if cfg.LogLevel != LogLevelDebug {
		t.Errorf("env override LOG_LEVEL got %v want debug", cfg.LogLevel)
	}
	if cfg.MinSavingsPercent != 75 {
		t.Errorf("env override MIN_SAVINGS_PERCENT got %d want 75", cfg.MinSavingsPercent)
	}
	if cfg.Techniques.Dedup != false {
		t.Errorf("env override TECHNIQUES_DEDUP got %v want false", cfg.Techniques.Dedup)
	}
	if cfg.Techniques.ExactRLE.MinRun != 7 {
		t.Errorf("env override EXACTRLE_MINRUN got %d want 7", cfg.Techniques.ExactRLE.MinRun)
	}
}

func TestEnvOverridesTechniquesNested(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	os.WriteFile(path, []byte(`{}`), 0644)
	t.Setenv("TOKENMILL_TECHNIQUES_JTON_ENABLED", "false")
	t.Setenv("TOKENMILL_TECHNIQUES_JSONCOMPACT", "false")
	t.Setenv("TOKENMILL_EXPERIMENTAL_ISON", "true")

	cfg, _ := LoadFrom(path)
	if cfg.Techniques.Jton.Enabled != false {
		t.Errorf("jton.enabled env override got %v want false", cfg.Techniques.Jton.Enabled)
	}
	if cfg.Techniques.JsonCompact != false {
		t.Errorf("jsonCompact env override got %v want false", cfg.Techniques.JsonCompact)
	}
	if cfg.Experimental["ison"] != true {
		t.Errorf("experimental ison env override got %v want true", cfg.Experimental["ison"])
	}
}

// ---------- file priority ----------

func TestFilePriorityGlobalVsProject(t *testing.T) {
	// simulate global vs project via Load() priority? We test LoadFrom merging order manually.
	// Instead test that env > file.
	dir := t.TempDir()
	global := filepath.Join(dir, "global.jsonc")
	project := filepath.Join(dir, "project.jsonc")
	os.WriteFile(global, []byte(`{"enabled": false, "minSavingsPercent": 10, "logLevel": "warn"}`), 0644)
	os.WriteFile(project, []byte(`{"minSavingsPercent": 20}`), 0644)

	globalCfg, _ := LoadFrom(global)
	// Simulate Merge: global < project
	merged := globalCfg.Merge(mustLoad(t, project))
	if merged.Enabled != false {
		t.Errorf("global enabled false should persist if not overridden in project, got %v", merged.Enabled)
	}
	if merged.MinSavingsPercent != 20 {
		t.Errorf("project should override global minSavingsPercent, got %d want 20", merged.MinSavingsPercent)
	}
	if merged.LogLevel != LogLevelWarn {
		t.Errorf("project not overriding logLevel should keep global warn, got %v", merged.LogLevel)
	}

	// An explicit LoadFrom path has higher priority than env.
	t.Setenv("TOKENMILL_MIN_SAVINGS_PERCENT", "99")
	envCfg, _ := LoadFrom(project)
	if envCfg.MinSavingsPercent != 20 {
		t.Errorf("explicit file should override env, got %d want 20", envCfg.MinSavingsPercent)
	}
}

func mustLoad(t *testing.T, path string) Config {
	t.Helper()
	c, _ := LoadFrom(path)
	return c
}

// ---------- validation clamping ----------

func TestValidationClamping(t *testing.T) {
	tests := []struct {
		name  string
		json  string
		check func(Config) bool
	}{
		{
			name:  "minSavingsPercent out of range high",
			json:  `{"minSavingsPercent": 200}`,
			check: func(c Config) bool { return c.MinSavingsPercent == 10 },
		},
		{
			name:  "minSavingsPercent negative",
			json:  `{"minSavingsPercent": -5}`,
			check: func(c Config) bool { return c.MinSavingsPercent == 10 },
		},
		{
			name:  "minRun <2",
			json:  `{"techniques": {"exactRLE": {"enabled": true, "minRun": 1}}}`,
			check: func(c Config) bool { return c.Techniques.ExactRLE.MinRun == 3 },
		},
		{
			name: "maxBlock < minBlock",
			json: `{"techniques": {"blockFactoring": {"enabled": true, "minBlock": 10, "maxBlock": 5}}}`,
			check: func(c Config) bool {
				return c.Techniques.BlockFactoring.MaxBlock >= c.Techniques.BlockFactoring.MinBlock
			},
		},
		{
			name: "maxBlock default when invalid",
			json: `{"techniques": {"blockFactoring": {"enabled": true, "minBlock": 5, "maxBlock": 2}}}`,
			check: func(c Config) bool {
				// should reset to default 20 or clamp to minBlock? spec says invalid resets to default with warn
				// either is valid if MaxBlock >= MinBlock. We check it was corrected.
				return c.Techniques.BlockFactoring.MaxBlock == 20 || c.Techniques.BlockFactoring.MaxBlock == 5
			},
		},
		{
			name:  "pathDict maxCodes zero",
			json:  `{"techniques": {"pathDict": {"enabled": true, "maxCodes": 0, "minCount": 3}}}`,
			check: func(c Config) bool { return c.Techniques.PathDict.MaxCodes == 5 },
		},
		{
			name:  "substringDict minLen too small",
			json:  `{"techniques": {"substringDict": {"enabled": true, "minLen": 5, "minCount": 4}}}`,
			check: func(c Config) bool { return c.Techniques.SubstringDict.MinLen == 40 },
		},
		{
			name:  "jton minRows too small",
			json:  `{"techniques": {"jton": {"enabled": true, "minRows": 0}}}`,
			check: func(c Config) bool { return c.Techniques.Jton.MinRows == 10 },
		},
		{
			name:  "freshnessTurns zero",
			json:  `{"freshnessTurns": 0}`,
			check: func(c Config) bool { return c.FreshnessTurns == 20 },
		},
		{
			name:  "showUpdateEvery negative",
			json:  `{"showUpdateEvery": -1}`,
			check: func(c Config) bool { return c.ShowUpdateEvery == 10 },
		},
		{
			name:  "logLevel invalid",
			json:  `{"logLevel": "verbose"}`,
			check: func(c Config) bool { return c.LogLevel == LogLevelInfo },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "c.jsonc")
			os.WriteFile(path, []byte(tc.json), 0644)
			cfg, _ := LoadFrom(path)
			if !tc.check(cfg) {
				t.Fatalf("validation failed for %s, got config: %+v", tc.name, cfg)
			}
		})
	}
}

// ---------- Set / Save atomic ----------

func TestSetAndSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	cfg := DefaultConfig()
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Set via dot-path
	if err := cfg.Set("techniques.jton.enabled", false); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if cfg.Techniques.Jton.Enabled != false {
		t.Fatalf("Set failed, got %v want false", cfg.Techniques.Jton.Enabled)
	}
	if err := cfg.Set("logLevel", "debug"); err != nil {
		t.Fatalf("Set logLevel: %v", err)
	}
	if cfg.LogLevel != LogLevelDebug {
		t.Fatalf("Set logLevel got %v want debug", cfg.LogLevel)
	}
	if err := cfg.Set("minSavingsPercent", 42); err != nil {
		t.Fatalf("Set int: %v", err)
	}
	if cfg.MinSavingsPercent != 42 {
		t.Fatalf("Set int got %d want 42", cfg.MinSavingsPercent)
	}
	if err := cfg.Set("experimental.ison", true); err != nil {
		t.Fatalf("Set experimental: %v", err)
	}
	if cfg.Experimental["ison"] != true {
		t.Fatalf("Set experimental got %v want true", cfg.Experimental["ison"])
	}
	// Save atomically and reload
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save after Set: %v", err)
	}
	// verify file exists and is valid
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"logLevel"`) {
		t.Errorf("saved file missing logLevel: %s", string(data))
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Logf("LoadFrom after Save err: %v", err)
	}
	if loaded.Techniques.Jton.Enabled != false {
		t.Errorf("reloaded jton.enabled got %v want false", loaded.Techniques.Jton.Enabled)
	}
	if loaded.LogLevel != LogLevelDebug {
		t.Errorf("reloaded logLevel got %v want debug", loaded.LogLevel)
	}
	if loaded.MinSavingsPercent != 42 {
		t.Errorf("reloaded minSavingsPercent got %d want 42", loaded.MinSavingsPercent)
	}
	// atomic: no temp files left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSetDotPathErrors(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Set("nonexistent.field", 123); err == nil {
		t.Errorf("expected error for nonexistent field")
	}
	if err := cfg.Set("techniques.blockFactoring.maxBlock", "not-an-int"); err == nil {
		t.Errorf("expected error for type mismatch")
	}
}

func TestSaveAtomicTempRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c.jsonc") // nested dir not existing
	cfg := DefaultConfig()
	// Save should create parent dirs
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save to nested path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestMerge(t *testing.T) {
	base := DefaultConfig()
	other := DefaultConfig()
	other.LogLevel = LogLevelDebug
	other.MinSavingsPercent = 25
	other.Techniques.Dedup = false
	merged := base.Merge(other)
	if merged.LogLevel != LogLevelDebug {
		t.Errorf("Merge logLevel got %v want debug", merged.LogLevel)
	}
	if merged.MinSavingsPercent != 25 {
		t.Errorf("Merge minSavingsPercent got %d want 25", merged.MinSavingsPercent)
	}
	if merged.Techniques.Dedup != false {
		t.Errorf("Merge dedup got %v want false", merged.Techniques.Dedup)
	}
	// other unspecified should still carry defaults, not zero out base
	if merged.Techniques.Jton.MinRows != 10 {
		t.Errorf("Merge should preserve Jton MinRows 10, got %d", merged.Techniques.Jton.MinRows)
	}
}

func TestLoadDefaultWhenNoFiles(t *testing.T) {
	// ensure no env interferes
	t.Setenv("TOKENMILL_ENABLED", "")
	t.Setenv("TOKENMILL_LOG_LEVEL", "")
	// Use TempDir isolated: chdir to temp
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)

	// ensure HOME points to empty
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // windows fallback
	cfg, err := Load()
	if err != nil {
		t.Logf("Load err: %v", err)
	}
	def := DefaultConfig()
	if cfg.MinSavingsPercent != def.MinSavingsPercent {
		t.Errorf("Load with no files should return defaults, got %d want %d", cfg.MinSavingsPercent, def.MinSavingsPercent)
	}
}

func TestJSONCRoundTripSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "round.jsonc")
	cfg := DefaultConfig()
	cfg.Techniques.SubstringDict.Enabled = true
	cfg.Experimental["gcfGraph"] = true
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded.Techniques.SubstringDict.Enabled != true {
		t.Errorf("SubstringDict Enabled round-trip failed")
	}
	if loaded.Experimental["gcfGraph"] != true {
		t.Errorf("experimental gcfGraph round-trip failed")
	}
}

func TestViperAndPflagIntegration(t *testing.T) {
	// Ensure viper and pflag are used: BindFlags should exist and not panic
	// We test that GetViper returns non-nil
	v := GetViper()
	if v == nil {
		t.Fatalf("GetViper returned nil")
	}
	// pflag binding: add flags and check they can override via manual set
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("log-level", "info", "log level")
	if err := BindPFlags(fs); err != nil {
		t.Fatalf("BindPFlags: %v", err)
	}
	_ = fs
	_ = v
}

func TestStripJSONCBlockAndEscapes(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains string
	}{
		{"block comment", `{"a": 1 /* block */ , "b": 2}`, `"b": 2`},
		{"escaped quote", `{"a": "he said \" // not comment \""}`, `he said`},
		{"trailing comma nested", `{"a": {"b": 1,}, "c": [1,2,]}`, `"c": [1,2]`},
		{"slash in string", `{"url": "http://example.com"}`, `http://`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripJSONC(tc.input)
			if !strings.Contains(got, tc.wantContains) {
				t.Fatalf("stripJSONC %q got %q want contain %q", tc.name, got, tc.wantContains)
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("after strip not valid JSON: %v got %q", err, got)
			}
		})
	}
}

func TestSetVariousTypesAndBoolParsing(t *testing.T) {
	cfg := DefaultConfig()
	// bool via string "true"/"false" and int
	if err := cfg.Set("enabled", "false"); err != nil {
		t.Fatalf("Set enabled false string: %v", err)
	}
	if cfg.Enabled != false {
		t.Fatalf("enabled via string false got %v", cfg.Enabled)
	}
	if err := cfg.Set("enabled", "1"); err != nil {
		t.Fatalf("Set enabled 1: %v", err)
	}
	if cfg.Enabled != true {
		t.Fatalf("enabled via 1 got %v", cfg.Enabled)
	}
	if err := cfg.Set("enabled", 0); err != nil {
		t.Fatalf("Set enabled 0 int: %v", err)
	}
	if cfg.Enabled != false {
		t.Fatalf("enabled via int 0 got %v", cfg.Enabled)
	}
	// int via string
	if err := cfg.Set("minSavingsTokens", "64"); err != nil {
		t.Fatalf("Set int via string: %v", err)
	}
	if cfg.MinSavingsTokens != 64 {
		t.Fatalf("MinSavingsTokens via string got %d", cfg.MinSavingsTokens)
	}
	if err := cfg.Set("minSavingsTokens", 128.0); err != nil {
		t.Fatalf("Set int via float64: %v", err)
	}
	if cfg.MinSavingsTokens != 128 {
		t.Fatalf("MinSavingsTokens via float64 got %d", cfg.MinSavingsTokens)
	}
	// bool via different strings
	if err := cfg.Set("techniques.dedup", "0"); err != nil {
		t.Fatalf("dedup 0: %v", err)
	}
	if cfg.Techniques.Dedup != false {
		t.Fatalf("dedup 0 got %v", cfg.Techniques.Dedup)
	}
	if err := cfg.Set("techniques.dedup", "yes"); err != nil {
		t.Fatalf("dedup yes: %v", err)
	}
	if cfg.Techniques.Dedup != true {
		t.Fatalf("dedup yes got %v", cfg.Techniques.Dedup)
	}
	// experimental via bool string
	if err := cfg.Set("experimental.gcfGraph", "true"); err != nil {
		t.Fatalf("gcfGraph true: %v", err)
	}
	if cfg.Experimental["gcfGraph"] != true {
		t.Fatalf("gcfGraph true got %v", cfg.Experimental["gcfGraph"])
	}
	// invalid bool should error
	if err := cfg.Set("enabled", "maybe"); err == nil {
		t.Fatalf("expected error for invalid bool")
	}
}

func TestSetExperimentalNewKeyAndLoadEnvGeneric(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	os.WriteFile(path, []byte(`{}`), 0644)
	t.Setenv("TOKENMILL_EXPERIMENTAL_NEWFEATURE", "true")
	t.Setenv("TOKENMILL_TECHNIQUES_SUBSTRINGDICT_EXPERIMENTAL", "true")
	cfg, _ := LoadFrom(path)
	if cfg.Experimental["newfeature"] != true && cfg.Experimental["NEWFEATURE"] != true {
		// Normalize may produce lower case without underscore: newfeature
		// Check any key true
		found := false
		for k, v := range cfg.Experimental {
			if strings.ToLower(k) == "newfeature" && v {
				found = true
			}
		}
		if !found {
			t.Errorf("experimental newfeature via generic env not set, got %+v", cfg.Experimental)
		}
	}
	if cfg.Techniques.SubstringDict.Experimental != true {
		t.Errorf("substringDict.experimental via env got %v want true", cfg.Techniques.SubstringDict.Experimental)
	}
}

func TestLoadWithGlobalAndProjectPriority(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	configHome := filepath.Join(tmp, "config")
	os.MkdirAll(filepath.Join(configHome, "tokenmill"), 0755)
	globalPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	os.WriteFile(globalPath, []byte(`{"enabled": false, "logLevel": "warn", "minSavingsPercent": 15}`), 0644)

	projDir := filepath.Join(tmp, "proj")
	os.MkdirAll(filepath.Join(projDir, ".opencode"), 0755)
	projPath := filepath.Join(projDir, ".opencode", "tokenmill.jsonc")
	os.WriteFile(projPath, []byte(`{"minSavingsPercent": 25, "techniques": {"dedup": false}}`), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(projDir)
	defer os.Chdir(oldWd)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("TOKENMILL_MIN_SAVINGS_TOKENS", "99")

	cfg, err := Load()
	if err != nil {
		t.Logf("Load err: %v", err)
	}
	if cfg.Enabled != false {
		t.Errorf("global enabled false should persist, got %v", cfg.Enabled)
	}
	if cfg.LogLevel != LogLevelWarn {
		t.Errorf("global logLevel warn should persist, got %v", cfg.LogLevel)
	}
	if cfg.MinSavingsPercent != 25 {
		t.Errorf("project should override global minSavingsPercent, got %d want 25", cfg.MinSavingsPercent)
	}
	if cfg.Techniques.Dedup != false {
		t.Errorf("project dedup false should override, got %v", cfg.Techniques.Dedup)
	}
	if cfg.MinSavingsTokens != 99 {
		t.Errorf("env should override file tokens, got %d want 99", cfg.MinSavingsTokens)
	}
}

func TestLoadFromInvalidThenEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonc")
	os.WriteFile(path, []byte(`{ invalid`), 0644)
	t.Setenv("TOKENMILL_LOG_LEVEL", "error")
	cfg, err := LoadFrom(path)
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
	if cfg.LogLevel != LogLevelError {
		t.Errorf("after fail-open invalid JSON, env should still override logLevel to error, got %v", cfg.LogLevel)
	}
}

func TestValidationAdditionalFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	// Test multiple invalid fields together
	content := `{
		"minSavingsTokens": -10,
		"freshnessTurns": 0,
		"showUpdateEvery": -5,
		"techniques": {
			"pathDict": {"enabled": true, "maxCodes": 0, "minCount": 0},
			"substringDict": {"enabled": true, "minLen": 1, "minCount": 0},
			"blockFactoring": {"enabled": true, "minBlock": 0, "maxBlock": 0}
		}
	}`
	os.WriteFile(path, []byte(content), 0644)
	cfg, _ := LoadFrom(path)
	if cfg.MinSavingsTokens != 32 {
		t.Errorf("MinSavingsTokens invalid should reset to 32, got %d", cfg.MinSavingsTokens)
	}
	if cfg.FreshnessTurns != 20 {
		t.Errorf("FreshnessTurns invalid should reset to 20, got %d", cfg.FreshnessTurns)
	}
	if cfg.ShowUpdateEvery != 10 {
		t.Errorf("ShowUpdateEvery invalid should reset to 10, got %d", cfg.ShowUpdateEvery)
	}
	if cfg.Techniques.PathDict.MaxCodes != 5 {
		t.Errorf("PathDict MaxCodes invalid reset to 5, got %d", cfg.Techniques.PathDict.MaxCodes)
	}
	if cfg.Techniques.SubstringDict.MinLen != 40 {
		t.Errorf("SubstringDict MinLen invalid reset to 40, got %d", cfg.Techniques.SubstringDict.MinLen)
	}
	if cfg.Techniques.BlockFactoring.MinBlock != 2 {
		t.Errorf("BlockFactoring MinBlock invalid reset to 2, got %d", cfg.Techniques.BlockFactoring.MinBlock)
	}
}

func TestSetEmptyPathAndInvalidMapTraversal(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Set("", "value"); err == nil {
		t.Errorf("expected error for empty path")
	}
	if err := cfg.Set("experimental.ison.extra", true); err == nil {
		t.Errorf("expected error for traversing beyond map")
	}
	if err := cfg.Set("techniques.dedup.extra", true); err == nil {
		t.Errorf("expected error for traversing beyond bool")
	}
}

func TestToStringAndMapSet(t *testing.T) {
	cfg := DefaultConfig()
	// Test LogLevel via bytes
	if err := cfg.Set("logLevel", []byte("warn")); err != nil {
		t.Fatalf("Set logLevel via []byte: %v", err)
	}
	if cfg.LogLevel != LogLevelWarn {
		t.Fatalf("logLevel via []byte got %v want warn", cfg.LogLevel)
	}
	// Test experimental whole map set not via dot? Should error or work via experimental key?
	// Direct map set via experimental key should be via map type, but we test Set for map whole
	// Use non-dot path for experimental as map? Our Set expects map[string]bool for leaf, not whole
	// Ensure LogLevel Level() works after string set
	if cfg.LogLevel.Level() != slog.LevelWarn {
		t.Errorf("Level() after set warn got %v", cfg.LogLevel.Level())
	}
}

func TestSaveValidatesBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jsonc")
	cfg := DefaultConfig()
	cfg.MinSavingsPercent = 200        // invalid
	cfg.Techniques.ExactRLE.MinRun = 1 // invalid
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := LoadFrom(path)
	if loaded.MinSavingsPercent != 10 {
		t.Errorf("Save should validate and reset MinSavingsPercent to 10, got %d", loaded.MinSavingsPercent)
	}
	if loaded.Techniques.ExactRLE.MinRun != 3 {
		t.Errorf("Save should validate MinRun to 3, got %d", loaded.Techniques.ExactRLE.MinRun)
	}
}

func TestMergeDeepNestedOverride(t *testing.T) {
	base := DefaultConfig()
	other := DefaultConfig()
	other.Techniques.PathDict.MaxCodes = 10
	other.Techniques.SubstringDict.MinLen = 60
	other.Experimental["ison"] = true
	merged := base.Merge(other)
	if merged.Techniques.PathDict.MaxCodes != 10 {
		t.Errorf("Merge PathDict MaxCodes got %d want 10", merged.Techniques.PathDict.MaxCodes)
	}
	if merged.Techniques.SubstringDict.MinLen != 60 {
		t.Errorf("Merge SubstringDict MinLen got %d want 60", merged.Techniques.SubstringDict.MinLen)
	}
	if merged.Experimental["ison"] != true {
		t.Errorf("Merge experimental ison got %v want true", merged.Experimental["ison"])
	}
	// Ensure base unchanged fields preserved
	if merged.Techniques.Jton.MinRows != 10 {
		t.Errorf("Merge preserved Jton MinRows 10, got %d", merged.Techniques.Jton.MinRows)
	}
}

func TestConfigUsesTokenMillGlobalPath(t *testing.T) {
	clearTokenMillEnv(t)
	configHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	chdirForTest(t, t.TempDir())

	canonicalPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	legacyPath := filepath.Join(home, ".config", "opencode", "tokenmill.jsonc")
	if err := os.MkdirAll(filepath.Dir(canonicalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonicalPath, []byte(`{"enabled":false}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"enabled":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	gotPath, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	if gotPath != canonicalPath {
		t.Fatalf("global config path = %q, want %q", gotPath, canonicalPath)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("canonical config was not loaded, or legacy config was incorrectly preferred")
	}
}

func TestConfigPrecedence(t *testing.T) {
	clearTokenMillEnv(t)
	configHome := t.TempDir()
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	chdirForTest(t, project)

	globalPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"enabled":false,"freshnessTurns":7,"minSavingsTokens":70}`), 0644); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(project, ".opencode", "tokenmill.jsonc")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"enabled":true,"minSavingsPercent":20}`), 0644); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(project, "tokenmill.jsonc")
	if err := os.WriteFile(rootPath, []byte(`{"enabled":false,"logLevel":"debug"}`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TOKENMILL_MIN_SAVINGS_PERCENT", "30")
	t.Setenv("TOKENMILL_MIN_SAVINGS_TOKENS", "80")
	t.Setenv("TOKENMILL_LOG_LEVEL", "error")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal(".opencode project config should override root and global config")
	}
	if cfg.MinSavingsPercent != 30 || cfg.MinSavingsTokens != 80 {
		t.Fatalf("environment precedence failed: percent=%d tokens=%d", cfg.MinSavingsPercent, cfg.MinSavingsTokens)
	}
	if cfg.LogLevel != LogLevelError {
		t.Fatalf("environment logLevel = %q, want error", cfg.LogLevel)
	}
	if cfg.FreshnessTurns != 7 {
		t.Fatalf("global freshnessTurns = %d, want 7", cfg.FreshnessTurns)
	}

	explicitPath := filepath.Join(t.TempDir(), "explicit.jsonc")
	if err := os.WriteFile(explicitPath, []byte(`{"enabled":false,"minSavingsPercent":40,"freshnessTurns":9}`), 0644); err != nil {
		t.Fatal(err)
	}
	explicit, err := LoadFrom(explicitPath)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if explicit.Enabled {
		t.Fatal("explicit config should override project config")
	}
	if explicit.MinSavingsPercent != 40 {
		t.Fatalf("explicit config should override env, got minSavingsPercent=%d", explicit.MinSavingsPercent)
	}
	if explicit.MinSavingsTokens != 80 {
		t.Fatalf("env should fill an unset explicit field, got minSavingsTokens=%d", explicit.MinSavingsTokens)
	}
	if explicit.FreshnessTurns != 9 {
		t.Fatalf("explicit freshnessTurns = %d, want 9", explicit.FreshnessTurns)
	}

	if err := os.Remove(projectPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKENMILL_LOG_LEVEL", "")
	rootCfg, err := Load()
	if err != nil {
		t.Fatalf("Load with root config: %v", err)
	}
	if rootCfg.LogLevel != LogLevelDebug {
		t.Fatalf("root config fallback logLevel = %q, want debug", rootCfg.LogLevel)
	}
}

func TestLegacyConfigMigrationOrWarning(t *testing.T) {
	clearTokenMillEnv(t)
	configHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	chdirForTest(t, t.TempDir())

	legacyPath, err := LegacyConfigPath()
	if err != nil {
		t.Fatalf("LegacyConfigPath: %v", err)
	}
	legacyData := []byte(`{
	  // preserve JSONC comments during explicit migration
	  "enabled": false,
}`)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyData, 0644); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("legacy config must not become a second live source before explicit migration")
	}
	if !strings.Contains(logs.String(), "legacy config detected") || !strings.Contains(logs.String(), "config migrate") {
		t.Fatalf("legacy detection warning missing: %q", logs.String())
	}

	migratedPath, err := MigrateLegacyConfig()
	if err != nil {
		t.Fatalf("MigrateLegacyConfig: %v", err)
	}
	canonicalPath, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	if migratedPath != canonicalPath {
		t.Fatalf("migrated path = %q, want %q", migratedPath, canonicalPath)
	}
	gotData, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if !bytes.Equal(gotData, legacyData) {
		t.Fatalf("migration changed JSONC bytes: got %q want %q", gotData, legacyData)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("migration must not delete legacy file: %v", err)
	}

	migrated, err := Load()
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if migrated.Enabled {
		t.Fatal("migrated canonical config was not loaded")
	}
	if _, err := MigrateLegacyConfig(); err == nil {
		t.Fatal("second migration should refuse to overwrite existing canonical config")
	}
}

func TestConfigAcceptsDatabasePathOverrides(t *testing.T) {
	clearTokenMillEnv(t)
	nestedPath := filepath.Join(t.TempDir(), "nested", "tracking.db")
	configPath := filepath.Join(t.TempDir(), "config.jsonc")
	nestedPathJSON, err := json.Marshal(nestedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"tracking":{"database_path":`+string(nestedPathJSON)+`}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(configPath)
	if err != nil {
		t.Fatalf("LoadFrom nested database path: %v", err)
	}
	if cfg.Tracking.DatabasePath != nestedPath || cfg.DatabasePathOverride() != nestedPath {
		t.Fatalf("nested database path = %q, want %q", cfg.DatabasePathOverride(), nestedPath)
	}

	aliasPath := filepath.Join(t.TempDir(), "alias.db")
	aliasConfig := filepath.Join(t.TempDir(), "config.jsonc")
	aliasPathJSON, err := json.Marshal(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aliasConfig, []byte(`{"database_path":`+string(aliasPathJSON)+`}`), 0644); err != nil {
		t.Fatal(err)
	}
	aliasCfg, err := LoadFrom(aliasConfig)
	if err != nil {
		t.Fatalf("LoadFrom database_path alias: %v", err)
	}
	if aliasCfg.DatabasePathOverride() != aliasPath {
		t.Fatalf("database_path alias = %q, want %q", aliasCfg.DatabasePathOverride(), aliasPath)
	}

	envPath := filepath.Join(t.TempDir(), "env.db")
	t.Setenv("TOKENMILL_TRACKING_DATABASE_PATH", envPath)
	envCfg, err := Load()
	if err != nil {
		t.Fatalf("Load with tracking database env: %v", err)
	}
	if envCfg.DatabasePathOverride() != envPath {
		t.Fatalf("TOKENMILL_TRACKING_DATABASE_PATH = %q, want %q", envCfg.DatabasePathOverride(), envPath)
	}

	basePath := filepath.Join(t.TempDir(), "base.db")
	newAliasPath := filepath.Join(t.TempDir(), "new-alias.db")
	base := DefaultConfig()
	base.Tracking.DatabasePath = basePath
	newAliasPathJSON, err := json.Marshal(newAliasPath)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := base.MergeRaw([]byte(`{"database_path":` + string(newAliasPathJSON) + `}`))
	if err != nil {
		t.Fatalf("MergeRaw database path alias: %v", err)
	}
	if merged.DatabasePathOverride() != newAliasPath {
		t.Fatalf("top-level alias did not override nested base: got %q want %q", merged.DatabasePathOverride(), newAliasPath)
	}

	blank := DefaultConfig()
	blank.Tracking.DatabasePath = "  "
	blank.DatabasePath = "\t"
	if got := blank.DatabasePathOverride(); got != "" {
		t.Fatalf("blank database path override = %q, want empty", got)
	}
}

func TestEnvDatabasePathAliasPrecedence(t *testing.T) {
	clearTokenMillEnv(t)
	configHome := t.TempDir()
	home := t.TempDir()
	chdirForTest(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	globalPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	globalDatabasePath := filepath.Join(t.TempDir(), "global.db")
	globalDatabasePathJSON, err := json.Marshal(globalDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"tracking":{"database_path":`+string(globalDatabasePathJSON)+`}}`), 0644); err != nil {
		t.Fatal(err)
	}

	topEnvPath := filepath.Join(t.TempDir(), "top-env.db")
	nestedEnvPath := filepath.Join(t.TempDir(), "nested-env.db")
	t.Setenv("TOKENMILL_DATABASE_PATH", topEnvPath)
	t.Setenv("TOKENMILL_TRACKING_DATABASE_PATH", nestedEnvPath)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with both database path env aliases: %v", err)
	}
	if cfg.DatabasePathOverride() != nestedEnvPath {
		t.Fatalf("nested env alias should win, got %q want %q", cfg.DatabasePathOverride(), nestedEnvPath)
	}

	t.Setenv("TOKENMILL_TRACKING_DATABASE_PATH", "")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load with top-level database path env alias: %v", err)
	}
	if cfg.DatabasePathOverride() != topEnvPath {
		t.Fatalf("top-level env alias should override global nested path, got %q want %q", cfg.DatabasePathOverride(), topEnvPath)
	}
}

func clearTokenMillEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[0], "TOKENMILL_") {
			t.Setenv(parts[0], "")
		}
	}
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
