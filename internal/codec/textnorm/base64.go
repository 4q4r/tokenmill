package textnorm

import (
	"encoding/base64"
	"regexp"
	"strings"
)

// base64Run matches a run that looks like base64 payload with optional
// embedded whitespace (certificates, PEM bodies, wrapped tokens). The run
// must be at least 64 characters of base64 alphabet/whitespace so ordinary
// prose is never touched.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/=\r\n\t -]{64,}`)

func isWhitespaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

// decodeBase64Tolerant decodes s under the standard, URL, raw, and padded
// variants; any successful decode counts.
func decodeBase64Tolerant(s string) ([]byte, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, false
	}
	for _, decoder := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if data, err := decoder.DecodeString(trimmed); err == nil && len(data) > 0 {
			return data, true
		}
	}
	// Tolerate missing/extra padding on the standard alphabet.
	padded := trimmed
	if pad := len(padded) % 4; pad != 0 {
		padded += strings.Repeat("=", 4-pad)
	}
	if data, err := base64.StdEncoding.DecodeString(padded); err == nil && len(data) > 0 {
		return data, true
	}
	return nil, false
}

// HasCompactableBase64 reports whether the text contains a decodable base64
// run with embedded whitespace that compaction would change.
func HasCompactableBase64(s string) bool {
	return compactedOnce(s) != s
}

// CompactBase64 removes whitespace inside decodable base64 runs. Removing
// whitespace from base64 is byte-lossless by specification: every decoder
// ignores it. Runs that do not cleanly decode are left untouched, so prose
// never changes.
func CompactBase64(s string) string {
	return compactedOnce(s)
}

// stripRunWhitespace removes space, tab, CR, and LF bytes from run.
func stripRunWhitespace(run string) string {
	return strings.Map(func(r rune) rune {
		if isWhitespaceRune(r) {
			return -1
		}
		return r
	}, run)
}

// rewriteEncodedRuns applies rewrite to every candidate run located by
// pattern, threading the untouched tail between runs back into the output.
func rewriteEncodedRuns(s string, pattern *regexp.Regexp, rewrite func(run string) string) string {
	var b strings.Builder
	b.Grow(len(s))
	for {
		loc := pattern.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String()
		}
		start, end := loc[0], loc[1]
		rewritten := rewrite(s[start:end])
		if rewritten == s[start:end] {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		b.WriteString(s[:start])
		b.WriteString(rewritten)
		s = s[end:]
	}
}

func compactedOnce(s string) string {
	if !strings.ContainsAny(s, " \t\r\n") {
		return s
	}
	return rewriteEncodedRuns(s, base64Run, func(run string) string {
		start := 0
		for start < len(run) && isWhitespaceRune(rune(run[start])) {
			start++
		}
		end := len(run)
		for end > start && isWhitespaceRune(rune(run[end-1])) {
			end--
		}
		core := run[start:end]
		_, compactedRun := compactRun(core)
		return compactedRun
	})
}

// compactRun strips whitespace from one candidate run when — and only when —
// the run decodes identically before and after stripping, which is the
// specification guarantee for base64 and the proof of losslessness.
func compactRun(run string) (decoded []byte, compacted string) {
	original, okOriginal := decodeBase64Tolerant(run)
	if !okOriginal {
		return nil, run
	}
	stripped := stripRunWhitespace(run)
	if stripped == run {
		return original, run
	}
	strippedDecoded, okStripped := decodeBase64Tolerant(stripped)
	if !okStripped || string(strippedDecoded) != string(original) {
		return nil, run
	}
	return original, stripped
}
