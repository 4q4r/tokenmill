package dictionary

import (
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

// D-01: dead tokenSavings check — Encode* keeps ok true even when saving <= HintOverhead.
// After fix it must return ok false when TokenSavings <= HintOverhead.
func TestP1_D01_TokenSavingsGate(t *testing.T) {
	// Input with 3 short repeats: header overhead dominates, saving negative => must fallback.
	input := strings.Repeat("/a/b/c/d ", 3)
	enc, dict, ok := EncodePaths(input, 5, 3)
	saving := codec.TokenSavings(input, enc)
	t.Logf("input=%q enc=%q ok=%v saving=%d hint=%d", input, enc, ok, saving, codec.HintOverhead)
	if saving <= codec.HintOverhead && ok {
		t.Fatalf("D-01 BUG: saving (%d) <= HintOverhead (%d) but ok=true, encoded=%q dict=%v; expect fallback (ok=false)", saving, codec.HintOverhead, enc, dict)
	}
	// Also test substring where saving negative but still ok true before fix
	sub := strings.Repeat("A", 40)
	inp2 := strings.Repeat(sub+" ", 4) // 4 repeats -> header huge, saving negative per earlier debug
	enc2, _, ok2 := EncodeSubstrings(inp2, 40, 4, 0)
	saving2 := codec.TokenSavings(inp2, enc2)
	t.Logf("substring saving=%d hint=%d ok=%v", saving2, codec.HintOverhead, ok2)
	if saving2 <= codec.HintOverhead && ok2 {
		t.Fatalf("D-01 BUG substring: saving %d <= %d but ok true", saving2, codec.HintOverhead)
	}
	// Positive case must still pass: long path repeated 10x should save > hint
	long := strings.Repeat("/very/long/path/with/many/segments/file ", 10)
	encL, _, okL := EncodePaths(long, 5, 3)
	savingL := codec.TokenSavings(long, encL)
	if savingL > codec.HintOverhead && !okL {
		t.Fatalf("expected positive saving case to be ok: saving=%d hint=%d enc=%q", savingL, codec.HintOverhead, encL)
	}
}

// D-03: fragile header parsing Index("]") vs LastIndex("]\n") when value contains ] or \n
func TestP1_D03_HeaderParsingRobust(t *testing.T) {
	// Craft header where dict value contains ']'
	// Simulate EncodePaths result with value containing ']'
	dict := map[string]string{"$P0": "/a/b]c"}
	// encoded header built as "[Paths: $P0=/a/b]c]\n$P0/d rest"
	// Real header would be "[Paths: $P0=/a/b]c]\n$P0/d rest" -> contains inner ] before header close
	encoded := "[Paths: $P0=/a/b]c]\n$P0/d rest"
	// Expected body is "$P0/d rest" -> after decode should restore "/a/b]c/d rest"
	expected := "/a/b]c/d rest"
	decoded := DecodePaths(encoded, dict)
	if decoded != expected {
		t.Fatalf("D-03 BUG DecodePaths fragile: got %q want %q (encoded=%q dict=%v); Index(\"]\") picks inner bracket", decoded, expected, encoded, dict)
	}

	// Same for URLs with value containing ]
	dictU := map[string]string{"$U0": "https://example.com/api]v1"}
	encodedU := "[URLs: $U0=https://example.com/api]v1]\n$U0/users"
	expectedU := "https://example.com/api]v1/users"
	decodedU := DecodeURLs(encodedU, dictU)
	if decodedU != expectedU {
		t.Fatalf("D-03 BUG DecodeURLs: got %q want %q", decodedU, expectedU)
	}

	// Substring header value containing ] and also body with marker
	dictS := map[string]string{"⟨M0⟩": "value]with]brackets"}
	encodedS := "[Substrings: ⟨M0⟩=value]with]brackets]\nhello ⟨M0⟩ world"
	expectedS := "hello value]with]brackets world"
	decodedS := DecodeSubstrings(encodedS, dictS)
	if decodedS != expectedS {
		t.Fatalf("D-03 BUG DecodeSubstrings: got %q want %q", decodedS, expectedS)
	}

	// Header parsing without dict but encoded contains header: body extraction should use LastIndex
	// When dict==nil, DecodePaths should still correctly extract body after header even if header value had ]
	encodedNoDict := "[Paths: $P0=/a/b]c]\nremaining body"
	decodedNoDict := DecodePaths(encodedNoDict, nil)
	if decodedNoDict != "remaining body" {
		t.Fatalf("D-03 BUG DecodePaths no-dict: got %q want %q", decodedNoDict, "remaining body")
	}

	// Also test StackTrace-like header with ] in value (though stacktrace prefixes rarely contain ], we still test generic)
	// Use dictionary substring value containing newline: value with "\n" inside header would break Index
	dictN := map[string]string{"⟨M0⟩": "line1\nline2"}
	encodedN := "[Substrings: ⟨M0⟩=line1\nline2]\nbody ⟨M0⟩"
	// After fix, header delimiter is "]\n", so body should be "body ⟨M0⟩" -> decoded "body line1\nline2"
	// With old Index("]") it would stop at first "]" before "\n" inside value? Actually value contains "\n" not "]", but combination.
	// We test that at least LastIndex handles "]\n" correctly for normal case
	expectedN := "body line1\nline2"
	decodedN := DecodeSubstrings(encodedN, dictN)
	if decodedN != expectedN {
		t.Fatalf("D-03 BUG DecodeSubstrings newline value: got %q want %q", decodedN, expectedN)
	}
}
