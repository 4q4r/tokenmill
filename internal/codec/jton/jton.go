package jton

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

// Codec is LosslessCodec for jton-zen.
// ID = "jton-zen", Detect via IsHomogeneousJSONArray rows>=10, Encode to [N: cols; row;...]
type Codec struct {
	MinRows int
}

func New() *Codec { return &Codec{MinRows: 10} }

func (c *Codec) ID() string { return "jton-zen" }

func (c *Codec) minRows() int {
	if c.MinRows <= 0 {
		return 10
	}
	return c.MinRows
}

// IsHomogeneousJSONArray checks uniform object array.
func IsHomogeneousJSONArray(input string) (bool, []string, int) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false, nil, 0
	}
	// Try JSONArray
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		if len(arr) < 2 {
			return false, nil, len(arr)
		}
		cols, ok := checkHomogeneous(arr)
		if !ok {
			return false, nil, len(arr)
		}
		return true, cols, len(arr)
	}
	// Try JSONL fallback: each line is JSON object
	lines := strings.Split(strings.TrimSpace(input), "\n")
	// Filter empty
	var nonEmpty []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	if len(nonEmpty) < 2 {
		return false, nil, 0
	}
	var arr2 []json.RawMessage
	for _, l := range nonEmpty {
		if !json.Valid([]byte(l)) {
			return false, nil, 0
		}
		// Must be object
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			return false, nil, 0
		}
		arr2 = append(arr2, json.RawMessage(l))
	}
	if len(arr2) >= 2 {
		cols, ok := checkHomogeneous(arr2)
		if ok {
			return true, cols, len(arr2)
		}
	}
	return false, nil, 0
}

func checkHomogeneous(arr []json.RawMessage) ([]string, bool) {
	if len(arr) == 0 {
		return nil, false
	}
	for _, raw := range arr {
		if !codec.VerifyJSON(string(raw), string(raw)) {
			return nil, false
		}
	}
	var first map[string]json.RawMessage
	if err := json.Unmarshal(arr[0], &first); err != nil {
		return nil, false
	}
	if len(first) == 0 {
		return nil, false
	}
	cols := make([]string, 0, len(first))
	for k := range first {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	// also ensure first has exactly cols
	for i := 1; i < len(arr); i++ {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(arr[i], &m); err != nil {
			return nil, false
		}
		if len(m) != len(cols) {
			return nil, false
		}
		for _, col := range cols {
			if _, ok := m[col]; !ok {
				return nil, false
			}
		}
		// also check no extra keys
		for k := range m {
			found := false
			for _, c := range cols {
				if k == c {
					found = true
					break
				}
			}
			if !found {
				return nil, false
			}
		}
	}
	return cols, true
}

func (c *Codec) Detect(input string) bool {
	ok, _, n := IsHomogeneousJSONArray(input)
	if !ok {
		return false
	}
	if n < c.minRows() {
		return false
	}
	return true
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
	return saving
}

// Encode implements Zen Grid.
// Input: homogeneous JSON array (or JSONL). Output: [N: cols; row; ...] format.
func (c *Codec) Encode(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", errors.New("empty input")
	}
	// Try JSONArray first
	var arr []json.RawMessage
	var cols []string
	var objects []map[string]json.RawMessage
	isArray := false
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		// Check if it's array of objects
		// Verify each element is object
		tempCols, ok := checkHomogeneous(arr)
		if !ok {
			return "", errors.New("not homogeneous array")
		}
		cols = tempCols
		// Build objects slice
		for _, raw := range arr {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				return "", errors.New("element not object")
			}
			objects = append(objects, m)
		}
		isArray = true
	} else {
		// Try JSONL
		lines := strings.Split(trimmed, "\n")
		var nonEmpty []string
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t != "" {
				nonEmpty = append(nonEmpty, t)
			}
		}
		if len(nonEmpty) < 1 {
			return "", errors.New("not json array or jsonl")
		}
		var arr2 []json.RawMessage
		for _, l := range nonEmpty {
			if !json.Valid([]byte(l)) {
				return "", errors.New("invalid jsonl line")
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				return "", errors.New("jsonl element not object")
			}
			arr2 = append(arr2, json.RawMessage(l))
		}
		tempCols, ok := checkHomogeneous(arr2)
		if !ok {
			return "", errors.New("jsonl not homogeneous")
		}
		cols = tempCols
		for _, raw := range arr2 {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(raw, &m)
			objects = append(objects, m)
		}
		// isArray = false, but we still encode same format
	}
	_ = isArray
	return encodeZen(objects, cols)
}

