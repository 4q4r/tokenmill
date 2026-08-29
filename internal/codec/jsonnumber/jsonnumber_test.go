package jsonnumber

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

func TestCodecImplementsLosslessCodec(t *testing.T) {
	var _ codec.LosslessCodec = New()
}

func TestCanonicalizesOnlyJSONNumbers(t *testing.T) {
	input := `{"message":"1.000000000000000000000", "values":[1.000000000000000000000, 2.500000000000000000000, -0, 1e+03], "url":"https://example.com/v1/1.000"}`
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded == input {
		t.Fatalf("expected numeric canonicalization, got unchanged %q", encoded)
	}
	if strings.Contains(encoded, `"message":"1"`) {
		t.Fatal("number-like string was rewritten")
	}
	if !strings.Contains(encoded, `"message":"1.000000000000000000000"`) {
		t.Fatal("string value was not preserved")
	}
	if !strings.Contains(encoded, `"url":"https://example.com/v1/1.000"`) {
		t.Fatal("URL string was changed")
	}
	if !json.Valid([]byte(encoded)) {
		t.Fatalf("encoded output is invalid JSON: %q", encoded)
	}
	if !c.Verify(input, encoded) {
		t.Fatal("Verify should use exact decimal numeric equality")
	}
	if !c.Detect(input) {
		t.Fatal("Detect should find canonicalizable JSON numbers")
	}
	if got := c.EstimateSavings(input); got <= 0 {
		t.Fatalf("EstimateSavings=%d, want positive", got)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if !codec.VerifyJSON(input, decoded) {
		t.Fatal("JSON data should remain equal after decoding")
	}
}

func TestCanonicalNumberForms(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`1.000`, `1`},
		{`1e+03`, `1000`},
		{`1.2300e+02`, `123`},
		{`-0`, `0`},
		{`0.00100`, `0.001`},
	}
	c := New()
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			encoded, err := c.Encode(`{"n":` + tc.input + `}`)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			want := `{"n":` + tc.want + `}`
			if encoded != want && encoded != `{"n":`+tc.input+`}` {
				t.Fatalf("got %q want canonical %q or conservative unchanged", encoded, want)
			}
			if !c.Verify(`{"n":`+tc.input+`}`, encoded) {
				t.Fatal("decimal semantic equality failed")
			}
		})
	}
}

func TestRejectsInvalidJSONNaNAndProse(t *testing.T) {
	c := New()
	for _, input := range []string{
		"",
		"not JSON 1.000",
		`{"n":`,
		`{"n":NaN}`,
		`{"n":Infinity}`,
		`{"n":01}`,
	} {
		t.Run(input, func(t *testing.T) {
			if c.Detect(input) {
				t.Fatal("Detect should reject invalid JSON or prose")
			}
			if got := c.EstimateSavings(input); got != -1 {
				t.Fatalf("EstimateSavings=%d want -1", got)
			}
			encoded, err := c.Encode(input)
			if input == "not JSON 1.000" || input == "" {
				if err == nil && encoded != input {
					t.Fatalf("prose/empty input changed: %q", encoded)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid JSON should return an error")
			}
		})
	}
}

func TestDecodeRejectsInvalidJSON(t *testing.T) {
	c := New()
	for _, encoded := range []string{"not JSON", `{"n":NaN}`, `{"n":01}`} {
		t.Run(encoded, func(t *testing.T) {
			if _, err := c.Decode(encoded); err == nil {
				t.Fatal("Decode should reject invalid JSON")
			}
		})
	}
}

func TestPreservesUTF8AndCRLF(t *testing.T) {
	input := "{\r\n  \"текст\": \"значение\",\r\n  \"n\": 10.000000000000000000\r\n}"
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if strings.Count(encoded, "\r\n") != strings.Count(input, "\r\n") {
		t.Fatal("line endings changed")
	}
	if !c.Verify(input, encoded) {
		t.Fatal("UTF-8/CRLF JSON should verify")
	}
}

func TestLargeExponentDoesNotCauseUnboundedExpansion(t *testing.T) {
	input := `{"n":1e100000000}`
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("valid large exponent should be handled conservatively: %v", err)
	}
	if encoded != input {
		t.Fatalf("large exponent should be skipped, got %q", encoded)
	}
	if got := c.EstimateSavings(input); got != -1 {
		t.Fatalf("EstimateSavings=%d want -1", got)
	}
}
