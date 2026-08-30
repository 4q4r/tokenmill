package textnorm

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// percentSeq matches a valid percent-encoded byte. Sequences are decoded
// greedily left to right; malformed escapes stay literal.
var percentSeq = regexp.MustCompile(`(?:%[0-9A-Fa-f]{2})+`)

// minPercentSequences is the smallest number of decodable escapes before the
// decoder will touch the text: single stray escapes are rarely encodings and
// more often literal text like "100% 20".
const minPercentSequences = 2

// HasPercentEncodings reports whether decoding percent-escapes would change
// the text, with at least minPercentSequences decodable sequences.
func HasPercentEncodings(s string) bool {
	if !strings.ContainsRune(s, '%') {
		return false
	}
	decoded, count := decodePercent(s)
	return count >= minPercentSequences && decoded != s
}

// decodePercent replaces every run of %XX escapes with the UTF-8 bytes they
// encode. Runs that do not form valid UTF-8 stay literal, and the count of
// decoded runs is returned.
func decodePercent(s string) (string, int) {
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for {
		loc := percentSeq.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String(), count
		}
		start, end := loc[0], loc[1]
		decoded := percentDecodeRun(s[start:end])
		if decoded == "" {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		b.WriteString(s[:start])
		b.WriteString(decoded)
		count++
		s = s[end:]
	}
}

// percentDecodeRun decodes a run of %XX pairs when the bytes form valid
// UTF-8; otherwise it returns "" so the caller keeps the literal text.
func percentDecodeRun(run string) string {
	if len(run)%3 != 0 {
		return ""
	}
	var data []byte
	for i := 0; i < len(run); i += 3 {
		high, ok1 := hexNibble(run[i+1])
		low, ok2 := hexNibble(run[i+2])
		if !ok1 || !ok2 {
			return ""
		}
		data = append(data, high<<4|low)
	}
	if !utf8.Valid(data) {
		return ""
	}
	return string(data)
}

func hexNibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

// DecodePercent returns the percent-decoded form of s.
func DecodePercent(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	decoded, _ := decodePercent(s)
	return decoded
}

// hexToken matches hex-digit groups that may carry an 0x prefix.
var hexToken = regexp.MustCompile(`(?:0[xX])?[0-9a-fA-F]{2,16}`)

// hexRun matches at least four hex tokens separated by single whitespace
// characters — the shape of hexdumps, byte arrays, and GUID-style dumps.
var hexRun = regexp.MustCompile(`(?:0[xX])?[0-9a-fA-F]{2,16}(?:[ \t]+(?:0[xX])?[0-9a-fA-F]{2,16}){3,}`)

// HasCompactableHex reports whether the text contains a whitespace-separated
// hex run that compaction would join.
func HasCompactableHex(s string) bool {
	return compactedHex(s) != s
}

// CompactHex joins whitespace-separated hex tokens into one continuous
// lowercase hex string. Hexadecimal digits carry the same bytes with or
// without separators or case, so joining is byte-lossless; runs are
// validated token-by-token and prose is never touched.
func CompactHex(s string) string {
	return compactedHex(s)
}

func compactedHex(s string) string {
	if !strings.ContainsAny(s, " \t") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		loc := hexRun.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String()
		}
		start, end := loc[0], loc[1]
		run := s[start:end]
		tokens := hexToken.FindAllString(run, -1)
		if len(tokens) < 4 {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		joined := strings.Join(tokens, "")
		if len(tokens) < 4 || joined == run {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		joined = strings.ToLower(joined)
		b.WriteString(s[:start])
		b.WriteString(joined)
		s = s[end:]
	}
}

// PrefixFoldResult is the outcome of folding a block of lines that share one
// identical leading prefix.
type PrefixFoldResult struct {
	Content string
	Changed bool
}

