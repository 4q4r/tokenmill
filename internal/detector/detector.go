package detector

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Precompiled regex patterns shared across detectors.
var (
	logTimestampRegex = regexp.MustCompile(`\d{4}[-/]\d{2}[-/]\d{2}[ T]\d{2}:\d{2}:\d{2}|\d{4}[-/]\d{2}[-/]\d{2}|\d{2}:\d{2}:\d{2}(?:\.\d+)?`)
	logLevelRegex     = regexp.MustCompile(`(?i)\b(INFO|WARN|WARNING|ERROR|DEBUG|TRACE|FATAL|CRITICAL|NOTICE|SEVERE|ERR)\b`)

	stackTraceAtRegex = regexp.MustCompile(`at\s+.*\(.*:\d+\)`)
	stackTraceGoRegex = regexp.MustCompile(`goroutine\s+\d+\s+\[`)
	// Java/Kotlin style: at package.Class.method(File.java:123)
	stackTraceGenericRegex = regexp.MustCompile(`(?m)^\s*at\s+[\w.$]+\(.*:\d+\)`)

	pathRegex = regexp.MustCompile(`(?:/[\w.\-~]+){3,}`)

	shellCommandRegex  = regexp.MustCompile(`(?m)(?:^|&&|\|\||[;|]|\$\(|` + "`" + `)[ \t]*(?:bash|sh|zsh|fish|printf|echo|cat|cd|pwd|ls|git|docker|kubectl|make|go)(?:[ \t\r\n]|$)`)
	shellOperatorRegex = regexp.MustCompile("&&|\\|\\||;|\\||[<>]|\\$\\(|`")

	codeKeywordRegex = regexp.MustCompile(`\b(func|import|package|class|def|const|var|let|fn|struct|interface|type)\b`)

	boxDrawingChars = "┌┐└┘├┤┬┴┼─│━┃┏┓┗┛╔╗╚╝╠╣╦╩╬═║"

	tracebackFileRegex = regexp.MustCompile(`File ".*", line \d+`)
	panicRegex         = regexp.MustCompile(`(?m)panic:`)

	whitespaceSplitRegex = regexp.MustCompile(`\s{2,}`)
)

// IsJSON reports whether input is valid JSON (object, array, or primitive).
// Returns confidence 0.95 for object/array, 0.6 for primitive, 0 otherwise.
func IsJSON(s string) (bool, float64) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false, 0
	}
	var v interface{}
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return false, 0
	}
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return true, 0.95
	default:
		// primitives like "123", "true", "null" are valid JSON but likely not what caller means for codec selection
		// still return true with lower confidence
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return true, 0.95
		}
		return true, 0.6
	}
}

// IsJSONL reports whether input is JSONL (≥2 lines each valid JSON).
func IsJSONL(s string) (bool, float64) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < 2 {
		return false, 0
	}
	valid := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		var v interface{}
		if err := json.Unmarshal([]byte(t), &v); err == nil {
			valid++
		}
	}
	if valid >= 2 && valid == len(lines) {
		return true, 0.95
	}
	if valid >= 2 && float64(valid)/float64(len(lines)) >= 0.8 {
		return true, 0.85
	}
	if valid >= 2 {
		return true, 0.7
	}
	return false, 0
}

