package textnorm

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// ---------- trailing whitespace (per line) ----------

// singleTrailingSpace matches exactly one trailing space or tab: the sloppy
// case. Markdown hard breaks (two or more spaces before a newline) are
// deliberately preserved by never matching their final two spaces.
var singleTrailingSpace = regexp.MustCompile(`(?m)(?:^|\S)([ \t])$`)

// HasTrailingWhitespace reports whether any line ends with a single
// trailing space or tab.
func HasTrailingWhitespace(s string) bool {
	return singleTrailingSpace.MatchString(s)
}

// StripTrailingWhitespace removes single trailing spaces and tabs from every
// line. Runs of two or more spaces before a newline — Markdown hard breaks —
// are preserved, as are their content bytes.
func StripTrailingWhitespace(s string) string {
	return singleTrailingSpace.ReplaceAllStringFunc(s, func(match string) string {
		return match[:len(match)-1]
	})
}

// ---------- blank line runs ----------

// blankRun matches three or more consecutive newlines (two or more blank
// lines). Everything else about the text stays identical.
var blankRun = regexp.MustCompile(`\n{3,}`)

// HasBlankRuns reports whether the text contains runs of two or more blank
// lines.
func HasBlankRuns(s string) bool {
	return blankRun.MatchString(s)
}

// CollapseBlankRuns folds runs of two or more blank lines into a single
// blank line. Paragraph structure is preserved — one blank line is exactly
// the separator models and renderers expect.
func CollapseBlankRuns(s string) string {
	return blankRun.ReplaceAllString(s, "\n\n")
}

// ---------- NFKC compatibility fold ----------

// hasCJK reports whether the text contains Han, Kana, or Hangul runes whose
// width and compatibility forms are intentional typography.
func hasCJK(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
			r >= 0x3400 && r <= 0x4DBF, // Extension A
			r >= 0x3040 && r <= 0x30FF, // Hiragana + Katakana
			r >= 0xAC00 && r <= 0xD7AF, // Hangul syllables
			r >= 0x1100 && r <= 0x11FF: // Hangul Jamo
			return true
		}
	}
	return false
}

// compatibilityRunes reports whether the text contains compatibility code
// points that NFKC would fold: fullwidth/halfwidth forms, ligatures,
// circled numbers and letters, superscripts/subscripts.
func compatibilityRunes(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF60, // fullwidth and halfwidth forms
			r >= 0xFFE0 && r <= 0xFFEE, // fullwidth signs
			r >= 0x2460 && r <= 0x24FF, // circled/parenthesized numbers
			r >= 0x2160 && r <= 0x217F, // Roman numerals
			r == 0xFB00 || r == 0xFB01 || r == 0xFB02 || r == 0xFB03 || r == 0xFB04, // ligatures
			r >= 0x2070 && r <= 0x209F, // super/subscripts
			r >= 0x24B6 && r <= 0x24E9: // circled letters
			return true
		}
	}
	return false
}

// HasCompatibilityForms reports whether NFKC folding would change the text
// and the text carries no CJK typography that must stay untouched.
func HasCompatibilityForms(s string) bool {
	if hasCJK(s) {
		return false
	}
	return compatibilityRunes(s) || norm.NFKC.String(s) != s
}

// FoldCompatibility applies NFKC to fold compatibility code points into
// their canonical forms: fullwidth ＡＢＣ → ABC, ligatures ﬁ → fi,
// circled ① → 1, superscript ² → 2. Text containing CJK typography is
// returned unchanged — width variants are intentional there.
func FoldCompatibility(s string) string {
	if hasCJK(s) {
		return s
	}
	return norm.NFKC.String(s)
}

// ---------- color compaction ----------

// hexColor6 matches six-digit hex colors, the shorthand candidates.
var hexColor6 = regexp.MustCompile(`#[0-9a-fA-F]{6}\b`)

// HasCompactableColors reports whether the text contains expandable hex
// colors.
func HasCompactableColors(s string) bool {
	return CompactHexColors(s) != s
}

// CompactHexColors collapses six-digit hex colors with repeating digit pairs
// into the three-digit CSS shorthand (#AABBCC → #abc). CSS renders both
// forms identically; colors without repeating pairs stay untouched.
func CompactHexColors(s string) string {
	return hexColor6.ReplaceAllStringFunc(s, func(color string) string {
		if color[1] == color[2] && color[3] == color[4] && color[5] == color[6] {
			return "#" + strings.ToLower(string(color[1])+string(color[3])+string(color[5]))
		}
		return color
	})
}
