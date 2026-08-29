// Package symboltable provides an exact, self-contained abbreviation codec
// for repeated word-like tokens. The codec is byte-lossless but not
// model-safe by default: callers must explicitly opt it into model-facing
// processing after teaching the model how to expand the markers.
package symboltable

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/detector"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

const (
	envelopeMagic  = "TMST1"
	markerOpen     = "§"
	markerClose    = "§"
	escapeMarker   = "§E§"
	maxSymbolBytes = 16 << 20
	maxDictionary  = 256
	maxTokenBytes  = 64 << 10
	maxTokenSpans  = 1_000_000
	maxOutputBytes = 32 << 20
)

// Codec replaces repeated word-like tokens with markers and embeds the full
// dictionary in its envelope. AllowCode is opt-in because exact reversibility
// alone does not make abbreviated source code a safe model input.
type Codec struct {
	MinOccurrences int
	MaxEntries     int
	AllowCode      bool
}

// New returns a conservative symbol-table codec. Code blocks remain excluded.
func New() *Codec {
	return &Codec{MinOccurrences: 2, MaxEntries: 32}
}

// NewWithConfig returns a symbol-table codec with explicit limits.
func NewWithConfig(minOccurrences, maxEntries int, allowCode bool) *Codec {
	return &Codec{
		MinOccurrences: minOccurrences,
		MaxEntries:     maxEntries,
		AllowCode:      allowCode,
	}
}

func (c *Codec) ID() string { return "symbol-table" }

func (c *Codec) minOccurrences() int {
	if c.MinOccurrences < 2 {
		return 2
	}
	return c.MinOccurrences
}

func (c *Codec) maxEntries() int {
	if c.MaxEntries <= 0 || c.MaxEntries > maxDictionary {
		return maxDictionary
	}
	return c.MaxEntries
}

// Detect reports whether the input has repeated word-like tokens and is safe
// for this codec's default model-facing firewall.
func (c *Codec) Detect(input string) bool {
	if input == "" || len(input) > maxSymbolBytes {
		return false
	}
	if !c.AllowCode {
		if code, _ := detector.IsCodeBlock(input); code {
			return false
		}
	}
	return len(repeatedCandidates(input, c.minOccurrences(), c.maxEntries())) > 0
}

// EstimateSavings returns exact tokenizer savings for the best deterministic
// dictionary prefix. Non-positive savings are reported as -1 for tournament
// callers that use negative values as a skip signal.
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

// Encode returns the self-contained abbreviation only when it strictly
// reduces tokenizer.Count. Otherwise it returns the original bytes unchanged.
func (c *Codec) Encode(input string) (string, error) {
	candidate, err := c.canonicalize(input)
	if err != nil {
		return "", err
	}
	if candidate == input || tokenizer.Count(candidate) >= tokenizer.Count(input) {
		return input, nil
	}
	if !c.Verify(input, candidate) {
		return "", errors.New("symboltable: internal round-trip verification failed")
	}
	return candidate, nil
}

// Decode expands a symbol-table envelope. Non-envelope input is the
// pass-through representation used when Encode skips a candidate.
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

// Canonicalize builds the best deterministic envelope without applying the
// tokenizer-size gate. Encode is the size-gated model-facing entry point.
func Canonicalize(input string) (string, error) {
	return New().canonicalize(input)
}

func (c *Codec) canonicalize(input string) (string, error) {
	if input == "" {
		return input, nil
	}
	if len(input) > maxSymbolBytes {
		return "", errors.New("symboltable: input exceeds size limit")
	}
	if !c.AllowCode {
		if code, _ := detector.IsCodeBlock(input); code {
			return input, nil
		}
	}

	spans := scanTokens(input)
	candidates := sortedCandidates(spans, c.minOccurrences(), c.maxEntries())
	if len(candidates) == 0 {
		return input, nil
	}

	// Marker lengths change at decimal boundaries, so evaluate every prefix.
	// Choosing the lowest exact token count is deterministic and avoids a
	// frequency-only choice whose dictionary header costs more than it saves.
	var best string
	bestTokens := int(^uint(0) >> 1)
	for count := 1; count <= len(candidates); count++ {
		candidate := buildEnvelope(input, spans, candidates[:count])
		tokens := tokenizer.Count(candidate)
		if tokens < bestTokens || (tokens == bestTokens && (best == "" || candidate < best)) {
			best = candidate
			bestTokens = tokens
		}
	}
	return best, nil
}