func encodeZen(objects []map[string]json.RawMessage, cols []string) (string, error) {
	n := len(objects)
	if n == 0 {
		return "", errors.New("no rows")
	}
	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(strconv.Itoa(n))
	sb.WriteString(": ")
	sb.WriteString(strings.Join(cols, ", "))
	sb.WriteString(";")
	// Each row
	for _, obj := range objects {
		sb.WriteString(" ")
		var vals []string
		for _, col := range cols {
			raw, ok := obj[col]
			if !ok {
				return "", fmt.Errorf("missing col %s", col)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, raw); err != nil {
				return "", fmt.Errorf("invalid JSON value in column %s: %w", col, err)
			}
			vals = append(vals, compact.String())
		}
		sb.WriteString(strings.Join(vals, ", "))
		sb.WriteString(";")
	}
	// Remove last ";"? Spec example includes trailing? Example "[2: id, name; 1, \"Alice\"; 2, \"Bob\"]" has no trailing ; inside before ]. But our builder adds trailing ; for each row. Remove last char if it's ";"
	str := sb.String()
	if strings.HasSuffix(str, ";") {
		str = str[:len(str)-1]
	}
	str += "]"
	return str, nil
}

// Decode reverses Encode.
func (c *Codec) Decode(encoded string) (string, error) {
	return decodeZen(encoded)
}

func decodeZen(encoded string) (string, error) {
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return "", errors.New("empty")
	}
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", errors.New("not zen grid format")
	}
	inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if inner == "" {
		return "", errors.New("empty inner")
	}
	// Split by ";" top-level
	parts := splitTopLevel(inner, ';')
	if len(parts) == 0 {
		return "", errors.New("no parts")
	}
	header := strings.TrimSpace(parts[0])
	colonIdx := strings.Index(header, ":")
	if colonIdx == -1 {
		return "", errors.New("missing colon in header")
	}
	nStr := strings.TrimSpace(header[:colonIdx])
	colsStr := strings.TrimSpace(header[colonIdx+1:])
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return "", fmt.Errorf("invalid N: %w", err)
	}
	var cols []string
	if colsStr != "" {
		colsParts := splitTopLevel(colsStr, ',')
		for _, p := range colsParts {
			t := strings.TrimSpace(p)
			if t != "" {
				cols = append(cols, t)
			}
		}
	}
	if len(cols) == 0 {
		return "", errors.New("no cols")
	}
	// Rows are remaining parts
	rowParts := parts[1:]
	// Filter empty due to trailing maybe
	var filteredRows []string
	for _, r := range rowParts {
		t := strings.TrimSpace(r)
		if t != "" {
			filteredRows = append(filteredRows, t)
		}
	}
	if len(filteredRows) != n {
		// Allow mismatch? But spec says N=row count. If mismatch, error
		// For tolerance, if mismatch, use actual filtered count
		if n != len(filteredRows) {
			// If mismatch, but we can still decode with actual count? We'll error if strict
			// For robustness, allow but warn; but per strict superset, should match
			// We'll return error if mismatch
			return "", fmt.Errorf("row count mismatch: header N=%d but got %d rows", n, len(filteredRows))
		}
	}
	var objects []map[string]json.RawMessage
	for _, rowStr := range filteredRows {
		vals := splitTopLevel(rowStr, ',')
		// Trim and filter empty?
		var trimmedVals []string
		for _, v := range vals {
			t := strings.TrimSpace(v)
			if t != "" {
				trimmedVals = append(trimmedVals, t)
			} else if len(vals) == 1 && t == "" {
				// empty value? keep?
				trimmedVals = append(trimmedVals, t)
			}
		}
		// Handle case where row has no values? error
		if len(trimmedVals) != len(cols) {
			// Try to handle if values contain commas inside nested structures our split should have handled
			// If mismatch, error
			return "", fmt.Errorf("column/value mismatch: cols %d vs vals %d in row %q", len(cols), len(trimmedVals), rowStr)
		}
		m := make(map[string]json.RawMessage, len(cols))
		for i, col := range cols {
			valStr := strings.TrimSpace(trimmedVals[i])
			if valStr == "" {
				// Treat empty as null?
				valStr = "null"
			}
			// Validate JSON
			if !json.Valid([]byte(valStr)) {
				// If bare string without quotes? The spec says Bare_strings optionally false, but if we encounter bare string we should quote it.
				// For now, if not valid json, try to quote as string
				// Example bare_strings: hello -> "hello"
				quoted, _ := json.Marshal(valStr)
				valStr = string(quoted)
			}
			m[col] = json.RawMessage(valStr)
		}
		objects = append(objects, m)
	}
	// Marshal to JSON array
	out, err := json.Marshal(objects)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func splitTopLevel(s string, sep rune) []string {
	var parts []string
	var cur strings.Builder
	inString := false
	escaped := false
	depthBrace := 0
	depthBracket := 0
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			cur.WriteRune(r)
			continue
		}
		if r == '"' {
			inString = !inString
			cur.WriteRune(r)
			continue
		}
		if !inString {
			switch r {
			case '{':
				depthBrace++
			case '}':
				if depthBrace > 0 {
					depthBrace--
				}
			case '[':
				depthBracket++
			case ']':
				if depthBracket > 0 {
					depthBracket--
				}
			}
			if r == sep && depthBrace == 0 && depthBracket == 0 {
				parts = append(parts, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
		}
		cur.WriteRune(r)
	}
	parts = append(parts, strings.TrimSpace(cur.String()))
	return parts
}

