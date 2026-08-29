package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCodecTechniqueDefaultsFollowSafetyPolicy(t *testing.T) {
	cfg := DefaultConfig()
	want := map[string]bool{
		"jcs":                true,
		"jsonNumber":         true,
		"markdownWhitespace": false,
		"opaqueDict":         false,
		"crossCallPack":      false,
		"csvCanonical":       false,
		"symbolTable":        false,
		"diffLogFold":        false,
	}

	got := map[string]bool{
		"jcs":                cfg.Techniques.JCS,
		"jsonNumber":         cfg.Techniques.JsonNumber,
		"markdownWhitespace": cfg.Techniques.MarkdownWhitespace,
		"opaqueDict":         cfg.Techniques.OpaqueDict,
		"crossCallPack":      cfg.Techniques.CrossCallPack,
		"csvCanonical":       cfg.Techniques.CsvCanonical,
		"symbolTable":        cfg.Techniques.SymbolTable,
		"diffLogFold":        cfg.Techniques.DiffLogFold,
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Errorf("default techniques.%s = %v, want %v", name, got[name], expected)
		}
	}
}

func TestNewCodecTechniqueConfigRoundTripsAndAcceptsEnvOverrides(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tokenmill.jsonc")
	contents := []byte(`{
		"techniques": {
			"jcs": false,
			"jsonNumber": false,
			"markdownWhitespace": true,
			"opaqueDict": true,
			"crossCallPack": true,
			"csvCanonical": true,
			"symbolTable": true,
			"diffLogFold": true
		}
	}`)
	if err := os.WriteFile(configPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TOKENMILL_TECHNIQUES_JSONNUMBER", "true")
	t.Setenv("TOKENMILL_TECHNIQUES_MARKDOWNWHITESPACE", "false")
	cfg, err := LoadFrom(configPath)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Techniques.JCS {
		t.Fatal("file value techniques.jcs=false was not preserved")
	}
	if cfg.Techniques.JsonNumber {
		t.Fatal("explicit config should override TOKENMILL_TECHNIQUES_JSONNUMBER")
	}
	if !cfg.Techniques.MarkdownWhitespace {
		t.Fatal("explicit config should override TOKENMILL_TECHNIQUES_MARKDOWNWHITESPACE")
	}
	for name, enabled := range map[string]bool{
		"opaqueDict":    cfg.Techniques.OpaqueDict,
		"crossCallPack": cfg.Techniques.CrossCallPack,
		"csvCanonical":  cfg.Techniques.CsvCanonical,
		"symbolTable":   cfg.Techniques.SymbolTable,
		"diffLogFold":   cfg.Techniques.DiffLogFold,
	} {
		if !enabled {
			t.Errorf("file value techniques.%s=true was not loaded", name)
		}
	}

	t.Setenv("TOKENMILL_TECHNIQUES_JSONNUMBER", "false")
	t.Setenv("TOKENMILL_TECHNIQUES_MARKDOWNWHITESPACE", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	chdirForTest(t, t.TempDir())
	envOnly, err := Load()
	if err != nil {
		t.Fatalf("Load with codec env overrides: %v", err)
	}
	if envOnly.Techniques.JsonNumber || envOnly.Techniques.MarkdownWhitespace {
		t.Fatalf("new codec env overrides were not applied: jsonNumber=%v markdownWhitespace=%v", envOnly.Techniques.JsonNumber, envOnly.Techniques.MarkdownWhitespace)
	}

	serializedPath := filepath.Join(t.TempDir(), "serialized.jsonc")
	if err := cfg.Save(serializedPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadFrom(serializedPath)
	if err != nil {
		t.Fatalf("LoadFrom serialized config: %v", err)
	}
	if loaded.Techniques.JsonNumber != cfg.Techniques.JsonNumber || loaded.Techniques.OpaqueDict != cfg.Techniques.OpaqueDict || loaded.Techniques.DiffLogFold != cfg.Techniques.DiffLogFold {
		t.Fatalf("new codec flags did not survive save/load: got %+v want %+v", loaded.Techniques, cfg.Techniques)
	}
}
