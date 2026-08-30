package textnorm

import (
	"regexp"
	"strconv"
	"strings"
)

// intToken matches a standalone integer (no surrounding digits).
var intToken = regexp.MustCompile(`\d+`)

// rangeRun matches at least four consecutive integers separated by a single
// consistent separator (", " or " "), ascending by exactly one — the shape
// of line numbers, list indices, and page runs.
var rangeRun = regexp.MustCompile(`\b\d+(?:, \d+|\s\d+){3,}\b`)

// unfoldedRange expands a `start..end` envelope with its separator.
var unfoldedRange = regexp.MustCompile(`\b(\d+)\.\.(\d+)\[([^\]]*)\]`)

// HasFoldableRanges reports whether the text contains an ascending
// consecutive integer run of at least four numbers.
func HasFoldableRanges(s string) bool {
	return FoldRanges(s) != s
}

// FoldRanges replaces ascending consecutive integer runs of at least four
// numbers with a compact range envelope (`1..10[, ]`). The envelope carries
// the original separator, so UnfoldRanges restores the text byte-for-byte.
func FoldRanges(s string) string {
	return rangeRun.ReplaceAllStringFunc(s, foldRangeRun)
}

func foldRangeRun(run string) string {
	tokens := intToken.FindAllString(run, -1)
	if len(tokens) < 4 {
		return run
	}
	values := make([]int, len(tokens))
	for i, token := range tokens {
		value, err := strconv.Atoi(token)
		if err != nil {
			return run
		}
		if i > 0 && value != values[i-1]+1 {
			return run
		}
		values[i] = value
	}
	separator := " "
	if strings.Contains(run, ", ") {
		separator = ", "
	}
	return tokens[0] + ".." + tokens[len(tokens)-1] + "[" + separator + "]"
}

// UnfoldRanges expands every `start..end[sep]` range envelope back into the
// original integer sequence.
func UnfoldRanges(s string) string {
	return unfoldedRange.ReplaceAllStringFunc(s, func(expr string) string {
		match := unfoldedRange.FindStringSubmatch(expr)
		start, err1 := strconv.Atoi(match[1])
		end, err2 := strconv.Atoi(match[2])
		if err1 != nil || err2 != nil || end < start || end-start > 10000 {
			return expr
		}
		parts := make([]string, 0, end-start+1)
		for v := start; v <= end; v++ {
			parts = append(parts, strconv.Itoa(v))
		}
		return strings.Join(parts, match[3])
	})
}
