package textnorm

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// thousandSep matches digit groups separated by commas, spaces, or thin
// spaces (U+2009): `1,234,567` / `1 234 567`.
var thousandSep = regexp.MustCompile(`\b\d{1,3}(?:[,\x{2009} ]\d{3})+\b`)

// HasThousandSeparators reports whether the text contains numbers with
// thousands separators.
func HasThousandSeparators(s string) bool {
	return thousandSep.MatchString(s)
}

// StripThousandSeparators removes comma, space, and thin-space separators
// from within numbers. `1,234,567` → `1234567` — the numeric value is
// identical and the model reads both forms.
func StripThousandSeparators(s string) string {
	return thousandSep.ReplaceAllStringFunc(s, func(m string) string {
		return strings.NewReplacer(",", "", "\u2009", "", " ", "").Replace(m)
	})
}

// HasQuotedPrintable reports whether the text contains quoted-printable
// escape sequences.
func HasQuotedPrintable(s string) bool {
	return strings.Contains(s, "=3D") || strings.Contains(s, "=20") ||
		strings.Contains(s, "=3C") || strings.Contains(s, "=0A") ||
		strings.Contains(s, "=\n") || strings.Contains(s, "=2C")
}

// DecodeQuotedPrintable decodes quoted-printable escape sequences (RFC 2045).
// Soft line breaks (=\n) are removed; =XX becomes the corresponding byte.
// The result must be valid UTF-8 or the input is returned unchanged.
func DecodeQuotedPrintable(s string) string {
	if !HasQuotedPrintable(s) {
		return s
	}
	var data []byte
	i := 0
	for i < len(s) {
		if s[i] == '=' && i+1 < len(s) {
			if s[i+1] == '\n' || (s[i+1] == '\r' && i+2 < len(s) && s[i+2] == '\n') {
				// Soft line break: remove entirely.
				if s[i+1] == '\r' {
					i += 3
				} else {
					i += 2
				}
				continue
			}
			if i+2 < len(s) {
				hi, okH := hexNibble(s[i+1])
				lo, okL := hexNibble(s[i+2])
				if okH && okL {
					data = append(data, hi<<4|lo)
					i += 3
					continue
				}
			}
		}
		data = append(data, s[i])
		i++
	}
	if !utf8.Valid(data) {
		return s
	}
	return string(data)
}
