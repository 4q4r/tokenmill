// Package opaque provides an exact dictionary codec for repeated opaque runs.
//
// It recognizes only long base64-like values, UUIDs, and URLs whose path or
// query contains an opaque value. Human-readable URLs, short values, code
// fences, and single occurrences are left alone. The dictionary is embedded
// in every encoded value; a hash-only placeholder is never emitted.
package opaque

import (
	"encoding/base64"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

const (
	headerPrefix      = "[[tokenmill-opaque:v1;"
	headerSuffix      = "]]"
	markerPrefix      = "⟦tm-o"
	markerSuffix      = "⟧"
	defaultMinBase64  = 32
	defaultMinURL     = 64
	defaultMinRepeats = 2
	defaultMaxEntries = 128
	maxHeaderBytes    = 8 << 20
	maxDecodedBytes   = 64 << 20
	maxCandidateBytes = 1 << 20
	maxInputBytes     = 16 << 20
)

var (
	errMalformedStream = errors.New("opaque: malformed encoded stream")
	errStreamTooLarge  = errors.New("opaque: encoded stream exceeds safety limit")
)

// Config controls conservative opaque-run detection. The codec is not added
// to the default tournament pool; callers opt in by constructing and adding
// it explicitly (or by setting Enabled to false in a project-specific pool).
type Config struct {
	Enabled         bool
	MinBase64Length int
	MinURLLength    int
	MinOccurrences  int
	MaxEntries      int
	MaxInputBytes   int
}

// DefaultConfig returns the active direct-use defaults. Integration remains
// opt-in because this codec is not registered by any existing caller.
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		MinBase64Length: defaultMinBase64,
		MinURLLength:    defaultMinURL,
		MinOccurrences:  defaultMinRepeats,
		MaxEntries:      defaultMaxEntries,
		MaxInputBytes:   maxInputBytes,
	}
}

func normalizeConfig(config Config) Config {
	defaults := DefaultConfig()
	if config.MinBase64Length <= 0 {
		config.MinBase64Length = defaults.MinBase64Length
	}
	if config.MinURLLength <= 0 {
		config.MinURLLength = defaults.MinURLLength
	}
	if config.MinOccurrences <= 0 {
		config.MinOccurrences = defaults.MinOccurrences
	}
	if config.MaxEntries <= 0 {
		config.MaxEntries = defaults.MaxEntries
	} else if config.MaxEntries > defaultMaxEntries {
		config.MaxEntries = defaultMaxEntries
	}
	if config.MaxInputBytes <= 0 {
		config.MaxInputBytes = defaults.MaxInputBytes
	}
	return config
}

// Codec implements codec.LosslessCodec with byte-exact opaque dictionaries.
type Codec struct {
	config Config
}

// New returns an active conservative codec for direct use.
func New() *Codec { return &Codec{config: DefaultConfig()} }

// NewWithConfig returns a codec with explicit feature-flag and threshold
// settings.
func NewWithConfig(config Config) *Codec {
	return &Codec{config: normalizeConfig(config)}
}

// ID identifies the codec for future tournament integration.
func (c *Codec) ID() string { return "opaque-dict" }

// Detect reports whether repeated opaque occurrences can be dictionary-coded.
func (c *Codec) Detect(input string) bool {
	c = c.configured()
	return c.config.Enabled && len(c.findPlan(input)) > 0
}

// EstimateSavings returns exact tokenizer savings, or -1 when the encoded
// representation is not strictly smaller.
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

