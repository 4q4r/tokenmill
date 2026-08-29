// Package markdown provides a deliberately narrow Markdown whitespace codec.
//
// It only folds runs of ASCII spaces between words in ordinary paragraph
// lines. Markdown syntax, code, URLs, tabs, and line endings stay untouched.
// A compact sidecar records every folded run, so the codec is byte-lossless
// even though its model-facing form is normalized.
package markdown

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

const (
	headerPrefix       = "[[tokenmill-markdown-ws:v1;"
	headerSuffix       = "]]"
	maxInputBytes      = 16 << 20
	maxHeaderBytes     = 8 << 20
	maxSidecarEntries  = 100_000
	maxSidecarRunBytes = 1 << 20
	maxDecodedBytes    = 64 << 20
)

var (
	errMalformedSidecar = errors.New("markdown: malformed whitespace sidecar")
	errSidecarTooLarge  = errors.New("markdown: whitespace sidecar exceeds safety limit")
)

type span struct {
	start int
	end   int
}

type sidecarEntry struct {
	offset int
	length int
}

// Codec implements codec.LosslessCodec for safe Markdown paragraph spacing.
type Codec struct{}

// New returns a conservative Markdown whitespace codec.
func New() *Codec { return &Codec{} }

// ID identifies the codec for tournament integration.
func (c *Codec) ID() string { return "markdown-whitespace" }

// Detect reports whether a protected-area-aware whitespace candidate exists.
func (c *Codec) Detect(input string) bool {
	return len(findCandidates(input)) > 0
}

// EstimateSavings returns exact tokenizer savings, or -1 when the candidate
// is unsafe or the encoded representation is not strictly smaller.
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

// Encode folds only safe inter-word ASCII-space runs. If the sidecar does not
// pay for itself in tokenizer.Count, the original bytes are returned.
func (c *Codec) Encode(input string) (string, error) {
	if input == "" || len(input) > maxInputBytes || !utf8.ValidString(input) {
		return input, nil
	}
	if strings.HasPrefix(input, headerPrefix) {
		// Do not make a natural header-looking input undecodable.
		return input, nil
	}

	candidates := findCandidates(input)
	if len(candidates) == 0 {
		return input, nil
	}

	var body strings.Builder
	body.Grow(len(input))
	entries := make([]sidecarEntry, 0, len(candidates))
	last := 0
	for _, candidate := range candidates {
		body.WriteString(input[last:candidate.start])
		offset := body.Len()
		body.WriteByte(' ')
		entries = append(entries, sidecarEntry{
			offset: offset,
			length: candidate.end - candidate.start,
		})
		last = candidate.end
	}
	body.WriteString(input[last:])

	header, err := encodeHeader(entries)
	if err != nil {
		return input, nil
	}
	encoded := header + body.String()
	if tokenizer.Count(encoded) >= tokenizer.Count(input) {
		return input, nil
	}
	decoded, err := c.Decode(encoded)
	if err != nil || decoded != input {
		return input, nil
	}
	return encoded, nil
}