// IsTable reports whether input looks like a table.
// Detects box-drawing `┌─┬┐│` or ≥5 rows with regular columns.
func IsTable(s string) (bool, float64) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false, 0
	}
	// Box-drawing detection — high confidence
	for _, r := range boxDrawingChars {
		if strings.ContainsRune(s, r) {
			return true, 0.95
		}
	}
	// Also check for ASCII box using +---+ pattern with many dashes and plus
	if strings.Contains(s, "+---") && strings.Contains(s, "|") {
		return true, 0.85
	}

	lines := splitNonEmptyLines(s)
	if len(lines) < 3 {
		// need at least 3 for pipe/markdown, 5 for whitespace; early out if too few
		return false, 0
	}

	// Pipe-separated (markdown) tables: | col | col |
	pipeLines := 0
	for _, l := range lines {
		if strings.Contains(l, "|") {
			pipeLines++
		}
	}
	if pipeLines >= 3 {
		// check column consistency
		cols := -1
		consistent := 0
		for _, l := range lines {
			if !strings.Contains(l, "|") {
				continue
			}
			parts := strings.Split(l, "|")
			// filter empty strings from leading/trailing |
			cnt := 0
			for _, p := range parts {
				if strings.TrimSpace(p) != "" {
					cnt++
				}
			}
			// also count separator lines like |---|---|
			if strings.Contains(l, "---") {
				cnt = cols // treat separator as matching
				if cols == -1 {
					// separator before header; skip
					continue
				}
			}
			if cols == -1 {
				cols = cnt
			}
			if cnt == cols && cnt >= 2 {
				consistent++
			}
		}
		if consistent >= 3 && float64(consistent)/float64(pipeLines) >= 0.7 {
			return true, 0.85
		}
	}

	// TSV detection
	tsvLines := 0
	tsvCols := -1
	tsvConsistent := 0
	for _, l := range lines {
		if strings.Contains(l, "\t") {
			tsvLines++
			cnt := len(strings.Split(l, "\t"))
			if tsvCols == -1 {
				tsvCols = cnt
			}
			if cnt == tsvCols && cnt >= 2 {
				tsvConsistent++
			}
		}
	}
	if tsvLines >= 3 && float64(tsvConsistent)/float64(tsvLines) >= 0.8 {
		return true, 0.85
	}

	// Whitespace regular columns: ≥5 rows, same column count via Fields
	if len(lines) >= 5 {
		colCount := -1
		consistent := 0
		for _, l := range lines {
			fields := strings.Fields(l)
			if len(fields) < 2 {
				continue
			}
			// also try splitting on 2+ spaces as delimiter
			// If line has 2+ spaces separating columns, we respect that as column indicator
			splitCandidates := whitespaceSplitRegex.Split(strings.TrimSpace(l), -1)
			useFields := len(fields)
			useSplit := len(splitCandidates)
			// Choose the larger structural indicator but require >=2 columns
			c := useFields
			if useSplit >= 2 && useSplit <= useFields {
				c = useSplit
			}
			if colCount == -1 {
				colCount = c
			}
			if c == colCount {
				consistent++
			}
		}
		if consistent >= 5 && float64(consistent)/float64(len(lines)) >= 0.7 {
			return true, 0.75
		}
		// Alternate: check for fixed-width style where each line length similar and column positions regular?
		// Simple fallback: if we have ≥5 lines and average field count stable, report table.
		// We already did; return fallback.
		// Also consider 5 rows with regular column counts via Strings.Fields exactly equal
		exactCols := -1
		exactConsistent := 0
		for _, l := range lines {
			f := strings.Fields(l)
			if exactCols == -1 {
				exactCols = len(f)
			}
			if len(f) == exactCols && len(f) >= 2 {
				exactConsistent++
			}
		}
		if exactConsistent >= 5 && float64(exactConsistent)/float64(len(lines)) >= 0.8 {
			return true, 0.7
		}
	}

	return false, 0
}

// IsLog reports whether input looks like log lines (timestamp/level).
func IsLog(s string) (bool, float64) {
	if strings.TrimSpace(s) == "" {
		return false, 0
	}
	hasTime := logTimestampRegex.MatchString(s)
	hasLevel := logLevelRegex.MatchString(s)

	if hasTime && hasLevel {
		return true, 0.9
	}
	if hasTime {
		// timestamp alone could be log (e.g., 2024-01-02T15:04:05)
		// check if appears on multiple lines or with colon
		lines := strings.Split(s, "\n")
		count := 0
		for _, l := range lines {
			if logTimestampRegex.MatchString(l) {
				count++
			}
		}
		if count >= 2 {
			return true, 0.85
		}
		return true, 0.65
	}
	if hasLevel {
		lines := strings.Split(s, "\n")
		count := 0
		for _, l := range lines {
			if logLevelRegex.MatchString(l) {
				count++
			}
		}
		if count >= 2 {
			return true, 0.8
		}
		return true, 0.6
	}
	return false, 0
}

// IsStackTrace reports whether input contains stack trace patterns.
func IsStackTrace(s string) (bool, float64) {
	if stackTraceGoRegex.MatchString(s) {
		return true, 0.95
	}
	if stackTraceAtRegex.MatchString(s) {
		return true, 0.9
	}
	if stackTraceGenericRegex.MatchString(s) {
		return true, 0.9
	}
	// Additional heuristics: "Traceback", "panic:", "Exception" with lines containing file:line
	if strings.Contains(s, "Traceback") && tracebackFileRegex.MatchString(s) {
		return true, 0.85
	}
	if panicRegex.MatchString(s) && strings.Contains(s, "goroutine") {
		return true, 0.85
	}
	// Count occurrences of at ...(...:digit)
	count := len(stackTraceAtRegex.FindAllString(s, -1))
	if count >= 2 {
		return true, 0.85
	}
	if strings.Contains(strings.ToLower(s), "exception") && count >= 1 {
		return true, 0.7
	}
	return false, 0
}

