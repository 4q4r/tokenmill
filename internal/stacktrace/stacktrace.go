package stacktrace

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	stackAtRegex = regexp.MustCompile(`at\s+.*\(.*:\d+\)`)
	githubRegex  = regexp.MustCompile(`github\.com(?:/[\w.\-~]+)+`)
	pathRegex    = regexp.MustCompile(`(?:/[\w.\-~]+){3,}`)
	// General file:line pattern - support multiple extensions
	fileLineRegex = regexp.MustCompile(`[\w./\-~]+\.\w+:\d+`)
)

const (
	defaultStackMinCount = 3
	defaultStackMaxCodes = 5
)

// CompressStackTrace detects at .*\(.*:\d+\) and github.com/.../, groups by file prefix, cmap $F0=prefix.
// Saves file:line:function exactly. Returns header + body with markers.
func CompressStackTrace(input string) (string, map[string]string, bool) {
	return CompressStackTraceWithConfig(input, defaultStackMinCount, defaultStackMaxCodes)
}

// CompressStackTraceWithConfig is configurable variant of CompressStackTrace.
// minCount and maxCodes are threshold and max dictionary size; 0 means default (3,5).
// This makes threshold configurable vs PathDict MinCount 3 (S-01 fix).
func CompressStackTraceWithConfig(input string, minCount int, maxCodes int) (string, map[string]string, bool) {
	if minCount <= 0 {
		minCount = defaultStackMinCount
	}
	if maxCodes <= 0 {
		maxCodes = defaultStackMaxCodes
	}
	if input == "" {
		return input, nil, false
	}
	hasAt := stackAtRegex.MatchString(input)
	hasGithub := githubRegex.MatchString(input) || fileLineRegex.MatchString(input) || pathRegex.MatchString(input)
	if !hasAt && !hasGithub {
		return input, nil, false
	}
	// Find candidate prefixes
	// Use similar counting as dictionary but for file prefixes
	counts := make(map[string]int)
	lines := strings.Split(input, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		isStackLine := stackAtRegex.MatchString(trimmed) || githubRegex.MatchString(trimmed) || fileLineRegex.MatchString(trimmed)
		if !isStackLine {
			continue
		}
		// Extract file path-like substrings
		// Prefer githubRegex first, then fileLineRegex, then pathRegex
		var paths []string
		if githubRegex.MatchString(trimmed) {
			paths = append(paths, githubRegex.FindAllString(trimmed, -1)...)
		}
		if fileLineRegex.MatchString(trimmed) {
			for _, m := range fileLineRegex.FindAllString(trimmed, -1) {
				// m is like github.com/foo/bar/baz.go:123 or pkg/file.go:10
				// Ensure not duplicate
				found := false
				for _, p := range paths {
					if p == m {
						found = true
						break
					}
				}
				if !found {
					paths = append(paths, m)
				}
			}
		}
		// Also try generic path regex for Java etc.
		if len(paths) == 0 && pathRegex.MatchString(trimmed) {
			paths = append(paths, pathRegex.FindAllString(trimmed, -1)...)
		}
		// For at ...(...:line) pattern, extract inside parentheses
		if stackAtRegex.MatchString(trimmed) {
			// Extract content inside parentheses: e.g., MyClass.java:123
			if idx1 := strings.Index(trimmed, "("); idx1 != -1 {
				if idx2 := strings.Index(trimmed[idx1:], ")"); idx2 != -1 {
					inside := trimmed[idx1+1 : idx1+idx2]
					// inside may be like MyClass.java:123 or github.com/.../file.go:123
					// Add inside if not already
					if !contains(paths, inside) && strings.Contains(inside, ":") {
						paths = append(paths, inside)
					}
					// Also try to extract directory part before file
					// For Java, inside is just file:line, not path, so not useful for prefix
				}
			}
		}
		for _, p := range paths {
			// Derive directory prefix: up to last "/"
			// For file like github.com/foo/bar/baz.go:123, dir is github.com/foo/bar/
			// For file like pkg/file.go:10, dir is pkg/
			// For MyClass.java:123, dir is empty (no slash)
			dir := ""
			if lastSlash := strings.LastIndex(p, "/"); lastSlash != -1 {
				dir = p[:lastSlash+1] // include slash
			} else {
				// No slash, try package prefix for Java style "com.example.MyClass.method"
				if stackAtRegex.MatchString(trimmed) {
					atIdx := strings.Index(trimmed, "at ")
					if atIdx != -1 {
						rest := trimmed[atIdx+3:]
						if paren := strings.Index(rest, "("); paren != -1 {
							fqn := strings.TrimSpace(rest[:paren])
							// fqn is like com.example.MyClass.method
							if lastDot := strings.LastIndex(fqn, "."); lastDot != -1 {
								pkg := fqn[:lastDot] // com.example.MyClass
								// Use dot-based prefix that actually appears in input
								// Generate prefixes like "com.", "com.example.", "com.example.MyClass."
								segs := strings.Split(pkg, ".")
								for i := 1; i < len(segs); i++ {
									candidate := strings.Join(segs[:i], ".") + "."
									counts[candidate]++
								}
								// Also count full package prefix with dot?
								dir = pkg + "."
								// We already counted parents above, now set dir to full pkg.
								// Continue to generic counting below without re-adding dir?
								// To avoid double, set dir to "" and continue after counting
								if dir != "" {
									counts[dir]++
									// parents already counted, skip further parent generation for this dir
									continue
								}
							}
						}
					}
				}
				if dir == "" {
					continue
				}
			}
			// dir must be at least 2 segments? For file prefix we want at least something like "a/b/" or "github.com/foo/"
			// Require at least one slash and length >=?
			if dir == "" || len(dir) < 3 {
				continue
			}
			// Count dir
			counts[dir]++
			// Also count higher-level prefixes: for dir "a/b/c/", also count "a/b/" etc.
			// Generate all parent prefixes length >=2 segments
			// Split dir by "/"
			trimmedDir := strings.TrimSuffix(dir, "/")
			segs := strings.Split(trimmedDir, "/")
			// For github.com/foo/bar/, segs = ["github.com","foo","bar"]
			// Generate prefixes for i from 2 to len(segs)-1 (since full dir is already counted)
			for i := 2; i < len(segs); i++ {
				parent := strings.Join(segs[:i], "/") + "/"
				counts[parent]++
			}
		}
	}
	if len(counts) == 0 {
		// Fallback: use general path prefix counting similar to dictionary
		matches := pathRegex.FindAllString(input, -1)
		matches = append(matches, githubRegex.FindAllString(input, -1)...)
		matches = append(matches, fileLineRegex.FindAllString(input, -1)...)
		for _, m := range matches {
			dir := ""
			if lastSlash := strings.LastIndex(m, "/"); lastSlash != -1 {
				dir = m[:lastSlash+1]
			}
			if dir != "" {
				counts[dir]++
			}
		}
		if len(counts) == 0 {
			return input, nil, false
		}
	}
	type cand struct {
		prefix string
		count  int
	}
	var candidates []cand
	for p, c := range counts {
		if c >= minCount {
			candidates = append(candidates, cand{prefix: p, count: c})
		}
	}
	if len(candidates) == 0 {
		return input, nil, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].prefix) != len(candidates[j].prefix) {
			return len(candidates[i].prefix) > len(candidates[j].prefix)
		}
		if candidates[i].count != candidates[j].count {
			return candidates[i].count > candidates[j].count
		}
		return candidates[i].prefix < candidates[j].prefix
	})
	if len(candidates) > maxCodes {
		candidates = candidates[:maxCodes]
	}
	dict := make(map[string]string, len(candidates))
	for i, c := range candidates {
		marker := fmt.Sprintf("$F%d", i)
		dict[marker] = c.prefix
	}
	body := input
	// Replace longest-first (already sorted)
	for i, c := range candidates {
		marker := fmt.Sprintf("$F%d", i)
		if strings.Contains(body, c.prefix) {
			body = strings.ReplaceAll(body, c.prefix, marker)
		}
	}
	// Build header
	var parts []string
	for i := 0; i < len(candidates); i++ {
		marker := fmt.Sprintf("$F%d", i)
		parts = append(parts, fmt.Sprintf("%s=%s", marker, dict[marker]))
	}
	header := fmt.Sprintf("[StackTrace: %s]", strings.Join(parts, " "))
	encoded := header + "\n" + body
	if body == input {
		return input, nil, false
	}
	if DecodeStackTrace(encoded, dict) != input {
		return input, nil, false
	}
	return encoded, dict, true
}

