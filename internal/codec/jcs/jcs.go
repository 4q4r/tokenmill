// Package jcs provides conservative canonical JSON for ordinary JSON values.
// It sorts object keys using UTF-16 code units and preserves number lexemes
// instead of performing potentially lossy IEEE-754 normalization.
package jcs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

type nodeKind uint8

const (
	nullNode nodeKind = iota
	booleanNode
	stringNode
	numberNode
	arrayNode
	objectNode
)

type node struct {
	kind    nodeKind
	boolean bool
	text    string
	array   []node
	object  []member
}

type member struct {
	key   string
	value node
}

// Canonicalize returns deterministic compact JSON. Object keys are sorted
// recursively by UTF-16 code units and array order is preserved. JSON number
// text is preserved exactly (for example, 1.0 remains 1.0), so this is a
// conservative RFC 8785-compatible subset rather than full IEEE-754 JCS
// number normalization. Invalid UTF-8, duplicate object keys, trailing JSON,
// and malformed JSON are rejected.
func Canonicalize(input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("jcs: empty input")
	}
	if !utf8.Valid(input) {
		return nil, errors.New("jcs: input is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	root, err := parseValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("jcs: parse JSON: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("jcs: trailing JSON: %w", err)
		}
		return nil, fmt.Errorf("jcs: trailing token %v", token)
	}

	var output strings.Builder
	if err := appendNode(&output, root); err != nil {
		return nil, fmt.Errorf("jcs: encode JSON: %w", err)
	}
	return []byte(output.String()), nil
}

func parseValue(decoder *json.Decoder) (node, error) {
	token, err := decoder.Token()
	if err != nil {
		return node{}, err
	}
	switch value := token.(type) {
	case nil:
		return node{kind: nullNode}, nil
	case bool:
		return node{kind: booleanNode, boolean: value}, nil
	case string:
		return node{kind: stringNode, text: value}, nil
	case json.Number:
		return node{kind: numberNode, text: value.String()}, nil
	case json.Delim:
		switch value {
		case '[':
			return parseArray(decoder)
		case '{':
			return parseObject(decoder)
		default:
			return node{}, fmt.Errorf("unexpected delimiter %q", value)
		}
	default:
		return node{}, fmt.Errorf("unsupported JSON token %T", token)
	}
}

func parseArray(decoder *json.Decoder) (node, error) {
	values := make([]node, 0)
	for decoder.More() {
		value, err := parseValue(decoder)
		if err != nil {
			return node{}, err
		}
		values = append(values, value)
	}
	end, err := decoder.Token()
	if err != nil {
		return node{}, err
	}
	if end != json.Delim(']') {
		return node{}, fmt.Errorf("expected array end, got %v", end)
	}
	return node{kind: arrayNode, array: values}, nil
}

func parseObject(decoder *json.Decoder) (node, error) {
	members := make([]member, 0)
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return node{}, err
		}
		key, ok := token.(string)
		if !ok {
			return node{}, fmt.Errorf("object key is %T, not string", token)
		}
		if _, exists := seen[key]; exists {
			return node{}, fmt.Errorf("duplicate object key %q", key)
		}
		seen[key] = struct{}{}
		value, err := parseValue(decoder)
		if err != nil {
			return node{}, err
		}
		members = append(members, member{key: key, value: value})
	}
	end, err := decoder.Token()
	if err != nil {
		return node{}, err
	}
	if end != json.Delim('}') {
		return node{}, fmt.Errorf("expected object end, got %v", end)
	}
	return node{kind: objectNode, object: members}, nil
}

func appendNode(output *strings.Builder, value node) error {
	switch value.kind {
	case nullNode:
		output.WriteString("null")
	case booleanNode:
		if value.boolean {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case stringNode:
		return appendJSONString(output, value.text)
	case numberNode:
		output.WriteString(value.text)
	case arrayNode:
		output.WriteByte('[')
		for i, item := range value.array {
			if i > 0 {
				output.WriteByte(',')
			}
			if err := appendNode(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case objectNode:
		members := append([]member(nil), value.object...)
		sort.Slice(members, func(i, j int) bool {
			return lessUTF16(members[i].key, members[j].key)
		})
		output.WriteByte('{')
		for i, item := range members {
			if i > 0 {
				output.WriteByte(',')
			}
			if err := appendJSONString(output, item.key); err != nil {
				return err
			}
			output.WriteByte(':')
			if err := appendNode(output, item.value); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unknown node kind %d", value.kind)
	}
	return nil
}

func appendJSONString(output *strings.Builder, value string) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	encoded := buffer.Bytes()
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		return errors.New("string encoder did not produce a JSON string")
	}
	output.Write(encoded[:len(encoded)-1])
	return nil
}

func lessUTF16(a, b string) bool {
	left := utf16.Encode([]rune(a))
	right := utf16.Encode([]rune(b))
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}

// Hash canonicalizes input and returns its lower-case SHA-256 hex digest. An
// invalid JSON input returns an empty string because the API has no error
// return; callers that need diagnostics should call Canonicalize directly.
func Hash(input []byte) string {
	canonical, err := Canonicalize(input)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

// CanonicalJSON is the historical string facade for Canonicalize.
func CanonicalJSON(input string) (string, error) {
	canonical, err := Canonicalize([]byte(input))
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// Codec preserves the existing LosslessCodec integration while using
// canonical JSON as its encoding. Its losslessness is JSON data-level, not
// byte-level, because insignificant whitespace and object-key order change.
type Codec struct{}

// New returns a canonical JSON codec.
func New() *Codec { return &Codec{} }

// ID identifies this codec for the tournament.
func (c *Codec) ID() string { return "jcs" }

// Detect reports whether input is valid JSON accepted by Canonicalize.
func (c *Codec) Detect(input string) bool {
	_, err := Canonicalize([]byte(input))
	return err == nil
}

// EstimateSavings returns token savings or -1 for invalid JSON.
func (c *Codec) EstimateSavings(input string) int {
	encoded, err := c.Encode(input)
	if err != nil {
		return -1
	}
	return tokenizer.Count(input) - tokenizer.Count(encoded)
}

// Encode canonicalizes input JSON.
func (c *Codec) Encode(input string) (string, error) {
	canonical, err := Canonicalize([]byte(input))
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// Decode validates and returns canonical JSON. JSON data, rather than source
// whitespace, is the round-trip contract.
func (c *Codec) Decode(encoded string) (string, error) {
	return c.Encode(encoded)
}

// Verify checks JSON data equality after decoding the canonical form.
func (c *Codec) Verify(original, encoded string) bool {
	decoded, err := c.Decode(encoded)
	if err != nil {
		return false
	}
	return codec.VerifyJSON(original, decoded)
}

var _ codec.LosslessCodec = (*Codec)(nil)
