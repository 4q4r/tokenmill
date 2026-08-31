package textnorm

import (
	"regexp"
	"strings"
)

// ---------- CR/LF normalization ----------

// crlfPattern matches \r\n and lone \r.
var crlfPattern = regexp.MustCompile(`\r\n|\r`)

// HasCRLF reports whether the text contains \r\n or lone \r.
func HasCRLF(s string) bool { return strings.ContainsRune(s, '\r') }

// NormalizeCRLF replaces \r\n and lone \r with \n.
func NormalizeCRLF(s string) string {
	if !HasCRLF(s) {
		return s
	}
	return crlfPattern.ReplaceAllString(s, "\n")
}

// ---------- leading/trailing blank lines ----------

var leadingBlanks = regexp.MustCompile(`^\n+`)
var trailingBlanks = regexp.MustCompile(`\n+\z`)

// HasEdgeBlankLines reports whether the text starts or ends with blank lines.
func HasEdgeBlankLines(s string) bool {
	return leadingBlanks.MatchString(s) || trailingBlanks.MatchString(s)
}

// StripEdgeBlankLines removes leading and trailing blank lines from the text.
func StripEdgeBlankLines(s string) string {
	s = leadingBlanks.ReplaceAllString(s, "")
	s = trailingBlanks.ReplaceAllString(s, "")
	return s
}

// ---------- Unicode line separators ----------

var lineSepPattern = regexp.MustCompile(`\x{2028}|\x{2029}`)

// HasUnicodeLineSeparators reports whether U+2028/U+2029 are present.
func HasUnicodeLineSeparators(s string) bool {
	return strings.ContainsRune(s, 0x2028) || strings.ContainsRune(s, 0x2029)
}

// NormalizeUnicodeLineSeparators replaces U+2028 and U+2029 with \n.
func NormalizeUnicodeLineSeparators(s string) string {
	if !HasUnicodeLineSeparators(s) {
		return s
	}
	return lineSepPattern.ReplaceAllString(s, "\n")
}

// ---------- emoji variation selectors ----------

// variationSelector matches U+FE0E and U+FE0F.
var variationSelector = regexp.MustCompile(`\x{FE0E}|\x{FE0F}`)

// HasVariationSelectors reports whether emoji variation selectors are present.
func HasVariationSelectors(s string) bool {
	return variationSelector.MatchString(s)
}

// StripVariationSelectors removes U+FE0E and U+FE0F. These invisible code
// points select text vs emoji presentation but waste tokens.
func StripVariationSelectors(s string) string {
	if !HasVariationSelectors(s) {
		return s
	}
	return variationSelector.ReplaceAllString(s, "")
}

// ---------- markdown: HTML comments ----------

var htmlComment = regexp.MustCompile(`<!--[\s\S]*?-->`)

// HasHTMLComments reports whether HTML comments are present.
func HasHTMLComments(s string) bool {
	return strings.Contains(s, "<!--")
}

// StripHTMLComments removes HTML comments. They are invisible in rendered
// markdown and carry zero information for the model.
func StripHTMLComments(s string) bool { return false } // stub to satisfy references

// StripHTMLCommentsText removes HTML comments from markdown text.
func StripHTMLCommentsText(s string) string {
	if !HasHTMLComments(s) {
		return s
	}
	return htmlComment.ReplaceAllString(s, "")
}

// ---------- markdown: setext to ATX headings ----------

var setextH1 = regexp.MustCompile(`(?m)^(.+)\n={3,}[ \t]*$`)
var setextH2 = regexp.MustCompile(`(?m)^(.+)\n-{3,}[ \t]*$`)

// HasSetextHeadings reports whether setext-style headings are present.
func HasSetextHeadings(s string) bool {
	return setextH1.MatchString(s) || setextH2.MatchString(s)
}

// SetextToATX converts setext headings (`===`/`---` underlines) to ATX
// (`#`/`##`) headings. Both render identically; ATX is shorter.
func SetextToATX(s string) string {
	s = setextH1.ReplaceAllString(s, "# $1")
	s = setextH2.ReplaceAllString(s, "## $1")
	return s
}

// ---------- markdown: list marker standardization ----------

