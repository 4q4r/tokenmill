// Package jsonnumber canonicalizes JSON number lexemes without touching JSON
// strings or ordinary prose. Numeric equality is checked with exact decimal
// arithmetic rather than float64, so large integers and exponents do not lose
// precision.
package jsonnumber

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

const (
	maxInputBytes        = 16 << 20
	maxCanonicalDigits   = 1 << 20
	maxCanonicalExponent = 1 << 20
)

var (
	errInvalidJSON   = errors.New("jsonnumber: invalid JSON")
	errInvalidNumber = errors.New("jsonnumber: invalid number")
)

type numberSpan struct {
	start int
	end   int
}

// Codec implements codec.LosslessCodec at the JSON data level. Whitespace and
// object order are not changed; only numeric spellings may be canonicalized.
type Codec struct{}

// New returns a conservative JSON-number codec.
func New() *Codec { return &Codec{} }

// ID identifies the codec for future tournament integration.
func (c *Codec) ID() string { return "json-number" }

// Detect reports whether valid JSON contains a safely canonicalizable number.
func (c *Codec) Detect(input string) bool {
	if len(input) == 0 || len(input) > maxInputBytes || !utf8.ValidString(input) || !json.Valid([]byte(input)) {
		return false
	}
	spans, err := findNumberSpans(input)
	if err != nil {
		return false
	}
	for _, span := range spans {
		canonical, changed := canonicalNumber(json.Number(input[span.start:span.end]))
		if changed && canonical != input[span.start:span.end] {
			return true
		}
	}
	return false
}

// EstimateSavings returns exact tokenizer savings, or -1 when canonical JSON
// is not strictly smaller.
func (c *Codec) EstimateSavings(input string) int {
	encoded, err := c.Encode(input)
	if err != nil || encoded == input {
		return -1
	}
	saving := codec.TokenSavings(input, encoded)
	if saving <= 0 {
		return -1
	}
	return saving
}

// Encode canonicalizes only numeric JSON tokens. Valid but expansion-prone
// exponents are retained verbatim, and a non-winning tokenizer result falls
// back to the original input.
func (c *Codec) Encode(input string) (string, error) {
	if len(input) == 0 || len(input) > maxInputBytes || !utf8.ValidString(input) || !json.Valid([]byte(input)) {
		return "", errInvalidJSON
	}
	encoded, changed, err := rewriteNumbers(input)
	if err != nil {
		return "", err
	}
	if !changed || tokenizer.Count(encoded) >= tokenizer.Count(input) {
		return input, nil
	}
	return encoded, nil
}

// Decode validates a JSON representation and returns it unchanged. JSON
// number canonicalization is data-lossless rather than source-byte-lossless.
func (c *Codec) Decode(encoded string) (string, error) {
	if len(encoded) == 0 || len(encoded) > maxInputBytes || !utf8.ValidString(encoded) || !json.Valid([]byte(encoded)) {
		return "", errInvalidJSON
	}
	return encoded, nil
}

// Verify checks JSON semantic equality with exact decimal number comparison.
func (c *Codec) Verify(original, encoded string) bool {
	decoded, err := c.Decode(encoded)
	if err != nil {
		return false
	}
	return semanticEqualJSON(original, decoded)
}

func rewriteNumbers(input string) (string, bool, error) {
	spans, err := findNumberSpans(input)
	if err != nil {
		return "", false, err
	}
	var output strings.Builder
	output.Grow(len(input))
	last := 0
	changed := false
	for _, span := range spans {
		original := json.Number(input[span.start:span.end])
		canonical, canCanonicalize := canonicalNumber(original)
		if !canCanonicalize || canonical == original.String() {
			continue
		}
		if !decimalEqual(original, json.Number(canonical)) {
			return "", false, errInvalidNumber
		}
		if !changed {
			changed = true
		}
		output.WriteString(input[last:span.start])
		output.WriteString(canonical)
		last = span.end
	}
	if !changed {
		return input, false, nil
	}
	output.WriteString(input[last:])
	result := output.String()
	if !json.Valid([]byte(result)) || !semanticEqualJSON(input, result) {
		return "", false, errors.New("jsonnumber: canonical output failed semantic verification")
	}
	return result, true, nil
}

