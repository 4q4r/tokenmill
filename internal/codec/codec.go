package codec

import (
	"bytes"

	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

// HintOverhead is the token cost of a dedup/format hint like `§ref:HASH§`
// as per finding 13 tok (see findings.md §ref).
const HintOverhead = 13

// LosslessCodec is the adaptive tournament contract (see design spec § LosslessCodec).
type LosslessCodec interface {
	ID() string
	Detect(input string) bool
	EstimateSavings(input string) int // negative => skip, cheap analytic
	Encode(input string) (string, error)
	Decode(encoded string) (string, error)
	Verify(original, encoded string) bool
}

// VerifyBytes checks byte-level equality (byte-lossless).
func VerifyBytes(original, decoded []byte) bool {
	return bytes.Equal(original, decoded)
}

// VerifyJSON checks data-lossless equality via an exact JSON value comparison.
// Both strings must be valid JSON, contain no duplicate object keys, and
// represent the same data structure.
func VerifyJSON(a, b string) bool {
	va, err := parseJSONDocument(a)
	if err != nil {
		return false
	}
	vb, err := parseJSONDocument(b)
	if err != nil {
		return false
	}
	return equalJSONValues(va, vb)
}

// TokenSavings returns token saving tokens: originalTokens - encodedTokens.
// Positive means compression saved tokens. Uses tiktoken o200k_base (via internal/tokenizer).
func TokenSavings(original, encoded string) int {
	return tokenizer.Count(original) - tokenizer.Count(encoded)
}
