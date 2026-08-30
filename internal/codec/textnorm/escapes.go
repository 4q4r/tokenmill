package textnorm

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// unicodeEscapeRun matches one or more consecutive JSON-style \uXXXX
// escapes, including surrogate pairs.
var unicodeEscapeRun = regexp.MustCompile(`(?:\\u[0-9a-fA-F]{4})+`)

// minUnicodeEscapes is the smallest number of escapes the decoder will
// unfold: a lone \u0041 is usually deliberate documentation, not bloat,
// while two escapes already form a surrogate pair — a real encoded
// character worth restoring.
const minUnicodeEscapes = 2

// HasUnicodeEscapes reports whether unfolding \uXXXX escapes would change
// the text by at least the minimum run size.
func HasUnicodeEscapes(s string) bool {
	if !strings.Contains(s, `\u`) {
		return false
	}
	decoded, count := unfoldUnicode(s)
	return count >= minUnicodeEscapes && decoded != s
}

// UnfoldUnicode replaces runs of \uXXXX escapes (JSON and JSONP style) with
// the characters they encode, combining surrogate pairs. Per RFC 8259 both
// forms carry the identical string value, so the transform is value-lossless
// by specification. Runs containing lone surrogates stay literal. Text with
// fewer than minUnicodeEscapes escapes is left untouched: a lone escape is
// usually deliberate documentation rather than serialization bloat.
func UnfoldUnicode(s string) string {
	if !strings.Contains(s, `\u`) {
		return s
	}
	decoded, count := unfoldUnicode(s)
	if count < minUnicodeEscapes {
		return s
	}
	return decoded
}

func unfoldUnicode(s string) (string, int) {
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for {
		loc := unicodeEscapeRun.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String(), count
		}
		start, end := loc[0], loc[1]
		decoded := decodeEscapeRun(s[start:end])
		if decoded == "" {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		b.WriteString(s[:start])
		b.WriteString(decoded)
		count += (end - start) / 6
		s = s[end:]
	}
}

// decodeEscapeRun converts a run of \uXXXX units into a string. Surrogate
// pairs combine into a single rune; lone surrogates invalidate the run.
func decodeEscapeRun(run string) string {
	if len(run)%6 != 0 {
		return ""
	}
	var units []uint16
	for i := 0; i < len(run); i += 6 {
		value, ok := parseHex4(run[i+2 : i+6])
		if !ok {
			return ""
		}
		units = append(units, value)
	}
	for i := 0; i < len(units); i++ {
		if units[i] >= 0xD800 && units[i] <= 0xDBFF {
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				return ""
			}
			i++ // consume the low surrogate of the pair
		} else if units[i] >= 0xDC00 && units[i] <= 0xDFFF {
			return ""
		}
	}
	decoded := string(utf16.Decode(units))
	if !utf8.ValidString(decoded) {
		return ""
	}
	return decoded
}

func parseHex4(s string) (uint16, bool) {
	var value uint16
	for i := 0; i < len(s); i++ {
		nibble, ok := hexNibble(s[i])
		if !ok {
			return 0, false
		}
		value = value<<4 | uint16(nibble)
	}
	return value, true
}

// canonicalUUID matches the 8-4-4-4-12 hexadecimal UUID shape.
var canonicalUUID = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)

// HasDashedUUIDs reports whether the text contains canonical dashed UUIDs
// that compaction would join.
func HasDashedUUIDs(s string) bool {
	return canonicalUUID.MatchString(s)
}

// CompactUUIDs removes the presentation dashes from canonical UUIDs. The
// hexadecimal digits are the identifier; dashes are fixed-position
// decoration, so reinserting them at positions 8, 13, 18, and 23 restores
// the original byte-for-byte.
func CompactUUIDs(s string) string {
	return canonicalUUID.ReplaceAllStringFunc(s, func(uuid string) string {
		return uuid[:8] + uuid[9:13] + uuid[14:18] + uuid[19:23] + uuid[24:]
	})
}

// RedashUUIDs reinserts canonical dashes into continuous 32-hex-digit UUID
// bodies. Non-UUID text is returned unchanged.
func RedashUUIDs(s string) string {
	run := regexp.MustCompile(`\b[0-9a-fA-F]{32}\b`)
	return run.ReplaceAllStringFunc(s, func(body string) string {
		return body[:8] + "-" + body[8:12] + "-" + body[12:16] + "-" + body[16:20] + "-" + body[20:]
	})
}

// smartPairs maps typographic punctuation artifacts to their ASCII display
// equivalents. Russian guillemets («») and the em dash are deliberately
// absent: they are meaningful, correct grammar in Cyrillic text, not paste
// artifacts.
var smartPairs = []struct{ from, to string }{
	{"\u201C", `"`},   // left double quotation mark
	{"\u201D", `"`},   // right double quotation mark
	{"\u2018", "'"},   // left single quotation mark
	{"\u2019", "'"},   // right single quotation mark
	{"\u201A", ","},   // single low quotation mark
	{"\u201E", `"`},   // double low quotation mark
	{"\u2026", "..."}, // horizontal ellipsis
}

// HasSmartPunctuation reports whether ASCII normalization would change the
// text.
func HasSmartPunctuation(s string) bool {
	for _, pair := range smartPairs {
		if strings.Contains(s, pair.from) {
			return true
		}
	}
	return false
}

// NormalizeSmartPunctuation replaces typographic quote and ellipsis
// artifacts with their ASCII display equivalents. This is display-lossless:
// the reader sees the same punctuation marks. Cyrillic guillemets and the
// em dash are preserved because they are grammatical, not artifacts.
func NormalizeSmartPunctuation(s string) string {
	for _, pair := range smartPairs {
		s = strings.ReplaceAll(s, pair.from, pair.to)
	}
	return s
}
