// Package csvcanonical provides a conservative, byte-lossless CSV/TSV
// canonicalizer. The wire envelope carries the original delimiter, record
// endings, and per-field quote decisions, so canonicalization never relies on
// a lossy "header-only" dictionary.
package csvcanonical

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

const (
	envelopeMagic         = "TMCV1"
	maxCSVBytes           = 16 << 20
	maxCSVRecords         = 100_000
	maxCSVFieldsPerRow    = 1_000_000
	maxCSVHeaderLineBytes = 1 << 20
	maxDecodedCSVBytes    = 32 << 20
)

// Codec canonicalizes valid CSV/TSV with a self-contained byte-lossless
// envelope. It is deliberately not registered in the default tournament.
type Codec struct{}

// New returns a CSV/TSV canonicalization codec.
func New() *Codec { return &Codec{} }

func (c *Codec) ID() string { return "csv-canonical" }

// Detect reports whether input is a regular, multi-record CSV or TSV stream.
// Fenced code blocks are excluded so a future model-facing integration cannot
// silently rewrite examples that merely look tabular.
func (c *Codec) Detect(input string) bool {
	if input == "" || len(input) > maxCSVBytes || containsCodeFence(input) {
		return false
	}
	_, err := parseInput(input)
	return err == nil
}

// EstimateSavings returns the exact tokenizer saving for the canonical
// envelope. A non-positive result is reported as -1 so callers can skip it.
func (c *Codec) EstimateSavings(input string) int {
	if !c.Detect(input) {
		return -1
	}
	candidate, err := Canonicalize(input)
	if err != nil || candidate == input {
		return -1
	}
	saving := tokenizer.Count(input) - tokenizer.Count(candidate)
	if saving <= 0 {
		return -1
	}
	return saving
}

// Encode returns the canonical envelope only when it is strictly smaller in
// tokenizer.Count terms. Otherwise it returns the original bytes unchanged.
func (c *Codec) Encode(input string) (string, error) {
	candidate, err := Canonicalize(input)
	if err != nil {
		return "", err
	}
	if candidate == input || tokenizer.Count(candidate) >= tokenizer.Count(input) {
		return input, nil
	}
	if !c.Verify(input, candidate) {
		return "", errors.New("csvcanonical: internal round-trip verification failed")
	}
	return candidate, nil
}

// Decode restores the exact original CSV/TSV bytes from an envelope. Non-
// envelope input is treated as the codec's pass-through representation.
func (c *Codec) Decode(encoded string) (string, error) {
	if !strings.HasPrefix(encoded, envelopeMagic) {
		return encoded, nil
	}
	return decodeEnvelope(encoded)
}

// Verify checks byte equality after decoding.
func (c *Codec) Verify(original, encoded string) bool {
	if original == encoded {
		return true
	}
	decoded, err := c.Decode(encoded)
	return err == nil && codec.VerifyBytes([]byte(original), []byte(decoded))
}

