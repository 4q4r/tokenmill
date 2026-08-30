package textnorm

import (
	"strings"
	"unicode/utf8"
)

// ContainsHexEscapes reports raw \xNN presence.
func ContainsHexEscapes(s string) bool {
	return strings.Contains(s, `\x`)
}

// HasHexEscapes reports whether hex-escape unfolding would change the text.
func HasHexEscapes(s string) bool {
	return UnfoldHexEscapes(s) != s
}

// minHexEscapes is the smallest number of escapes the unfolder applies.
const minHexEscapes = 2

// UnfoldHexEscapes decodes runs of \xNN hex escapes (Python and shell
// string-escape style) into the bytes they encode when those bytes form
// valid UTF-8. Lone or malformed escapes stay literal, and text with fewer
// than minHexEscapes decoded runs is untouched — a lone \x41 in
// documentation is deliberate.
func UnfoldHexEscapes(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	original := s
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for {
		idx := strings.Index(s, `\x`)
		if idx < 0 {
			b.WriteString(s)
			break
		}
		runEnd, data := collectHexRun(s[idx:])
		if data == nil {
			// Not a decodable escape: keep the two marker bytes and move on.
			b.WriteString(s[:idx+2])
			s = s[idx+2:]
			continue
		}
		b.WriteString(s[:idx])
		b.Write(data)
		count += runEnd / 4
		s = s[idx+runEnd:]
	}
	if count < minHexEscapes {
		return original
	}
	return b.String()
}

// collectHexRun decodes a run of adjacent \xNN escapes starting at run[0].
// It returns the number of consumed bytes and the decoded data, or nil when
// the first escape is malformed or the decoded bytes are not valid UTF-8.
func collectHexRun(run string) (int, []byte) {
	if len(run) < 4 {
		return 0, nil
	}
	var data []byte
	consumed := 0
	for consumed+4 <= len(run) && run[consumed] == '\\' && run[consumed+1] == 'x' {
		h, okH := hexNibble(run[consumed+2])
		l, okL := hexNibble(run[consumed+3])
		if !okH || !okL {
			break
		}
		data = append(data, h<<4|l)
		consumed += 4
	}
	if !utf8.Valid(data) {
		return consumed, nil
	}
	return consumed, data
}

// HasDeepEntities reports whether repeated bounded entity decoding would
// change the text further after a single pass.
func HasDeepEntities(s string) bool {
	return DeepUnescapeEntities(s) != s
}

// DeepUnescapeEntities decodes HTML entities repeatedly (bounded) so
// double-encoded payloads like `&amp;amp;` collapse to their intended
// character. Decoding stops as soon as a pass stops changing the text.
func DeepUnescapeEntities(s string) string {
	const maxPasses = 4
	current := s
	for pass := 0; pass < maxPasses; pass++ {
		next := htmlUnescape(current)
		if next == current {
			break
		}
		current = next
	}
	return current
}