// Decode expands a valid whitespace sidecar. Non-sidecar text is passed
// through unchanged so an unselected candidate remains safe for the caller.
func (c *Codec) Decode(encoded string) (string, error) {
	if encoded == "" || !strings.HasPrefix(encoded, headerPrefix) {
		return encoded, nil
	}
	if len(encoded) > maxDecodedBytes {
		return "", errSidecarTooLarge
	}

	relativeEnd := strings.Index(encoded[len(headerPrefix):], headerSuffix)
	if relativeEnd < 0 {
		return "", errMalformedSidecar
	}
	headerEnd := len(headerPrefix) + relativeEnd
	if headerEnd-len(headerPrefix) > maxHeaderBytes {
		return "", errSidecarTooLarge
	}
	header := encoded[len(headerPrefix):headerEnd]
	body := encoded[headerEnd+len(headerSuffix):]
	entries, err := parseHeader(header)
	if err != nil {
		return "", err
	}

	total := len(body)
	for _, entry := range entries {
		if entry.length < 2 || entry.length > maxSidecarRunBytes {
			return "", errMalformedSidecar
		}
		extra := entry.length - 1
		if extra > maxDecodedBytes-total {
			return "", errSidecarTooLarge
		}
		total += extra
		if entry.offset < 0 || entry.offset >= len(body) || body[entry.offset] != ' ' {
			return "", errMalformedSidecar
		}
	}

	var decoded strings.Builder
	decoded.Grow(total)
	last := 0
	for _, entry := range entries {
		decoded.WriteString(body[last:entry.offset])
		decoded.WriteByte(' ')
		for i := 1; i < entry.length; i++ {
			decoded.WriteByte(' ')
		}
		last = entry.offset + 1
	}
	decoded.WriteString(body[last:])
	return decoded.String(), nil
}

// Verify checks byte-level equality after decoding.
func (c *Codec) Verify(original, encoded string) bool {
	if original == encoded {
		return true
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		return original == encoded
	}
	return decoded == original
}

func encodeHeader(entries []sidecarEntry) (string, error) {
	if len(entries) == 0 || len(entries) > maxSidecarEntries {
		return "", errMalformedSidecar
	}
	var header strings.Builder
	header.WriteString(headerPrefix)
	for i, entry := range entries {
		if entry.length < 2 || entry.length > maxSidecarRunBytes {
			return "", errMalformedSidecar
		}
		if i > 0 {
			header.WriteByte(',')
		}
		header.WriteString(strconv.Itoa(entry.offset))
		header.WriteByte(':')
		header.WriteString(strconv.Itoa(entry.length))
	}
	header.WriteString(headerSuffix)
	if header.Len() > maxHeaderBytes {
		return "", errSidecarTooLarge
	}
	return header.String(), nil
}

func parseHeader(header string) ([]sidecarEntry, error) {
	if header == "" || len(header) > maxHeaderBytes {
		return nil, errMalformedSidecar
	}
	parts := strings.Split(header, ",")
	if len(parts) == 0 || len(parts) > maxSidecarEntries {
		return nil, errMalformedSidecar
	}
	entries := make([]sidecarEntry, 0, len(parts))
	previous := -1
	for _, part := range parts {
		separator := strings.IndexByte(part, ':')
		if separator <= 0 || separator == len(part)-1 || strings.IndexByte(part[separator+1:], ':') >= 0 {
			return nil, errMalformedSidecar
		}
		offset, err := parseBoundedInt(part[:separator], maxDecodedBytes)
		if err != nil {
			return nil, errMalformedSidecar
		}
		length, err := parseBoundedInt(part[separator+1:], maxSidecarRunBytes)
		if err != nil || length < 2 {
			return nil, errMalformedSidecar
		}
		if offset <= previous {
			return nil, errMalformedSidecar
		}
		previous = offset
		entries = append(entries, sidecarEntry{offset: offset, length: length})
	}
	return entries, nil
}

