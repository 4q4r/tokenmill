package jsoncompact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

func TestJSONCompact_ID(t *testing.T) {
	c := New()
	if c.ID() != "json-compact" {
		t.Fatalf("ID got %q want %q", c.ID(), "json-compact")
	}
}

func TestJSONCompact_Detect(t *testing.T) {
	c := New()
	pretty := `{
  "name": "Alice",
  "age": 30,
  "city": "NYC",
  "extra": "this is a long string to make length >50 chars for detection test",
  "nested": {"a": 1, "b": [2,3,4]}
}`
	short := `{"a":1,"b":2}`
	if !c.Detect(pretty) {
		t.Fatal("Detect should be true for pretty long JSON")
	}
	if c.Detect(short) {
		t.Fatal("Detect should be false for short len<=50")
	}
	if c.Detect("not json") {
		t.Fatal("Detect should be false for invalid json")
	}
	if c.Detect("") {
		t.Fatal("Detect should be false for empty")
	}
	// exactly 50? Len 50 should be false, 51 true
	exact50 := strings.Repeat("a", 50)
	// not valid json anyway false
	if c.Detect(exact50) {
		t.Fatal("Detect should be false for non-json even if len>50")
	}
	// valid json but len 51
	valid51 := `{"a": "` + strings.Repeat("x", 45) + `"}` // len >50
	if len(valid51) <= 50 {
		t.Fatalf("test setup len %d not >50", len(valid51))
	}
	if !c.Detect(valid51) {
		t.Fatalf("Detect should be true for valid json len>50, got false for %q len %d", valid51, len(valid51))
	}
}

func TestJSONCompact_EncodeCompact(t *testing.T) {
	c := New()
	pretty := "{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": [1, 2, 3]\n}"
	enc, err := c.Encode(pretty)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if strings.Contains(enc, "\n") {
		t.Fatalf("Encode should be compact without newline, got %q", enc)
	}
	if strings.Contains(enc, ": ") {
		t.Fatalf("Encode should use ':' without space, got %q", enc)
	}
	if strings.Contains(enc, ", ") {
		t.Fatalf("Encode should use ',' without space, got %q", enc)
	}
	// Verify is compact via json.Compact vs Marshal
	var buf2 []byte
	_ = buf2
	// Ensure valid json
	if !json.Valid([]byte(enc)) {
		t.Fatalf("Encode output not valid json: %q", enc)
	}
	// Ensure deepEqual
	if !codec.VerifyJSON(pretty, enc) {
		t.Fatalf("VerifyJSON should be true for pretty vs compact")
	}
}

func TestJSONCompact_Decode(t *testing.T) {
	c := New()
	compact := `{"a":1,"b":[2,3]}`
	dec, err := c.Decode(compact)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if !json.Valid([]byte(dec)) {
		t.Fatalf("Decode output not valid json: %q", dec)
	}
	if !codec.VerifyJSON(compact, dec) {
		t.Fatalf("VerifyJSON compact vs dec should be true")
	}
	// Decoded should be indented (contains newline or spaces)
	if !strings.Contains(dec, "\n") && !strings.Contains(dec, "  ") {
		// Could still be valid but we expect indent
		t.Logf("Decode not indented but still valid: %q", dec)
	}
}

func TestJSONCompact_Verify(t *testing.T) {
	c := New()
	pretty := `{"a": 1, "b": 2}`
	compact := `{"a":1,"b":2}`
	if !c.Verify(pretty, compact) {
		t.Fatal("Verify should be true for same data different whitespace")
	}
	if c.Verify(pretty, `{"a":2,"b":2}`) {
		t.Fatal("Verify should be false for different values")
	}
	if c.Verify(pretty, `not json`) {
		t.Fatal("Verify should be false for invalid encoded")
	}
}

func TestJSONCompact_EstimateSavings(t *testing.T) {
	c := New()
	pretty := `{
  "name": "Alice",
  "age": 30,
  "city": "NYC",
  "extra": "this is a long string to make length >50 chars for detection test",
  "nested": {"a": 1, "b": [2,3,4]}
}`
	saving := c.EstimateSavings(pretty)
	if saving <= 0 {
		t.Fatalf("EstimateSavings should be >0 for pretty, got %d", saving)
	}
	// Short should be -1 (skip)
	short := `{"a":1}`
	if got := c.EstimateSavings(short); got != -1 {
		t.Fatalf("EstimateSavings for short should be -1, got %d", got)
	}
	// Invalid should be -1
	if got := c.EstimateSavings("not json"); got != -1 {
		t.Fatalf("EstimateSavings for invalid should be -1, got %d", got)
	}
	// Already compact should have 0 or negative? But len>50 compact may still have 0 saving?
	compactLong := `{"name":"Alice","age":30,"city":"NYC","extra":"this is a long string to make length >50 chars for detection test","nested":{"a":1,"b":[2,3,4]}}`
	saving2 := c.EstimateSavings(compactLong)
	// Could be 0 or small, but should be >= -1
	if saving2 < -1 {
		t.Fatalf("EstimateSavings compact long unexpected %d", saving2)
	}
}

func TestJSONCompact_Lossless(t *testing.T) {
	c := New()
	cases := []string{
		`{"a":1,"b":2,"c":3}`,
		`{"name":"Alice","age":30,"city":"NYC","extra":"long string to exceed fifty characters total length here"}`,
		`[{"id":1},{"id":2}]`,
		`{"nested":{"a":[1,2,3],"b":null,"c":true}}`,
	}
	// Make each case >50 by padding if needed
	for i, tc := range cases {
		// Ensure len>50 for Detect? For Encode we test regardless
		enc, err := c.Encode(tc)
		if err != nil {
			t.Fatalf("case %d Encode error: %v", i, err)
		}
		if !c.Verify(tc, enc) {
			t.Fatalf("case %d Verify failed: original %q enc %q", i, tc, enc)
		}
		dec, err := c.Decode(enc)
		if err != nil {
			t.Fatalf("case %d Decode error: %v", i, err)
		}
		if !codec.VerifyJSON(tc, dec) {
			t.Fatalf("case %d VerifyJSON tc vs dec failed", i)
		}
		// Also Verify via codec should pass
		if !c.Verify(tc, enc) {
			t.Fatalf("case %d Verify via codec failed", i)
		}
	}
}

func TestJSONCompact_InvalidEncode(t *testing.T) {
	c := New()
	_, err := c.Encode("not json")
	if err == nil {
		t.Fatal("expected error for invalid json encode")
	}
	_, err2 := c.Decode("not json")
	if err2 == nil {
		t.Fatal("expected error for invalid json decode")
	}
}

func TestJSONCompact_ImplementsLosslessCodec(t *testing.T) {
	var _ codec.LosslessCodec = New()
}
