package textnorm

import (
	"testing"
)

func TestUnfoldUnicodeCyrillic(t *testing.T) {
	escaped := `\u0418\u043c\u044f \u043f\u043e\u043b\u044c\u0437\u043e\u0432\u0430\u0442\u0435\u043b\u044f`
	want := "Имя пользователя"
	if got := UnfoldUnicode(escaped); got != want {
		t.Fatalf("UnfoldUnicode = %q, want %q", got, want)
	}
	if !HasUnicodeEscapes(escaped) {
		t.Fatal("HasUnicodeEscapes missed Cyrillic escapes")
	}
}

func TestUnfoldUnicodeSurrogatePairs(t *testing.T) {
	escaped := `rocket: \ud83d\ude80 done`
	want := "rocket: 🚀 done"
	if got := UnfoldUnicode(escaped); got != want {
		t.Fatalf("surrogate decode = %q, want %q", got, want)
	}
}

func TestUnfoldUnicodeLoneSurrogateStaysLiteral(t *testing.T) {
	input := `broken \ud83d tail`
	if got := UnfoldUnicode(input); got != input {
		t.Fatalf("lone surrogate was unfolded: %q", got)
	}
	if HasUnicodeEscapes(input) {
		t.Fatal("lone surrogate flagged as unfoldable")
	}
}

func TestUnfoldUnicodeMinimumRun(t *testing.T) {
	one := `only \u0041 here`
	if HasUnicodeEscapes(one) {
		t.Fatal("a single escape should not be unfolded")
	}
	if got := UnfoldUnicode(one); got != one {
		t.Fatalf("single escape changed: %q", got)
	}
}

func TestUnfoldUnicodeIdempotent(t *testing.T) {
	input := `{"text": "\u043f\u0440\u0438\u0432\u0435\u0442 \u043c\u0438\u0440"}`
	once := UnfoldUnicode(input)
	if HasUnicodeEscapes(once) {
		t.Fatal("unfolded text still flagged")
	}
	if UnfoldUnicode(once) != once {
		t.Fatal("UnfoldUnicode not idempotent")
	}
}

func TestCompactUUIDs(t *testing.T) {
	input := "session 550e8400-e29b-41d4-a716-446655440000 created"
	want := "session 550e8400e29b41d4a716446655440000 created"
	if !HasDashedUUIDs(input) {
		t.Fatal("HasDashedUUIDs missed a canonical UUID")
	}
	if got := CompactUUIDs(input); got != want {
		t.Fatalf("CompactUUIDs = %q, want %q", got, want)
	}
	if RedashUUIDs(want) != input {
		t.Fatalf("RedashUUIDs roundtrip = %q", RedashUUIDs(want))
	}
	plain := "the port 8080 and hex deadbeef are not UUIDs"
	if HasDashedUUIDs(plain) {
		t.Fatal("non-UUID flagged")
	}
	if got := CompactUUIDs(plain); got != plain {
		t.Fatalf("plain text changed: %q", got)
	}
}

func TestNormalizeSmartPunctuation(t *testing.T) {
	input := "“smart” and ‘single’ and ellipsis… done"
	want := `"smart" and 'single' and ellipsis... done`
	if !HasSmartPunctuation(input) {
		t.Fatal("HasSmartPunctuation missed input")
	}
	if got := NormalizeSmartPunctuation(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	russian := "«ёлочки» и тире — остаются"
	if got := NormalizeSmartPunctuation(russian); got != russian {
		t.Fatalf("russian punctuation changed: %q", got)
	}
	if HasSmartPunctuation(russian) {
		t.Fatal("russian text flagged as smart punctuation")
	}
}
