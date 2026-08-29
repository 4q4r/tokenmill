package textnorm

import (
	"strings"
	"testing"
)

func TestNormalizeStripsInvisibleCharacters(t *testing.T) {
	input := "data\u200Bscience\u200C and\u00ADword\uFEFF here"
	got := Normalize(input)
	if got != "datascience andword here" {
		t.Fatalf("Normalize = %q, want %q", got, "datascience andword here")
	}
	if !NeedsNormalization(input) {
		t.Fatal("NeedsNormalization missed invisible characters")
	}
}

func TestNormalizeMapsExoticSpaces(t *testing.T) {
	got := Normalize("hello\u00A0world\u2003and\u3000more")
	if got != "hello world and more" {
		t.Fatalf("Normalize = %q, want %q", got, "hello world and more")
	}
}

func TestNormalizePreservesZeroWidthJoinerEmoji(t *testing.T) {
	family := "👨" + "\u200D" + "👩" + "\u200D" + "👧"
	if got := Normalize(family); got != family {
		t.Fatalf("Normalize corrupted an emoji ZWJ sequence: %q", got)
	}
}

func TestNormalizeComposesNFC(t *testing.T) {
	decomposed := "e\u0301cole" // é as e + combining acute
	got := Normalize(decomposed)
	if got != "école" {
		t.Fatalf("Normalize = %q, want composed é", got)
	}
}

func TestNormalizeStraysControlsButKeepsNewlines(t *testing.T) {
	got := Normalize("a\x00b\x1Fc\r\nd\te\x7Ff")
	if got != "abc\r\nd\tef" {
		t.Fatalf("Normalize = %q, want %q", got, "abc\r\nd\tef")
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	input := "mix\u200B\u00A0of\u00ADartifacts\nline two"
	once := Normalize(input)
	twice := Normalize(once)
	if once != twice {
		t.Fatalf("Normalize not idempotent: %q vs %q", once, twice)
	}
	if NeedsNormalization(once) {
		t.Fatal("normalized text still reported as needing normalization")
	}
}

func TestNormalizeCleanTextUntouched(t *testing.T) {
	clean := "plain ASCII text\nwith tabs\tand newlines\n"
	if got := Normalize(clean); got != clean {
		t.Fatalf("Normalize changed clean text: %q", got)
	}
	if NeedsNormalization(clean) {
		t.Fatal("clean text flagged as needing normalization")
	}
}

func TestUnescapeEntities(t *testing.T) {
	input := "&lt;div class=&quot;x&quot;&gt;Tom &amp; Jerry &#39;s&lt;/div&gt;"
	want := `<div class="x">Tom & Jerry 's</div>`
	if got := UnescapeEntities(input); got != want {
		t.Fatalf("UnescapeEntities = %q, want %q", got, want)
	}
	if !HasHTMLEntities(input) {
		t.Fatal("HasHTMLEntities missed encoded input")
	}
	plain := " Tom & Jerry 's "
	if got := UnescapeEntities(plain); got != plain {
		t.Fatalf("plain text changed: %q", got)
	}
	if HasHTMLEntities(plain) {
		t.Fatal("HasHTMLEntities flagged plain text")
	}
	once := UnescapeEntities(input)
	if UnescapeEntities(once) != once {
		t.Fatal("UnescapeEntities not idempotent")
	}
}

const sampleBase64 = "TWFuIGlzIGRpc3Rpbmd1aXNoZWQsIG5vdCBvbmx5IGJ5IGhpcyByZWFzb24s\nIGJ1dCBieSB0aGlz\n"

func TestCompactBase64StripsWhitespaceInDecodableRun(t *testing.T) {
	input := "-----BEGIN-----\n" + sampleBase64 + "\n-----END-----"
	if !HasCompactableBase64(input) {
		t.Fatal("HasCompactableBase64 missed a wrapped base64 payload")
	}
	got := CompactBase64(input)
	if strings.ContainsAny(got, "\n") && strings.Count(got, "\n") != strings.Count(input, "\n")-2 {
		t.Fatalf("compaction left newlines inside the base64 run: %q", got)
	}
	if strings.Contains(got, sampleBase64) {
		t.Fatal("base64 payload was not compacted")
	}
	if !strings.Contains(got, CompactBase64(sampleBase64)) {
		t.Fatalf("compacted payload missing: %q", got)
	}
}

func TestCompactBase64LeavesProseAlone(t *testing.T) {
	prose := "This sentence has words with spaces and hyphens - like so - and stays intact."
	if got := CompactBase64(prose); got != prose {
		t.Fatalf("CompactBase64 changed prose: %q", got)
	}
	if HasCompactableBase64(prose) {
		t.Fatal("prose flagged as compactable")
	}
}

func TestCompactBase64Idempotent(t *testing.T) {
	input := "token: " + sampleBase64 + " end"
	once := CompactBase64(input)
	if CompactBase64(once) != once {
		t.Fatalf("CompactBase64 not idempotent: %q vs %q", once, CompactBase64(once))
	}
}

func TestCompactBase64RejectsUndecodableRun(t *testing.T) {
	// Looks base64-ish (long, alphabet characters, wrapped) but does not
	// decode; must be left byte-for-byte unchanged.
	input := "note this is just a fairly long plain english sentence with no secrets\nspread over two lines"
	if got := CompactBase64(input); got != input {
		t.Fatalf("undecodable run was modified: %q", got)
	}
}
