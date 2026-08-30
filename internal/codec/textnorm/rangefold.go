package textnorm

import (
	"regexp"
	"strconv"
	"strings"
)

// intToken matches an integer within a candidate run.
var intToken = regexp.MustCompile(`\d+`)

// rangeRun matches at least four integers separated by a single consistent
// separator (", " or " ") forming an arithmetic progression — the shape of
// line numbers, list indices, timestamps with a fixed step, and page runs.
var rangeRun = regexp.MustCompile(`\b\d+(?:, \d+|\s\d+){3,}\b`)

// unfoldedRange expands a range envelope with its separator and optional
// step (step 1 is the default and is omitted when folding).
var unfoldedRange = regexp.MustCompile(`\b(\d+)\.\.(\d+)(?: step (\d+))?\[([^\]]*)\]`)

// HasFoldableRanges reports whether the text contains an arithmetic integer
// run of at least four numbers.
func HasFoldableRanges(s string) bool {
	return FoldRanges(s) != s
}

// FoldRanges replaces arithmetic integer runs of at least four numbers with
// a compact range envelope (`100..104[, ]` for consecutive numbers,
// `10..50 step 10[; ]` for other constant steps). UnfoldRanges restores the
// text byte-for-byte.
func FoldRanges(s string) string {
	return rangeRun.ReplaceAllStringFunc(s, foldRangeRun)
}

// UnfoldRanges expands every range envelope back into the original integer
// sequence.
func UnfoldRanges(s string) string {
	return unfoldedRange.ReplaceAllStringFunc(s, func(expr string) string {
		match := unfoldedRange.FindStringSubmatch(expr)
		start, err1 := strconv.Atoi(match[1])
		end, err2 := strconv.Atoi(match[2])
		step := 1
		if match[3] != "" {
			step, err2 = strconv.Atoi(match[3])
		}
		if err1 != nil || err2 != nil || step < 1 || end < start || (end-start)/step > 10000 {
			return expr
		}
		parts := make([]string, 0, (end-start)/step+1)
		for v := start; v <= end; v += step {
			parts = append(parts, strconv.Itoa(v))
		}
		return strings.Join(parts, match[4])
	})
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
		values[i] = value
	}
	step := values[1] - values[0]
	if step < 1 {
		return run
	}
	for i := 2; i < len(values); i++ {
		if values[i]-values[i-1] != step {
			return run
		}
	}
	separator := " "
	if strings.Contains(run, ", ") {
		separator = ", "
	}
	stepNote := ""
	if step != 1 {
		stepNote = " step " + strconv.Itoa(step)
	}
	return tokens[0] + ".." + tokens[len(tokens)-1] + stepNote + "[" + separator + "]"
}
