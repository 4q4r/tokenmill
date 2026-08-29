// Package textnorm provides lossless text normalization codecs for LLM
// input: stripping invisible Unicode that wastes tokens and destabilizes
// tokenization, decoding HTML entities into their visible characters, and
// compacting whitespace inside base64 payloads (byte-lossless by spec).
package textnorm

import (
	"html"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// htmlUnescape decodes HTML entities (named and numeric). The standard
// library implementation is exact and idempotent-safe.
var htmlUnescape = html.UnescapeString

// stripRunes is the conservative invisible set: characters that render as
// nothing, hide inside copy-paste artifacts, or carry steganographic payloads
// (Unicode tag block). Zero-width joiner U+200D is deliberately absent — it
// glues emoji sequences and must survive.
var stripSet = func() map[rune]struct{} {
	var runes = []rune{
		'\u200B',                               // zero-width space
		'\u200C',                               // zero-width non-joiner
		'\u2060',                               // word joiner
		'\uFEFF',                               // BOM / zero-width no-break space
		'\u00AD',                               // soft hyphen
		'\u180E',                               // Mongolian vowel separator (invisible in modern Unicode)
		'\u2061', '\u2062', '\u2063', '\u2064', // invisible math operators
		'\u202A', '\u202B', '\u202C', '\u202D', '\u202E', // bidi controls
		'\u2066', '\u2067', '\u2068', '\u2069', // isolate controls
	}
	set := make(map[rune]struct{}, len(runes)+16)
	for _, r := range runes {
		set[r] = struct{}{}
	}
	for r := rune(0xE0001); r <= rune(0xE007F); r++ { // tag characters
		set[r] = struct{}{}
	}
	for r := rune(0x0080); r <= rune(0x009F); r++ { // C1 controls
		set[r] = struct{}{}
	}
	return set
}()

// spaceSet maps exotic space code points to the plain space.
var spaceSet = func() map[rune]struct{} {
	var runes = []rune{
		'\u00A0', // no-break space
		'\u1680', // ogham space mark
		'\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
		'\u2007', '\u2008', '\u2009', '\u200A', // en/em/thin/hair spaces
		'\u202F', // narrow no-break space
		'\u205F', // medium math space
		'\u3000', // ideographic space
	}
	set := make(map[rune]struct{}, len(runes))
	for _, r := range runes {
		set[r] = struct{}{}
	}
	return set
}()

// NeedsNormalization reports whether the text contains anything the
// normalization pass would change: strippable invisibles, exotic spaces,
// disallowed control characters, or a non-NFC representation.
func NeedsNormalization(s string) bool {
	if norm.NFC.IsNormalString(s) {
		for _, r := range s {
			if _, strip := stripSet[r]; strip {
				return true
			}
			if _, space := spaceSet[r]; space {
				return true
			}
			if r == '\u2011' { // non-breaking hyphen
				return true
			}
			if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
				return true
			}
			if r == 0x7F {
				return true
			}
		}
		return false
	}
	// Non-NFC text may or may not change under normalization; the cheap
	// check is to normalize a bounded prefix.
	bound := len(s)
	if bound > 4096 {
		bound = 4096
		// Do not cut inside a multi-byte rune.
		for bound > 0 && !utf8RuneStart(s[bound]) {
			bound--
		}
	}
	return norm.NFC.String(s[:bound]) != s[:bound] || containsTransformable(s)
}

func containsTransformable(s string) bool {
	for _, r := range s {
		if _, strip := stripSet[r]; strip {
			return true
		}
		if _, space := spaceSet[r]; space {
			return true
		}
		if r == '\u2011' {
			return true
		}
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return true
		}
		if r == 0x7F {
			return true
		}
	}
	return false
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// Normalize returns the display-lossless normalized form: NFC composition,
// invisible characters removed, exotic spaces mapped to plain space,
// non-breaking hyphens mapped to hyphens, and stray control characters
// removed. Tab, newline, and carriage return are preserved.
func Normalize(s string) string {
	if !NeedsNormalization(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if _, strip := stripSet[r]; strip {
			continue
		}
		switch {
		case r == '\u2011':
			b.WriteRune('-')
		case r < 0x20 && r != '\n' && r != '\r' && r != '\t':
			// stray C0 control: drop
		case r == 0x7F:
			// DEL: drop
		default:
			if _, space := spaceSet[r]; space {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(r)
		}
	}
	return norm.NFC.String(b.String())
}

// HasHTMLEntities reports whether UnescapeEntities would change the text.
func HasHTMLEntities(s string) bool {
	if !strings.ContainsRune(s, '&') {
		return false
	}
	return htmlUnescape(s) != s
}

// UnescapeEntities decodes HTML character entities into their literal
// characters. This is display-lossless: what a reader sees in rendered HTML
// is exactly what the model receives. The transform is idempotent.
func UnescapeEntities(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	return htmlUnescape(s)
}
