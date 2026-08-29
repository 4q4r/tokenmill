// Package folding provides exact repeated-line and repeated-block folding for
// logs and diffs. Every folded block is embedded in a length-framed
// dictionary; a decoder never has to guess or rely on an omitted-line marker.
package folding

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/detector"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

const (
	envelopeMagic       = "TMFOLD1"
	maxFoldInputBytes   = 16 << 20
	maxFoldLines        = 100_000
	maxDictionary       = 1024
	maxBlockBytes       = 4 << 20
	maxRepeatCount      = 1_000_000
	maxDecodedBytes     = 64 << 20
	maxControlLineSize  = 128
	defaultMinLineRun   = 3
	defaultMinBlockRun  = 2
	defaultMaxBlockSize = 20
)

// Codec folds exact adjacent lines and multi-line blocks. It is byte-lossless
// but marked opt-in by the feature registry because compact instruction
// streams can be less immediately readable to a model.
type Codec struct {
	MinLineRepeat  int
	MinBlockRepeat int
	MinBlockLines  int
	MaxBlockLines  int
	AllowCode      bool
}

// New returns a conservative folding codec. Fenced and detector-classified
// code blocks are excluded unless AllowCode is explicitly enabled.
func New() *Codec {
	return &Codec{
		MinLineRepeat:  defaultMinLineRun,
		MinBlockRepeat: defaultMinBlockRun,
		MinBlockLines:  2,
		MaxBlockLines:  defaultMaxBlockSize,
	}
}

// NewWithConfig returns a folding codec with explicit fold limits.
func NewWithConfig(minLineRepeat, minBlockRepeat, minBlockLines, maxBlockLines int, allowCode bool) *Codec {
	return &Codec{
		MinLineRepeat:  minLineRepeat,
		MinBlockRepeat: minBlockRepeat,
		MinBlockLines:  minBlockLines,
		MaxBlockLines:  maxBlockLines,
		AllowCode:      allowCode,
	}
}

func (c *Codec) ID() string { return "diff-log-fold" }

func (c *Codec) minLineRepeat() int {
	if c.MinLineRepeat < 2 {
		return defaultMinLineRun
	}
	return c.MinLineRepeat
}

func (c *Codec) minBlockRepeat() int {
	if c.MinBlockRepeat < 2 {
		return defaultMinBlockRun
	}
	return c.MinBlockRepeat
}

func (c *Codec) minBlockLines() int {
	if c.MinBlockLines < 2 {
		return 2
	}
	return c.MinBlockLines
}

func (c *Codec) maxBlockLines() int {
	if c.MaxBlockLines < c.minBlockLines() {
		return c.minBlockLines()
	}
	if c.MaxBlockLines > defaultMaxBlockSize {
		return defaultMaxBlockSize
	}
	return c.MaxBlockLines
}

// Detect reports whether an exact adjacent line or block repeat exists. Code
// blocks are excluded by default as a model-quality firewall.
func (c *Codec) Detect(input string) bool {
	if input == "" || len(input) > maxFoldInputBytes {
		return false
	}
	if !c.AllowCode {
		if code, _ := detector.IsCodeBlock(input); code {
			return false
		}
	}
	_, _, found, err := buildPlan(input, c)
	return err == nil && found
}

// EstimateSavings returns exact tokenizer savings for the embedded fold
// envelope, or -1 when the candidate should be skipped.
func (c *Codec) EstimateSavings(input string) int {
	if !c.Detect(input) {
		return -1
	}
	candidate, err := c.canonicalize(input)
	if err != nil || candidate == input {
		return -1
	}
	saving := tokenizer.Count(input) - tokenizer.Count(candidate)
	if saving <= 0 {
		return -1
	}
	return saving
}

// Encode returns a fold envelope only when it is strictly smaller by
// tokenizer.Count. If the instruction stream costs more, input passes
// through unchanged.
func (c *Codec) Encode(input string) (string, error) {
	candidate, err := c.canonicalize(input)
	if err != nil {
		return "", err
	}
	if candidate == input || tokenizer.Count(candidate) >= tokenizer.Count(input) {
		return input, nil
	}
	if !c.Verify(input, candidate) {
		return "", errors.New("folding: internal round-trip verification failed")
	}
	return candidate, nil
}

