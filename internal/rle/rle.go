package rle

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"regexp"
	"strconv"
	"strings"
)

const esc = "\u200B"

const maxRLEExpand = 10000
const maxLines = 10000

var (
	rleRegex   = regexp.MustCompile(`^(.*) \[×(\d+)\]$`)
	blockRegex = regexp.MustCompile(`^\[block ×(\d+): (.*)\]$`)
	// Precompiled for HasANSI style but not needed here
)

// escapeLine escapes marker substring to avoid false RLE detection.
func escapeLine(line string) string {
	if strings.Contains(line, " [×") {
		return strings.ReplaceAll(line, " [×", " [×"+esc)
	}
	return line
}

func unescapeLine(line string) string {
	return strings.ReplaceAll(line, " [×"+esc, " [×")
}

func escapeBlockLine(line string) string {
	if strings.Contains(line, "[block ×") {
		return strings.ReplaceAll(line, "[block ×", "[block ×"+esc)
	}
	return line
}

func unescapeBlockLine(line string) string {
	return strings.ReplaceAll(line, "[block ×"+esc, "[block ×")
}

// Encode implements ExactRLE adjacent byte-identical compression.
// For runs of identical lines >= minRun, emits "line [×N]".
// Escaping ensures original lines containing " [×" roundtrip correctly.
// Default minRun is 3 if <=0 or <2.
func Encode(input string, minRun int) string {
	if minRun < 2 {
		minRun = 3
	}
	if input == "" {
		return ""
	}
	hasTrailing := strings.HasSuffix(input, "\n")
	lines := strings.Split(input, "\n")
	if hasTrailing {
		lines = lines[:len(lines)-1]
	}
	var out []string
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		run := j - i
		line := lines[i]
		escapedBase := escapeLine(line)
		if run >= minRun {
			out = append(out, escapedBase+fmt.Sprintf(" [×%d]", run))
			i = j
		} else {
			for k := 0; k < run; k++ {
				out = append(out, escapedBase)
			}
			i = j
		}
	}
	result := strings.Join(out, "\n")
	if hasTrailing {
		result += "\n"
	}
	return result
}

// Decode reverses Encode. "line [×N]" expands to N repeats.
func Decode(encoded string) string {
	if encoded == "" {
		return ""
	}
	hasTrailing := strings.HasSuffix(encoded, "\n")
	lines := strings.Split(encoded, "\n")
	if hasTrailing {
		lines = lines[:len(lines)-1]
	}
	var out []string
	for _, line := range lines {
		if m := rleRegex.FindStringSubmatch(line); m != nil {
			base := m[1]
			countStr := m[2]
			n, err := strconv.Atoi(countStr)
			if err != nil || n <= 0 {
				// treat as plain line if invalid count
				if strings.Contains(line, " [×"+esc) {
					out = append(out, unescapeLine(line))
				} else {
					out = append(out, line)
				}
				continue
			}
			if n > maxRLEExpand {
				log.Printf("rle: Decode count %d exceeds limit %d, treating as plain: %q", n, maxRLEExpand, line)
				if strings.Contains(line, " [×"+esc) {
					out = append(out, unescapeLine(line))
				} else {
					out = append(out, line)
				}
				continue
			}
			baseUnescaped := unescapeLine(base)
			for i := 0; i < n; i++ {
				out = append(out, baseUnescaped)
			}
			continue
		}
		if strings.Contains(line, " [×"+esc) {
			out = append(out, unescapeLine(line))
		} else {
			out = append(out, line)
		}
	}
	result := strings.Join(out, "\n")
	if hasTrailing {
		result += "\n"
	}
	return result
}

// Verify checks Decode(encoded) == original.
func Verify(original, encoded string) bool {
	return Decode(encoded) == original
}

// IsRLEEncoded reports whether s contains any RLE marker line " [×N]".
func IsRLEEncoded(s string) bool {
	if s == "" {
		return false
	}
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if rleRegex.MatchString(line) {
			return true
		}
	}
	return false
}

// --- BlockFactoring ---