// canonicalNumber returns a finite decimal spelling and whether exact
// canonicalization was possible within the safety limits. It accepts a
// json.Number so callers never pass a float through the conversion path.
func canonicalNumber(number json.Number) (string, bool) {
	value := number.String()
	if !json.Valid([]byte(value)) {
		return "", false
	}
	if len(value) == 0 {
		return "", false
	}

	negative := false
	if value[0] == '-' {
		negative = true
		value = value[1:]
	}
	eIndex := strings.IndexAny(value, "eE")
	exponent := 0
	if eIndex >= 0 {
		parsed, ok := parseBoundedExponent(value[eIndex+1:])
		if !ok {
			return number.String(), false
		}
		exponent = parsed
		value = value[:eIndex]
	}
	dotIndex := strings.IndexByte(value, '.')
	integerPart := value
	fractionPart := ""
	if dotIndex >= 0 {
		integerPart = value[:dotIndex]
		fractionPart = value[dotIndex+1:]
	}
	digits := integerPart + fractionPart
	decimalPosition := len(integerPart) + exponent
	firstNonZero := 0
	for firstNonZero < len(digits) && digits[firstNonZero] == '0' {
		firstNonZero++
	}
	if firstNonZero == len(digits) {
		return "0", true
	}
	digits = digits[firstNonZero:]
	decimalPosition -= firstNonZero
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}

	if decimalPosition > maxCanonicalDigits || decimalPosition < -maxCanonicalDigits || len(digits) > maxCanonicalDigits {
		return number.String(), false
	}
	var result string
	switch {
	case decimalPosition <= 0:
		zeroes := -decimalPosition
		if zeroes+len(digits)+2 > maxCanonicalDigits {
			return number.String(), false
		}
		result = "0." + strings.Repeat("0", zeroes) + digits
	case decimalPosition >= len(digits):
		zeroes := decimalPosition - len(digits)
		if decimalPosition > maxCanonicalDigits || len(digits)+zeroes > maxCanonicalDigits {
			return number.String(), false
		}
		result = digits + strings.Repeat("0", zeroes)
	default:
		result = digits[:decimalPosition] + "." + digits[decimalPosition:]
	}
	if negative && result != "0" {
		result = "-" + result
	}
	if !json.Valid([]byte(result)) {
		return number.String(), false
	}
	return result, true
}

func parseBoundedExponent(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	negative := false
	if value[0] == '+' || value[0] == '-' {
		negative = value[0] == '-'
		value = value[1:]
	}
	if value == "" {
		return 0, false
	}
	parsed := 0
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
		if parsed > maxCanonicalExponent/10 || (parsed == maxCanonicalExponent/10 && int(value[i]-'0') > maxCanonicalExponent%10) {
			return 0, false
		}
		parsed = parsed*10 + int(value[i]-'0')
	}
	if negative {
		return -parsed, true
	}
	return parsed, true
}

func findNumberSpans(input string) ([]numberSpan, error) {
	spans := make([]numberSpan, 0)
	inString := false
	escaped := false
	for i := 0; i < len(input); {
		if inString {
			switch {
			case escaped:
				escaped = false
				i++
			case input[i] == '\\':
				escaped = true
				i++
			case input[i] == '"':
				inString = false
				i++
			default:
				i++
			}
			continue
		}
		if input[i] == '"' {
			inString = true
			i++
			continue
		}
		if input[i] == '-' || (input[i] >= '0' && input[i] <= '9') {
			end, ok := scanNumber(input, i)
			if !ok {
				return nil, errInvalidNumber
			}
			spans = append(spans, numberSpan{start: i, end: end})
			i = end
			continue
		}
		i++
	}
	return spans, nil
}

func scanNumber(input string, start int) (int, bool) {
	i := start
	if i < len(input) && input[i] == '-' {
		i++
	}
	if i >= len(input) {
		return 0, false
	}
	if input[i] == '0' {
		i++
	} else {
		if input[i] < '1' || input[i] > '9' {
			return 0, false
		}
		for i < len(input) && input[i] >= '0' && input[i] <= '9' {
			i++
		}
	}
	if i < len(input) && input[i] == '.' {
		i++
		fractionStart := i
		for i < len(input) && input[i] >= '0' && input[i] <= '9' {
			i++
		}
		if i == fractionStart {
			return 0, false
		}
	}
	if i < len(input) && (input[i] == 'e' || input[i] == 'E') {
		i++
		if i < len(input) && (input[i] == '+' || input[i] == '-') {
			i++
		}
		exponentStart := i
		for i < len(input) && input[i] >= '0' && input[i] <= '9' {
			i++
		}
		if i == exponentStart {
			return 0, false
		}
	}
	return i, true
}

func decimalEqual(left, right json.Number) bool {
	leftCanonical, leftOK := canonicalNumber(left)
	rightCanonical, rightOK := canonicalNumber(right)
	if !leftOK || !rightOK {
		return false
	}
	return leftCanonical == rightCanonical
}

func semanticEqualJSON(left, right string) bool {
	leftValue, err := parseJSON(left)
	if err != nil {
		return false
	}
	rightValue, err := parseJSON(right)
	if err != nil {
		return false
	}
	return semanticEqualValue(leftValue, rightValue)
}

func parseJSON(value string) (any, error) {
	if len(value) == 0 || len(value) > maxInputBytes || !utf8.ValidString(value) {
		return nil, errInvalidJSON
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("jsonnumber: trailing JSON value")
		}
		return nil, err
	}
	return parsed, nil
}

func semanticEqualValue(left, right any) bool {
	switch leftValue := left.(type) {
	case json.Number:
		rightValue, ok := right.(json.Number)
		return ok && decimalEqual(leftValue, rightValue)
	case []any:
		rightValue, ok := right.([]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for i := range leftValue {
			if !semanticEqualValue(leftValue[i], rightValue[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		rightValue, ok := right.(map[string]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for key, value := range leftValue {
			other, ok := rightValue[key]
			if !ok || !semanticEqualValue(value, other) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

var _ codec.LosslessCodec = (*Codec)(nil)