// Encode replaces only repeated, opaque runs and embeds their exact bytes in
// a deterministic header. A non-winning representation returns input.
func (c *Codec) Encode(input string) (string, error) {
	c = c.configured()
	if !c.config.Enabled || len(input) == 0 || len(input) > c.config.MaxInputBytes {
		return input, nil
	}
	plan := c.findPlan(input)
	if len(plan) == 0 {
		return input, nil
	}

	replacements := make([]replacement, 0)
	for id, candidate := range plan {
		for _, occurrence := range candidate.occurrences {
			replacements = append(replacements, replacement{
				start: occurrence.start,
				end:   occurrence.end,
				id:    id,
			})
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })

	var body strings.Builder
	body.Grow(len(input))
	last := 0
	for _, replacement := range replacements {
		if replacement.start < last || replacement.end > len(input) {
			return input, nil
		}
		body.WriteString(input[last:replacement.start])
		body.WriteString(marker(replacement.id))
		last = replacement.end
	}
	body.WriteString(input[last:])

	header, err := buildHeader(plan)
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

// Decode expands a valid self-contained opaque dictionary. Non-encoded text
// is returned unchanged; malformed streams with the codec header fail loudly.
func (c *Codec) Decode(encoded string) (string, error) {
	if encoded == "" || !strings.HasPrefix(encoded, headerPrefix) {
		return encoded, nil
	}
	if len(encoded) > maxDecodedBytes {
		return "", errStreamTooLarge
	}
	relativeEnd := strings.Index(encoded[len(headerPrefix):], headerSuffix)
	if relativeEnd < 0 {
		return "", errMalformedStream
	}
	headerEnd := len(headerPrefix) + relativeEnd
	if headerEnd-len(headerPrefix) > maxHeaderBytes {
		return "", errStreamTooLarge
	}
	dictionary, err := parseHeader(encoded[len(headerPrefix):headerEnd])
	if err != nil {
		return "", err
	}
	body := encoded[headerEnd+len(headerSuffix):]
	if len(dictionary) == 0 {
		return "", errMalformedStream
	}

	total := len(body)
	foundMarker := false
	for i := 0; i < len(body); {
		markerStart := strings.Index(body[i:], markerPrefix)
		if markerStart < 0 {
			break
		}
		markerStart += i
		id, markerEnd, err := parseMarker(body, markerStart)
		if err != nil || id >= len(dictionary) {
			return "", errMalformedStream
		}
		foundMarker = true
		delta := len(dictionary[id]) - (markerEnd - markerStart)
		if delta >= 0 {
			if delta > maxDecodedBytes-total {
				return "", errStreamTooLarge
			}
			total += delta
		} else {
			total += delta
		}
		i = markerEnd
	}
	if !foundMarker {
		return "", errMalformedStream
	}
	if total < 0 || total > maxDecodedBytes {
		return "", errStreamTooLarge
	}

	var decoded strings.Builder
	decoded.Grow(total)
	last := 0
	for i := 0; i < len(body); {
		markerStart := strings.Index(body[i:], markerPrefix)
		if markerStart < 0 {
			decoded.WriteString(body[last:])
			break
		}
		markerStart += i
		decoded.WriteString(body[last:markerStart])
		id, markerEnd, err := parseMarker(body, markerStart)
		if err != nil || id >= len(dictionary) {
			return "", errMalformedStream
		}
		decoded.Write(dictionary[id])
		last = markerEnd
		i = markerEnd
	}
	if len(body) == 0 {
		// A valid dictionary with an empty body cannot be produced by Encode.
		return "", errMalformedStream
	}
	return decoded.String(), nil
}

// Verify checks exact byte equality after decoding.
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

type occurrence struct {
	start int
	end   int
	value string
}

type candidate struct {
	value       string
	occurrences []occurrence
}

type replacement struct {
	start int
	end   int
	id    int
}

func (c *Codec) configured() *Codec {
	if c == nil {
		return NewWithConfig(Config{Enabled: false})
	}
	if c.config.MinBase64Length <= 0 || c.config.MinURLLength <= 0 || c.config.MinOccurrences <= 0 || c.config.MaxEntries <= 0 || c.config.MaxInputBytes <= 0 {
		copy := *c
		copy.config = normalizeConfig(copy.config)
		return &copy
	}
	return c
}

func (c *Codec) findPlan(input string) []candidate {
	if input == "" || len(input) > c.config.MaxInputBytes || !utf8.ValidString(input) || strings.Contains(input, "\x00") {
		// NUL is not an opaque textual run and makes the model-facing form less
		// predictable; pass it through rather than partially rewriting it.
		return nil
	}
	if strings.Contains(input, "```") || strings.Contains(input, "~~~") || strings.Contains(input, markerPrefix) {
		return nil
	}

	urlSpans := findURLSpans(input)
	byValue := make(map[string][]occurrence)
	for _, span := range urlSpans {
		value := input[span.start:span.end]
		if isOpaqueURL(value, c.config) && safeHeaderValue(value) {
			byValue[value] = append(byValue[value], occurrence{start: span.start, end: span.end, value: value})
		}
	}

	for i := 0; i < len(input); {
		if span, ok := containingSpan(urlSpans, i); ok {
			i = span.end
			continue
		}
		if !isOpaqueTokenStart(input[i]) {
			i++
			continue
		}
		start := i
		i++
		for i < len(input) && isOpaqueTokenByte(input[i]) {
			i++
		}
		end := i
		value := input[start:end]
		if isOpaqueAtom(value, c.config.MinBase64Length) {
			// Keep standard base64 padding with the dictionary entry, while
			// treating an assignment delimiter (key=value) as a delimiter.
			for end < len(input) && end-i < 2 && input[end] == '=' {
				end++
			}
			value = input[start:end]
		}
		if isOpaqueAtom(value, c.config.MinBase64Length) {
			byValue[value] = append(byValue[value], occurrence{start: start, end: end, value: value})
		}
		i = end
	}

	candidates := make([]candidate, 0, len(byValue))
	for value, occurrences := range byValue {
		if len(occurrences) < c.config.MinOccurrences {
			continue
		}
		sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].start < occurrences[j].start })
		candidates = append(candidates, candidate{value: value, occurrences: occurrences})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].value) != len(candidates[j].value) {
			return len(candidates[i].value) > len(candidates[j].value)
		}
		if len(candidates[i].occurrences) != len(candidates[j].occurrences) {
			return len(candidates[i].occurrences) > len(candidates[j].occurrences)
		}
		return candidates[i].value < candidates[j].value
	})

	selected := make([]candidate, 0, minInt(len(candidates), c.config.MaxEntries))
	occupied := make([]occurrence, 0)
	for _, current := range candidates {
		if len(selected) >= c.config.MaxEntries {
			break
		}
		available := make([]occurrence, 0, len(current.occurrences))
		for _, occurrence := range current.occurrences {
			if overlapsAny(occurrence, occupied) {
				continue
			}
			available = append(available, occurrence)
		}
		if len(available) < c.config.MinOccurrences {
			continue
		}
		selected = append(selected, candidate{value: current.value, occurrences: available})
		occupied = append(occupied, available...)
	}
	return selected
}

