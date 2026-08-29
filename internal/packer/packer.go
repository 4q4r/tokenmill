// Package packer implements exact repeated-block packing.
//
// The binary representation is an internal transport format. The model-facing
// codec is opt-in and uses a self-contained textual dictionary with explicit
// markers. Both forms carry the complete dictionary and are verified against
// the original size and SHA-256 provenance before they are returned.
package packer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec"
)

const (
	formatMagic          = "TBP1"
	formatVersion        = byte(1)
	modelHeaderPrefix    = "@tm-b:v1;"
	modelHeaderSuffix    = ";@"
	modelMarkerPrefix    = "@b"
	modelMarkerSuffix    = "@"
	defaultMaxInputBytes = 16 << 20
	defaultMinBlockBytes = 32
	defaultMaxBlockBytes = 1 << 20
	defaultMaxDictionary = 1024
	defaultMaxSegments   = 1_000_000
	maxPackedBytes       = 64 << 20
)

var (
	errInvalidConfig   = errors.New("packer: invalid configuration")
	errInputTooLarge   = errors.New("packer: input exceeds configured limit")
	errMalformedPacked = errors.New("packer: malformed packed stream")
	errMetadata        = errors.New("packer: invalid packed metadata")
	errModelStream     = errors.New("packer: malformed model-facing stream")
)

// Config controls exact line-block discovery and safety limits. Config{} uses
// conservative defaults; model-facing use is separately opt-in via NewCodec.
type Config struct {
	MaxInputBytes        int
	MinBlockBytes        int
	MaxBlockBytes        int
	MaxDictionaryEntries int
	MaxSegments          int
}

func normalizeConfig(config Config) (Config, error) {
	if config.MaxInputBytes <= 0 {
		config.MaxInputBytes = defaultMaxInputBytes
	}
	if config.MinBlockBytes <= 0 {
		config.MinBlockBytes = defaultMinBlockBytes
	}
	if config.MaxBlockBytes <= 0 {
		config.MaxBlockBytes = defaultMaxBlockBytes
	}
	if config.MaxDictionaryEntries <= 0 {
		config.MaxDictionaryEntries = defaultMaxDictionary
	}
	if config.MaxSegments <= 0 {
		config.MaxSegments = defaultMaxSegments
	}
	if config.MinBlockBytes > config.MaxBlockBytes || config.MaxInputBytes > maxPackedBytes || config.MaxDictionaryEntries > defaultMaxDictionary || config.MaxSegments > defaultMaxSegments {
		return Config{}, errInvalidConfig
	}
	return config, nil
}

// DictionaryEntry is an exact block retained in packed metadata.
type DictionaryEntry struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}