// IsPathHeavy reports whether input contains many file paths (≥3 matches of `/(?:/[\w.-]+){3,}`).
func IsPathHeavy(s string) (bool, float64) {
	matches := pathRegex.FindAllString(s, -1)
	if len(matches) >= 3 {
		return true, 0.9
	}
	if len(matches) == 1 || len(matches) == 2 {
		return false, 0.3
	}
	return false, 0
}

// IsCodeBlock reports whether input is a code block (fence ``` or func|import|package heavy).
// Bypass: returns true → firewall (only dedup allowed).
func IsCodeBlock(s string) (bool, float64) {
	if strings.Contains(s, "```") {
		return true, 0.95
	}
	// fence variants
	if strings.Contains(s, "~~~") {
		return true, 0.9
	}
	if shell, confidence := IsShellCommand(s); shell {
		return true, confidence
	}
	// Check for markdown code block with indentation (4 spaces) on multiple lines? too generic; skip.

	keywords := codeKeywordRegex.FindAllString(s, -1)
	if len(keywords) >= 3 {
		return true, 0.9
	}
	if len(keywords) >= 2 {
		// also require braces or semicolon to be code-like
		if strings.Contains(s, "{") && strings.Contains(s, "}") {
			return true, 0.8
		}
		return true, 0.7
	}
	if len(keywords) == 1 {
		// A single programming keyword is not enough to classify prose as code.
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "package ") {
			fields := strings.Fields(trimmed)
			if len(fields) == 2 && !strings.HasSuffix(trimmed, ".") {
				return true, 0.75
			}
		}
		if strings.HasPrefix(trimmed, "import ") && (strings.Contains(trimmed, `"`) || strings.Contains(trimmed, "(") || strings.Contains(trimmed, "`")) {
			return true, 0.75
		}
		if strings.HasPrefix(trimmed, "func ") && (strings.Contains(trimmed, "(") || strings.Contains(trimmed, "{")) {
			return true, 0.75
		}
		return false, 0.2
	}
	// Heuristic: many semicolons + braces may indicate code
	if strings.Count(s, ";") >= 3 && strings.Contains(s, "{") {
		return true, 0.6
	}
	return false, 0
}

// IsShellCommand reports whether input has a conservative shell-command
// signal. Known executable names must occur at the beginning of the input or
// immediately after a shell command separator; arbitrary prose containing a
// programming keyword is not enough.
func IsShellCommand(s string) (bool, float64) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || !shellCommandRegex.MatchString(trimmed) {
		return false, 0
	}
	if shellOperatorRegex.MatchString(trimmed) {
		return true, 0.98
	}
	return true, 0.92
}

// IsHomogeneousJSONArray reports whether input is a homogeneous JSON array
// (object array with uniform keys). Returns confidence based on uniformity.
func IsHomogeneousJSONArray(s string) (bool, float64) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false, 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return false, 0
	}
	if len(arr) < 2 {
		return false, 0
	}
	// Need to check each element is an object and keys are uniform
	type keySet = map[string]struct{}
	var firstKeys []string
	firstObj := make(map[string]interface{})
	if err := json.Unmarshal(arr[0], &firstObj); err != nil {
		return false, 0
	}
	for k := range firstObj {
		firstKeys = append(firstKeys, k)
	}
	sort.Strings(firstKeys)
	if len(firstKeys) == 0 {
		return false, 0
	}
	uniformCount := 1
	for i := 1; i < len(arr); i++ {
		obj := make(map[string]interface{})
		if err := json.Unmarshal(arr[i], &obj); err != nil {
			return false, 0
		}
		if len(obj) != len(firstKeys) {
			return false, 0.3 // not uniform length
		}
		var keys []string
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		match := true
		for j, k := range keys {
			if k != firstKeys[j] {
				match = false
				break
			}
		}
		if !match {
			return false, 0.4
		}
		uniformCount++
	}
	if uniformCount == len(arr) {
		// check also value types homogeneity? optional high confidence
		return true, 0.95
	}
	if float64(uniformCount)/float64(len(arr)) >= 0.8 {
		return true, 0.8
	}
	return false, 0
}

func splitNonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	var out []string
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