func buildHeader(dictionary []candidate) (string, error) {
	if len(dictionary) == 0 || len(dictionary) > defaultMaxEntries {
		return "", errMalformedStream
	}
	var header strings.Builder
	header.WriteString(headerPrefix)
	for i, entry := range dictionary {
		if !safeHeaderValue(entry.value) {
			return "", errMalformedStream
		}
		if i > 0 {
			header.WriteByte(';')
		}
		header.WriteString(strconv.Itoa(i))
		header.WriteByte(':')
		header.WriteString(strconv.Itoa(len(entry.value)))
		header.WriteByte(':')
		header.WriteString(entry.value)
	}
	header.WriteString(headerSuffix)
	if header.Len() > maxHeaderBytes {
		return "", errStreamTooLarge
	}
	return header.String(), nil
}

func parseHeader(header string) ([][]byte, error) {
	if header == "" || len(header) > maxHeaderBytes {
		return nil, errMalformedStream
	}
	dictionary := make([][]byte, 0)
	position := 0
	for index := 0; position < len(header); index++ {
		if index >= defaultMaxEntries {
			return nil, errMalformedStream
		}
		idEnd := strings.IndexByte(header[position:], ':')
		if idEnd <= 0 {
			return nil, errMalformedStream
		}
		idEnd += position
		id, err := parseID(header[position:idEnd])
		if err != nil || id != index {
			return nil, errMalformedStream
		}
		lengthStart := idEnd + 1
		lengthEndRelative := strings.IndexByte(header[lengthStart:], ':')
		if lengthEndRelative <= 0 {
			return nil, errMalformedStream
		}
		lengthEnd := lengthStart + lengthEndRelative
		length, err := parseBoundedLength(header[lengthStart:lengthEnd])
		if err != nil || length == 0 || length > maxCandidateBytes {
			return nil, errMalformedStream
		}
		valueStart := lengthEnd + 1
		valueEnd := valueStart + length
		if valueEnd > len(header) || !safeHeaderValue(header[valueStart:valueEnd]) {
			return nil, errMalformedStream
		}
		dictionary = append(dictionary, []byte(header[valueStart:valueEnd]))
		position = valueEnd
		if position < len(header) {
			if header[position] != ';' {
				return nil, errMalformedStream
			}
			position++
		}
	}
	return dictionary, nil
}

