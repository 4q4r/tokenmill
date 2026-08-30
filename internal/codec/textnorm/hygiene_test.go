package textnorm

import (
	"testing"
)

func TestStripTrailingWhitespace(t *testing.T) {
	input := "line one \nline two\t\nhard break  \nlast line   "
	want := "line one\nline two\nhard break  \nlast line   "
	if !HasTrailingWhitespace(input) {
		t.Fatal("HasTrailingWhitespace missed single trailing whitespace")
	}
	if got := StripTrailingWhitespace(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasTrailingWhitespace(want) {
		t.Fatal("clean text flagged")
	}
	if StripTrailingWhitespace(want) != want {
		t.Fatal("clean text changed")
	}
}

func TestCollapseBlankRuns(t *testing.T) {
	input := "para one\n\n\n\n\npara two\n\n\npara three"
	want := "para one\n\npara two\n\npara three"
	if !HasBlankRuns(input) {
		t.Fatal("HasBlankRuns missed blank run")
	}
	if got := CollapseBlankRuns(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasBlankRuns(want) {
		t.Fatal("collapsed text flagged")
	}
	if CollapseBlankRuns(want) != want {
		t.Fatal("collapsed text changed")
	}
}

func TestFoldCompatibility(t *testing.T) {
	input := "ＡＢＣ ｆｕｌｌｗｉｄｔｈ ① ﬁ ²"
	want := "ABC fullwidth 1 fi 2"
	if !HasCompatibilityForms(input) {
		t.Fatal("HasCompatibilityForms missed compatibility forms")
	}
	if got := FoldCompatibility(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	cjk := "中文，标点，测试"
	if FoldCompatibility(cjk) != cjk {
		t.Fatal("CJK typography changed")
	}
	if HasCompatibilityForms(cjk) {
		t.Fatal("CJK text flagged")
	}
	plain := "plain ascii text"
	if HasCompatibilityForms(plain) || FoldCompatibility(plain) != plain {
		t.Fatal("plain text flagged or changed")
	}
}

func TestCompactHexColors(t *testing.T) {
	input := "color: #AABBCC; border: #aabb00; keep: #AAB; other: #123456"
	want := "color: #abc; border: #ab0; keep: #AAB; other: #123456"
	if !HasCompactableColors(input) {
		t.Fatal("HasCompactableColors missed expandable color")
	}
	if got := CompactHexColors(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasCompactableColors(want) {
		t.Fatal("shorthand colors flagged")
	}
}

func TestMinifyXML(t *testing.T) {
	input := "<root>\n  <item>value</item>\n  <item>other</item>\n</root>"
	want := "<root><item>value</item><item>other</item></root>"
	if !HasMinifiableXML(input) {
		t.Fatal("HasMinifiableXML missed indented XML")
	}
	if got := MinifyXML(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	inline := "<b>bold</b> <i>italic</i>"
	if got := MinifyXML(inline); got != inline {
		t.Fatalf("inline text space removed: %q", got)
	}
	if HasMinifiableXML(inline) {
		t.Fatal("inline text flagged")
	}
	if MinifyXML("plain text no tags") != "plain text no tags" {
		t.Fatal("plain text changed")
	}
}