// FoldLinePrefixes replaces runs of at least minLines consecutive lines that
// share one identical non-empty prefix (at least minPrefix bytes, ending on a
// whitespace boundary) with an explicit envelope line stating the prefix and
// the folded line count, followed by the trimmed lines. UnfoldLinePrefixes
// restores the original byte-for-byte, so the transform is lossless and the
// envelope is plain, model-readable text.
func FoldLinePrefixes(s string, minLines, minPrefix int) PrefixFoldResult {
	if minLines < 2 {
		minLines = 2
	}
	lines := strings.Split(s, "\n")
	changed := false
	for start := 0; start+minLines <= len(lines); {
		prefix, runEnd := commonPrefixRun(lines, start, minLines, minPrefix)
		if prefix == "" {
			start++
			continue
		}
		count := runEnd - start
		for i := start; i < runEnd; i++ {
			lines[i] = strings.TrimPrefix(lines[i], prefix)
		}
		envelope := "[the next " + itoa(count) + " lines start with " + quotePrefix(prefix) + "]"
		folded := append([]string{envelope}, lines[start:runEnd]...)
		rest := append([]string{}, lines[runEnd:]...)
		lines = append(lines[:start], append(folded, rest...)...)
		changed = true
		start += 1 + count
	}
	if !changed {
		return PrefixFoldResult{Content: s, Changed: false}
	}
	return PrefixFoldResult{Content: strings.Join(lines, "\n"), Changed: true}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// unfoldEnvelope matches the envelope emitted by FoldLinePrefixes and
// captures the folded line count and the quoted prefix.
var unfoldEnvelope = regexp.MustCompile(`^\[the next (\d+) lines start with "((?:[^"\\]|\\.)*)"\]$`)

// UnfoldLinePrefixes reverses FoldLinePrefixes byte-for-byte.
func UnfoldLinePrefixes(s string) string {
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		match := unfoldEnvelope.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		count := 0
		for _, d := range match[1] {
			count = count*10 + int(d-'0')
		}
		prefix := unquotePrefix(match[2])
		if i+count >= len(lines)+1 {
			return s // corrupted envelope: leave untouched
		}
		for j := 1; j <= count && i+j < len(lines)+1; j++ {
			if i+j < len(lines) {
				lines[i+j] = prefix + lines[i+j]
			}
		}
		lines = append(lines[:i], append([]string{}, lines[i+1:]...)...)
		i--
	}
	return strings.Join(lines, "\n")
}

func unquotePrefix(quoted string) string {
	var b strings.Builder
	for i := 0; i < len(quoted); i++ {
		if quoted[i] == '\\' && i+1 < len(quoted) {
			switch quoted[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(quoted[i+1])
			}
			i++
			continue
		}
		b.WriteByte(quoted[i])
	}
	return b.String()
}

// commonPrefixRun finds the longest common prefix shared by every line in a
// run of consecutive lines starting at start. The run extends while the
// shared prefix stays at least minPrefix bytes; the first line that breaks
// the prefix ends the run. The prefix must end on a token boundary in the
// first line. It returns the prefix and the exclusive end index of the
// folded run.
func commonPrefixRun(lines []string, start, minLines, minPrefix int) (string, int) {
	prefix := lines[start]
	if len(prefix) < minPrefix {
		return "", 0
	}
	end := start + 1
	lastGood := 0
	lastGoodPrefix := prefix
	for end < len(lines) && end-start < 512 {
		merged := commonBytes(prefix, lines[end])
		if len(merged) < minPrefix {
			break
		}
		prefix = merged
		end++
		lastGood = end
		lastGoodPrefix = merged
	}
	if lastGood-start < minLines {
		return "", 0
	}
	end = lastGood
	prefix = lastGoodPrefix
	// Trim the prefix back to a token boundary so it never cuts a token
	// mid-word, then drop trailing separators.
	for len(prefix) > minPrefix && !isBoundaryByte(prefix[len(prefix)-1]) {
		prefix = prefix[:len(prefix)-1]
	}
	for len(prefix) >= minPrefix && (prefix[len(prefix)-1] == ' ' || prefix[len(prefix)-1] == '\t' || prefix[len(prefix)-1] == '\r') {
		prefix = prefix[:len(prefix)-1]
	}
	if len(prefix) < minPrefix {
		return "", 0
	}
	if !utf8.ValidString(prefix) {
		return "", 0
	}
	return prefix, end
}

func isBoundaryByte(b byte) bool {
	return b == ' ' || b == '\t' || b == ']' || b == ':' || b == '|'
}

func commonBytes(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

func quotePrefix(prefix string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range prefix {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
