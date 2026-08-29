package stacktrace

import (
	"strings"
	"testing"
)

// S-01: stacktrace c>=2 hardcode not configurable vs PathDict MinCount 3
func TestP1_S01_ThresholdConfigurable(t *testing.T) {
	// Use plain path prefix to avoid double-counting via github+fileLine regex
	// Each line contributes exactly 1 dir count, so 2 lines = count 2
	input2 := "/a/b/c/d/a.go:10 foo\n" +
		"/a/b/c/d/b.go:20 bar\n" +
		"noise line without prefix\n"

	// Check that WithConfig variant exists and respects threshold
	enc2, _, ok2 := CompressStackTraceWithConfig(input2, 2, 5)
	if !ok2 {
		t.Fatalf("S-01: with minCount=2 expected ok true for 2 occurrences, got ok=false enc=%q", enc2)
	}
	enc3, _, ok3 := CompressStackTraceWithConfig(input2, 3, 5)
	if ok3 {
		t.Fatalf("S-01: with minCount=3 expected ok false for only 2 occurrences, got ok=true enc=%q", enc3)
	}

	// Input with 3 occurrences (each line 1 count) should pass 2 and 3, fail with 4
	input3 := strings.Repeat("/a/b/c/d/file.go:123 main.Foo\n", 3)
	_, _, ok2b := CompressStackTraceWithConfig(input3, 2, 5)
	_, _, ok3b := CompressStackTraceWithConfig(input3, 3, 5)
	_, _, ok4 := CompressStackTraceWithConfig(input3, 4, 5)
	if !ok2b || !ok3b {
		t.Fatalf("S-01: 3 occurrences should pass minCount 2 and 3")
	}
	if ok4 {
		t.Fatalf("S-01: 3 occurrences should fail minCount 4")
	}

	// Also test MaxCodes configurability
	inputMany := ""
	for i := 0; i < 5; i++ {
		inputMany += "github.com/a/b/c/d/e/file.go:10 foo\n"
		inputMany += "github.com/a/b/x/y/z/file.go:10 bar\n"
		inputMany += "github.com/other/pkg/file.go:10 baz\n"
	}
	// With maxCodes 1 should produce only 1 marker, with 5 should produce up to 5
	enc1, dict1, ok1 := CompressStackTraceWithConfig(inputMany, 2, 1)
	if !ok1 {
		t.Fatalf("expected ok with many prefixes and maxCodes 1")
	}
	if len(dict1) != 1 {
		t.Fatalf("maxCodes=1 should limit dict to 1, got %d dict=%v enc=%q", len(dict1), dict1, enc1)
	}
	enc5, dict5, ok5 := CompressStackTraceWithConfig(inputMany, 2, 5)
	if !ok5 {
		t.Fatalf("expected ok with max 5")
	}
	if len(dict5) <= 1 {
		t.Fatalf("maxCodes=5 should allow more than 1, got %d", len(dict5))
	}
	t.Logf("many enc1 %q dict1 %v", enc1[:100], dict1)
	t.Logf("many enc5 %q dict5 %v", enc5[:100], dict5)

	// Backward compat: CompressStackTrace() should still work and equal default config (3,5) per fix
	// It must not panic and roundtrip must hold
	inputDefault := strings.Repeat("github.com/example/project/pkg/file.go:123 at foo.Bar\n", 3)
	encDef, dictDef, okDef := CompressStackTrace(inputDefault)
	if okDef {
		dec := DecodeStackTrace(encDef, dictDef)
		if dec != inputDefault {
			t.Fatalf("default CompressStackTrace roundtrip failed")
		}
	}
}

// D-03 for stacktrace header parsing
func TestP1_D03_StacktraceHeaderParsing(t *testing.T) {
	dict := map[string]string{"$F0": "github.com/foo/b]ar/"}
	encoded := "[StackTrace: $F0=github.com/foo/b]ar/]\n$F0file.go:10 foo"
	expected := "github.com/foo/b]ar/file.go:10 foo"
	decoded := DecodeStackTrace(encoded, dict)
	if decoded != expected {
		t.Fatalf("D-03 stacktrace fragile: got %q want %q encoded=%q", decoded, expected, encoded)
	}
	// No-dict case with inner ] in header
	encoded2 := "[StackTrace: $F0=github.com/foo/b]ar/]\nremaining"
	decoded2 := DecodeStackTrace(encoded2, nil)
	if decoded2 != "remaining" {
		t.Fatalf("D-03 stacktrace no-dict: got %q want %q", decoded2, "remaining")
	}
}
