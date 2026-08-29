package main

import (
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/config"
)

func TestBuildPoolRegistersSubstringDictWhenEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Techniques.SubstringDict = config.SubstringDict{Enabled: true, MinLen: 12, MinCount: 3}
	pool := buildPool(cfg)
	var target codec.LosslessCodec
	found := false
	for _, candidate := range pool {
		if candidate.ID() == "substring-dict" {
			target = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("buildPool did not register substring-dict: %v", poolIDs(pool))
	}

	input := strings.Repeat("GET /api/v2/warehouse/stocks?limit=100 HTTP/1.1\n", 20)
	if !target.Detect(input) {
		t.Fatal("substring-dict Detect rejected repetitive input")
	}
	encoded, err := target.Encode(input)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encodedAgain, err := target.Encode(input)
	if err != nil {
		t.Fatalf("second Encode: %v", err)
	}
	if encodedAgain != encoded {
		t.Fatal("substring-dict Encode is not deterministic for identical input")
	}
	if !target.Verify(input, encoded) {
		t.Fatal("substring-dict Verify failed for its own encoding")
	}
	if target.EstimateSavings(input) <= 0 {
		t.Fatal("substring-dict EstimateSavings was not positive for repetitive input")
	}
}

func TestSubstringDictLeavesShortInputUntouched(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Techniques.SubstringDict = config.SubstringDict{Enabled: true, MinLen: 40, MinCount: 4}
	pool := buildPool(cfg)
	var target codec.LosslessCodec
	for _, candidate := range pool {
		if candidate.ID() == "substring-dict" {
			target = candidate
			break
		}
	}
	if target == nil {
		t.Fatal("substring-dict not registered")
	}
	short := "just a short line"
	if target.Detect(short) {
		t.Fatal("Detect accepted input shorter than minLen")
	}
	if _, err := target.Encode(short); err == nil {
		t.Fatal("Encode succeeded on input shorter than minLen")
	}
}

func poolIDs(pool []codec.LosslessCodec) []string {
	ids := make([]string, 0, len(pool))
	for _, candidate := range pool {
		ids = append(ids, candidate.ID())
	}
	return ids
}

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
		"text-norm":           {},
		"html-entity":         {},
		"base64-compact":      {},
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
	cfg.Techniques.UnicodeNormalize = false
	cfg.Techniques.HtmlEntityDecode = false
	cfg.Techniques.Base64Compact = false

	if pool := buildPool(cfg); len(pool) != 0 {
		t.Fatalf("all-disabled config produced %d codecs: %v", len(pool), pool)
	}
}