// Decode expands a self-contained fold envelope. Non-envelope input is
// returned unchanged for the Encode pass-through path.
func (c *Codec) Decode(encoded string) (string, error) {
	if !strings.HasPrefix(encoded, envelopeMagic) {
		return encoded, nil
	}
	return decodeEnvelope(encoded)
}

// Verify checks exact byte equality after decoding.
func (c *Codec) Verify(original, encoded string) bool {
	if original == encoded {
		return true
	}
	decoded, err := c.Decode(encoded)
	return err == nil && codec.VerifyBytes([]byte(original), []byte(decoded))
}

// Canonicalize builds the fold envelope without applying the tokenizer gate.
// It is useful for diagnostics and for tests; model-facing callers should use
// Encode.
func Canonicalize(input string) (string, error) {
	return New().canonicalize(input)
}

func (c *Codec) canonicalize(input string) (string, error) {
	if input == "" {
		return input, nil
	}
	if len(input) > maxFoldInputBytes {
		return "", errors.New("folding: input exceeds size limit")
	}
	if !c.AllowCode {
		if code, _ := detector.IsCodeBlock(input); code {
			return input, nil
		}
	}
	dictionary, operations, found, err := buildPlan(input, c)
	if err != nil {
		return "", err
	}
	if !found {
		return input, nil
	}
	return buildEnvelope(dictionary, operations), nil
}

type operation struct {
	kind    byte
	literal []byte
	dictID  int
	repeats int
}

func buildPlan(input string, c *Codec) ([][]byte, []operation, bool, error) {
	lines, err := splitLines(input)
	if err != nil {
		return nil, nil, false, err
	}
	if len(lines) < 2 {
		return nil, nil, false, nil
	}

	dictionary := make([][]byte, 0)
	dictIDs := make(map[string]int)
	operations := make([]operation, 0)
	literalStart := 0
	found := false
	for position := 0; position < len(lines); {
		size, repeats, covered := bestRepeat(lines, position, c)
		if covered == 0 {
			position++
			continue
		}
		if literalStart < position {
			literal := joinLines(lines[literalStart:position])
			operations = append(operations, operation{kind: 'L', literal: literal})
		}
		block := joinLines(lines[position : position+size])
		key := string(block)
		dictID, ok := dictIDs[key]
		if !ok {
			if len(dictionary) >= maxDictionary {
				return nil, nil, false, errors.New("folding: dictionary limit exceeded")
			}
			dictID = len(dictionary)
			dictIDs[key] = dictID
			dictionary = append(dictionary, block)
		}
		operations = append(operations, operation{kind: 'R', dictID: dictID, repeats: repeats})
		position += covered
		literalStart = position
		found = true
	}
	if literalStart < len(lines) {
		operations = append(operations, operation{kind: 'L', literal: joinLines(lines[literalStart:])})
	}
	return dictionary, operations, found, nil
}

func splitLines(input string) ([][]byte, error) {
	if len(input) > maxFoldInputBytes {
		return nil, errors.New("folding: input exceeds size limit")
	}
	data := []byte(input)
	if len(data) == 0 {
		return nil, nil
	}
	lines := make([][]byte, 0, 32)
	start := 0
	for position, value := range data {
		if value != '\n' {
			continue
		}
		lines = append(lines, append([]byte(nil), data[start:position+1]...))
		if len(lines) > maxFoldLines {
			return nil, errors.New("folding: line count exceeds size limit")
		}
		start = position + 1
	}
	if start < len(data) {
		lines = append(lines, append([]byte(nil), data[start:]...))
		if len(lines) > maxFoldLines {
			return nil, errors.New("folding: line count exceeds size limit")
		}
	}
	return lines, nil
}

