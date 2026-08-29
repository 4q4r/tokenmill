package symboltable

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

func TestCodecImplementsLosslessCodec(t *testing.T) {
	var _ codec.LosslessCodec = New()
}

func TestCanonicalizeRoundTripsUnicodeAndMarkerCollisions(t *testing.T) {
	input := strings.Repeat("alpha beta Ошибка ", 30) +
		"literal " + marker(0) + " and literal " + escapeMarker + "\n" +
		strings.Repeat("alpha beta Ошибка ", 20)

	candidate, err := Canonicalize(input)
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	if !strings.HasPrefix(candidate, envelopeMagic) {
		t.Fatalf("Canonicalize should produce an envelope, got %q", candidate)
	}
	if !strings.Contains(candidate, markerOpen) {
		t.Fatalf("canonical body should contain a symbol marker: %q", candidate)
	}
	if !strings.Contains(candidate, escapeMarker) {
		t.Fatalf("literal marker opener should be escaped: %q", candidate)
	}

	decoded, err := New().Decode(candidate)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if !bytes.Equal([]byte(decoded), []byte(input)) {
		t.Fatalf("round-trip changed bytes: got %q want %q", decoded, input)
	}
}

func TestCodecEncodeSkipsNoRepeatAndDoesNotRewriteCode(t *testing.T) {
	c := New()
	noRepeat := "one two three four five six seven eight nine ten"
	if c.Detect(noRepeat) {
		t.Fatal("input without repeated tokens should not be detected")
	}
	if got := c.EstimateSavings(noRepeat); got >= 0 {
		t.Fatalf("no-repeat input should be skipped, got %d", got)
	}
	encoded, err := c.Encode(noRepeat)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if encoded != noRepeat {
		t.Fatalf("no-repeat input should pass through unchanged")
	}

	code := "```go\npackage main\nfunc main() { fmt.Println(\"alpha alpha alpha\") }\n```"
	if c.Detect(code) {
		t.Fatal("symbol abbreviation must be opt-in for code blocks")
	}
	encoded, err = c.Encode(code)
	if err != nil {
		t.Fatalf("Encode code returned error: %v", err)
	}
	if encoded != code {
		t.Fatal("code block should pass through unchanged")
	}
}

func TestCodecEncodeUsesOnlyTokenizerSavingCandidates(t *testing.T) {
	input := strings.Repeat("veryLongRepeatedIdentifierToken ", 400)
	if got := New().EstimateSavings(input); got <= 0 {
		t.Fatalf("expected positive tokenizer savings, got %d", got)
	}
	encoded, err := New().Encode(input)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if encoded == input {
		t.Fatalf("long repeated tokens should have a tokenizer-saving symbol table: original=%d candidate=%d", tokenizer.Count(input), tokenizer.Count(encoded))
	}
	if got := tokenizer.Count(input) - tokenizer.Count(encoded); got <= 0 {
		t.Fatalf("Encode returned a non-saving candidate: %d tokens", got)
	}
	if !New().Verify(input, encoded) {
		t.Fatal("Encode candidate failed byte-lossless verification")
	}
}

func TestCodecDecodeRejectsMalformedEnvelopesWithoutLargeAllocation(t *testing.T) {
	c := New()
	malformed := []string{
		envelopeMagic,
		envelopeMagic + "\nD:999999999\n",
		envelopeMagic + "\nD:1\n999999999\n",
		envelopeMagic + "\nD:1\n3\nab\nB\n" + marker(0),
		envelopeMagic + "\nD:1\n3\nfoo\nB\n" + marker(999999),
		envelopeMagic + "\nD:1\n3\nfoo\nB\n§X§",
	}
	for _, input := range malformed {
		input := input
		t.Run(input, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Decode panicked on malformed input: %v", recovered)
				}
			}()
			if _, err := c.Decode(input); err == nil {
				t.Fatalf("Decode(%q) should return an error", input)
			}
		})
	}
}

func TestCodecRandomBoundedRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(20260827))
	words := []string{"alpha", "beta", "Γάμμα", "Ошибка", "delta"}
	for i := 0; i < 80; i++ {
		parts := make([]string, 0, 24)
		for j := 0; j < 24; j++ {
			parts = append(parts, words[rng.Intn(len(words))])
			if rng.Intn(4) == 0 {
				parts = append(parts, marker(0))
			}
			if rng.Intn(5) == 0 {
				parts = append(parts, "\r\n")
			} else {
				parts = append(parts, " ")
			}
		}
		input := strings.Join(parts, "")
		candidate, err := Canonicalize(input)
		if err != nil {
			t.Fatalf("case %d Canonicalize returned error: %v", i, err)
		}
		decoded, err := New().Decode(candidate)
		if err != nil {
			t.Fatalf("case %d Decode returned error: %v", i, err)
		}
		if decoded != input {
			t.Fatalf("case %d changed bytes: got %q want %q", i, decoded, input)
		}
	}
}