type tokenSpan struct {
	start int
	end   int
	value string
}

func scanTokens(input string) []tokenSpan {
	var spans []tokenSpan
	for position := 0; position < len(input); {
		runeValue, size := utf8.DecodeRuneInString(input[position:])
		if size == 0 {
			break
		}
		if !isTokenRune(runeValue) {
			position += size
			continue
		}
		start := position
		position += size
		for position < len(input) {
			nextRune, nextSize := utf8.DecodeRuneInString(input[position:])
			if !isTokenRune(nextRune) {
				break
			}
			position += nextSize
		}
		if len(spans) >= maxTokenSpans {
			return nil
		}
		spans = append(spans, tokenSpan{start: start, end: position, value: input[start:position]})
	}
	return spans
}

func isTokenRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func repeatedCandidates(input string, minOccurrences, maxEntries int) []string {
	return sortedCandidates(scanTokens(input), minOccurrences, maxEntries)
}

func sortedCandidates(spans []tokenSpan, minOccurrences, maxEntries int) []string {
	counts := make(map[string]int)
	for _, span := range spans {
		counts[span.value]++
	}
	values := make([]string, 0, len(counts))
	for value, count := range counts {
		if count >= minOccurrences && len(value) <= maxTokenBytes {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if counts[values[i]] != counts[values[j]] {
			return counts[values[i]] > counts[values[j]]
		}
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	if len(values) > maxEntries {
		values = values[:maxEntries]
	}
	return values
}

func buildEnvelope(input string, spans []tokenSpan, entries []string) string {
	index := make(map[string]int, len(entries))
	for i, entry := range entries {
		index[entry] = i
	}

	var envelope strings.Builder
	envelope.WriteString(envelopeMagic)
	envelope.WriteString("\nD:")
	envelope.WriteString(strconv.Itoa(len(entries)))
	envelope.WriteByte('\n')
	for _, entry := range entries {
		envelope.WriteString(strconv.Itoa(len(entry)))
		envelope.WriteByte('\n')
		envelope.WriteString(entry)
		envelope.WriteByte('\n')
	}
	envelope.WriteString("B\n")

	position := 0
	for _, span := range spans {
		writeEscapedLiteral(&envelope, input[position:span.start])
		if index, ok := index[span.value]; ok {
			envelope.WriteString(marker(index))
		} else {
			writeEscapedLiteral(&envelope, input[span.start:span.end])
		}
		position = span.end
	}
	writeEscapedLiteral(&envelope, input[position:])
	return envelope.String()
}

func writeEscapedLiteral(output *strings.Builder, value string) {
	for position := 0; position < len(value); {
		if strings.HasPrefix(value[position:], markerOpen) {
			output.WriteString(escapeMarker)
			position += len(markerOpen)
			continue
		}
		output.WriteByte(value[position])
		position++
	}
}

func marker(index int) string {
	return markerOpen + strconv.Itoa(index) + markerClose
}

func decodeEnvelope(encoded string) (string, error) {
	if len(encoded) > maxSymbolBytes+maxOutputBytes {
		return "", errors.New("symboltable: envelope exceeds size limit")
	}
	data := []byte(encoded)
	position := 0
	line, next, err := readControlLine(data, position)
	if err != nil || string(line) != envelopeMagic {
		return "", errors.New("symboltable: invalid envelope magic")
	}
	position = next

	dictionaryLine, next, err := readControlLine(data, position)
	if err != nil {
		return "", err
	}
	dictionaryCount, err := parseCountLine(dictionaryLine)
	if err != nil || dictionaryCount == 0 {
		return "", errors.New("symboltable: invalid dictionary count")
	}
	position = next

	dictionary := make([][]byte, dictionaryCount)
	seen := make(map[string]struct{}, dictionaryCount)
	for i := range dictionary {
		lengthLine, next, err := readControlLine(data, position)
		if err != nil {
			return "", err
		}
		length, err := parseLengthLine(lengthLine)
		if err != nil {
			return "", err
		}
		position = next
		if length > maxTokenBytes || length > len(data)-position {
			return "", errors.New("symboltable: dictionary entry exceeds bounds")
		}
		entry := append([]byte(nil), data[position:position+length]...)
		position += length
		if position >= len(data) || data[position] != '\n' {
			return "", errors.New("symboltable: unterminated dictionary entry")
		}
		position++
		if !validTokenBytes(entry) {
			return "", errors.New("symboltable: invalid dictionary token")
		}
		if _, exists := seen[string(entry)]; exists {
			return "", errors.New("symboltable: duplicate dictionary token")
		}
		seen[string(entry)] = struct{}{}
		dictionary[i] = entry
	}

	bodyLine, next, err := readControlLine(data, position)
	if err != nil || string(bodyLine) != "B" {
		return "", errors.New("symboltable: missing body marker")
	}
	position = next
	return expandBody(data[position:], dictionary)
}

func expandBody(body []byte, dictionary [][]byte) (string, error) {
	var output bytes.Buffer
	for position := 0; position < len(body); {
		open := bytes.Index(body[position:], []byte(markerOpen))
		if open < 0 {
			if err := appendBounded(&output, body[position:], maxOutputBytes); err != nil {
				return "", err
			}
			break
		}
		open += position
		if err := appendBounded(&output, body[position:open], maxOutputBytes); err != nil {
			return "", err
		}
		if bytes.HasPrefix(body[open:], []byte(escapeMarker)) {
			if err := appendBounded(&output, []byte(markerOpen), maxOutputBytes); err != nil {
				return "", err
			}
			position = open + len(escapeMarker)
			continue
		}
		closeOffset := bytes.Index(body[open+len(markerOpen):], []byte(markerClose))
		if closeOffset < 0 || closeOffset > 12 {
			return "", errors.New("symboltable: unterminated or oversized token marker")
		}
		closeOffset += open + len(markerOpen)
		indexText := string(body[open+len(markerOpen) : closeOffset])
		index, err := strconv.Atoi(indexText)
		if err != nil || index < 0 || index >= len(dictionary) {
			return "", errors.New("symboltable: dictionary marker index is invalid")
		}
		if err := appendBounded(&output, dictionary[index], maxOutputBytes); err != nil {
			return "", err
		}
		position = closeOffset + len(markerClose)
	}
	return output.String(), nil
}

func validTokenBytes(value []byte) bool {
	if len(value) == 0 || !utf8.Valid(value) {
		return false
	}
	for position := 0; position < len(value); {
		runeValue, size := utf8.DecodeRune(value[position:])
		if !isTokenRune(runeValue) {
			return false
		}
		position += size
	}
	return true
}

func appendBounded(output *bytes.Buffer, value []byte, max int) error {
	if len(value) > max-output.Len() {
		return errors.New("symboltable: decoded output exceeds size limit")
	}
	_, _ = output.Write(value)
	return nil
}

func readControlLine(data []byte, position int) ([]byte, int, error) {
	if position >= len(data) {
		return nil, position, errors.New("symboltable: truncated envelope")
	}
	newline := bytes.IndexByte(data[position:], '\n')
	if newline < 0 || newline > 128 {
		return nil, position, errors.New("symboltable: invalid control line")
	}
	newline += position
	return data[position:newline], newline + 1, nil
}

func parseCountLine(line []byte) (int, error) {
	if len(line) < 3 || line[0] != 'D' || line[1] != ':' {
		return 0, errors.New("symboltable: expected D: count")
	}
	count, err := strconv.Atoi(string(line[2:]))
	if err != nil || count < 1 || count > maxDictionary {
		return 0, errors.New("symboltable: dictionary count is out of bounds")
	}
	return count, nil
}

func parseLengthLine(line []byte) (int, error) {
	if len(line) == 0 {
		return 0, errors.New("symboltable: empty dictionary length")
	}
	length, err := strconv.Atoi(string(line))
	if err != nil || length < 1 || length > maxTokenBytes {
		return 0, errors.New("symboltable: dictionary length is out of bounds")
	}
	return length, nil
}

var _ codec.LosslessCodec = (*Codec)(nil)
