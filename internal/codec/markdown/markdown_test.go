package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
)

func TestCodecImplementsLosslessCodec(t *testing.T) {
	var _ codec.LosslessCodec = New()
}

func TestEncodeNormalizesSafeProseWithReversibleSidecar(t *testing.T) {
	// o200k_base tokenizes long space runs compactly, so the fixture needs a
	// sufficiently large run to amortize the reversible sidecar header.
	longGap := strings.Repeat(" ", 10000)
	input := "---\n" +
		"settings:\n  nested:\n    value:    keep-this-indentation\n" +
		"---\n\n" +
		"Paragraph before" + longGap + "paragraph after.\n\n" +
		"`inline    code`\n" +
		"    indented    code\n" +
		"```go\n" +
		"value    :=    1\n" +
		"```\n" +
		"See https://example.com/docs/getting-started?lang=en.\n"

	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded == input {
		t.Fatal("expected a token-saving safe prose normalization")
	}
	if !strings.HasPrefix(encoded, "[[tokenmill-markdown-ws:v1;") {
		t.Fatalf("expected explicit reversible sidecar header, got %q", encoded[:min(len(encoded), 120)])
	}
	if !strings.Contains(encoded, "Paragraph before paragraph after.") {
		t.Fatalf("safe prose was not normalized: %q", encoded)
	}
	for _, protected := range []string{
		"settings:\n  nested:\n    value:    keep-this-indentation\n",
		"`inline    code`",
		"    indented    code",
		"value    :=    1",
		"https://example.com/docs/getting-started?lang=en",
	} {
		if !strings.Contains(encoded, protected) {
			t.Fatalf("protected content changed or disappeared: %q", protected)
		}
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if decoded != input {
		t.Fatalf("round-trip mismatch: got %q want %q", decoded, input)
	}
	if !c.Verify(input, encoded) {
		t.Fatal("Verify should pass for exact round-trip")
	}
	if got := c.EstimateSavings(input); got <= 0 {
		t.Fatalf("EstimateSavings=%d, want positive", got)
	}
	if !c.Detect(input) {
		t.Fatal("Detect should find the safe prose candidate")
	}

	encodedAgain, err := c.Encode(input)
	if err != nil {
		t.Fatalf("second Encode error: %v", err)
	}
	if encodedAgain != encoded {
		t.Fatalf("encoding is not deterministic:\nfirst:  %q\nsecond: %q", encoded, encodedAgain)
	}
}

func TestEncodePreservesCRLFAndUTF8(t *testing.T) {
	input := "Привет" + strings.Repeat(" ", 1000) + "мир\r\nСледующая строка\r\n"
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if strings.Count(encoded, "\r\n") != strings.Count(input, "\r\n") {
		t.Fatalf("CRLF count changed: got %d want %d", strings.Count(encoded, "\r\n"), strings.Count(input, "\r\n"))
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if decoded != input {
		t.Fatalf("UTF-8/CRLF round-trip mismatch")
	}
	if !utf8.ValidString(decoded) {
		t.Fatal("decoded text is not valid UTF-8")
	}
}

func TestEncodeSkipsProtectedOrUnsafeInput(t *testing.T) {
	longGap := strings.Repeat(" ", 1000)
	tests := []struct {
		name  string
		input string
	}{
		{"fenced code", "```text\nalpha" + longGap + "omega\n```"},
		{"inline code", "`alpha" + longGap + "omega`"},
		{"indented code", "    alpha" + longGap + "omega"},
		{"yaml indentation", "---\nroot:\n  child:\n    value:" + longGap + "keep\n---"},
		{"tabs", "alpha\tbeta\nvalue" + longGap + "next"},
		{"url", "https://example.com/a/b/c?query=human-readable-text"},
		{"empty", ""},
	}
	c := New()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := c.Encode(tc.input)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if encoded != tc.input {
				t.Fatalf("unsafe input changed: got %q want %q", encoded, tc.input)
			}
			if c.Detect(tc.input) {
				t.Fatal("Detect should skip protected or unsafe input")
			}
			if got := c.EstimateSavings(tc.input); got != -1 {
				t.Fatalf("EstimateSavings=%d want -1", got)
			}
		})
	}
}

func TestDecodeRejectsMalformedSidecar(t *testing.T) {
	c := New()
	for _, encoded := range []string{
		"[[tokenmill-markdown-ws:v1;broken]]text",
		"[[tokenmill-markdown-ws:v1;0:3]]text",
		"[[tokenmill-markdown-ws:v1;999999999999999999999:3]] text",
	} {
		t.Run(encoded, func(t *testing.T) {
			if _, err := c.Decode(encoded); err == nil {
				t.Fatal("Decode should reject malformed sidecar")
			}
		})
	}
}

func TestInvalidUTF8IsSkipped(t *testing.T) {
	input := string([]byte{'a', ' ', 0xff, ' ', 'b'})
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded != input || c.Detect(input) {
		t.Fatal("invalid UTF-8 should be skipped unchanged")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
