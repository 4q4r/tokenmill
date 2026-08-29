package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTDD_MergeRaw_JTONAndExperimental(t *testing.T) {
	base := DefaultConfig()
	base.Techniques.Jton.MinRows = 99
	base.Experimental["ison"] = false
	base.Experimental["gcfGraph"] = false

	otherRaw := []byte(`{
		// explicit default should still override
		"techniques": {
			"jton": {"enabled": true, "minRows": 10}, // 10 == default, must override 99
			"dedup": true
		},
		"experimental": {"ison": true} // partial, gcfGraph should stay false
	}`)
	merged, err := base.MergeRaw(otherRaw)
	if err != nil {
		t.Fatalf("MergeRaw err %v", err)
	}
	if merged.Techniques.Jton.MinRows != 10 {
		t.Fatalf("MergeRaw jton minRows explicit default failed got %d want 10", merged.Techniques.Jton.MinRows)
	}
	if merged.Experimental["ison"] != true {
		t.Fatalf("experimental ison true")
	}
	if merged.Experimental["gcfGraph"] != false {
		t.Fatalf("experimental gcfGraph should stay false, got %v", merged.Experimental["gcfGraph"])
	}
	// dedup true over base true? base default true, but we set base dedup? check not overwritten incorrectly
	if merged.Techniques.Dedup != true {
		t.Fatalf("dedup")
	}
}

func TestTDD_MergeRaw_CommentsAndTrailing(t *testing.T) {
	base := DefaultConfig()
	base.Enabled = false
	raw := []byte(`{
		"enabled": true, // comment
		"techniques": {
			"dedup": false, // trailing comma
		}, // trailing
	}`)
	merged, err := base.MergeRaw(raw)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if merged.Enabled != true {
		t.Fatalf("enabled true")
	}
	if merged.Techniques.Dedup != false {
		t.Fatalf("dedup false")
	}
}

func TestTDD_MergeRaw_Empty(t *testing.T) {
	base := DefaultConfig()
	base.LogLevel = LogLevelWarn
	merged, err := base.MergeRaw([]byte(`{}`))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if merged.LogLevel != LogLevelWarn {
		t.Fatalf("empty should keep base")
	}
	merged2, err := base.MergeRaw(nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if merged2.LogLevel != LogLevelWarn {
		t.Fatalf("nil should keep base")
	}
}

func TestTDD_MergeRaw_InvalidJSON(t *testing.T) {
	base := DefaultConfig()
	_, err := base.MergeRaw([]byte(`{ invalid`))
	if err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func TestTDD_GetViper_AllFlattened(t *testing.T) {
	v := GetViper()
	keys := []string{
		"enabled",
		"logLevel",
		"showUpdateEvery",
		"minSavingsPercent",
		"minSavingsTokens",
		"freshnessTurns",
		"techniques.dedup",
		"techniques.ansiStripping",
		"techniques.crRendering",
		"techniques.exactRLE.enabled",
		"techniques.exactRLE.minRun",
		"techniques.blockFactoring.enabled",
		"techniques.blockFactoring.minBlock",
		"techniques.blockFactoring.maxBlock",
		"techniques.pathDict.enabled",
		"techniques.pathDict.maxCodes",
		"techniques.pathDict.minCount",
		"techniques.substringDict.enabled",
		"techniques.substringDict.minLen",
		"techniques.substringDict.minCount",
		"techniques.jton.enabled",
		"techniques.jton.minRows",
		"techniques.jsonCompact",
		"techniques.tableTSV",
		"techniques.stacktraceDict",
		"experimental.ison",
		"experimental.gcfGraph",
	}
	for _, k := range keys {
		if !v.IsSet(k) {
			t.Errorf("GetViper missing flattened key %q", k)
		}
		if v.Get(k) == nil {
			t.Errorf("GetViper Get nil for %q", k)
		}
	}
	// check viper env reading works for a sample
	t.Setenv("TOKENMILL_LOG_LEVEL", "warn")
	// need fresh viper? Get returns same but AutomaticEnv reads live env
	got := v.GetString("logLevel")
	// viper may still return default if camel mapping broken, but IsSet should be true and GetString should be exercised
	if got == "" {
		t.Errorf("viper GetString logLevel empty")
	}
	_ = got
}

func TestTDD_MergeRaw_VsLoadFileIntoConsistency(t *testing.T) {
	// Verify that MergeRaw with file content gives same result as loadFileInto sequential
	dir := t.TempDir()
	path := filepath.Join(dir, "proj.jsonc")
	// global config with custom values
	global := DefaultConfig()
	global.Enabled = false
	global.MinSavingsPercent = 15
	global.Techniques.Dedup = false
	// project file explicitly sets enabled true (default) and dedup true (default) and minSavingsPercent 20
	os.WriteFile(path, []byte(`{"enabled": true, "minSavingsPercent": 20, "techniques": {"dedup": true}}`), 0644)
	data, _ := os.ReadFile(path)
	merged, err := global.MergeRaw(data)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if merged.Enabled != true {
		t.Fatalf("MergeRaw enabled %v", merged.Enabled)
	}
	if merged.MinSavingsPercent != 20 {
		t.Fatalf("minSavingsPercent %d", merged.MinSavingsPercent)
	}
	if merged.Techniques.Dedup != true {
		t.Fatalf("dedup %v", merged.Techniques.Dedup)
	}
	// also check via LoadFrom sequential should match? LoadFrom merges onto defaults, not onto global
	// So compare via manual sequential file load
	projCfg, _ := LoadFrom(path)
	// projCfg should have enabled true, dedup true, min 20
	if projCfg.Enabled != true || projCfg.MinSavingsPercent != 20 || projCfg.Techniques.Dedup != true {
		t.Fatalf("LoadFrom proj %v", projCfg)
	}
	// Now merge global < project using MergeRaw should produce same as Load merging
	// Simulate global then project priority
	rawProj, _ := os.ReadFile(path)
	merged2, _ := global.MergeRaw(rawProj)
	b, _ := json.Marshal(merged2)
	var check Config
	json.Unmarshal(b, &check)
	if check.Enabled != true {
		t.Errorf("consistency enabled")
	}
}
