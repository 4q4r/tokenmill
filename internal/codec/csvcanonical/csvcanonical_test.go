package csvcanonical

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

func TestCanonicalizeRoundTripsQuotedCSVWithCRLFAndEmbeddedNewlines(t *testing.T) {
	input := strings.Join([]string{
		`"name","note","city"`,
		`"Алиса","first line` + "\r\n" + `second line","Москва"`,
		`"Боб","said ""hello""","Санкт-Петербург"`,
		`"Алиса","first line` + "\r\n" + `second line","Москва"`,
	}, "\r\n") + "\r\n"

	candidate, err := Canonicalize(input)
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	if !strings.HasPrefix(candidate, envelopeMagic) {
		t.Fatalf("Canonicalize should produce a self-contained envelope, got %q", candidate)
	}
	if !strings.Contains(candidate, "\t") {
		t.Fatalf("canonical body should use the canonical tab delimiter: %q", candidate)
	}

	decoded, err := New().Decode(candidate)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if !bytes.Equal([]byte(decoded), []byte(input)) {
		t.Fatalf("round-trip changed bytes: got %q want %q", decoded, input)
	}
}

func TestCodecEncodeSkipsNonBeneficialInput(t *testing.T) {
	c := New()
	input := "header,value\nalpha,beta\n"

	if got := c.EstimateSavings(input); got >= 0 {
		t.Fatalf("EstimateSavings should skip a tiny input, got %d", got)
	}
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if encoded != input {
		t.Fatalf("non-beneficial input must pass through unchanged: got %q", encoded)
	}
}

func TestCodecEncodeAppliesStrictTokenizerGate(t *testing.T) {
	rows := []string{`"id","description"`}
	for i := 0; i < 200; i++ {
		rows = append(rows, `"`+string(rune('0'+i%10))+`","long repeated description`+"\r\n"+`continued"`)
	}
	input := strings.Join(rows, "\r\n") + "\r\n"

	candidate, err := Canonicalize(input)
	if err != nil {
		t.Fatalf("Canonicalize returned error: %v", err)
	}
	encoded, err := New().Encode(input)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if tokenizer.Count(candidate) >= tokenizer.Count(input) && encoded != input {
		t.Fatal("Encode returned a candidate despite non-positive tokenizer savings")
	}
	if encoded != input && tokenizer.Count(input)-tokenizer.Count(encoded) <= 0 {
		t.Fatal("Encode returned a non-saving candidate")
	}
	if encoded != input && !New().Verify(input, encoded) {
		t.Fatal("Encode candidate failed byte-lossless verification")
	}
}

func TestCodecDetectRejectsCodeFenceAndMalformedCSV(t *testing.T) {
	c := New()
	code := "```csv\na,b\n1,2\n```"
	if c.Detect(code) {
		t.Fatal("CSV codec must not rewrite fenced code by default")
	}
	encoded, err := c.Encode(code)
	if err != nil {
		t.Fatalf("Encode should pass fenced code through: %v", err)
	}
	if encoded != code {
		t.Fatal("fenced code should pass through unchanged")
	}
	if c.Detect("a,b\n\"unterminated,1\n") {
		t.Fatal("malformed quoted CSV must not be detected")
	}
	if c.Detect("plain text") {
		t.Fatal("plain text must not be detected as CSV")
	}
}

func TestCodecDecodeRejectsMalformedEnvelopes(t *testing.T) {
	c := New()
	malformed := []string{
		envelopeMagic,
		envelopeMagic + "\nD:2\nR:1\nE:L\nQ:00\n\n",
		envelopeMagic + "\nD:999999999\n",
		envelopeMagic + "\nD:1\nR:1\nE:L\nQ:zz\n\n",
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
	for i := 0; i < 40; i++ {
		rows := make([]string, 0, 6)
		for row := 0; row < 6; row++ {
			name := []string{"Алиса", "Боб", "Ева"}[rng.Intn(3)]
			value := strings.Repeat("x", 4+rng.Intn(8))
			if row%2 == 0 {
				value += "\nчасть"
			}
			rows = append(rows, `"`+name+`","`+strings.ReplaceAll(value, `"`, `""`)+`"`)
		}
		input := strings.Join(rows, "\r\n")
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