// Canonicalize builds a canonical tab-delimited envelope without applying the
// tokenizer-size gate. Encode is the model-facing, size-gated entry point.
func Canonicalize(input string) (string, error) {
	if len(input) > maxCSVBytes {
		return "", errors.New("csvcanonical: input exceeds size limit")
	}
	if containsCodeFence(input) {
		return input, nil
	}
	parsed, err := parseInput(input)
	if err != nil {
		return "", err
	}

	totalFields := 0
	quoteBits := make([]byte, 0)
	var body strings.Builder
	var endings strings.Builder
	for rowIndex, record := range parsed.records {
		if rowIndex > 0 && body.Len() == 0 {
			return "", errors.New("csvcanonical: empty canonical body")
		}
		for fieldIndex, field := range record.fields {
			if fieldIndex > 0 {
				body.WriteByte('\t')
			}
			writeCanonicalField(&body, field.value)
			if field.quoted {
				if totalFields%8 == 0 {
					quoteBits = append(quoteBits, 0)
				}
				quoteBits[totalFields/8] |= 1 << uint(totalFields%8)
			} else if totalFields%8 == 0 {
				quoteBits = append(quoteBits, 0)
			}
			totalFields++
		}
		switch record.ending {
		case 'L':
			body.WriteByte('\n')
			endings.WriteByte('L')
		case 'C':
			body.WriteByte('\n')
			endings.WriteByte('C')
		case 'N':
			endings.WriteByte('N')
		default:
			return "", errors.New("csvcanonical: unknown record ending")
		}
	}

	var envelope strings.Builder
	envelope.WriteString(envelopeMagic)
	envelope.WriteByte('\n')
	envelope.WriteString("D:")
	envelope.WriteString(hex.EncodeToString([]byte{parsed.delimiter}))
	envelope.WriteByte('\n')
	envelope.WriteString("R:")
	envelope.WriteString(strconv.Itoa(len(parsed.records)))
	envelope.WriteByte('\n')
	envelope.WriteString("E:")
	envelope.WriteString(endings.String())
	envelope.WriteByte('\n')
	envelope.WriteString("Q:")
	envelope.WriteString(hex.EncodeToString(quoteBits))
	envelope.WriteString("\n\n")
	envelope.WriteString(body.String())
	return envelope.String(), nil
}

type field struct {
	value  []byte
	quoted bool
}

type record struct {
	fields []field
	ending byte
}

type parsedCSV struct {
	delimiter byte
	records   []record
}

func parseInput(input string) (parsedCSV, error) {
	if input == "" {
		return parsedCSV{}, errors.New("csvcanonical: empty input")
	}
	if len(input) > maxCSVBytes {
		return parsedCSV{}, errors.New("csvcanonical: input exceeds size limit")
	}

	type candidate struct {
		parsed parsedCSV
		score  int
	}
	var candidates []candidate
	for _, delimiter := range []byte{',', '\t'} {
		records, err := parseCSV(input, delimiter)
		if err != nil || !regularRecords(records) {
			continue
		}
		score := 0
		for _, record := range records {
			score += len(record.fields) - 1
		}
		candidates = append(candidates, candidate{
			parsed: parsedCSV{delimiter: delimiter, records: records},
			score:  score,
		})
	}
	if len(candidates) == 0 {
		return parsedCSV{}, errors.New("csvcanonical: not a regular CSV or TSV stream")
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.score > best.score {
			best = candidate
		}
	}
	return best.parsed, nil
}

func regularRecords(records []record) bool {
	if len(records) < 2 || len(records) > maxCSVRecords || len(records[0].fields) < 2 {
		return false
	}
	fieldCount := len(records[0].fields)
	for _, record := range records {
		if len(record.fields) != fieldCount {
			return false
		}
	}
	return true
}

func parseCSV(input string, delimiter byte) ([]record, error) {
	data := []byte(input)
	if len(data) == 0 || len(data) > maxCSVBytes {
		return nil, errors.New("csvcanonical: CSV input size is invalid")
	}

	var records []record
	position := 0
	for {
		fields := make([]field, 0, 4)
		for {
			var value []byte
			quoted := false
			if position < len(data) && data[position] == '"' {
				quoted = true
				position++
				value = make([]byte, 0, 16)
				closed := false
				for position < len(data) {
					if data[position] != '"' {
						value = append(value, data[position])
						position++
						continue
					}
					if position+1 < len(data) && data[position+1] == '"' {
						value = append(value, '"')
						position += 2
						continue
					}
					position++
					closed = true
					break
				}
				if !closed {
					return nil, errors.New("csvcanonical: unterminated quoted field")
				}
				if position < len(data) && data[position] != delimiter && data[position] != '\n' && data[position] != '\r' {
					return nil, errors.New("csvcanonical: bytes after closing quote")
				}
			} else {
				start := position
				for position < len(data) {
					switch data[position] {
					case delimiter, '\n':
						goto unquotedDone
					case '\r':
						if position+1 < len(data) && data[position+1] == '\n' {
							goto unquotedDone
						}
						return nil, errors.New("csvcanonical: bare carriage return outside quotes")
					case '"':
						return nil, errors.New("csvcanonical: quote in unquoted field")
					default:
						position++
					}
				}
			unquotedDone:
				value = append([]byte(nil), data[start:position]...)
			}

			fields = append(fields, field{value: value, quoted: quoted})
			if len(fields) > maxCSVFieldsPerRow {
				return nil, errors.New("csvcanonical: field count exceeds size limit")
			}
			if position == len(data) {
				records = append(records, record{fields: fields, ending: 'N'})
				if len(records) > maxCSVRecords {
					return nil, errors.New("csvcanonical: record count exceeds size limit")
				}
				return records, nil
			}
			if data[position] == delimiter {
				position++
				continue
			}
			ending, next, err := consumeEnding(data, position)
			if err != nil {
				return nil, err
			}
			records = append(records, record{fields: fields, ending: ending})
			if len(records) > maxCSVRecords {
				return nil, errors.New("csvcanonical: record count exceeds size limit")
			}
			position = next
			if position == len(data) {
				return records, nil
			}
			break
		}
	}
}

