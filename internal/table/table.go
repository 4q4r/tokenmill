package table

import (
	"errors"
	"math"
	"regexp"
	"strings"
)

const boxChars = "┌┐└┘├┤┬┴┼─│━┃┏┓┗┛╔╗╚╝╠╣╦╩╬═║"

var ws2Regex = regexp.MustCompile(`\s{2,}`)

// splitNonEmptyLines splits by \n and returns non-empty trimmed lines count.
func splitLines(input string) []string {
	// Preserve trailing newline handling: strings.Split
	parts := strings.Split(input, "\n")
	return parts
}

func nonEmptyLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func containsBoxDrawing(s string) bool {
	for _, r := range boxChars {
		if strings.ContainsRune(s, r) {
			return true
		}
	}
	return false
}

// DetectTable reports whether input looks like a table.
// Requirement: ≥5 rows + regular columns (box-drawing ┌─┬┐│ or fixed-width with ≥3 cols where ≥70% consistent)
func DetectTable(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	lines := nonEmptyLines(splitLines(input))
	if len(lines) < 5 {
		return false
	}
	// Box-drawing detection: require parseBoxRow >=2 cols and 70% consistency as for fixed
	if containsBoxDrawing(input) {
		var boxCounts []int
		for _, l := range lines {
			cells := parseBoxRow(l)
			if cells != nil {
				boxCounts = append(boxCounts, len(cells))
			}
		}
		if len(boxCounts) >= 5 {
			freqBox := make(map[int]int)
			for _, c := range boxCounts {
				if c >= 2 {
					freqBox[c]++
				}
			}
			if len(freqBox) > 0 {
				maxCntBox := 0
				maxFreqBox := 0
				for col, f := range freqBox {
					if f > maxFreqBox {
						maxFreqBox = f
						maxCntBox = col
					}
				}
				// count consistent
				consistentBox := 0
				for _, c := range boxCounts {
					if c == maxCntBox {
						consistentBox++
					}
				}
				needBox := int(math.Ceil(0.7 * float64(len(boxCounts))))
				if maxCntBox >= 2 && consistentBox >= needBox {
					return true
				}
			}
		}
		// If box chars present but not enough consistent box rows, not a table
		// Check if there are any box content rows; if so, fail fast (don't fall through to fixed)
		// But if input contains stray box char among fixed table, fall through? For now return false when boxCounts non-empty
		if len(boxCounts) > 0 {
			return false
		}
	}
	// ASCII box fallback: +---+ and |
	if strings.Contains(input, "+---") && strings.Contains(input, "|") {
		return true
	}
	// Fixed-width with ≥3 cols where ≥70% consistent
	// Use split by \s{2,}
	counts := make([]int, 0, len(lines))
	for _, l := range lines {
		cells := parseFixedWidthRow(l)
		if len(cells) < 3 {
			// not enough cols, count as 0 or skip?
			// For consistency, we count only rows that have >=3 cols?
			// But spec says regular columns where ≥70% rows consistently have same col count >=3.
			// So we should count all rows, but only those with >=3 are considered consistent?
			// We'll count per line cols via ws2Regex, but need to decide.
			counts = append(counts, len(cells))
			continue
		}
		counts = append(counts, len(cells))
	}
	if len(counts) < 5 {
		return false
	}
	// Find most frequent column count >=3
	freq := make(map[int]int)
	for _, c := range counts {
		if c >= 3 {
			freq[c]++
		}
	}
	if len(freq) == 0 {
		return false
	}
	maxCnt := 0
	maxFreq := 0
	for col, f := range freq {
		if f > maxFreq {
			maxFreq = f
			maxCnt = col
		}
	}
	consistent := 0
	for _, c := range counts {
		if c == maxCnt {
			consistent++
		}
	}
	need := int(math.Ceil(0.7 * float64(len(lines))))
	if consistent >= need {
		return true
	}
	return false
}

func parseFixedWidthRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	parts := ws2Regex.Split(trimmed, -1)
	var cells []string
	for _, p := range parts {
		c := strings.TrimSpace(p)
		if c != "" {
			cells = append(cells, c)
		}
	}
	// If split produced 1 part but line has many single spaces, it will be 1 col => not table
	return cells
}

