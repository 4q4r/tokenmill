package textnorm

import (
	"strings"
	"testing"
)

func TestDecodePercent(t *testing.T) {
	input := "https://example.com/search?q=hello%20world%20again&lang=en"
	want := "https://example.com/search?q=hello world again&lang=en"
	if got := DecodePercent(input); got != want {
		t.Fatalf("DecodePercent = %q, want %q", got, want)
	}
	if !HasPercentEncodings(input) {
		t.Fatal("HasPercentEncodings missed encoded URL")
	}
	utf8Case := "name=%C3%A9cole%E2%82%AC"
	if got := DecodePercent(utf8Case); got != "name=école€" {
		t.Fatalf("multi-byte decode = %q", got)
	}
	literal := "100% 20 degrees is 20% of the scale"
	if HasPercentEncodings(literal) {
		t.Fatal("isolated escapes should not count as encodings")
	}
	if got := DecodePercent(literal); got != literal {
		t.Fatalf("literal percent text changed: %q", got)
	}
	malformed := "trailing %2 followed by %20 end"
	got := DecodePercent(malformed)
	if !strings.Contains(got, "%2 ") {
		t.Fatalf("malformed escape was altered: %q", got)
	}
}

func TestCompactHex(t *testing.T) {
	input := "bytes: de ad be ef 01 02 03 04 end"
	want := "bytes: deadbeef01020304 end"
	if !HasCompactableHex(input) {
		t.Fatal("HasCompactableHex missed hex run")
	}
	if got := CompactHex(input); got != want {
		t.Fatalf("CompactHex = %q, want %q", got, want)
	}
	prose := "the answer is 42 and words like abc def do not join here"
	if got := CompactHex(prose); got != prose {
		t.Fatalf("prose changed: %q", got)
	}
	if HasCompactableHex(prose) {
		t.Fatal("prose flagged as hex")
	}
	oxPrefix := "0xde 0xad 0xbe 0xef"
	if got := CompactHex(oxPrefix); got != "0xde0xad0xbe0xef" {
		t.Fatalf("0x-prefixed run = %q", got)
	}
}

func TestFoldLinePrefixes(t *testing.T) {
	log := strings.Join([]string{
		"2026-08-29T10:00:00Z INFO request served in 12ms",
		"2026-08-29T10:00:00Z INFO request served in 14ms",
		"2026-08-29T10:00:01Z INFO request served in 9ms",
		"2026-08-29T10:00:01Z INFO request served in 11ms",
		"2026-08-29T10:00:02Z INFO request served in 10ms",
		"",
		"unrelated tail line",
	}, "\n")
	folded := FoldLinePrefixes(log, 5, 8)
	if !folded.Changed {
		t.Fatal("prefix fold did not fire on a log block")
	}
	if !strings.Contains(folded.Content, "[the next 5 lines start with") {
		t.Fatalf("missing envelope: %q", folded.Content)
	}
	if strings.Count(folded.Content, "2026-08-29T10:00") != 1 {
		t.Fatalf("prefix repeated instead of folded: %q", folded.Content)
	}
	if strings.Contains(folded.Content, "unrelated tail line\"") {
		t.Fatalf("tail line was folded: %q", folded.Content)
	}
	restored := UnfoldLinePrefixes(folded.Content)
	if restored != log {
		t.Fatalf("unfold roundtrip mismatch:\n%q\nvs\n%q", restored, log)
	}
}

func TestFoldLinePrefixesIgnoresShortRuns(t *testing.T) {
	log := strings.Join([]string{
		"prefixed alpha one",
		"prefixed alpha two",
	}, "\n")
	folded := FoldLinePrefixes(log, 5, 4)
	if folded.Changed {
		t.Fatalf("run shorter than minLines was folded: %q", folded.Content)
	}
}

func TestFoldUnfoldIdempotent(t *testing.T) {
	log := strings.Repeat("INFO 127.0.0.1 ok\n", 8)
	folded := FoldLinePrefixes(log, 5, 4)
	once := folded.Content
	if UnfoldLinePrefixes(once) != log {
		t.Fatal("first unfold mismatch")
	}
	refolded := FoldLinePrefixes(UnfoldLinePrefixes(once), 5, 4)
	if refolded.Content != once {
		t.Fatal("refold differs from first fold")
	}
}
