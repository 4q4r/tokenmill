// Package cache contains provider-neutral metadata helpers for prompt-prefix
// caching. It does not call, configure, or assume any provider API.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/codec/jcs"
)

// Message is the minimal provider-neutral chat message used by this package.
// Content and Role are payload; CacheControl is metadata only.
type Message struct {
	Role         string      `json:"role"`
	Content      string      `json:"content"`
	CacheControl *Breakpoint `json:"cache_control,omitempty"`
}

// Breakpoint marks a message at which a provider adapter may request prefix
// caching. Position is informational and refers to the message index supplied
// to AddBreakpoint; the adapter remains responsible for provider translation.
type Breakpoint struct {
	Position int    `json:"position"`
	Type     string `json:"type"`
}

// CacheControl is kept as a source-compatible name for existing callers.
// It is an alias, not a provider-specific integration.
type CacheControl = Breakpoint

// Tool is a minimal JSON-compatible tool definition. InputSchema must contain
// values supported by encoding/json (maps, slices, strings, numbers, booleans,
// and nil).
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema,omitempty"`
}

// PrefixCache is provider-neutral cache metadata. It records breakpoints for
// an adapter; it does not manage provider state or make network calls.
type PrefixCache struct {
	Breakpoints []Breakpoint `json:"breakpoints,omitempty"`
}

// NewPrefixCache returns an empty provider-neutral metadata value.
// The value is stateless; the constructor remains for compatibility with the
// original method-based facade.
func NewPrefixCache() *PrefixCache {
	return &PrefixCache{}
}

// AddBreakpoint returns a copied message slice with cache metadata attached
// at position. Message payload fields are never changed and the input slice is
// never mutated. Invalid positions return a copied, unchanged slice.
func AddBreakpoint(messages []Message, position int) []Message {
	out := cloneMessages(messages)
	if position < 0 || position >= len(out) {
		return out
	}
	out[position].CacheControl = &Breakpoint{
		Position: position,
		Type:     "ephemeral",
	}
	return out
}

// AddBreakpoint is a compatibility method that delegates to the pure package
// function. PrefixCache has no mutable state.
func (p *PrefixCache) AddBreakpoint(messages []Message, position int) []Message {
	return AddBreakpoint(messages, position)
}

// StablePrefix returns a copied prefix through the last explicit breakpoint.
// If there is no breakpoint, it returns a non-nil empty slice.
func StablePrefix(messages []Message) []Message {
	last := -1
	for i, message := range messages {
		if message.CacheControl != nil {
			last = i
		}
	}
	if last < 0 {
		return make([]Message, 0)
	}
	return cloneMessages(messages[:last+1])
}

// StablePrefix is a compatibility method that delegates to the pure package
// function. PrefixCache has no mutable state.
func (p *PrefixCache) StablePrefix(messages []Message) []Message {
	return StablePrefix(messages)
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].CacheControl == nil {
			continue
		}
		breakpoint := *out[i].CacheControl
		out[i].CacheControl = &breakpoint
	}
	return out
}

// MarshalMetadata serializes JSON-compatible cache metadata using the package
// JCS-compatible canonicalizer. It returns an error for invalid JSON values.
func MarshalMetadata(metadata interface{}) ([]byte, error) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return jcs.Canonicalize(raw)
}