// Reference records one exact dictionary substitution at an original offset.
type Reference struct {
	ID     string `json:"id"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// Metadata is provenance and dictionary data required to validate/unpack Data.
type Metadata struct {
	Version        string            `json:"version"`
	OriginalSize   int               `json:"original_size"`
	OriginalSHA256 string            `json:"original_sha256"`
	Source         string            `json:"source,omitempty"`
	Dictionary     []DictionaryEntry `json:"dictionary"`
	References     []Reference       `json:"references"`
}

// Packed contains the binary segment stream and its exact dictionary metadata.
type Packed struct {
	Data     []byte   `json:"data"`
	Metadata Metadata `json:"metadata"`
}

// PackOptions carries caller provenance without affecting encoded bytes.
type PackOptions struct {
	Source string
}

// Packer is safe for concurrent use. Its in-memory dictionary is deliberately
// bounded and exact: entries are keyed by their complete byte content.
type Packer struct {
	mu         sync.RWMutex
	config     Config
	dictionary map[string]DictionaryEntry
	invalid    bool
}

// New constructs an exact block packer. The dictionary starts empty.
func New(config Config) *Packer {
	normalized, err := normalizeConfig(config)
	return &Packer{
		config:     normalized,
		dictionary: make(map[string]DictionaryEntry),
		invalid:    err != nil,
	}
}

// Pack packs exact repeated line blocks and learns only blocks that are
// actually referenced in the returned stream.
func (p *Packer) Pack(input []byte) (Packed, error) {
	return p.PackWithOptions(input, PackOptions{})
}

// PackWithOptions packs input while preserving source provenance in metadata.
func (p *Packer) PackWithOptions(input []byte, options PackOptions) (Packed, error) {
	if p == nil || p.invalid {
		return Packed{}, errInvalidConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.packLocked(input, options)
}

func (p *Packer) packLocked(input []byte, options PackOptions) (Packed, error) {
	if len(input) > p.config.MaxInputBytes {
		return Packed{}, errInputTooLarge
	}
	spans := findBlockSpans(input, p.config)
	counts := make(map[string]int)
	spansByValue := make(map[string][]blockSpan)
	for _, span := range spans {
		value := string(input[span.start:span.end])
		counts[value]++
		spansByValue[value] = append(spansByValue[value], span)
	}

	candidates := make([]string, 0)
	for value, count := range counts {
		if count >= 2 || p.hasDictionary(value) {
			candidates = append(candidates, value)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		firstI := spansByValue[candidates[i]][0].start
		firstJ := spansByValue[candidates[j]][0].start
		if firstI != firstJ {
			return firstI < firstJ
		}
		return candidates[i] < candidates[j]
	})
	if len(candidates) > p.config.MaxDictionaryEntries {
		candidates = candidates[:p.config.MaxDictionaryEntries]
	}

	localDictionary := make([]DictionaryEntry, 0, len(candidates))
	refsByStart := make(map[int]int)
	selectedValues := make([]string, 0, len(candidates))
	for _, value := range candidates {
		occurrences := spansByValue[value]
		cached := p.hasDictionary(value)
		if !cached && len(occurrences) < 2 {
			continue
		}
		entry := DictionaryEntry{ID: fmt.Sprintf("b%d", len(localDictionary)), Data: []byte(value)}
		localDictionary = append(localDictionary, entry)
		selectedValues = append(selectedValues, value)
		firstReference := 0
		if !cached {
			firstReference = 1 // canonical-first: retain the first new occurrence
		}
		for _, occurrence := range occurrences[firstReference:] {
			refsByStart[occurrence.start] = len(localDictionary) - 1
		}
	}

	segments := buildSegments(input, spans, refsByStart)
	data, err := encodeSegments(segments, p.config.MaxSegments)
	if err != nil {
		return Packed{}, err
	}

	metadata := Metadata{
		Version:        formatMagic,
		OriginalSize:   len(input),
		OriginalSHA256: sha256Hex(input),
		Source:         options.Source,
		Dictionary:     cloneDictionary(localDictionary),
		References:     referencesForSegments(segments, localDictionary),
	}
	packed := Packed{Data: data, Metadata: metadata}
	if err := validatePacked(packed); err != nil {
		return Packed{}, err
	}

	for _, value := range selectedValues {
		if _, exists := p.dictionary[value]; exists || len(p.dictionary) >= p.config.MaxDictionaryEntries {
			continue
		}
		p.dictionary[value] = DictionaryEntry{
			ID:   fmt.Sprintf("b%d", len(p.dictionary)),
			Data: []byte(value),
		}
	}
	return packed, nil
}

// Unpack expands and verifies a packed stream's exact bytes and provenance.
func (p *Packer) Unpack(packed Packed) ([]byte, error) { return Unpack(packed) }

// Detect reports whether input contains an exact repeated block or a cached
// exact block. It does not mutate the cross-call dictionary.
func (p *Packer) Detect(input []byte) bool {
	if p == nil || p.invalid || len(input) > p.config.MaxInputBytes {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	spans := findBlockSpans(input, p.config)
	counts := make(map[string]int)
	for _, span := range spans {
		counts[string(input[span.start:span.end])]++
	}
	for value, count := range counts {
		if count >= 2 || p.hasDictionary(value) {
			return true
		}
	}
	return false
}

// EstimateSavings measures the opt-in model-facing representation without
// mutating the packer's learned dictionary.
func (p *Packer) EstimateSavings(input []byte) int {
	if p == nil || !utf8.Valid(input) {
		return -1
	}
	clone := p.clone()
	packed, err := clone.Pack(input)
	if err != nil {
		return -1
	}
	model, err := renderModel(packed)
	if err != nil || model == string(input) {
		return -1
	}
	saving := codec.TokenSavings(string(input), model)
	if saving <= 0 {
		return -1
	}
	return saving
}

func (p *Packer) clone() *Packer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	clone := &Packer{config: p.config, dictionary: make(map[string]DictionaryEntry, len(p.dictionary)), invalid: p.invalid}
	for value, entry := range p.dictionary {
		clone.dictionary[value] = DictionaryEntry{ID: entry.ID, Data: append([]byte(nil), entry.Data...)}
	}
	return clone
}

func (p *Packer) hasDictionary(value string) bool {
	_, ok := p.dictionary[value]
	return ok
}

type blockSpan struct {
	start int
	end   int
}

type segment struct {
	kind byte
	data []byte
	ref  int
}

const (
	literalSegment = byte(0)
	refSegment     = byte(1)
)

func findBlockSpans(input []byte, config Config) []blockSpan {
	spans := make([]blockSpan, 0)
	for start := 0; start < len(input); {
		relativeEnd := bytes.IndexByte(input[start:], '\n')
		end := len(input)
		if relativeEnd >= 0 {
			end = start + relativeEnd + 1
		}
		if end-start >= config.MinBlockBytes && end-start <= config.MaxBlockBytes {
			spans = append(spans, blockSpan{start: start, end: end})
		}
		if end <= start {
			break
		}
		start = end
	}
	return spans
}

func buildSegments(input []byte, spans []blockSpan, refsByStart map[int]int) []segment {
	segments := make([]segment, 0, len(spans)*2+1)
	last := 0
	for _, span := range spans {
		ref, ok := refsByStart[span.start]
		if !ok {
			continue
		}
		appendLiteralSegment(&segments, input[last:span.start])
		segments = append(segments, segment{kind: refSegment, ref: ref})
		last = span.end
	}
	appendLiteralSegment(&segments, input[last:])
	return segments
}

func appendLiteralSegment(segments *[]segment, value []byte) {
	if len(value) == 0 {
		return
	}
	if len(*segments) > 0 && (*segments)[len(*segments)-1].kind == literalSegment {
		(*segments)[len(*segments)-1].data = append((*segments)[len(*segments)-1].data, value...)
		return
	}
	*segments = append(*segments, segment{kind: literalSegment, data: append([]byte(nil), value...)})
}

func referencesForSegments(segments []segment, dictionary []DictionaryEntry) []Reference {
	references := make([]Reference, 0)
	offset := 0
	for _, segment := range segments {
		if segment.kind == literalSegment {
			offset += len(segment.data)
			continue
		}
		length := len(dictionary[segment.ref].Data)
		references = append(references, Reference{
			ID:     dictionary[segment.ref].ID,
			Offset: offset,
			Length: length,
		})
		offset += length
	}
	return references
}

func encodeSegments(segments []segment, maxSegments int) ([]byte, error) {
	if len(segments) > maxSegments {
		return nil, errMalformedPacked
	}
	data := make([]byte, 0, len(formatMagic)+1+binary.MaxVarintLen64)
	data = append(data, formatMagic...)
	data = append(data, formatVersion)
	data = appendUvarint(data, uint64(len(segments)))
	for _, segment := range segments {
		data = append(data, segment.kind)
		switch segment.kind {
		case literalSegment:
			data = appendUvarint(data, uint64(len(segment.data)))
			data = append(data, segment.data...)
		case refSegment:
			data = appendUvarint(data, uint64(segment.ref))
		default:
			return nil, errMalformedPacked
		}
		if len(data) > maxPackedBytes {
			return nil, errMalformedPacked
		}
	}
	return data, nil
}

func decodeSegments(data []byte, dictionary []DictionaryEntry) ([]segment, error) {
	if len(data) < len(formatMagic)+1 || len(data) > maxPackedBytes || string(data[:len(formatMagic)]) != formatMagic || data[len(formatMagic)] != formatVersion {
		return nil, errMalformedPacked
	}
	position := len(formatMagic) + 1
	count, err := readUvarint(data, &position)
	if err != nil || count > defaultMaxSegments {
		return nil, errMalformedPacked
	}
	segments := make([]segment, 0, int(count))
	for index := uint64(0); index < count; index++ {
		if position >= len(data) {
			return nil, errMalformedPacked
		}
		kind := data[position]
		position++
		value, err := readUvarint(data, &position)
		if err != nil {
			return nil, errMalformedPacked
		}
		switch kind {
		case literalSegment:
			if value > uint64(maxPackedBytes) || value > uint64(len(data)-position) {
				return nil, errMalformedPacked
			}
			end := position + int(value)
			if value == 0 {
				return nil, errMalformedPacked
			}
			segments = append(segments, segment{kind: literalSegment, data: append([]byte(nil), data[position:end]...)})
			position = end
		case refSegment:
			if value >= uint64(len(dictionary)) {
				return nil, errMalformedPacked
			}
			segments = append(segments, segment{kind: refSegment, ref: int(value)})
		default:
			return nil, errMalformedPacked
		}
	}
	if position != len(data) {
		return nil, errMalformedPacked
	}
	return segments, nil
}

func appendUvarint(destination []byte, value uint64) []byte {
	var buffer [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(buffer[:], value)
	return append(destination, buffer[:count]...)
}

func readUvarint(data []byte, position *int) (uint64, error) {
	if *position >= len(data) {
		return 0, errMalformedPacked
	}
	value, count := binary.Uvarint(data[*position:])
	if count <= 0 {
		return 0, errMalformedPacked
	}
	*position += count
	return value, nil
}

// Unpack is the package-level exact unpacker for serialized Packed values.
func Unpack(packed Packed) ([]byte, error) {
	if err := validatePacked(packed); err != nil {
		return nil, err
	}
	segments, err := decodeSegments(packed.Data, packed.Metadata.Dictionary)
	if err != nil {
		return nil, err
	}
	output := make([]byte, 0, packed.Metadata.OriginalSize)
	referenceIndex := 0
	for _, segment := range segments {
		if segment.kind == literalSegment {
			output = append(output, segment.data...)
			continue
		}
		if referenceIndex >= len(packed.Metadata.References) {
			return nil, errMetadata
		}
		reference := packed.Metadata.References[referenceIndex]
		entry := packed.Metadata.Dictionary[segment.ref]
		if reference.ID != entry.ID || reference.Offset != len(output) || reference.Length != len(entry.Data) {
			return nil, errMetadata
		}
		output = append(output, entry.Data...)
		referenceIndex++
	}
	if referenceIndex != len(packed.Metadata.References) || len(output) != packed.Metadata.OriginalSize || sha256Hex(output) != packed.Metadata.OriginalSHA256 {
		return nil, errMetadata
	}
	return output, nil
}

// Verify checks that packed data expands to original byte-for-byte.
func Verify(original []byte, packed Packed) bool {
	decoded, err := Unpack(packed)
	return err == nil && bytes.Equal(original, decoded)
}

func validatePacked(packed Packed) error {
	metadata := packed.Metadata
	if metadata.Version != formatMagic || metadata.OriginalSize < 0 || metadata.OriginalSize > defaultMaxInputBytes || len(packed.Data) == 0 || len(packed.Data) > maxPackedBytes {
		return errMetadata
	}
	hash, err := hex.DecodeString(metadata.OriginalSHA256)
	if err != nil || len(hash) != sha256.Size || strings.ToLower(metadata.OriginalSHA256) != metadata.OriginalSHA256 {
		return errMetadata
	}
	if len(metadata.Dictionary) > defaultMaxDictionary || len(metadata.References) > defaultMaxSegments {
		return errMetadata
	}
	seen := make(map[string]struct{}, len(metadata.Dictionary))
	for index, entry := range metadata.Dictionary {
		if entry.ID != fmt.Sprintf("b%d", index) || len(entry.Data) == 0 || len(entry.Data) > defaultMaxBlockBytes {
			return errMetadata
		}
		if _, exists := seen[string(entry.Data)]; exists {
			return errMetadata
		}
		seen[string(entry.Data)] = struct{}{}
	}
	if (len(metadata.Dictionary) == 0) != (len(metadata.References) == 0) {
		return errMetadata
	}
	return nil
}

func sha256Hex(input []byte) string {
	hash := sha256.Sum256(input)
	return hex.EncodeToString(hash[:])
}

func cloneDictionary(dictionary []DictionaryEntry) []DictionaryEntry {
	cloned := make([]DictionaryEntry, len(dictionary))
	for index, entry := range dictionary {
		cloned[index] = DictionaryEntry{ID: entry.ID, Data: append([]byte(nil), entry.Data...)}
	}
	return cloned
}

// Codec is an opt-in model-facing textual adapter around an exact Packer.
type Codec struct {
	enabled bool
	packer  *Packer
}

// NewCodec constructs the model-facing codec. It is intentionally not wired
// into the existing tournament; callers must opt in explicitly.
func NewCodec(enabled bool) *Codec {
	return &Codec{enabled: enabled, packer: New(Config{})}
}

func (c *Codec) ID() string { return "block-pack" }

func (c *Codec) Detect(input string) bool {
	return c != nil && c.enabled && c.packer != nil && c.packer.Detect([]byte(input))
}

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

func (c *Codec) Encode(input string) (string, error) {
	if c == nil || !c.enabled {
		return input, nil
	}
	if !utf8.ValidString(input) || strings.Contains(input, modelHeaderPrefix) || strings.Contains(input, modelMarkerPrefix) {
		return input, nil
	}
	packed, err := c.packer.Pack([]byte(input))
	if err != nil {
		return "", err
	}
	model, err := renderModel(packed)
	if err != nil || model == input || codec.TokenSavings(input, model) <= 0 {
		return input, nil
	}
	decoded, err := c.Decode(model)
	if err != nil || decoded != input {
		return input, nil
	}
	return model, nil
}

func (c *Codec) Decode(encoded string) (string, error) {
	if encoded == "" || !strings.HasPrefix(encoded, modelHeaderPrefix) {
		return encoded, nil
	}
	if len(encoded) > maxPackedBytes || !utf8.ValidString(encoded) {
		return "", errModelStream
	}
	relativeEnd := strings.Index(encoded[len(modelHeaderPrefix):], modelHeaderSuffix)
	if relativeEnd < 0 {
		return "", errModelStream
	}
	headerEnd := len(modelHeaderPrefix) + relativeEnd
	dictionary, err := parseModelHeader(encoded[len(modelHeaderPrefix):headerEnd])
	if err != nil {
		return "", err
	}
	body := encoded[headerEnd+len(modelHeaderSuffix):]
	decoded, err := expandModelBody(body, dictionary)
	if err != nil {
		return "", err
	}
	if len(decoded) > defaultMaxInputBytes {
		return "", errModelStream
	}
	return string(decoded), nil
}

func (c *Codec) Verify(original, encoded string) bool {
	if original == encoded {
		return true
	}
	decoded, err := c.Decode(encoded)
	return err == nil && decoded == original
}

func renderModel(packed Packed) (string, error) {
	original, err := Unpack(packed)
	if err != nil || len(packed.Metadata.References) == 0 || !utf8.Valid(original) {
		if err != nil {
			return "", err
		}
		return string(original), nil
	}
	if strings.Contains(string(original), modelHeaderPrefix) || strings.Contains(string(original), modelMarkerPrefix) {
		return string(original), nil
	}
	segments, err := decodeSegments(packed.Data, packed.Metadata.Dictionary)
	if err != nil {
		return "", err
	}
	header, err := buildModelHeader(packed.Metadata.Dictionary)
	if err != nil {
		return "", err
	}
	var body strings.Builder
	body.Grow(len(original))
	for _, segment := range segments {
		if segment.kind == literalSegment {
			body.Write(segment.data)
			continue
		}
		body.WriteString(modelMarkerPrefix)
		body.WriteString(strconv.Itoa(segment.ref))
		body.WriteString(modelMarkerSuffix)
	}
	return header + body.String(), nil
}

func buildModelHeader(dictionary []DictionaryEntry) (string, error) {
	if len(dictionary) == 0 || len(dictionary) > defaultMaxDictionary {
		return "", errModelStream
	}
	var header strings.Builder
	header.WriteString(modelHeaderPrefix)
	for index, entry := range dictionary {
		if entry.ID != fmt.Sprintf("b%d", index) || len(entry.Data) == 0 || bytes.Contains(entry.Data, []byte(modelHeaderSuffix)) {
			return "", errModelStream
		}
		if index > 0 {
			header.WriteByte(';')
		}
		header.WriteString(entry.ID)
		header.WriteByte(':')
		header.WriteString(strconv.Itoa(len(entry.Data)))
		header.WriteByte(':')
		header.Write(entry.Data)
	}
	header.WriteString(modelHeaderSuffix)
	return header.String(), nil
}

func parseModelHeader(header string) ([][]byte, error) {
	if header == "" {
		return nil, errModelStream
	}
	dictionary := make([][]byte, 0)
	position := 0
	for index := 0; position < len(header); index++ {
		if index >= defaultMaxDictionary {
			return nil, errModelStream
		}
		idEndRelative := strings.IndexByte(header[position:], ':')
		if idEndRelative <= 0 {
			return nil, errModelStream
		}
		idEnd := position + idEndRelative
		if header[position:idEnd] != fmt.Sprintf("b%d", index) {
			return nil, errModelStream
		}
		lengthStart := idEnd + 1
		lengthEndRelative := strings.IndexByte(header[lengthStart:], ':')
		if lengthEndRelative <= 0 {
			return nil, errModelStream
		}
		lengthEnd := lengthStart + lengthEndRelative
		length, err := strconv.Atoi(header[lengthStart:lengthEnd])
		if err != nil || length <= 0 || length > defaultMaxBlockBytes {
			return nil, errModelStream
		}
		valueStart := lengthEnd + 1
		valueEnd := valueStart + length
		if valueEnd > len(header) || bytes.Contains([]byte(header[valueStart:valueEnd]), []byte(modelHeaderSuffix)) {
			return nil, errModelStream
		}
		dictionary = append(dictionary, []byte(header[valueStart:valueEnd]))
		position = valueEnd
		if position < len(header) {
			if header[position] != ';' {
				return nil, errModelStream
			}
			position++
		}
	}
	return dictionary, nil
}

func expandModelBody(body string, dictionary [][]byte) ([]byte, error) {
	if len(dictionary) == 0 {
		return nil, errModelStream
	}
	output := make([]byte, 0, len(body))
	foundMarker := false
	for position := 0; position < len(body); {
		relativeMarker := strings.Index(body[position:], modelMarkerPrefix)
		if relativeMarker < 0 {
			output = append(output, body[position:]...)
			if len(output) > defaultMaxInputBytes {
				return nil, errModelStream
			}
			break
		}
		markerStart := position + relativeMarker
		output = append(output, body[position:markerStart]...)
		id, markerEnd, err := parseModelMarker(body, markerStart)
		if err != nil || id >= len(dictionary) {
			return nil, errModelStream
		}
		foundMarker = true
		output = append(output, dictionary[id]...)
		position = markerEnd
		if len(output) > defaultMaxInputBytes {
			return nil, errModelStream
		}
	}
	if !foundMarker {
		return nil, errModelStream
	}
	return output, nil
}

func parseModelMarker(body string, start int) (int, int, error) {
	if !strings.HasPrefix(body[start:], modelMarkerPrefix) {
		return 0, 0, errModelStream
	}
	position := start + len(modelMarkerPrefix)
	digitStart := position
	for position < len(body) && body[position] >= '0' && body[position] <= '9' {
		position++
	}
	if digitStart == position || !strings.HasPrefix(body[position:], modelMarkerSuffix) {
		return 0, 0, errModelStream
	}
	id, err := strconv.Atoi(body[digitStart:position])
	if err != nil || id < 0 || id > defaultMaxDictionary {
		return 0, 0, errModelStream
	}
	return id, position + len(modelMarkerSuffix), nil
}

var _ codec.LosslessCodec = (*Codec)(nil)