func parseBoxRow(line string) []string {
	// If line contains "│", extract cells between
	if strings.Contains(line, "│") {
		parts := strings.Split(line, "│")
		start, end := 0, len(parts)
		if end > start && strings.TrimSpace(parts[start]) == "" {
			start++
		}
		if end > start && strings.TrimSpace(parts[end-1]) == "" {
			end--
		}
		if start >= end {
			return nil
		}
		cells := make([]string, 0, end-start)
		for _, p := range parts[start:end] {
			// Keep empty interior cells: their position is part of the table data.
			cells = append(cells, strings.TrimSpace(p))
		}
		return cells
	}
	// Border lines like ┌─────┬─────┐ or ├─────┼─────┤ without │ -> skip
	return nil
}

func parsePipeRow(line string) []string {
	if !strings.Contains(line, "|") {
		return nil
	}
	trimmed := strings.TrimSpace(line)
	// Border detection for pipe tables: lines that are only pipes, dashes, plus, spaces
	// e.g. "|---|---|", "+---+---+"
	isBorder := true
	hasDash := false
	for _, r := range trimmed {
		if r == '-' {
			hasDash = true
			continue
		}
		if r == '|' || r == '+' || r == ' ' {
			continue
		}
		isBorder = false
		break
	}
	if isBorder && hasDash {
		return nil
	}
	// Also if line contains "---" and all pipe-separated cells are dashes
	if strings.Contains(trimmed, "---") {
		parts := strings.Split(trimmed, "|")
		allDash := true
		foundDashCell := false
		for _, p := range parts {
			c := strings.TrimSpace(p)
			if c == "" {
				continue
			}
			foundDashCell = true
			for _, r := range c {
				if r != '-' && r != '+' {
					allDash = false
					break
				}
			}
			if !allDash {
				break
			}
		}
		if foundDashCell && allDash {
			return nil
		}
	}
	parts := strings.Split(line, "|")
	start, end := 0, len(parts)
	if end > start && strings.TrimSpace(parts[start]) == "" {
		start++
	}
	if end > start && strings.TrimSpace(parts[end-1]) == "" {
		end--
	}
	var cells []string
	for _, p := range parts[start:end] {
		c := strings.TrimSpace(p)
		// skip dash-only cells that slipped through
		isDashCell := true
		for _, r := range c {
			if r != '-' && r != '+' {
				isDashCell = false
				break
			}
		}
		if isDashCell && strings.Contains(c, "-") {
			continue
		}
		cells = append(cells, c)
	}
	if len(cells) == 0 {
		return nil
	}
	return cells
}

func parseRow(line string) []string {
	if containsBoxDrawing(line) {
		return parseBoxRow(line)
	}
	if strings.Contains(line, "|") {
		return parsePipeRow(line)
	}
	if strings.Contains(line, "+---") {
		return nil
	}
	return parseFixedWidthRow(line)
}

