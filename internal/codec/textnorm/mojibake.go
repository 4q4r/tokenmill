package textnorm

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// mojibakePairs maps the most common Windows-1252 misread sequences to the
// characters the author actually typed. The list is explicit and bounded so
// the repair can never corrupt ordinary text.
var mojibakePairs = []struct{ from, to string }{
	{"â€™", "'"},
	{"â€œ", "\""},
	{"â€\x9d", "\""},
	{"â€˜", "'"},
	{"â€œâ€“", "–"},
	{"â€“", "–"},
	{"â€”", "—"},
	{"â€¦", "…"},
	{"Â«", "«"},
	{"Â»", "»"},
	{"Ã©", "é"},
	{"Ã¨", "è"},
	{"Ã¤", "ä"},
	{"Ã¶", "ö"},
	{"Ã¼", "ü"},
	{"Ã±", "ñ"},
	{"Ã§", "ç"},
	{"Â ", " "},
	{"â†’", "→"},
	{"ðŸ˜€", "😀"},
}

// HasMojibake reports whether the text contains a known mojibake sequence.
func HasMojibake(s string) bool {
	for _, pair := range mojibakePairs {
		if strings.Contains(s, pair.from) {
			return true
		}
	}
	return false
}

// RepairMojibake replaces known UTF-8-read-as-Windows-1252 sequences with
// the characters the author intended. The repair is deterministic; NFC
// composition runs afterwards so repaired combining sequences settle.
func RepairMojibake(s string) string {
	if !HasMojibake(s) {
		return s
	}
	for _, pair := range mojibakePairs {
		s = strings.ReplaceAll(s, pair.from, pair.to)
	}
	return norm.NFC.String(s)
}
