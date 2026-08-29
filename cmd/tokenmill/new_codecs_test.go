package main

import (
	"testing"

	"github.com/tokenmill/tokenmill/internal/config"
)

func TestBuildPoolRegistersEveryEnabledNewCodec(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Techniques.Dedup = false
	cfg.Techniques.AnsiStripping = false
	cfg.Techniques.CrRendering = false
	cfg.Techniques.ExactRLE.Enabled = false
	cfg.Techniques.BlockFactoring.Enabled = false
	cfg.Techniques.PathDict.Enabled = false
	cfg.Techniques.SubstringDict.Enabled = false
	cfg.Techniques.Jton.Enabled = false
	cfg.Techniques.JsonCompact = false
	cfg.Techniques.TableTSV = false
	cfg.Techniques.StacktraceDict = false

	cfg.Techniques.JCS = true
	cfg.Techniques.JsonNumber = true
	cfg.Techniques.MarkdownWhitespace = true
	cfg.Techniques.OpaqueDict = true
	cfg.Techniques.CrossCallPack = true
	cfg.Techniques.CsvCanonical = true
	cfg.Techniques.SymbolTable = true
	cfg.Techniques.DiffLogFold = true

	pool := buildPool(cfg)
	want := map[string]struct{}{
		"jcs":                 {},
		"json-number":         {},
		"markdown-whitespace": {},
		"opaque-dict":         {},
		"block-pack":          {},
		"csv-canonical":       {},
		"symbol-table":        {},
		"diff-log-fold":       {},
	}
	got := make(map[string]struct{}, len(pool))
	for _, candidate := range pool {
		if candidate == nil {
			t.Fatal("buildPool returned a nil codec")
		}
		id := candidate.ID()
		if _, duplicate := got[id]; duplicate {
			t.Fatalf("buildPool registered codec %q more than once", id)
		}
		got[id] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("buildPool registered %d codecs, want %d: got=%v", len(got), len(want), got)
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("buildPool did not register enabled codec %q", id)
		}
	}
}

func TestBuildPoolExcludesDisabledNewCodecs(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Techniques.Dedup = false
	cfg.Techniques.AnsiStripping = false
	cfg.Techniques.CrRendering = false
	cfg.Techniques.ExactRLE.Enabled = false
	cfg.Techniques.BlockFactoring.Enabled = false
	cfg.Techniques.PathDict.Enabled = false
	cfg.Techniques.SubstringDict.Enabled = false
	cfg.Techniques.Jton.Enabled = false
	cfg.Techniques.JsonCompact = false
	cfg.Techniques.TableTSV = false
	cfg.Techniques.StacktraceDict = false
	cfg.Techniques.JCS = false
	cfg.Techniques.JsonNumber = false
	cfg.Techniques.MarkdownWhitespace = false
	cfg.Techniques.OpaqueDict = false
	cfg.Techniques.CrossCallPack = false
	cfg.Techniques.CsvCanonical = false
	cfg.Techniques.SymbolTable = false
	cfg.Techniques.DiffLogFold = false

	if pool := buildPool(cfg); len(pool) != 0 {
		t.Fatalf("all-disabled config produced %d codecs: %v", len(pool), pool)
	}
}