// SerializeMetadata is the string form of MarshalMetadata.
func SerializeMetadata(metadata interface{}) (string, error) {
	encoded, err := MarshalMetadata(metadata)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// CacheScopeKey returns a lower-case SHA-256 key for a system/model/tool
// scope. Tools are stably ordered before hashing. The encoded envelope avoids
// delimiter collisions and is suitable for a later provider plugin adapter.
func CacheScopeKey(system string, tools []Tool, model string) string {
	payload := struct {
		System string `json:"system"`
		Tools  []Tool `json:"tools"`
		Model  string `json:"model"`
	}{
		System: system,
		Tools:  SortTools(tools),
		Model:  model,
	}
	encoded, err := MarshalMetadata(payload)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

// PromptCacheKey is the historical name for CacheScopeKey.
func PromptCacheKey(system string, tools []Tool, model string) string {
	return CacheScopeKey(system, tools, model)
}

// SortTools returns a stable sorted copy ordered by tool name. Equal names
// retain their input order, so duplicate-name definitions are not silently
// reordered or merged.
func SortTools(tools []Tool) []Tool {
	out := make([]Tool, len(tools))
	copy(out, tools)
	sort.SliceStable(out, func(i, j int) bool {
		return lessUTF16(out[i].Name, out[j].Name)
	})
	return out
}

// FreezeSchema returns the SHA-256 hash of the canonical, stably ordered tool
// schema. It returns an empty string if InputSchema contains a non-JSON value.
func FreezeSchema(tools []Tool) string {
	encoded, err := MarshalMetadata(SortTools(tools))
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
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

// CacheControlHeader returns the generic ephemeral breakpoint metadata used by
// the compatibility facade. A plugin adapter may translate it as needed.
func CacheControlHeader() CacheControl {
	return CacheControl{Type: "ephemeral"}
}

// CacheControlHeaderJSON returns deterministic JSON for CacheControlHeader.
func CacheControlHeaderJSON() string {
	encoded, err := SerializeMetadata(CacheControlHeader())
	if err != nil {
		return ""
	}
	return encoded
}

// CacheControlHeaderMap returns a generic map representation for adapters.
func CacheControlHeaderMap() map[string]interface{} {
	return map[string]interface{}{"type": "ephemeral"}
}

var (
	uuidValueRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dateValueRegex = regexp.MustCompile(`^(?:\d{4}[-/]\d{2}[-/]\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?|\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)$`)
	dateFieldRegex = regexp.MustCompile(`(?i)^(?:(?:current|now)[ _-]+)?(timestamp|date|datetime|datetime\.now\(\)|time)\s*[:=]\s*(.+)$`)
	idFieldRegex   = regexp.MustCompile(`(?i)^(uuid|request(?:[_ -]?id)?|correlation[_ -]?id|x-request-id|trace[_ -]?id)\s*[:=]\s*(.+)$`)
)

const volatileSuffixHeader = "tokenmill-volatile-v1"

// volatileEntry preserves an extracted source line and its line ending. The
// index lets an adapter or decoder restore the exact original ordering.
type volatileEntry struct {
	Index  int    `json:"index"`
	Line   string `json:"line"`
	Ending string `json:"ending"`
}

type preservedLine struct {
	content string
	ending  string
}

// StabilizePrefix replaces only clearly standalone volatile metadata lines
// with deterministic placeholders and returns a versioned suffix containing
// the original lines. It deliberately ignores code fences, URLs, arbitrary
// prose, and invalid UTF-8 input.
func StabilizePrefix(system string) (stable, volatile string) {
	if system == "" || !utf8.ValidString(system) {
		return system, ""
	}
	lines := splitPreservedLines(system)
	inFence := false
	entries := make([]volatileEntry, 0)
	for index := range lines {
		line := lines[index].content
		trimmed := strings.TrimSpace(line)
		if isFenceLine(trimmed) {
			inFence = !inFence
			continue
		}
		if inFence || !isVolatileMetadataLine(line) {
			continue
		}
		entries = append(entries, volatileEntry{
			Index:  index,
			Line:   line,
			Ending: lines[index].ending,
		})
		lines[index].content = "[tokenmill:volatile:" + strconv.Itoa(index) + "]"
	}
	if len(entries) == 0 {
		return system, ""
	}
	return joinPreservedLines(lines), formatVolatile(entries)
}

func isFenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func isVolatileMetadataLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return false
	}
	if uuidValueRegex.MatchString(trimmed) {
		return true
	}
	if match := dateFieldRegex.FindStringSubmatch(trimmed); len(match) == 3 {
		return dateValueRegex.MatchString(strings.TrimSpace(match[2]))
	}
	if match := idFieldRegex.FindStringSubmatch(trimmed); len(match) == 3 {
		key := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(match[1], "_", ""), "-", ""))
		value := strings.TrimSpace(match[2])
		if value == "" || strings.Contains(value, "://") {
			return false
		}
		if key == "uuid" || key == "request" {
			return uuidValueRegex.MatchString(value)
		}
		return true
	}
	return false
}

func formatVolatile(entries []volatileEntry) string {
	encoded, err := json.Marshal(entries)
	if err != nil {
		panic("cache: failed to encode internal volatile metadata")
	}
	return volatileSuffixHeader + "\n" + string(encoded)
}

func splitPreservedLines(input string) []preservedLine {
	if input == "" {
		return nil
	}
	lines := make([]preservedLine, 0, strings.Count(input, "\n")+1)
	start := 0
	for i := 0; i < len(input); i++ {
		if input[i] != '\n' {
			continue
		}
		content := input[start:i]
		ending := "\n"
		if strings.HasSuffix(content, "\r") {
			content = strings.TrimSuffix(content, "\r")
			ending = "\r\n"
		}
		lines = append(lines, preservedLine{content: content, ending: ending})
		start = i + 1
	}
	if start < len(input) {
		lines = append(lines, preservedLine{content: input[start:]})
	}
	return lines
}

func joinPreservedLines(lines []preservedLine) string {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.content)
		builder.WriteString(line.ending)
	}
	return builder.String()
}
