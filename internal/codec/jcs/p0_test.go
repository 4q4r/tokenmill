package jcs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalizeSortsNestedObjectsAndPreservesArrayOrder(t *testing.T) {
	input := `{"z":{"b":2,"a":1},"items":[{"d":4,"c":3},{"b":2,"a":1}],"a":0}`
	want := `{"a":0,"items":[{"c":3,"d":4},{"a":1,"b":2}],"z":{"a":1,"b":2}}`

	got, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	if string(got) != want {
		t.Fatalf("unexpected canonical JSON: got %q want %q", got, want)
	}
	if strings.ContainsAny(string(got), "\n\r\t") || strings.Contains(string(got), ": ") || strings.Contains(string(got), ", ") {
		t.Fatalf("canonical JSON is not compact: %q", got)
	}
}

func TestCanonicalizePreservesNumberLexemesAndIsIdempotent(t *testing.T) {
	input := `{"z":-0,"n":1.00,"e":1e+06,"small":0.0001}`
	want := `{"e":1e+06,"n":1.00,"small":0.0001,"z":-0}`

	got, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	if string(got) != want {
		t.Fatalf("number lexeme changed: got %q want %q", got, want)
	}
	again, err := Canonicalize(got)
	if err != nil {
		t.Fatalf("second Canonicalize returned error: %v", err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("canonicalization is not idempotent: got %q want %q", again, got)
	}
}

func TestCanonicalizeScalarsAndStrings(t *testing.T) {
	for _, input := range []string{`null`, `true`, `false`, `"hello\nworld"`, `[3,2,1]`} {
		got, err := Canonicalize([]byte(input))
		if err != nil {
			t.Fatalf("Canonicalize(%q) returned error: %v", input, err)
		}
		if !json.Valid(got) {
			t.Fatalf("Canonicalize(%q) returned invalid JSON %q", input, got)
		}
	}
}

func TestCanonicalizeUsesUTF16KeyOrdering(t *testing.T) {
	input := "{\"\uE000\":1,\"a\":2,\"𐀀\":3}"
	want := "{\"a\":2,\"𐀀\":3,\"\":1}"

	got, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	if string(got) != want {
		t.Fatalf("unexpected UTF-16 key order: got %q want %q", got, want)
	}
}

func TestCanonicalizeRejectsInvalidTrailingAndDuplicateJSON(t *testing.T) {
	for _, input := range []string{
		"",
		"not json",
		`{"a":1} trailing`,
		`[1,]`,
		`{"a":1,"a":2}`,
	} {
		if got, err := Canonicalize([]byte(input)); err == nil {
			t.Fatalf("Canonicalize(%q) unexpectedly succeeded with %q", input, got)
		}
	}
}

func TestCanonicalizeRoundTripPreservesJSONValues(t *testing.T) {
	input := `{"message":"unicode \u263A","numbers":[1.00,-0],"nested":{"b":true,"a":null}}`
	canonical, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}

	var before, after any
	beforeDecoder := json.NewDecoder(strings.NewReader(input))
	beforeDecoder.UseNumber()
	if err := beforeDecoder.Decode(&before); err != nil {
		t.Fatalf("decoding input: %v", err)
	}
	afterDecoder := json.NewDecoder(strings.NewReader(string(canonical)))
	afterDecoder.UseNumber()
	if err := afterDecoder.Decode(&after); err != nil {
		t.Fatalf("decoding canonical output: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("JSON value changed after round-trip: got %#v want %#v", after, before)
	}
}

func TestHashIsCanonicalSHA256AndRejectsInvalidJSON(t *testing.T) {
	a := []byte(`{"b":2,"a":1}`)
	b := []byte(`{"a":1,"b":2}`)
	gotA := Hash(a)
	gotB := Hash(b)
	if gotA == "" || gotA != gotB {
		t.Fatalf("Hash is not stable under object permutation: %q vs %q", gotA, gotB)
	}
	canonical, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	wantBytes := sha256.Sum256(canonical)
	want := hex.EncodeToString(wantBytes[:])
	if gotA != want {
		t.Fatalf("unexpected SHA-256: got %q want %q", gotA, want)
	}
	if got := Hash([]byte("invalid")); got != "" {
		t.Fatalf("invalid JSON must not produce a hash: %q", got)
	}
}
