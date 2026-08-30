package textnorm

import (
	"strings"
	"testing"
)

func TestUnfoldHexEscapes(t *testing.T) {
	input := `\xd0\xbf\xd1\x80\xd0\xb8\xd0\xb2\xd0\xb5\xd1\x82`
	if got := UnfoldHexEscapes(input); got != "привет" {
		t.Fatalf("got %q, want привет", got)
	}
	if !HasHexEscapes(input) {
		t.Fatal("HasHexEscapes missed input")
	}
	single := `literal \x41 docs`
	if got := UnfoldHexEscapes(single); got != single {
		t.Fatalf("sub-threshold input changed: %q", got)
	}
}

func TestDeepUnescapeEntities(t *testing.T) {
	input := "&amp;amp; &amp;lt;"
	want := "& <"
	if got := DeepUnescapeEntities(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !HasDeepEntities(input) {
		t.Fatal("HasDeepEntities missed double-encoded text")
	}
	if got := DeepUnescapeEntities("plain & text"); got != "plain & text" {
		t.Fatalf("plain text changed: %q", got)
	}
}

func TestRepairMojibake(t *testing.T) {
	input := "Ã©cole â€” ï»¿test â€™quoteâ€™"
	got := RepairMojibake(input)
	if got == input {
		t.Fatal("mojibake was not repaired")
	}
	if strings.Contains(got, "â€") {
		t.Fatalf("mojibake remains: %q", got)
	}
	clean := "обычный русский текст без поломок"
	if RepairMojibake(clean) != clean {
		t.Fatal("clean text changed")
	}
}

func TestCanonicalizeIPv6(t *testing.T) {
	input := "connect 2001:0DB8:0:0:0:0:0002:0001 and fe80:0:0:0:0:0:0:1"
	want := "connect 2001:db8::2:1 and fe80::1"
	if !HasNonCanonicalIPv6(input) {
		t.Fatal("HasNonCanonicalIPv6 missed input")
	}
	if got := CanonicalizeIPv6(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	canonical := "2001:db8::1"
	if CanonicalizeIPv6(canonical) != canonical {
		t.Fatal("canonical address changed")
	}
	if HasNonCanonicalIPv6(canonical) {
		t.Fatal("canonical address flagged")
	}
}

func TestCanonicalizeTimestamps(t *testing.T) {
	input := "at 2026-08-29t10:00:00.000+03:00 and 2026-08-29T10:00:00Z"
	want := "at 2026-08-29T07:00:00Z and 2026-08-29T10:00:00Z"
	if !HasNonCanonicalTimestamp(input) {
		t.Fatal("HasNonCanonicalTimestamp missed input")
	}
	if got := CanonicalizeTimestamps(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEpochToISO(t *testing.T) {
	input := "finished at 1796000000 (ms: 1796000000123)"
	if !HasEpochTimestamps(input) {
		t.Fatal("HasEpochTimestamps missed input")
	}
	got := EpochToISO(input)
	if !strings.Contains(got, "2026-11-30T") || !strings.Contains(got, "Z") || got == input {
		t.Fatalf("epoch conversion produced %q", got)
	}
	small := "id 123456789 stays"
	if EpochToISO(small) != small {
		t.Fatal("out-of-window number changed")
	}
}

func TestCompactBase32(t *testing.T) {
	payload := "MFZWIZLTOQXXIZLTOQXXIZLTOQXXIZLTOQXXIZLTOQXXIZLTOQXXIZLTOQXXIZLT"
	input := "data: " + payload[:8] + "\n" + payload[8:] + "\nend"
	if !HasCompactableBase32(input) {
		t.Fatal("HasCompactableBase32 missed wrapped base32")
	}
	if got := CompactBase32(input); strings.Contains(got, "\n") {
		t.Fatalf("base32 run kept newlines: %q", got)
	}
}

func TestUnwrapCDATA(t *testing.T) {
	input := "<desc><![CDATA[10 < 20 & ok]]></desc>"
	want := "<desc>10 < 20 & ok</desc>"
	if !HasCDATA(input) {
		t.Fatal("HasCDATA missed input")
	}
	if got := UnwrapCDATA(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeHeaders(t *testing.T) {
	input := "content-type:   application/json\nHost: example.com\nnote: stays as-is"
	want := "Content-Type: application/json\nHost: example.com\nnote: stays as-is"
	if !HasLooseHeaders(input) {
		t.Fatal("HasLooseHeaders missed input")
	}
	if got := NormalizeHeaders(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnquoteCSV(t *testing.T) {
	input := "\"id\",\"name\",\"city\"\n\"1\",\"Alice\",\"Springfield\"\n\"2\",\"Bob, Jr\",\"Austin\"\n"
	want := "id,name,city\n1,Alice,Springfield\n2,\"Bob, Jr\",Austin\n"
	if !HasOverQuotedCSV(input) {
		t.Fatal("HasOverQuotedCSV missed input")
	}
	if got := UnquoteCSV(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasOverQuotedCSV(want) {
		t.Fatal("minimal CSV flagged as over-quoted")
	}
}

func TestMinifySQL(t *testing.T) {
	input := "SELECT a, b\nFROM t\nWHERE s = 'hello  world -- not comment'\n-- real comment\nAND b > 5"
	got := MinifySQL(input)
	if strings.Contains(got, "\n") {
		t.Fatalf("newlines survived outside protected regions: %q", got)
	}
	if !strings.Contains(got, "'hello  world -- not comment'") {
		t.Fatalf("string literal was damaged: %q", got)
	}
	if !strings.Contains(got, "-- real comment") {
		t.Fatalf("comment was dropped: %q", got)
	}
	if !HasMinifiableSQL(input) {
		t.Fatal("HasMinifiableSQL missed input")
	}
}

func TestFoldMarkdownLinks(t *testing.T) {
	input := "see [docs](https://example.com/long/url) and [api](https://example.com/long/url) plus [other](https://other.example)."
	folded := FoldMarkdownLinks(input)
	if !strings.Contains(folded, "[docs](https://example.com/long/url)") {
		t.Fatalf("first occurrence should stay inline: %q", folded)
	}
	if !strings.Contains(folded, "[api][md1]") || !strings.Contains(folded, "\n[md1]: https://example.com/long/url") {
		t.Fatalf("repeat not converted to reference: %q", folded)
	}
	if UnfoldMarkdownLinks(folded) != input {
		t.Fatalf("unfold mismatch: %q", UnfoldMarkdownLinks(folded))
	}
	if HasDuplicatedMarkdownLinks("single [link](https://once.example) only") {
		t.Fatal("single link flagged as duplicated")
	}
}

func TestFoldPrefixEmailQuotes(t *testing.T) {
	log := strings.Join([]string{
		"> > quote depth two line one",
		"> > quote depth two line two",
		"> > quote depth two line three",
		"> > quote depth two line four",
		"> > quote depth two line five",
	}, "\n")
	folded := FoldLinePrefixes(log, 5, 2)
	if !folded.Changed {
		t.Fatal("email quote prefix was not folded")
	}
	if UnfoldLinePrefixes(folded.Content) != log {
		t.Fatal("email quote fold roundtrip mismatch")
	}
}