func consumeEnding(data []byte, position int) (byte, int, error) {
	switch data[position] {
	case '\n':
		return 'L', position + 1, nil
	case '\r':
		if position+1 < len(data) && data[position+1] == '\n' {
			return 'C', position + 2, nil
		}
		return 0, position, errors.New("csvcanonical: invalid record ending")
	default:
		return 0, position, fmt.Errorf("csvcanonical: unexpected byte 0x%02x after field", data[position])
	}
}

func writeCanonicalField(output *strings.Builder, value []byte) {
	needsQuote := bytes.ContainsAny(value, "\t\"\r\n")
	if !needsQuote {
		output.Write(value)
		return
	}
	output.WriteByte('"')
	for _, b := range value {
		if b == '"' {
			output.WriteString(`""`)
		} else {
			output.WriteByte(b)
		}
	}
	output.WriteByte('"')
}

func decodeEnvelope(encoded string) (string, error) {
	if len(encoded) > maxCSVBytes+maxCSVHeaderLineBytes {
		return "", errors.New("csvcanonical: envelope exceeds size limit")
	}
	data := []byte(encoded)
	position := 0
	line, next, err := readControlLine(data, position)
	if err != nil || string(line) != envelopeMagic {
		return "", errors.New("csvcanonical: invalid envelope magic")
	}
	position = next

	delimiterLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	delimiter, err := parseDelimiterLine(delimiterLine)
	if err != nil {
		return "", err
	}
	position = next

	rowLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	rowCount, err := parseCountLine(rowLine, 'R', maxCSVRecords)
	if err != nil {
		return "", err
	}
	position = next

	endingLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	endingCodes, err := parseEndingLine(endingLine, rowCount)
	if err != nil {
		return "", err
	}
	position = next

	quoteLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	quoteHex, err := parsePrefixedLine(quoteLine, 'Q')
	if err != nil {
		return "", err
	}
	quoteBits, err := hex.DecodeString(quoteHex)
	if err != nil {
		return "", fmt.Errorf("csvcanonical: invalid quote bitmap: %w", err)
	}
	position = next

	blank, next, err := readControlLine(data, position)
	if err != nil || len(blank) != 0 {
		return "", errors.New("csvcanonical: missing envelope separator")
	}
	position = next
	if position >= len(data) {
		return "", errors.New("csvcanonical: empty envelope body")
	}

	bodyRecords, err := parseCSV(string(data[position:]), '\t')
	if err != nil || len(bodyRecords) != rowCount {
		if err != nil {
			return "", fmt.Errorf("csvcanonical: invalid canonical body: %w", err)
		}
		return "", errors.New("csvcanonical: body row count mismatch")
	}
	if !regularRecords(bodyRecords) {
		return "", errors.New("csvcanonical: invalid canonical body shape")
	}
	for _, bodyRecord := range bodyRecords {
		if bodyRecord.ending == 'C' {
			return "", errors.New("csvcanonical: canonical body must use LF record endings")
		}
	}

	totalFields := 0
	for _, bodyRecord := range bodyRecords {
		totalFields += len(bodyRecord.fields)
	}
	expectedQuoteBytes := (totalFields + 7) / 8
	if len(quoteBits) != expectedQuoteBytes {
		return "", fmt.Errorf("csvcanonical: quote bitmap length %d, want %d", len(quoteBits), expectedQuoteBytes)
	}
	if totalFields > 0 && totalFields%8 != 0 {
		unusedMask := byte(0xff << uint(totalFields%8))
		if quoteBits[len(quoteBits)-1]&unusedMask != 0 {
			return "", errors.New("csvcanonical: quote bitmap has non-zero unused bits")
		}
	}

	totalFields = 0
	var output bytes.Buffer
	for rowIndex, bodyRecord := range bodyRecords {
		for fieldIndex, bodyField := range bodyRecord.fields {
			if fieldIndex > 0 {
				output.WriteByte(delimiter)
			}
			quoted := quoteBits[totalFields/8]&(1<<uint(totalFields%8)) != 0
			totalFields++
			if quoted {
				output.WriteByte('"')
				for _, b := range bodyField.value {
					if b == '"' {
						output.WriteString(`""`)
					} else {
						output.WriteByte(b)
					}
				}
				output.WriteByte('"')
			} else {
				if bytes.ContainsAny(bodyField.value, string(delimiter)+`"`+"\r\n") {
					return "", errors.New("csvcanonical: unquoted field cannot be restored")
				}
				output.Write(bodyField.value)
			}
		}
		switch endingCodes[rowIndex] {
		case 'L':
			output.WriteByte('\n')
		case 'C':
			output.WriteString("\r\n")
		case 'N':
		default:
			return "", errors.New("csvcanonical: invalid ending code")
		}
		if output.Len() > maxDecodedCSVBytes {
			return "", errors.New("csvcanonical: decoded output exceeds size limit")
		}
	}
	return output.String(), nil
}

