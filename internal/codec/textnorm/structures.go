package textnorm

import (
	"regexp"
	"strings"
)

// HasOverQuotedCSV reports whether the text looks like CSV whose fields are
// uniformly wrapped in quotes.
func HasOverQuotedCSV(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return false
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, `"`) || !strings.HasSuffix(line, `"`) {
			return false
		}
	}
	return true
}

// parseCSVLine splits one RFC 4180 record into fields, honoring quoting and
// doubled-quote escapes. ok is false when the line is malformed.
func parseCSVLine(line string) ([]string, bool) {
	var fields []string
	var field strings.Builder
	inQuotes := false
	for i := 0; i < len(line); i++ {
		char := line[i]
		switch {
		case inQuotes:
			if char == '"' {
				if i+1 < len(line) && line[i+1] == '"' {
					field.WriteByte('"')
					i++
					continue
				}
				inQuotes = false
				continue
			}
			field.WriteByte(char)
		case char == '"':
			inQuotes = true
		case char == ',':
			fields = append(fields, field.String())
			field.Reset()
		default:
			field.WriteByte(char)
		}
	}
	if inQuotes {
		return nil, false
	}
	fields = append(fields, field.String())
	return fields, true
}

// emitCSVLine re-serializes fields with minimal RFC 4180 quoting: quotes
// appear only when a field contains a delimiter, quote, or control
// character. Field values are byte-identical to the input values.
func emitCSVLine(fields []string) string {
	parts := make([]string, len(fields))
	for i, field := range fields {
		if strings.ContainsAny(field, ",\"\n\r") {
			parts[i] = `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
			continue
		}
		parts[i] = field
	}
	return strings.Join(parts, ",")
}

// UnquoteCSV parses every record and re-emits it with minimal quoting.
// Field values are identical before and after; only redundant quote
// characters are removed. Malformed records keep their original bytes.
func UnquoteCSV(s string) string {
	if !HasOverQuotedCSV(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, len(lines))
	changed := false
	for i, line := range lines {
		out[i] = line
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields, ok := parseCSVLine(line)
		if !ok {
			continue
		}
		reEmitted := emitCSVLine(fields)
		if reEmitted != line {
			out[i] = reEmitted
			changed = true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(out, "\n")
}

// sqlStringsAndComments lexes SQL just enough to distinguish code from
// protected regions: string literals, quoted identifiers, dollar-quoted
// bodies, and comments. Protected byte ranges are returned as intervals.
func sqlProtectedRanges(s string) [][2]int {
	var ranges [][2]int
	i := 0
	push := func(start, end int) {
		if end > start {
			ranges = append(ranges, [2]int{start, end})
		}
	}
	for i < len(s) {
		char := s[i]
		switch {
		case char == '\'' || char == '"' || char == '`':
			quote := char
			start := i
			i++
			for i < len(s) {
				if s[i] == quote {
					if i+1 < len(s) && s[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			push(start, i)
		case char == '$':
			if end := strings.IndexByte(s[i+1:], '$'); end >= 0 {
				tag := s[i : i+end+2]
				if close := strings.Index(s[i+len(tag):], tag); close >= 0 {
					start := i
					i = i + len(tag) + close + len(tag)
					push(start, i)
					continue
				}
				i++
				continue
			}
			i++
		case char == '-' && i+1 < len(s) && s[i+1] == '-':
			start := i
			for i < len(s) && s[i] != '\n' {
				i++
			}
			push(start, i)
		case char == '/' && i+1 < len(s) && s[i+1] == '*':
			start := i
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s)
			} else {
				i = i + 2 + end + 2
			}
			push(start, i)
		default:
			i++
		}
	}
	return ranges
}

// HasMinifiableSQL reports whether code (non-string, non-comment) regions
// contain whitespace runs longer than one space.
func HasMinifiableSQL(s string) bool {
	return MinifySQL(s) != s
}

// MinifySQL collapses whitespace runs outside of SQL string literals,
// quoted identifiers, dollar-quoted bodies, and comments. Whitespace inside
// those regions is part of the stored text and must survive untouched.
// Collapsed code whitespace does not change SQL semantics.
func MinifySQL(s string) string {
	ranges := sqlProtectedRanges(s)
	if len(ranges) == 0 {
		return collapseSQLWhitespace(s, nil)
	}
	var b strings.Builder
	b.Grow(len(s))
	copied := 0
	for _, r := range ranges {
		b.WriteString(collapseSQLWhitespace(s[copied:r[0]], ranges))
		b.WriteString(s[r[0]:r[1]])
		copied = r[1]
	}
	b.WriteString(collapseSQLWhitespace(s[copied:], ranges))
	return b.String()
}

func collapseSQLWhitespace(segment string, _ [][2]int) string {
	var b strings.Builder
	b.Grow(len(segment))
	inSpace := false
	for _, r := range segment {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			inSpace = true
			continue
		}
		if inSpace {
			b.WriteByte(' ')
			inSpace = false
		}
		b.WriteRune(r)
	}
	if inSpace {
		b.WriteByte(' ')
	}
	return b.String()
}