func parseBoundedLength(value string) (int, error) {
	if value == "" || len(value) > 10 {
		return 0, errMalformedStream
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > maxCandidateBytes {
		return 0, errMalformedStream
	}
	return parsed, nil
}

func safeHeaderValue(value string) bool {
	return value != "" && len(value) <= maxCandidateBytes && !strings.ContainsAny(value, ";\r\n\x00") && !strings.Contains(value, headerSuffix)
}

func parseMarker(value string, start int) (int, int, error) {
	if start < 0 || start >= len(value) || !strings.HasPrefix(value[start:], markerPrefix) {
		return 0, 0, errMalformedStream
	}
	i := start + len(markerPrefix)
	digitStart := i
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == digitStart || i-digitStart > 10 || !strings.HasPrefix(value[i:], markerSuffix) {
		return 0, 0, errMalformedStream
	}
	id, err := parseID(value[digitStart:i])
	if err != nil {
		return 0, 0, errMalformedStream
	}
	return id, i + len(markerSuffix), nil
}

func parseID(value string) (int, error) {
	if value == "" || len(value) > 10 {
		return 0, errMalformedStream
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > defaultMaxEntries {
		return 0, errMalformedStream
	}
	return parsed, nil
}

func marker(id int) string { return markerPrefix + strconv.Itoa(id) + markerSuffix }

func findURLSpans(input string) []occurrence {
	var spans []occurrence
	for cursor := 0; cursor < len(input); {
		httpIndex := strings.Index(input[cursor:], "http://")
		httpsIndex := strings.Index(input[cursor:], "https://")
		index := -1
		if httpIndex >= 0 {
			index = cursor + httpIndex
		}
		if httpsIndex >= 0 && (index < 0 || cursor+httpsIndex < index) {
			index = cursor + httpsIndex
		}
		if index < 0 {
			break
		}
		if index > 0 && isOpaqueTokenByte(input[index-1]) {
			cursor = index + 1
			continue
		}
		end := index
		for end < len(input) {
			r, size := utf8.DecodeRuneInString(input[end:])
			if unicode.IsSpace(r) || strings.ContainsRune("<>\"'", r) {
				break
			}
			end += size
		}
		for end > index && strings.ContainsRune(".,;:!?)]}", rune(input[end-1])) {
			end--
		}
		if end > index {
			spans = append(spans, occurrence{start: index, end: end, value: input[index:end]})
		}
		cursor = maxInt(end, index+1)
	}
	return spans
}

func containingSpan(spans []occurrence, position int) (occurrence, bool) {
	for _, span := range spans {
		if position < span.start {
			return occurrence{}, false
		}
		if position < span.end {
			return span, true
		}
	}
	return occurrence{}, false
}

func isOpaqueURL(value string, config Config) bool {
	if len(value) < config.MinURLLength {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if isOpaqueAtom(segment, config.MinBase64Length) {
			return true
		}
	}
	for _, parameter := range strings.Split(parsed.RawQuery, "&") {
		parts := strings.SplitN(parameter, "=", 2)
		if len(parts) != 2 {
			continue
		}
		decoded, err := url.QueryUnescape(parts[1])
		if err == nil && isOpaqueAtom(decoded, config.MinBase64Length) {
			return true
		}
	}
	return false
}

func isOpaqueAtom(value string, minBase64Length int) bool {
	if isUUID(value) {
		return true
	}
	if len(value) < minBase64Length || len(value) > maxCandidateBytes || len(value)%4 == 1 {
		return false
	}
	hasSignal := false
	for i := 0; i < len(value); i++ {
		if value[i] >= '0' && value[i] <= '9' || strings.ContainsRune("+/_=", rune(value[i])) {
			hasSignal = true
		}
		if value[i] == '=' && i < len(value)-2 {
			return false
		}
	}
	if !hasSignal {
		return false
	}
	for _, encoding := range []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) > 0 {
			return true
		}
	}
	return false
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !isHex(value[i]) {
			return false
		}
	}
	return true
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func isOpaqueTokenStart(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func isOpaqueTokenByte(value byte) bool {
	return isOpaqueTokenStart(value) || strings.ContainsRune("+/_-", rune(value))
}

func overlapsAny(value occurrence, occupied []occurrence) bool {
	for _, other := range occupied {
		if value.start < other.end && other.start < value.end {
			return true
		}
	}
	return false
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

var _ codec.LosslessCodec = (*Codec)(nil)
