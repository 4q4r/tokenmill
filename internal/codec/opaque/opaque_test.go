package opaque

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

func TestCodecImplementsLosslessCodec(t *testing.T) {
	var _ codec.LosslessCodec = New()
}

func TestRepeatedBase64UsesExactDeterministicDictionary(t *testing.T) {
	value := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("opaque-byte-payload-", 12)))
	input := "token=" + value + "\nagain=" + value + "\nthird=" + value
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded == input {
		t.Fatal("expected repeated opaque run to be encoded")
	}
	if !strings.HasPrefix(encoded, "[[tokenmill-opaque:v1;") {
		t.Fatalf("missing deterministic opaque header: %q", encoded)
	}
	if strings.Count(encoded, value) != 1 {
		t.Fatal("dictionary should carry the original value exactly once")
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if decoded != input {
		t.Fatalf("opaque round-trip mismatch: got %q want %q", decoded, input)
	}
	if !c.Verify(input, encoded) {
		t.Fatal("Verify should pass byte-exactly")
	}
	if got := c.EstimateSavings(input); got <= 0 {
		t.Fatalf("EstimateSavings=%d want positive", got)
	}
	if !c.Detect(input) {
		t.Fatal("Detect should find repeated base64")
	}

	encodedAgain, err := c.Encode(input)
	if err != nil {
		t.Fatalf("second Encode error: %v", err)
	}
	if encodedAgain != encoded {
		t.Fatalf("encoding is not deterministic:\nfirst:  %q\nsecond: %q", encoded, encodedAgain)
	}
}

func TestPaddedBase64AndOpaqueURLQueryRoundTrip(t *testing.T) {
	padded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("opaque-", 12) + "x"))
	queryURL := "https://api.example.com/v2/download?token=" + padded
	input := "first=" + padded + " url=" + queryURL + "\nsecond=" + padded + " url=" + queryURL

	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded == input {
		t.Fatal("expected padded base64 and opaque query URL to be encoded")
	}
	if decoded, err := c.Decode(encoded); err != nil || decoded != input {
		t.Fatalf("round-trip failed: decoded=%q err=%v", decoded, err)
	}
}

func TestRepeatedUUIDAndOpaqueURLAreSupported(t *testing.T) {
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	opaqueURL := "https://api.example.com/v1/" + strings.Repeat("ab12", 24)
	input := strings.Repeat("id="+uuid+" url="+opaqueURL+"\n", 4)
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded == input {
		t.Fatal("expected repeated UUID/opaque URL encoding")
	}
	decoded, err := c.Decode(encoded)
	if err != nil || decoded != input {
		t.Fatalf("round-trip failed: decoded=%q err=%v", decoded, err)
	}
}

func TestSkipsShortHumanReadableAndNonRepeatedValues(t *testing.T) {
	shortBase64 := base64.StdEncoding.EncodeToString([]byte("short"))
	longOnce := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("once-", 12)))
	readableURL := "https://example.com/docs/getting-started-with-human-readable-text"
	tests := []string{
		"short=" + shortBase64,
		"single=" + longOnce,
		"See " + readableURL + " for details.",
		"",
	}
	c := New()
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			encoded, err := c.Encode(input)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if encoded != input || c.Detect(input) {
				t.Fatalf("value should be skipped unchanged: got %q", encoded)
			}
			if got := c.EstimateSavings(input); got != -1 {
				t.Fatalf("EstimateSavings=%d want -1", got)
			}
		})
	}
}

func TestNeverRewritesCodeFenceOrMarkerCollision(t *testing.T) {
	value := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("fenced-", 16)))
	c := New()
	for _, input := range []string{
		"```text\n" + value + "\n" + value + "\n```",
		"prefix ⟦tm-o0⟧ value=" + value + "\nvalue=" + value,
	} {
		encoded, err := c.Encode(input)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if encoded != input {
			t.Fatalf("protected/collision input changed: %q", encoded)
		}
	}
}

func TestDecodeRejectsMalformedOpaqueStreams(t *testing.T) {
	c := New()
	for _, encoded := range []string{
		"[[tokenmill-opaque:v1;broken]]text",
		"[[tokenmill-opaque:v1;0:not-base64!]]⟦tm-o0⟧",
		"[[tokenmill-opaque:v1;0:YWJj]]⟦tm-o1⟧",
		"[[tokenmill-opaque:v1;0:YWJj]]⟦tm-o0",
		"[[tokenmill-opaque:v1;0:3:abc]]text",
	} {
		t.Run(encoded, func(t *testing.T) {
			if _, err := c.Decode(encoded); err == nil {
				t.Fatal("Decode should reject malformed opaque stream")
			}
		})
	}
}

func TestInvalidBytesRoundTripWhenNoOpaqueCandidate(t *testing.T) {
	input := string([]byte{0xff, 0x00, 'x', 'y'})
	c := New()
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if encoded != input || !c.Verify(input, encoded) {
		t.Fatal("non-candidate bytes should pass through exactly")
	}
}

func TestConfigCanDisableAndLimitInput(t *testing.T) {
	value := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("opaque-", 12)))
	input := "value=" + value + "\nagain=" + value

	disabled := NewWithConfig(Config{Enabled: false})
	if encoded, err := disabled.Encode(input); err != nil || encoded != input || disabled.Detect(input) {
		t.Fatal("disabled codec should pass through")
	}

	limited := NewWithConfig(Config{Enabled: true, MaxInputBytes: len(input) - 1})
	if encoded, err := limited.Encode(input); err != nil || encoded != input || limited.Detect(input) {
		t.Fatal("input over the configured limit should pass through")
	}
}