func contains(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

// DecodeStackTrace reverses CompressStackTrace.
func DecodeStackTrace(encoded string, dict map[string]string) string {
	if encoded == "" {
		return encoded
	}
	if len(dict) == 0 {
		if strings.HasPrefix(encoded, "[StackTrace:") {
			if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
				return encoded[idx+2:]
			}
		}
		return encoded
	}
	body := encoded
	if strings.HasPrefix(encoded, "[StackTrace:") {
		if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
			body = encoded[idx+2:]
		} else if nl := strings.Index(encoded, "\n"); nl != -1 {
			body = encoded[nl+1:]
		}
	}
	type kv struct {
		marker string
		prefix string
	}
	var kvs []kv
	for m, p := range dict {
		kvs = append(kvs, kv{marker: m, prefix: p})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if len(kvs[i].marker) != len(kvs[j].marker) {
			return len(kvs[i].marker) > len(kvs[j].marker)
		}
		return kvs[i].marker > kvs[j].marker
	})
	for _, kv := range kvs {
		body = strings.ReplaceAll(body, kv.marker, kv.prefix)
	}
	return body
}

// VerifyStackTrace checks roundtrip.
func VerifyStackTrace(original, encoded string, dict map[string]string) bool {
	return DecodeStackTrace(encoded, dict) == original
}

// Verify is alias for VerifyStackTrace.
func Verify(original, encoded string, dict map[string]string) bool {
	return VerifyStackTrace(original, encoded, dict)
}

// DecompressStackTrace is alias for DecodeStackTrace.
func DecompressStackTrace(encoded string, dict map[string]string) string {
	return DecodeStackTrace(encoded, dict)
}

// Decode is generic alias.
func Decode(encoded string, dict map[string]string) string {
	return DecodeStackTrace(encoded, dict)
}
