package folding

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

func TestCanonicalizeRoundTripsExactRepeatedLinesAndBlocksWithCRLF(t *testing.T) {
	input := strings.Join([]string{
		"INFO Ω connected",
		"INFO Ω connected",
		"INFO Ω connected",
		"diff --git a/a.go b/a.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/a.go b/a.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/a.go b/a.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
	}, "\r\n")

	candidate, err := Canonicalize(input)
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	if !strings.HasPrefix(candidate, envelopeMagic) {
		t.Fatalf("Canonicalize should produce an envelope, got %q", candidate)
	}
	decoded, err := New().Decode(candidate)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if !bytes.Equal([]byte(decoded), []byte(input)) {
		t.Fatalf("round-trip changed bytes: got %q want %q", decoded, input)
	}
}

func TestCodecEncodeSkipsNoRepeatInput(t *testing.T) {
	c := New()
	input := "one\r\ntwo\r\nthree"
	if c.Detect(input) {
		t.Fatal("no-repeat input should not be detected")
	}
	if got := c.EstimateSavings(input); got >= 0 {
		t.Fatalf("no-repeat input should be skipped, got %d", got)
	}
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if encoded != input {
		t.Fatalf("no-repeat input should pass through unchanged")
	}
}

func TestCodecEncodeUsesOnlyTokenizerSavingCandidates(t *testing.T) {
	line := strings.Repeat("long repeated log payload ", 8) + "\r\n"
	input := strings.Repeat(line, 100)
	if got := New().EstimateSavings(input); got <= 0 {
		t.Fatalf("expected positive tokenizer savings, got %d", got)
	}
	encoded, err := New().Encode(input)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if encoded == input {
		t.Fatal("large repeated logs should have a tokenizer-saving fold")
	}
	if got := tokenizer.Count(input) - tokenizer.Count(encoded); got <= 0 {
		t.Fatalf("Encode returned a non-saving candidate: %d tokens", got)
	}
	if !New().Verify(input, encoded) {
		t.Fatal("Encode candidate failed byte-lossless verification")
	}
}

func TestCodecDetectRejectsCodeFenceByDefault(t *testing.T) {
	c := New()
	code := "```diff\n@@ -1 +1 @@\n-foo\n-foo\n-foo\n```"
	if c.Detect(code) {
		t.Fatal("folding must be opt-in for fenced code blocks")
	}
}

func TestCodecDecodeRejectsMalformedEnvelopesWithoutOOM(t *testing.T) {
	c := New()
	malformed := []string{
		envelopeMagic,
		envelopeMagic + "\nD:999999999\nO:1\n\n",
		envelopeMagic + "\nD:1\nO:1\n\nD\n999999999\n",
		envelopeMagic + "\nD:1\nO:1\n\nD\n3\nabc\nR\n0\n999999999\n",
		envelopeMagic + "\nD:1\nO:1\n\nD\n3\nabc\nR\n1\n2\n",
		envelopeMagic + "\nD:0\nO:1\n\nX\n",
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
	for i := 0; i < 60; i++ {
		line := []string{"INFO Ошибка", "INFO Ошибка", "INFO Ошибка", "detail αβ", "detail αβ"}
		lines := make([]string, 0, 20)
		for j := 0; j < 20; j++ {
			if rng.Intn(3) == 0 {
				lines = append(lines, line[rng.Intn(len(line))])
			} else {
				lines = append(lines, "unique-"+string(rune('а'+rng.Intn(8))))
			}
		}
		input := strings.Join(lines, "\r\n")
		if i%2 == 0 {
			input += "\r\n"
		}
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