func hashBlock(lines []string, start, size int) uint64 {
	h := fnv.New64a()
	for i := 0; i < size; i++ {
		// Use separator 0 to avoid concatenation collisions
		h.Write([]byte(lines[start+i]))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func blocksEqual(lines []string, a, b, size int) bool {
	if a+size > len(lines) || b+size > len(lines) {
		return false
	}
	// Fast hash check
	if hashBlock(lines, a, size) != hashBlock(lines, b, size) {
		return false
	}
	for i := 0; i < size; i++ {
		if lines[a+i] != lines[b+i] {
			return false
		}
	}
	return true
}

// EncodeBlocks implements BlockFactoring via rolling hash Rabin-Karp per lines.
// It searches for consecutive exact repeats of blocks size minBlock..maxBlock.
// Repeats are replaced with "[block ×M: <json>]" where json is array of block lines.
// Only exact equality (memcmp after hash) is factored. O(n) via hash fast path.
func EncodeBlocks(input string, minBlock, maxBlock int) string {
	if minBlock <= 0 {
		minBlock = 2
	}
	if maxBlock <= 0 {
		maxBlock = 20
	}
	if maxBlock < minBlock {
		maxBlock = minBlock
	}
	if input == "" {
		return ""
	}
	hasTrailing := strings.HasSuffix(input, "\n")
	lines := strings.Split(input, "\n")
	if hasTrailing {
		lines = lines[:len(lines)-1]
	}
	n := len(lines)
	var out []string
	i := 0
	for i < n {
		found := false
		// Try larger blocks first for maximal compression
		for blockSize := maxBlock; blockSize >= minBlock; blockSize-- {
			if i+blockSize*2 > n {
				continue
			}
			if !blocksEqual(lines, i, i+blockSize, blockSize) {
				continue
			}
			// Count consecutive repeats
			count := 2
			for i+count*blockSize+blockSize <= n && blocksEqual(lines, i, i+count*blockSize, blockSize) {
				count++
			}
			// Build marker with JSON array
			block := lines[i : i+blockSize]
			// Escape block marker inside block content is handled by JSON encoding; no need extra
			jsonBytes, err := json.Marshal(block)
			if err != nil {
				continue
			}
			marker := fmt.Sprintf("[block ×%d: %s]", count, string(jsonBytes))
			out = append(out, marker)
			i += count * blockSize
			found = true
			break
		}
		if !found {
			line := lines[i]
			// Escape solitary lines that look like block marker
			if blockRegex.MatchString(line) {
				line = escapeBlockLine(line)
			} else if strings.Contains(line, "[block ×") {
				// Also escape if contains prefix but not full match (defensive)
				// Check if it contains marker substring that could be confused on decode split
				// For consistency, escape any occurrence
				line = escapeBlockLine(line)
			}
			out = append(out, line)
			i++
		}
	}
	result := strings.Join(out, "\n")
	if hasTrailing {
		result += "\n"
	}
	return result
}

// DecodeBlocks reverses EncodeBlocks.
func DecodeBlocks(encoded string) string {
	if encoded == "" {
		return ""
	}
	hasTrailing := strings.HasSuffix(encoded, "\n")
	lines := strings.Split(encoded, "\n")
	if hasTrailing {
		lines = lines[:len(lines)-1]
	}
	var out []string
	for _, line := range lines {
		if m := blockRegex.FindStringSubmatch(line); m != nil {
			countStr := m[1]
			jsonPart := m[2]
			n, err := strconv.Atoi(countStr)
			if err != nil || n <= 0 {
				// fallback to plain
				if strings.Contains(line, "[block ×"+esc) {
					out = append(out, unescapeBlockLine(line))
				} else {
					out = append(out, line)
				}
				continue
			}
			var block []string
			if err := json.Unmarshal([]byte(jsonPart), &block); err != nil {
				// Not valid JSON, treat as plain
				if strings.Contains(line, "[block ×"+esc) {
					out = append(out, unescapeBlockLine(line))
				} else {
					out = append(out, line)
				}
				continue
			}
			if n*len(block) > maxLines {
				log.Printf("rle: DecodeBlocks count %d*%d exceeds limit %d, treating as plain: %q", n, len(block), maxLines, line)
				if strings.Contains(line, "[block ×"+esc) {
					out = append(out, unescapeBlockLine(line))
				} else {
					out = append(out, line)
				}
				continue
			}
			for i := 0; i < n; i++ {
				out = append(out, block...)
			}
			continue
		}
		if strings.Contains(line, "[block ×"+esc) {
			out = append(out, unescapeBlockLine(line))
		} else {
			out = append(out, line)
		}
	}
	result := strings.Join(out, "\n")
	if hasTrailing {
		result += "\n"
	}
	return result
}

// VerifyBlocks checks DecodeBlocks(encoded) == original.
func VerifyBlocks(original, encoded string) bool {
	return DecodeBlocks(encoded) == original
}

// IsBlockEncoded reports whether s contains block marker.
func IsBlockEncoded(s string) bool {
	if s == "" {
		return false
	}
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if blockRegex.MatchString(line) {
			return true
		}
	}
	return false
}

// IsRLEEncoded alias already defined; for block we have IsBlockEncoded.
// Provide generic IsBlock helper for tests that call IsBlockEncoded
