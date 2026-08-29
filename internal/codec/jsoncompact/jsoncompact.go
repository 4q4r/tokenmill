package jsoncompact

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

// Codec is LosslessCodec for json-compact.
// ID = "json-compact", Detect via json.Valid && len>50, Encode compact, Decode indent.
type Codec struct{}

func New() *Codec { return &Codec{} }

func (c *Codec) ID() string { return "json-compact" }

func (c *Codec) Detect(input string) bool {
	if len(input) <= 50 {
		return false
	}
	return json.Valid([]byte(input))
}

func (c *Codec) Encode(input string) (string, error) {
	if !json.Valid([]byte(input)) {
		return "", errors.New("invalid json")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(input)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (c *Codec) Decode(encoded string) (string, error) {
	if !json.Valid([]byte(encoded)) {
		return "", errors.New("invalid json")
	}
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(encoded), "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (c *Codec) Verify(original, encoded string) bool {
	// Decode then VerifyJSON; if decode fails, fallback to direct VerifyJSON with encoded
	decoded, err := c.Decode(encoded)
	if err != nil {
		// fallback: compare encoded directly
		return codec.VerifyJSON(original, encoded)
	}
	return codec.VerifyJSON(original, decoded)
}

func (c *Codec) EstimateSavings(input string) int {
	if !c.Detect(input) {
		return -1
	}
	enc, err := c.Encode(input)
	if err != nil {
		return -1
	}
	saving := tokenizer.Count(input) - tokenizer.Count(enc)
	if saving < 0 {
		return saving
	}
	// also check len diff: if saving ==0 but len reduced, still consider
	return saving
}

var _ codec.LosslessCodec = (*Codec)(nil)