func bestRepeat(lines [][]byte, position int, c *Codec) (int, int, int) {
	bestSize := 0
	bestRepeats := 0
	bestCovered := 0
	maxSize := c.maxBlockLines()
	if remaining := (len(lines) - position) / 2; maxSize > remaining {
		maxSize = remaining
	}
	for size := 1; size <= maxSize; size++ {
		minimum := c.minBlockRepeat()
		if size == 1 {
			minimum = c.minLineRepeat()
		} else if size < c.minBlockLines() {
			continue
		}
		if position+size*minimum > len(lines) {
			continue
		}
		repeats := 1
		for position+(repeats+1)*size <= len(lines) && blocksEqual(lines, position, position+repeats*size, size) {
			repeats++
		}
		if repeats < minimum {
			continue
		}
		covered := size * repeats
		if covered > bestCovered || (covered == bestCovered && size > bestSize) {
			bestSize = size
			bestRepeats = repeats
			bestCovered = covered
		}
	}
	return bestSize, bestRepeats, bestCovered
}

func blocksEqual(lines [][]byte, left, right, size int) bool {
	for offset := 0; offset < size; offset++ {
		if !bytes.Equal(lines[left+offset], lines[right+offset]) {
			return false
		}
	}
	return true
}

func joinLines(lines [][]byte) []byte {
	var joined bytes.Buffer
	for _, line := range lines {
		_, _ = joined.Write(line)
	}
	return joined.Bytes()
}

func buildEnvelope(dictionary [][]byte, operations []operation) string {
	var envelope bytes.Buffer
	envelope.WriteString(envelopeMagic)
	envelope.WriteString("\nD:")
	envelope.WriteString(strconv.Itoa(len(dictionary)))
	envelope.WriteString("\nO:")
	envelope.WriteString(strconv.Itoa(len(operations)))
	envelope.WriteString("\n\n")
	for _, block := range dictionary {
		envelope.WriteString("D\n")
		envelope.WriteString(strconv.Itoa(len(block)))
		envelope.WriteByte('\n')
		_, _ = envelope.Write(block)
		envelope.WriteByte('\n')
	}
	for _, operation := range operations {
		switch operation.kind {
		case 'L':
			envelope.WriteString("L\n")
			envelope.WriteString(strconv.Itoa(len(operation.literal)))
			envelope.WriteByte('\n')
			_, _ = envelope.Write(operation.literal)
			envelope.WriteByte('\n')
		case 'R':
			envelope.WriteString("R\n")
			envelope.WriteString(strconv.Itoa(operation.dictID))
			envelope.WriteByte('\n')
			envelope.WriteString(strconv.Itoa(operation.repeats))
			envelope.WriteByte('\n')
		}
	}
	return envelope.String()
}

