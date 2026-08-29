package terminal

import (
	"regexp"
	"strings"
)

// Precompiled regex patterns O(1) via MustCompile at init.
// CSI: \x1b\[[0-9;?]*[ -/]*[@-~]
// OSC: \x1b\][^\x07]*\x07
// Hyperlink: \x1b]8;;URL\x07TEXT\x1b]8;;\x07
var (
	csiRegex       = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	oscRegex       = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	hyperlinkRegex = regexp.MustCompile("\x1b\\]8;;([^\x07\x1b]*)(?:\x07|\x1b\\\\)([^\x1b]*?)\x1b\\]8;;(?:\x07|\x1b\\\\)")
)

// StripANSI removes ANSI escape sequences.
// If preserveHyperlink is true, OSC 8 hyperlink URL is preserved as "text (url)".
// Fast path: if no ESC byte present, returns original string unchanged.
func StripANSI(s string, preserveHyperlink ...bool) string {
	preserve := false
	if len(preserveHyperlink) > 0 {
		preserve = preserveHyperlink[0]
	}
	if !strings.Contains(s, "\x1b") {
		return s
	}
	if preserve {
		// Preserve hyperlink: replace hyperlink sequences with "text (url)" before stripping others.
		// hyperlinkRegex captures URL in group1 and text in group2.
		s = hyperlinkRegex.ReplaceAllStringFunc(s, func(m string) string {
			sub := hyperlinkRegex.FindStringSubmatch(m)
			if len(sub) < 3 {
				return ""
			}
			url := sub[1]
			text := sub[2]
			// Strip any CSI inside text? Keep simple: text as is (CSI will be stripped later if present)
			// But we should strip CSI from text to avoid leftover escapes in preserved form.
			// Do a quick CSI strip on text fragment.
			if strings.Contains(text, "\x1b") {
				text = csiRegex.ReplaceAllString(text, "")
				text = oscRegex.ReplaceAllString(text, "")
			}
			if url == "" {
				return text
			}
			if text == "" {
				return url
			}
			return text + " (" + url + ")"
		})
		// Strip remaining CSI and OSC
		s = csiRegex.ReplaceAllString(s, "")
		s = oscRegex.ReplaceAllString(s, "")
		return s
	}
	// Strict: strip all
	s = csiRegex.ReplaceAllString(s, "")
	s = oscRegex.ReplaceAllString(s, "")
	return s
}

// HasANSI reports whether s contains ANSI escape sequences.
func HasANSI(s string) bool {
	if !strings.Contains(s, "\x1b") {
		return false
	}
	if csiRegex.MatchString(s) || oscRegex.MatchString(s) {
		return true
	}
	// Fallback: contains ESC but not matched by regex (e.g., single ESC)
	return false
}

// RenderCR emulates terminal display for carriage returns.
// First replaces CRLF with LF to preserve Windows line endings,
// then per line keeps only segment after last '\r' (rsplit('\r').last()).
//
// This is display-lossless: it keeps the last visible part of progress bars
// like "Downloading 1%\rDownloading 50%\rDownloading 100%".
//
// Edge: "abcdefg\rxyz" becomes "xyz", not "xyzdefg" (terminal overwrite
// emulation would require fixed-width overwrite). Documented as last segment
// behavior like llmtrim and termcp.
func RenderCR(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	// Preserve CRLF: convert to LF first
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if idx := strings.LastIndex(line, "\r"); idx != -1 {
			lines[i] = line[idx+1:]
		}
	}
	return strings.Join(lines, "\n")
}

// NormalizeTerminal is RenderCR(StripANSI(s, preserveHyperlink)).
// It is display-lossless normalization for LLM consumption.
func NormalizeTerminal(s string, preserveHyperlink bool) string {
	return RenderCR(StripANSI(s, preserveHyperlink))
}