// xmlTagGap matches whitespace runs containing a newline between two tags —
// formatting indentation, not inline text.
var xmlTagGap = regexp.MustCompile(`>[ \t]*\n\s*<`)

// HasMinifiableXML reports whether the text contains newline indentation
// between XML tags.
func HasMinifiableXML(s string) bool {
	if !strings.Contains(s, "><") && !strings.Contains(s, ">") {
		return false
	}
	return xmlTagGap.MatchString(s)
}

// MinifyXML collapses newline indentation between XML tags into adjacent
// tags. Indentation between tags is insignificant in XML — parsers deliver
// only character data to applications — so the document value is unchanged.
// Inline text separated by single spaces is never matched.
func MinifyXML(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	return xmlTagGap.ReplaceAllString(s, "><")
}

var mdLink = regexp.MustCompile(`(!?)\[([^\]\[]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// mdFence splits text into fenced and code-free segments so link rewriting
// never touches code blocks.
func mdFenceSegments(s string) []struct {
	code bool
	text string
} {
	segments := strings.Split(s, "```")
	result := make([]struct {
		code bool
		text string
	}, len(segments))
	for i, segment := range segments {
		result[i] = struct {
			code bool
			text string
		}{code: i%2 == 1, text: segment}
	}
	return result
}

// HasDuplicatedMarkdownLinks reports whether a repeated inline link target
// exists outside code fences.
func HasDuplicatedMarkdownLinks(s string) bool {
	_, changed := foldMarkdownLinks(s)
	return changed
}

// FoldMarkdownLinks converts repeated inline markdown links (and images)
// with the same target URL into reference-style links with definitions at
// the end. Rendered markdown is identical; UnfoldMarkdownLinks restores the
// original bytes.
func FoldMarkdownLinks(s string) string {
	folded, _ := foldMarkdownLinks(s)
	return folded
}

// UnfoldMarkdownLinks reverses FoldMarkdownLinks byte-for-byte.
func UnfoldMarkdownLinks(s string) string {
	if !strings.Contains(s, "[md") {
		return s
	}
	definitions := regexp.MustCompile(`\n\[md(\d+)\]: ([^\n]+)`)
	var refs []struct{ ref, url string }
	rest := definitions.ReplaceAllStringFunc(s, func(line string) string {
		match := definitions.FindStringSubmatch(line)
		refs = append(refs, struct{ ref, url string }{ref: match[1], url: match[2]})
		return ""
	})
	for _, entry := range refs {
		pattern := regexp.MustCompile(`(!?)\[([^\]\[]*)\]\[md` + entry.ref + `\]`)
		rest = pattern.ReplaceAllString(rest, `$1[$2](`+entry.url+`)`)
	}
	return rest
}

func foldMarkdownLinks(s string) (string, bool) {
	segments := mdFenceSegments(s)

	type linkDef struct {
		ref, url string
	}
	var defs []linkDef
	refByURL := map[string]string{}
	changed := false

	for index, segment := range segments {
		if segment.code {
			continue
		}
		segment.text = mdLink.ReplaceAllStringFunc(segment.text, func(link string) string {
			match := mdLink.FindStringSubmatch(link)
			bang, text, url := match[1], match[2], match[3]
			ref, exists := refByURL[url]
			if !exists {
				refByURL[url] = ""
				return link
			}
			if ref == "" {
				// First repeat: promote the URL to a definition.
				ref = "md" + itoa(len(defs)+1)
				defs = append(defs, linkDef{ref: ref, url: url})
				refByURL[url] = ref
			}
			changed = true
			return bang + "[" + text + "][" + ref + "]"
		})
		segments[index].text = segment.text
	}

	if !changed {
		return s, false
	}
	var b strings.Builder
	for _, segment := range segments {
		b.WriteString(segment.text)
	}
	for _, def := range defs {
		b.WriteString("\n[" + def.ref + "]: " + def.url)
	}
	return b.String(), true
}