func parseBoundedInt(value string, max int) (int, error) {
	if value == "" || len(value) > 10 {
		return 0, fmt.Errorf("integer out of bounds")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > max {
		return 0, fmt.Errorf("integer out of bounds")
	}
	return parsed, nil
}

func findCandidates(input string) []span {
	if input == "" || len(input) > maxInputBytes || !utf8.ValidString(input) || strings.Contains(input, "\t") {
		return nil
	}

	var candidates []span
	lineStart := 0
	lineNumber := 0
	inYAML := false
	inFence := false
	fenceChar := byte(0)
	fenceLength := 0
	for lineStart <= len(input) {
		lineEnd := strings.IndexByte(input[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(input)
		} else {
			lineEnd += lineStart
		}
		contentEnd := lineEnd
		if contentEnd > lineStart && input[contentEnd-1] == '\r' {
			contentEnd--
		}
		line := input[lineStart:contentEnd]

		firstLine := line
		if lineNumber == 0 && strings.HasPrefix(firstLine, "\ufeff") {
			firstLine = firstLine[len("\ufeff"):]
		}
		trimmed := strings.TrimSpace(firstLine)
		if lineNumber == 0 && trimmed == "---" {
			inYAML = true
		}
		if inYAML {
			if lineNumber > 0 && (trimmed == "---" || trimmed == "...") {
				inYAML = false
			}
		} else if inFence {
			if isFenceClose(line, fenceChar, fenceLength) {
				inFence = false
			}
		} else if char, length, ok := parseFence(line); ok {
			inFence = true
			fenceChar = char
			fenceLength = length
		} else if !isIndentedCode(line) && isSafeParagraphLine(line) {
			candidates = append(candidates, findLineCandidates(line, lineStart)...)
		}

		if lineEnd == len(input) {
			break
		}
		lineStart = lineEnd + 1
		lineNumber++
	}
	return candidates
}

func isSafeParagraphLine(line string) bool {
	if line == "" || strings.HasPrefix(line, " ") {
		return false
	}
	if strings.Contains(line, "http://") || strings.Contains(line, "https://") {
		return false
	}
	trimmed := strings.TrimLeft(line, " ")
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ">") || strings.Contains(trimmed, "|") {
		return false
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] == '.' || trimmed[i] == ')' {
			if i > 0 && i+1 < len(trimmed) && isASCIIDigit(trimmed[i-1]) && trimmed[i+1] == ' ' {
				return false
			}
		}
	}
	return true
}

func findLineCandidates(line string, absoluteStart int) []span {
	var candidates []span
	for i := 0; i < len(line); {
		if line[i] == '`' {
			run := 1
			for i+run < len(line) && line[i+run] == '`' {
				run++
			}
			close := findBacktickRun(line, i+run, run)
			if close < 0 {
				return candidates
			}
			i = close + run
			continue
		}
		if line[i] != ' ' {
			i++
			continue
		}
		end := i + 1
		for end < len(line) && line[end] == ' ' {
			end++
		}
		if end-i >= 2 && i > 0 && end < len(line) && isWordAtEnd(line[:i]) && isWordAtStart(line[end:]) {
			candidates = append(candidates, span{start: absoluteStart + i, end: absoluteStart + end})
		}
		i = end
	}
	return candidates
}

func findBacktickRun(line string, start, length int) int {
	for i := start; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		run := 1
		for i+run < len(line) && line[i+run] == '`' {
			run++
		}
		if run == length {
			return i
		}
		i += run
	}
	return -1
}

func isWordAtEnd(value string) bool {
	r, _ := utf8.DecodeLastRuneInString(value)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isWordAtStart(value string) bool {
	r, _ := utf8.DecodeRuneInString(value)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isIndentedCode(line string) bool {
	if strings.HasPrefix(line, "\t") {
		return true
	}
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	return spaces >= 4
}

func parseFence(line string) (byte, int, bool) {
	start := 0
	for start < len(line) && start < 3 && line[start] == ' ' {
		start++
	}
	if start >= len(line) || (line[start] != '`' && line[start] != '~') {
		return 0, 0, false
	}
	char := line[start]
	length := 0
	for start+length < len(line) && line[start+length] == char {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	return char, length, true
}

func isFenceClose(line string, wanted byte, minimum int) bool {
	char, length, ok := parseFence(line)
	if !ok || char != wanted || length < minimum {
		return false
	}
	start := 0
	for start < len(line) && start < 3 && line[start] == ' ' {
		start++
	}
	for start+length < len(line) {
		if line[start+length] != ' ' {
			return false
		}
		length++
	}
	return true
}

func isASCIIDigit(value byte) bool { return value >= '0' && value <= '9' }

var _ codec.LosslessCodec = (*Codec)(nil)