func decodeEnvelope(encoded string) (string, error) {
	if len(encoded) > maxFoldInputBytes+maxDecodedBytes {
		return "", errors.New("folding: envelope exceeds size limit")
	}
	data := []byte(encoded)
	position := 0
	line, next, err := readControlLine(data, position)
	if err != nil || string(line) != envelopeMagic {
		return "", errors.New("folding: invalid envelope magic")
	}
	position = next

	dictionaryLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	dictionaryCount, err := parsePrefixedCount(dictionaryLine, 'D', maxDictionary)
	if err != nil || dictionaryCount == 0 {
		return "", errors.New("folding: invalid dictionary count")
	}
	position = next

	operationLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	operationCount, err := parsePrefixedCount(operationLine, 'O', maxFoldLines*2)
	if err != nil || operationCount == 0 {
		return "", errors.New("folding: invalid operation count")
	}
	position = next

	blank, next, err := readControlLine(data, position)
	if err != nil || len(blank) != 0 {
		return "", errors.New("folding: missing envelope separator")
	}
	position = next

	dictionary := make([][]byte, dictionaryCount)
	for i := range dictionary {
		command, next, err := readControlLine(data, position)
		if err != nil || string(command) != "D" {
			return "", errors.New("folding: malformed dictionary entry command")
		}
		position = next
		lengthLine, next, err := readControlLine(data, position)
		if err != nil {
			return "", err
		}
		length, err := parseLength(lengthLine, maxBlockBytes)
		if err != nil {
			return "", err
		}
		position = next
		block, next, err := readFramedBytes(data, position, length)
		if err != nil {
			return "", err
		}
		dictionary[i] = block
		position = next
	}

	var output bytes.Buffer
	for i := 0; i < operationCount; i++ {
		command, next, err := readControlLine(data, position)
		if err != nil {
			return "", err
		}
		position = next
		switch string(command) {
		case "L":
			lengthLine, next, err := readControlLine(data, position)
			if err != nil {
				return "", err
			}
			length, err := parseLength(lengthLine, maxDecodedBytes)
			if err != nil {
				return "", err
			}
			position = next
			literal, next, err := readFramedBytes(data, position, length)
			if err != nil {
				return "", err
			}
			if err := appendBounded(&output, literal); err != nil {
				return "", err
			}
			position = next
		case "R":
			idLine, next, err := readControlLine(data, position)
			if err != nil {
				return "", err
			}
			id, err := parseIndex(idLine, len(dictionary))
			if err != nil {
				return "", err
			}
			position = next
			countLine, next, err := readControlLine(data, position)
			if err != nil {
				return "", err
			}
			count, err := parsePositiveCount(countLine)
			if err != nil || count > maxRepeatCount {
				return "", errors.New("folding: repeat count is out of bounds")
			}
			position = next
			block := dictionary[id]
			if len(block) == 0 || len(block) > maxBlockBytes || count > (maxDecodedBytes-output.Len())/len(block) {
				return "", errors.New("folding: repeated output exceeds size limit")
			}
			for repeat := 0; repeat < count; repeat++ {
				_, _ = output.Write(block)
			}
		default:
			return "", errors.New("folding: unknown operation command")
		}
	}
	if position != len(data) {
		return "", errors.New("folding: trailing bytes after operations")
	}
	return output.String(), nil
}

func readControlLine(data []byte, position int) ([]byte, int, error) {
	if position >= len(data) {
		return nil, position, errors.New("folding: truncated envelope")
	}
	newline := bytes.IndexByte(data[position:], '\n')
	if newline < 0 || newline > maxControlLineSize {
		return nil, position, errors.New("folding: invalid control line")
	}
	newline += position
	return data[position:newline], newline + 1, nil
}

func readFramedBytes(data []byte, position, length int) ([]byte, int, error) {
	if length < 0 || length > len(data)-position {
		return nil, position, errors.New("folding: framed payload exceeds input")
	}
	end := position + length
	if end >= len(data) || data[end] != '\n' {
		return nil, position, errors.New("folding: missing framed payload separator")
	}
	return append([]byte(nil), data[position:end]...), end + 1, nil
}

func parsePrefixedCount(line []byte, prefix byte, max int) (int, error) {
	if len(line) < 3 || line[0] != prefix || line[1] != ':' {
		return 0, fmt.Errorf("folding: expected %c: count", prefix)
	}
	value, err := strconv.Atoi(string(line[2:]))
	if err != nil || value < 1 || value > max {
		return 0, errors.New("folding: count is out of bounds")
	}
	return value, nil
}

func parseLength(line []byte, max int) (int, error) {
	value, err := strconv.Atoi(string(line))
	if err != nil || value < 1 || value > max {
		return 0, errors.New("folding: payload length is out of bounds")
	}
	return value, nil
}

func parseIndex(line []byte, dictionarySize int) (int, error) {
	value, err := strconv.Atoi(string(line))
	if err != nil || value < 0 || value >= dictionarySize {
		return 0, errors.New("folding: dictionary index is invalid")
	}
	return value, nil
}

func parsePositiveCount(line []byte) (int, error) {
	value, err := strconv.Atoi(string(line))
	if err != nil || value < 1 {
		return 0, errors.New("folding: repeat count is invalid")
	}
	return value, nil
}

func appendBounded(output *bytes.Buffer, value []byte) error {
	if len(value) > maxDecodedBytes-output.Len() {
		return errors.New("folding: decoded output exceeds size limit")
	}
	_, _ = output.Write(value)
	return nil
}

var _ codec.LosslessCodec = (*Codec)(nil)