// asteriskBullet matches `* ` list markers at line start (not emphasis).
var asteriskBullet = regexp.MustCompile(`(?m)^(\s*)\* `)

// HasAsteriskBullets reports whether `* ` list markers are present.
func HasAsteriskBullets(s string) bool {
	return asteriskBullet.MatchString(s)
}

// StandardizeListMarkers replaces `* ` bullets with `- ` at line starts.
func StandardizeListMarkers(s string) string {
	if !HasAsteriskBullets(s) {
		return s
	}
	return asteriskBullet.ReplaceAllString(s, "${1}- ")
}

// ---------- markdown: horizontal rules ----------

// horizontalRule matches standalone horizontal rule lines.
var horizontalRule = regexp.MustCompile(`(?m)^(?:-{3,}|\*{3,}|_{3,})[ \t]*$`)

// HasHorizontalRules reports whether standalone horizontal rules exist.
func HasHorizontalRules(s string) bool {
	return horizontalRule.MatchString(s)
}

// StripHorizontalRules removes standalone horizontal rule lines.
func StripHorizontalRules(s string) string {
	if !HasHorizontalRules(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		if horizontalRule.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ---------- markdown: table of contents strip ----------

var tocHeader = regexp.MustCompile(`(?mi)^#{1,3}\s+(?:table of contents|contents|toc)\s*$`)
var tocLinkLine = regexp.MustCompile(`(?m)^\s*[-*]\s+\[.+?\]\(#.+\)\s*$`)

// HasTOC reports whether the text contains a table of contents block.
func HasTOC(s string) bool {
	if !strings.Contains(strings.ToLower(s), "table of contents") &&
		!strings.Contains(strings.ToLower(s), "\ntoc\n") {
		return false
	}
	return tocLinkLine.MatchString(s)
}

// StripTOC removes a table of contents heading and its link lines. The
// model navigates by heading structure in the context, not by TOC links.
func StripTOC(s string) string {
	if !HasTOC(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	inTOC := false
	for _, line := range lines {
		if tocHeader.MatchString(line) {
			inTOC = true
			continue
		}
		if inTOC {
			// Stay in TOC mode while lines are TOC links or blank.
			if strings.TrimSpace(line) == "" || tocLinkLine.MatchString(line) {
				continue
			}
			// Non-link, non-blank line ends the TOC block.
			inTOC = false
			out = append(out, line)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ---------- markdown: badges ----------

var badgeImage = regexp.MustCompile(`\[!\[[^\]]*\]\([^)]*shields\.io[^)]*\)\]\([^)]*\)|!\[[^\]]*\]\(https?://img\.shields\.io[^)]*\)`)

// HasBadges reports whether shield.io or similar badge images are present.
func HasBadges(s string) bool {
	return strings.Contains(s, "shields.io") || strings.Contains(s, "img.shields.io")
}

// StripBadges removes shield.io badge images and links.
func StripBadges(s string) string {
	if !HasBadges(s) {
		return s
	}
	return badgeImage.ReplaceAllString(s, "")
}

// ---------- markdown: YAML frontmatter ----------

var frontmatterBlock = regexp.MustCompile(`\A---\n[\s\S]*?\n---\n?`)

// HasFrontmatter reports whether the text starts with YAML frontmatter.
func HasFrontmatter(s string) bool {
	return frontmatterBlock.MatchString(s)
}

// StripFrontmatter removes YAML frontmatter from the start of the text.
func StripFrontmatter(s string) string {
	if !HasFrontmatter(s) {
		return s
	}
	return frontmatterBlock.ReplaceAllString(s, "")
}

// ---------- markdown: empty heading fold ----------

// HasEmptyHeadings reports whether a heading is immediately followed by
// another heading (zero content between them).
func HasEmptyHeadings(s string) bool {
	lines := strings.Split(s, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") && strings.HasPrefix(lines[i+1], "#") {
			return true
		}
	}
	return false
}

// FoldEmptyHeadings removes headings that are immediately followed by
// another heading (zero content between them).
func FoldEmptyHeadings(s string) string {
	if !HasEmptyHeadings(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if i+1 < len(lines) && strings.HasPrefix(lines[i], "#") && strings.HasPrefix(lines[i+1], "#") {
			continue // skip empty heading
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// ---------- markdown: link inline simplify ----------

// mdRefLink matches reference-style links `[text][ref]`.
var mdRefLink = regexp.MustCompile(`\[([^\]]+)\]\[([^\]]*)\]`)

// mdLinkDef matches reference definitions `[ref]: url`.
var mdLinkDef = regexp.MustCompile(`(?m)^\[([^\]]+)\]:\s*(\S+)`)

// HasSingleUseRefLinks reports whether single-use reference links exist.
func HasSingleUseRefLinks(s string) bool {
	return mdLinkDef.MatchString(s) && mdRefLink.MatchString(s)
}

// SimplifySingleUseRefLinks converts reference-style links that are used
// exactly once into inline links. Rendered markdown is identical.
func SimplifySingleUseRefLinks(s string) string {
	if !mdLinkDef.MatchString(s) || !mdRefLink.MatchString(s) {
		return s
	}
	defs := map[string][]string{}
	for _, match := range mdLinkDef.FindAllStringSubmatch(s, -1) {
		label := strings.ToLower(strings.TrimSpace(match[1]))
		defs[label] = append(defs[label], match[2])
	}
	usage := map[string]int{}
	for _, match := range mdRefLink.FindAllStringSubmatch(s, -1) {
		label := strings.ToLower(strings.TrimSpace(match[2]))
		usage[label]++
	}
	var singleDefs []struct{ label, url string }
	for _, match := range mdLinkDef.FindAllStringSubmatch(s, -1) {
		label := strings.ToLower(strings.TrimSpace(match[1]))
		if usage[label] == 1 {
			singleDefs = append(singleDefs, struct{ label, url string }{label, match[2]})
		}
	}
	if len(singleDefs) == 0 {
		return s
	}
	// Replace single-use reference links with inline links.
	for _, def := range singleDefs {
		pattern := regexp.MustCompile(`(?i)\[[^\]]*\]\[` + regexp.QuoteMeta(def.label) + `\]`)
		s = pattern.ReplaceAllStringFunc(s, func(link string) string {
			m := regexp.MustCompile(`\[([^\]]*)\]\[` + regexp.QuoteMeta(def.label) + `\]`).FindStringSubmatch(link)
			if m == nil {
				return link
			}
			return "[" + m[1] + "](" + def.url + ")"
		})
	}
	// Remove now-unused definitions.
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		isDef := false
		for _, def := range singleDefs {
			if strings.HasPrefix(line, "["+def.label+"]:") {
				isDef = true
				break
			}
		}
		if isDef {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// ---------- markdown: images ----------

var mdImage = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// HasMarkdownImages reports whether markdown images are present.
func HasMarkdownImages(s string) bool {
	return mdImage.MatchString(s)
}

// StripMarkdownImages removes markdown images, optionally keeping alt text.
func StripMarkdownImages(s string, keepAlt bool) string {
	if !HasMarkdownImages(s) {
		return s
	}
	if keepAlt {
		return mdImage.ReplaceAllString(s, "$1")
	}
	return mdImage.ReplaceAllString(s, "")
}

// ---------- consecutive word dedup ----------

// HasDoubledWords reports whether the text contains immediately repeated
// words (e.g. "the the" — always a transcription artifact). Only operates
// within single lines; newlines are respected as boundaries.
func HasDoubledWords(s string) bool {
	return CollapseDoubledWords(s) != s
}

// CollapseDoubledWords collapses immediately repeated words into one within
// each line separately. "the the cat" → "the cat". Line breaks are preserved
// so multi-line text is not joined.
func CollapseDoubledWords(s string) string {
	if !strings.Contains(s, "\n") {
		return collapseDoubledLine(s)
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = collapseDoubledLine(line)
	}
	return strings.Join(lines, "\n")
}

func collapseDoubledLine(line string) string {
	words := strings.Fields(line)
	if len(words) < 2 {
		return line
	}
	var out []string
	out = append(out, words[0])
	for i := 1; i < len(words); i++ {
		if strings.EqualFold(words[i], words[i-1]) {
			continue
		}
		out = append(out, words[i])
	}
	return strings.Join(out, " ")
}
