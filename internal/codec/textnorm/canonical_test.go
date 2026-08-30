package textnorm

import (
	"strings"
	"testing"
)

func TestCanonicalizeURL(t *testing.T) {
	input := `fetch https://Example.COM:443/api?utm_source=ad&b=2&a=1 for data`
	want := `fetch https://example.com/api?a=1&b=2 for data`
	got := CanonicalizeURL(input)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasCanonicalizableURL(want) {
		t.Fatal("canonical URL flagged")
	}
	if CanonicalizeURL(want) != want {
		t.Fatal("canonical URL changed")
	}
}

func TestCanonicalizeTimestampsExtended(t *testing.T) {
	input := "posted at 10:00 PM (GMT+3) on 20260829T220000Z"
	got := CanonicalizeTimestampsExtended(input)
	if !strings.Contains(got, "22:00") && !strings.Contains(got, "+03:00") {
		t.Fatalf("AM/PM or TZ not canonicalized: %q", got)
	}
	if strings.Contains(got, "20260829") {
		t.Fatalf("basic ISO not expanded: %q", got)
	}
	if HasExtendedTimestampForms(got) {
		t.Fatal("canonical timestamps flagged")
	}
}

func TestStripThousandSeparators(t *testing.T) {
	input := "total 1,234,567 bytes and 500 items"
	want := "total 1234567 bytes and 500 items"
	if !HasThousandSeparators(input) {
		t.Fatal("HasThousandSeparators missed input")
	}
	if got := StripThousandSeparators(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasThousandSeparators(want) {
		t.Fatal("clean text flagged")
	}
}

func TestDecodeQuotedPrintable(t *testing.T) {
	input := "key=3D value=20 with=3Dline =\nbreak"
	want := "key= value  with=line break"
	if !HasQuotedPrintable(input) {
		t.Fatal("HasQuotedPrintable missed input")
	}
	if got := DecodeQuotedPrintable(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	plain := "no equals escapes here"
	if DecodeQuotedPrintable(plain) != plain {
		t.Fatal("plain text changed")
	}
}

func TestNormalizeBase64URL(t *testing.T) {
	input := "token: aBcD-_eFgH123456789012"
	want := "token: aBcD+/eFgH123456789012"
	if !HasBase64URL(input) {
		t.Fatal("HasBase64URL missed input")
	}
	if got := NormalizeBase64URL(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeBase32Case(t *testing.T) {
	input := "payload: mfzwidgetssomebase32datahere"
	if !HasLowercaseBase32(input) {
		t.Fatal("HasLowercaseBase32 missed lowercase base32")
	}
	got := NormalizeBase32Case(input)
	if got == input {
		t.Fatal("base32 case not normalized")
	}
}

func TestCanonicalizeSemver(t *testing.T) {
	input := "upgrade to v01.02.03 from v1.02.03"
	want := "upgrade to v1.2.3 from v1.2.3"
	if !HasNonCanonicalSemver(input) {
		t.Fatal("HasNonCanonicalSemver missed input")
	}
	if got := CanonicalizeSemver(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	clean := "v1.2.3 is fine"
	if CanonicalizeSemver(clean) != clean {
		t.Fatal("clean semver changed")
	}
}

func TestCompactHexColorsExtended(t *testing.T) {
	input := "color: #AABBCC; border: #DEADBEEF; alpha: #AABBCCFF; short: #aBc"
	got := CompactHexColors(input)
	if strings.Contains(got, "AABBCC") && strings.Contains(got, "#aabbcc") == false {
		t.Fatalf("uppercase not lowered: %q", got)
	}
	if strings.Contains(got, "DEADBEEF") {
		t.Fatalf("uppercase not lowered: %q", got)
	}
	if strings.Contains(got, "FF") && !strings.Contains(got, "#aabbcc") {
		t.Fatalf("opaque alpha not stripped: %q", got)
	}
}
