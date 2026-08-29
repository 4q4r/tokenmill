package codec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

type jsonValueKind uint8

const (
	jsonNull jsonValueKind = iota
	jsonBool
	jsonString
	jsonNumber
	jsonArray
	jsonObject
)

type jsonValue struct {
	kind   jsonValueKind
	bool   bool
	string string
	number decimalNumber
	array  []jsonValue
	object map[string]jsonValue
}

// decimalNumber stores a JSON number as sign * digits * 10^exponent. The
// exponent is arbitrary precision so verifying a large exponent never expands
// the value into a potentially enormous integer or decimal string.
type decimalNumber struct {
	negative bool
	digits   string
	exponent *big.Int
}

func parseJSONDocument(input string) (jsonValue, error) {
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder)
	if err != nil {
		return jsonValue{}, err
	}

	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return jsonValue{}, errors.New("json document contains trailing value")
		}
		return jsonValue{}, err
	}
	return value, nil
}

func decodeJSONValue(decoder *json.Decoder) (jsonValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return jsonValue{}, err
	}

	switch value := token.(type) {
	case nil:
		return jsonValue{kind: jsonNull}, nil
	case bool:
		return jsonValue{kind: jsonBool, bool: value}, nil
	case string:
		return jsonValue{kind: jsonString, string: value}, nil
	case json.Number:
		number, err := normalizeDecimalNumber(value.String())
		if err != nil {
			return jsonValue{}, err
		}
		return jsonValue{kind: jsonNumber, number: number}, nil
	case json.Delim:
		switch value {
		case '[':
			return decodeJSONArray(decoder)
		case '{':
			return decodeJSONObject(decoder)
		default:
			return jsonValue{}, fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	default:
		return jsonValue{}, fmt.Errorf("unexpected JSON token %T", token)
	}
}

func decodeJSONArray(decoder *json.Decoder) (jsonValue, error) {
	values := make([]jsonValue, 0)
	for decoder.More() {
		value, err := decodeJSONValue(decoder)
		if err != nil {
			return jsonValue{}, err
		}
		values = append(values, value)
	}
	end, err := decoder.Token()
	if err != nil {
		return jsonValue{}, err
	}
	if delim, ok := end.(json.Delim); !ok || delim != ']' {
		return jsonValue{}, errors.New("unterminated JSON array")
	}
	return jsonValue{kind: jsonArray, array: values}, nil
}

func decodeJSONObject(decoder *json.Decoder) (jsonValue, error) {
	values := make(map[string]jsonValue)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return jsonValue{}, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return jsonValue{}, errors.New("JSON object key is not a string")
		}
		if _, exists := values[key]; exists {
			return jsonValue{}, fmt.Errorf("duplicate JSON object key %q", key)
		}
		value, err := decodeJSONValue(decoder)
		if err != nil {
			return jsonValue{}, err
		}
		values[key] = value
	}
	end, err := decoder.Token()
	if err != nil {
		return jsonValue{}, err
	}
	if delim, ok := end.(json.Delim); !ok || delim != '}' {
		return jsonValue{}, errors.New("unterminated JSON object")
	}
	return jsonValue{kind: jsonObject, object: values}, nil
}

func normalizeDecimalNumber(raw string) (decimalNumber, error) {
	if raw == "" {
		return decimalNumber{}, errors.New("empty JSON number")
	}

	negative := strings.HasPrefix(raw, "-")
	if negative {
		raw = raw[1:]
	}

	exponent := new(big.Int)
	mantissa := raw
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		mantissa = raw[:index]
		exponentText := strings.TrimPrefix(raw[index+1:], "+")
		if exponentText == "" {
			return decimalNumber{}, fmt.Errorf("invalid JSON number %q", raw)
		}
		if _, ok := exponent.SetString(exponentText, 10); !ok {
			return decimalNumber{}, fmt.Errorf("invalid JSON exponent %q", exponentText)
		}
	}

	fractionDigits := 0
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		fractionDigits = len(mantissa) - index - 1
		mantissa = mantissa[:index] + mantissa[index+1:]
	}
	if mantissa == "" {
		return decimalNumber{}, fmt.Errorf("invalid JSON number %q", raw)
	}

	coefficient := strings.TrimLeft(mantissa, "0")
	if coefficient == "" {
		return decimalNumber{digits: "0", exponent: new(big.Int)}, nil
	}

	exponent.Sub(exponent, big.NewInt(int64(fractionDigits)))
	coefficient = strings.TrimRight(coefficient, "0")
	trailingZeroes := len(mantissa) - len(strings.TrimRight(mantissa, "0"))
	if trailingZeroes > 0 {
		exponent.Add(exponent, big.NewInt(int64(trailingZeroes)))
	}
	return decimalNumber{negative: negative, digits: coefficient, exponent: exponent}, nil
}

func equalJSONValues(a, b jsonValue) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case jsonNull:
		return true
	case jsonBool:
		return a.bool == b.bool
	case jsonString:
		return a.string == b.string
	case jsonNumber:
		return equalDecimalNumbers(a.number, b.number)
	case jsonArray:
		if len(a.array) != len(b.array) {
			return false
		}
		for index := range a.array {
			if !equalJSONValues(a.array[index], b.array[index]) {
				return false
			}
		}
		return true
	case jsonObject:
		if len(a.object) != len(b.object) {
			return false
		}
		for key, value := range a.object {
			other, ok := b.object[key]
			if !ok || !equalJSONValues(value, other) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func equalDecimalNumbers(a, b decimalNumber) bool {
	if a.negative != b.negative || a.digits != b.digits {
		return false
	}
	return a.exponent.Cmp(b.exponent) == 0
}