func readControlLine(data []byte, position int) ([]byte, int, error) {
	if position >= len(data) {
		return nil, position, errors.New("csvcanonical: truncated envelope header")
	}
	newline := bytes.IndexByte(data[position:], '\n')
	if newline < 0 {
		return nil, position, errors.New("csvcanonical: unterminated envelope header line")
	}
	newline += position
	if newline-position > maxCSVHeaderLineBytes {
		return nil, position, errors.New("csvcanonical: envelope header line exceeds size limit")
	}
	return data[position:newline], newline + 1, nil
}

func parsePrefixedLine(line []byte, prefix byte) (string, error) {
	if len(line) < 2 || line[0] != prefix || line[1] != ':' {
		return "", fmt.Errorf("csvcanonical: expected %c: header line", prefix)
	}
	return string(line[2:]), nil
}

func parseDelimiterLine(line []byte) (byte, error) {
	value, err := parsePrefixedLine(line, 'D')
	if err != nil {
		return 0, err
	}
	if len(value) != 2 {
		return 0, errors.New("csvcanonical: delimiter must be one byte in hex")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 1 || (decoded[0] != ',' && decoded[0] != '\t') {
		return 0, errors.New("csvcanonical: unsupported original delimiter")
	}
	return decoded[0], nil
}

func parseCountLine(line []byte, prefix byte, max int) (int, error) {
	value, err := parsePrefixedLine(line, prefix)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 2 || count > max {
		return 0, fmt.Errorf("csvcanonical: invalid %c count", prefix)
	}
	return count, nil
}

func parseEndingLine(line []byte, rows int) ([]byte, error) {
	value, err := parsePrefixedLine(line, 'E')
	if err != nil {
		return nil, err
	}
	if len(value) != rows {
		return nil, errors.New("csvcanonical: ending map length mismatch")
	}
	for i := range value {
		if value[i] != 'L' && value[i] != 'C' && value[i] != 'N' {
			return nil, errors.New("csvcanonical: invalid ending map code")
		}
	}
	return []byte(value), nil
}

func containsCodeFence(input string) bool {
	return strings.Contains(input, "```") || strings.Contains(input, "~~~")
}

var _ codec.LosslessCodec = (*Codec)(nil)