// TableToTSV converts a fixed-width or box-drawing table to TSV.
// Split by \s{2,} → join \t, PSV fallback if \t in cell → |.
func TableToTSV(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("empty input")
	}
	if !DetectTable(input) {
		// Still try to convert? But spec says DetectTable is gate, so error if not table
		return "", errors.New("not a table")
	}
	lines := splitLines(input)
	var rows [][]string
	hasTabInCell := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := parseRow(line)
		if cells == nil {
			// Could be border line -> skip
			continue
		}
		if len(cells) == 0 {
			continue
		}
		for _, c := range cells {
			if strings.Contains(c, "\t") {
				hasTabInCell = true
			}
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return "", errors.New("no data rows parsed")
	}
	// Determine delimiter
	delim := "\t"
	if hasTabInCell {
		delim = "|"
	}
	// Check if any cell contains delim after fallback? If delim is "|" and cell contains "|", we could need escaping but spec says PSV fallback if \t in cell -> |. It doesn't handle | in cell. We'll assume no further fallback.
	// Build TSV
	var sb strings.Builder
	for i, row := range rows {
		for j, cell := range row {
			if j > 0 {
				sb.WriteString(delim)
			}
			sb.WriteString(cell)
		}
		if i < len(rows)-1 {
			sb.WriteString("\n")
		}
	}
	// Preserve trailing newline if original had it? Not required but we can add if original ends with \n
	if strings.HasSuffix(input, "\n") {
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// IsTSV reports whether input looks like TSV (or PSV fallback).
func IsTSV(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	lines := nonEmptyLines(splitLines(input))
	if len(lines) < 2 {
		return false
	}
	hasTab := strings.Contains(input, "\t")
	hasPipe := strings.Contains(input, "|")
	if !hasTab && !hasPipe {
		return false
	}
	// Evaluate both delimiters, pick best consistency
	bestConsistent := 0
	bestCnt := 0
	bestTotal := len(lines)
	for _, delim := range []string{"\t", "|"} {
		if delim == "\t" && !hasTab {
			continue
		}
		if delim == "|" && !hasPipe {
			continue
		}
		colCounts := make([]int, 0, len(lines))
		for _, l := range lines {
			cnt := len(strings.Split(l, delim))
			colCounts = append(colCounts, cnt)
		}
		freq := make(map[int]int)
		for _, c := range colCounts {
			freq[c]++
		}
		maxCnt := 0
		maxFreq := 0
		for c, f := range freq {
			if f > maxFreq {
				maxFreq = f
				maxCnt = c
			}
		}
		if maxCnt < 2 {
			continue
		}
		consistent := 0
		for _, c := range colCounts {
			if c == maxCnt {
				consistent++
			}
		}
		// Keep best
		if consistent > bestConsistent {
			bestConsistent = consistent
			bestCnt = maxCnt
		}
		// Also if equal, prefer larger cnt?
		_ = bestTotal
		_ = bestCnt
	}
	if bestConsistent == 0 {
		return false
	}
	return bestConsistent >= 2 && float64(bestConsistent)/float64(len(lines)) >= 0.7
}

// VerifyTable checks that TSV cells equal original table cells (cells equality).
// It parses both inputs into 2D cells and compares deep equality.
func VerifyTable(original, tsv string) bool {
	if strings.TrimSpace(original) == "" && strings.TrimSpace(tsv) == "" {
		return true
	}
	if strings.TrimSpace(original) == "" || strings.TrimSpace(tsv) == "" {
		return false
	}
	// Parse original table rows
	origRows := parseTableCells(original)
	if len(origRows) == 0 {
		return false
	}
	// Parse TSV rows
	tsvRows := parseTSVCells(tsv)
	if len(tsvRows) == 0 {
		return false
	}
	if len(origRows) != len(tsvRows) {
		return false
	}
	for i := range origRows {
		if len(origRows[i]) != len(tsvRows[i]) {
			return false
		}
		for j := range origRows[i] {
			if origRows[i][j] != tsvRows[i][j] {
				return false
			}
		}
	}
	return true
}

func parseTableCells(input string) [][]string {
	lines := splitLines(input)
	var rows [][]string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := parseRow(line)
		if cells == nil {
			continue
		}
		if len(cells) == 0 {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

func detectDelim(input string) string {
	hasTab := strings.Contains(input, "\t")
	hasPipe := strings.Contains(input, "|")
	if !hasTab && !hasPipe {
		return ""
	}
	lines := nonEmptyLines(splitLines(input))
	bestDelim := ""
	bestScore := -1
	bestConsistent := 0
	for _, delim := range []string{"\t", "|"} {
		if delim == "\t" && !hasTab {
			continue
		}
		if delim == "|" && !hasPipe {
			continue
		}
		colCounts := make([]int, 0, len(lines))
		for _, l := range lines {
			cnt := len(strings.Split(l, delim))
			colCounts = append(colCounts, cnt)
		}
		freq := make(map[int]int)
		for _, c := range colCounts {
			freq[c]++
		}
		maxCnt := 0
		maxFreq := 0
		for c, f := range freq {
			if f > maxFreq {
				maxFreq = f
				maxCnt = c
			}
		}
		if maxCnt < 2 {
			continue
		}
		consistent := 0
		for _, c := range colCounts {
			if c == maxCnt {
				consistent++
			}
		}
		score := consistent*100 + maxCnt // weighted
		if score > bestScore {
			bestScore = score
			bestDelim = delim
			bestConsistent = consistent
		}
		_ = bestConsistent
	}
	return bestDelim
}

func parseTSVCells(input string) [][]string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	lines := nonEmptyLines(splitLines(input))
	if len(lines) == 0 {
		return nil
	}
	delim := detectDelim(input)
	if delim == "" {
		var rows [][]string
		for _, l := range lines {
			rows = append(rows, []string{strings.TrimSpace(l)})
		}
		return rows
	}
	var rows [][]string
	for _, l := range lines {
		parts := strings.Split(l, delim)
		var cells []string
		for _, p := range parts {
			cells = append(cells, strings.TrimSpace(p))
		}
		rows = append(rows, cells)
	}
	return rows
}