func (c *Codec) Verify(original, encoded string) bool {
	decoded, err := c.Decode(encoded)
	if err != nil {
		return false
	}
	// Try direct JSON verify first (handles array case)
	if codec.VerifyJSON(original, decoded) {
		return true
	}
	// Handle JSONL vs array: compare validated raw documents.
	origSlice, err1 := parseJSONDocuments(original)
	decSlice, err2 := parseJSONDocuments(decoded)
	if err1 != nil || err2 != nil {
		return false
	}
	if len(origSlice) != len(decSlice) {
		return false
	}
	for i := range origSlice {
		if !codec.VerifyJSON(origSlice[i], decSlice[i]) {
			return false
		}
	}
	return true
}

func parseJSONDocuments(s string) ([]string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, errors.New("empty")
	}
	if strings.HasPrefix(trimmed, "[") {
		var values []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			text := string(value)
			if !codec.VerifyJSON(text, text) {
				return nil, errors.New("invalid or duplicate-key JSON value")
			}
			out = append(out, text)
		}
		return out, nil
	}

	lines := strings.Split(trimmed, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if !codec.VerifyJSON(text, text) {
			return nil, errors.New("invalid or duplicate-key JSONL value")
		}
		out = append(out, text)
	}
	if len(out) == 0 {
		return nil, errors.New("not parseable")
	}
	return out, nil
}

// JSONLToZen helper: converts JSONL string to Zen Grid via same logic
func JSONLToZen(input string) (string, error) {
	// Reuse Codec Encode which handles JSONL fallback
	c := New()
	_ = c
	// Bypass MinRows check: directly encode
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", errors.New("empty")
	}
	lines := strings.Split(trimmed, "\n")
	var nonEmpty []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	var arr []json.RawMessage
	for _, l := range nonEmpty {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			return "", err
		}
		arr = append(arr, json.RawMessage(l))
	}
	cols, ok := checkHomogeneous(arr)
	if !ok {
		return "", errors.New("not homogeneous jsonl")
	}
	var objects []map[string]json.RawMessage
	for _, raw := range arr {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		objects = append(objects, m)
	}
	return encodeZen(objects, cols)
}

var _ codec.LosslessCodec = (*Codec)(nil)
