package dictionary

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

// Precompiled regexes.
var (
	// Path regex: ≥3 segments each like /word
	pathRegex = regexp.MustCompile(`(?:/[\w.\-~]+){3,}`)
	// URL regex: https:// + host + at least 2 path segments
	urlRegex = regexp.MustCompile(`https?://[\w.\-~]+(?:/[\w.\-~]+){2,}/?`)
)

const (
	defaultMaxCodes    = 5
	defaultMinCount    = 3
	defaultMinLen      = 40
	defaultMinCountSub = 4
)

// EncodePaths implements path-prefix dictionary longest-first.
// Regex /(?:/[\w.\-~]+){3,} finds paths ≥3 segments, counts prefixes length ≥2 segments,
// count≥minCount (default 3), maxCodes 5, sorts longest-first, replacement strings.ReplaceAll,
// header [Paths: $P0=/a/b/ ...] + body with $P0 suffix.
func EncodePaths(input string, maxCodes int, minCount int) (string, map[string]string, bool) {
	if maxCodes <= 0 {
		maxCodes = defaultMaxCodes
	}
	if minCount <= 0 {
		minCount = defaultMinCount
	}
	if input == "" {
		return input, nil, false
	}
	matches := pathRegex.FindAllString(input, -1)
	if len(matches) == 0 {
		return input, nil, false
	}
	// Count prefixes.
	counts := make(map[string]int)
	for _, m := range matches {
		// segments split
		segments := strings.Split(m, "/")
		// ["", "a","b","c",...]
		if len(segments) < 4 { // need at least 3 segments => length 4 including leading empty
			continue
		}
		numSeg := len(segments) - 1 // number of real segments
		// generate prefixes length >=2
		for prefLen := 2; prefLen <= numSeg; prefLen++ {
			// without trailing slash prefix (exact)
			prefixNoSlash := "/" + strings.Join(segments[1:prefLen+1], "/")
			counts[prefixNoSlash]++
			// with trailing slash for directory prefixes (if not last)
			if prefLen < numSeg {
				prefixSlash := prefixNoSlash + "/"
				counts[prefixSlash]++
			}
		}
	}
	type cand struct {
		prefix string
		count  int
	}
	var candidates []cand
	for p, c := range counts {
		if c >= minCount {
			// ensure prefix length >=2 segments and at least length sensible
			// also ensure prefix itself matches path-like pattern (at least 2 segments)
			if strings.Count(p, "/") < 2 {
				continue
			}
			candidates = append(candidates, cand{prefix: p, count: c})
		}
	}
	if len(candidates) == 0 {
		return input, nil, false
	}
	// Sort longest-first, then by count descending, then lexicographically
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
	// Build dict and body longest-first replacement
	dict := make(map[string]string, len(candidates))
	for i, c := range candidates {
		marker := fmt.Sprintf("$P%d", i)
		dict[marker] = c.prefix
	}
	// Replacement longest-first (candidates already sorted)
	body := input
	for i, c := range candidates {
		marker := fmt.Sprintf("$P%d", i)
		// Use strings.ReplaceAll
		if strings.Contains(body, c.prefix) {
			body = strings.ReplaceAll(body, c.prefix, marker)
		}
	}
	// Build header
	var parts []string
	for i := 0; i < len(candidates); i++ {
		marker := fmt.Sprintf("$P%d", i)
		parts = append(parts, fmt.Sprintf("%s=%s", marker, dict[marker]))
	}
	header := fmt.Sprintf("[Paths: %s]", strings.Join(parts, " "))
	encoded := header + "\n" + body

	if codec.TokenSavings(input, encoded) <= codec.HintOverhead {
		return input, nil, false
	}
	// Ensure we actually replaced something
	if body == input {
		return input, nil, false
	}
	// Verify roundtrip will hold; if not, fail
	if DecodePaths(encoded, dict) != input {
		// failed to roundtrip due to marker collision? fallback
		return input, nil, false
	}
	return encoded, dict, true
}

// DecodePaths reverses EncodePaths.
func DecodePaths(encoded string, dict map[string]string) string {
	if encoded == "" || len(dict) == 0 {
		// Try to handle header parsing if dict empty but encoded contains header?
		if encoded == "" {
			return encoded
		}
		// If dict nil, try to extract body after header
		if strings.HasPrefix(encoded, "[Paths:") {
			if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
				return encoded[idx+2:]
			}
		}
		return encoded
	}
	// Extract body
	body := encoded
	if strings.HasPrefix(encoded, "[Paths:") {
		if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
			body = encoded[idx+2:]
		} else if nl := strings.Index(encoded, "\n"); nl != -1 {
			body = encoded[nl+1:]
		}
	}
	// Replace markers with prefixes. Need longest marker first to avoid $P1 inside $P10 etc.
	// Sort markers by length descending
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

// VerifyPaths checks DecodePaths(encoded, dict) == original.
func VerifyPaths(original, encoded string, dict map[string]string) bool {
	return DecodePaths(encoded, dict) == original
}

// EncodeURLs is analogous for URL prefix https://.../v1/
func EncodeURLs(input string, maxCodes int, minCount int) (string, map[string]string, bool) {
	if maxCodes <= 0 {
		maxCodes = defaultMaxCodes
	}
	if minCount <= 0 {
		minCount = defaultMinCount
	}
	if input == "" {
		return input, nil, false
	}
	matches := urlRegex.FindAllString(input, -1)
	if len(matches) == 0 {
		return input, nil, false
	}
	counts := make(map[string]int)
	for _, m := range matches {
		// For each URL, generate prefixes similar to path but for URL.
		// Find host part: after "://"
		// Example: https://example.com/api/v1/users -> prefixes: https://example.com/api/, https://example.com/api/v1/, https://example.com/api/v1/users
		// Generate prefixes length >= host + 2 segments
		// Simplistic: split by "/" but keep scheme.
		// We'll generate prefixes by cutting at each "/" after host.
		// Locate "://"
		idx := strings.Index(m, "://")
		if idx == -1 {
			continue
		}
		afterScheme := m[idx+3:] // host + path
		// Split afterScheme by "/"
		parts := strings.Split(afterScheme, "/")
		// parts[0] is host, rest are path segments
		if len(parts) < 3 { // need host + at least 2 path segments
			continue
		}
		host := parts[0]
		pathSegs := parts[1:]
		// Remove trailing empty if URL ends with "/"
		// Count empty? If last is "" due to trailing slash, ignore for counting
		if len(pathSegs) > 0 && pathSegs[len(pathSegs)-1] == "" {
			pathSegs = pathSegs[:len(pathSegs)-1]
		}
		if len(pathSegs) < 2 {
			continue
		}
		scheme := m[:idx] // https or http
		base := scheme + "://" + host
		for prefLen := 2; prefLen <= len(pathSegs); prefLen++ {
			prefixNoSlash := base + "/" + strings.Join(pathSegs[:prefLen], "/")
			counts[prefixNoSlash]++
			if prefLen < len(pathSegs) {
				prefixSlash := prefixNoSlash + "/"
				counts[prefixSlash]++
			}
		}
		// Also consider base with slash? but need at least 2 segments so ignore base alone
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
		marker := fmt.Sprintf("$U%d", i)
		dict[marker] = c.prefix
	}
	body := input
	for i, c := range candidates {
		marker := fmt.Sprintf("$U%d", i)
		if strings.Contains(body, c.prefix) {
			body = strings.ReplaceAll(body, c.prefix, marker)
		}
	}
	var parts []string
	for i := 0; i < len(candidates); i++ {
		marker := fmt.Sprintf("$U%d", i)
		parts = append(parts, fmt.Sprintf("%s=%s", marker, dict[marker]))
	}
	header := fmt.Sprintf("[URLs: %s]", strings.Join(parts, " "))
	encoded := header + "\n" + body
	if body == input {
		return input, nil, false
	}
	if DecodeURLs(encoded, dict) != input {
		return input, nil, false
	}
	return encoded, dict, true
}

// DecodeURLs reverses EncodeURLs.
func DecodeURLs(encoded string, dict map[string]string) string {
	if encoded == "" {
		return encoded
	}
	if len(dict) == 0 {
		if strings.HasPrefix(encoded, "[URLs:") {
			if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
				return encoded[idx+2:]
			}
		}
		return encoded
	}
	body := encoded
	if strings.HasPrefix(encoded, "[URLs:") {
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

// VerifyURLs checks roundtrip.
func VerifyURLs(original, encoded string, dict map[string]string) bool {
	return DecodeURLs(encoded, dict) == original
}

// EncodeSubstrings implements substring exact dictionary.
// Rolling hash Rabin-Karp minLen 40 default, minCount 4, non-overlapping, no nested meta ⟨M#⟩,
// tokenSavings via tokenizer.Count → f*n(S) - ((1+f)*1 + n(S)) > minSavings.
// Dict marker ⟨M0⟩ or $S0.
func EncodeSubstrings(input string, minLen int, minCount int, minSavings int) (string, map[string]string, bool) {
	if minLen <= 0 {
		minLen = defaultMinLen
	}
	if minCount <= 0 {
		minCount = defaultMinCountSub
	}
	// minSavings can be 0 or negative; no default override for 0 (means allow any positive)
	if input == "" || len(input) < minLen {
		return input, nil, false
	}
	// If input already contains marker pattern, we should skip substrings containing it
	// But we filter candidates containing marker.
	n := len(input)
	// Map substring -> count and positions
	type info struct {
		count     int
		positions []int
	}
	m := make(map[string]*info)
	// Use sliding window hash grouping: O(n*minLen) but okay for tests.
	for i := 0; i <= n-minLen; i++ {
		sub := input[i : i+minLen]
		if strings.Contains(sub, "⟨M") || strings.Contains(sub, "$S") || strings.Contains(sub, "⟨M#") {
			continue
		}
		if _, ok := m[sub]; !ok {
			m[sub] = &info{}
		}
		m[sub].count++ // raw overlapping count, will recalc non-overlapping later
		m[sub].positions = append(m[sub].positions, i)
	}
	// Extend each repeated window to its maximal repeat: occurrences of a
	// minLen window that all share the next byte grow one byte at a time.
	// This collapses the many overlapping minLen-window candidates into a
	// small set of maximal substrings before any envelope math runs.
	type cand struct {
		sub       string
		positions []int
		f         int
		nS        int
	}
	var candidates []cand
	seenMaximal := make(map[string]struct{})
	for sub, inf := range m {
		if _, done := seenMaximal[sub]; done {
			continue
		}
		positions := inf.positions
		length := minLen
		for {
			next := positions[len(positions)-1] + length
			if next >= n {
				break
			}
			char := input[positions[0]+length]
			same := true
			for _, p := range positions {
				if p+length >= n || input[p+length] != char {
					same = false
					break
				}
			}
			if !same {
				break
			}
			length++
		}
		maximal := input[positions[0] : positions[0]+length]
		if _, done := seenMaximal[maximal]; done {
			continue
		}
		seenMaximal[maximal] = struct{}{}
		// Recompute non-overlapping occurrences of the maximal substring.
		var occ []int
		lastEnd := -1
		for p := 0; p+len(maximal) <= n; {
			found := strings.Index(input[p:], maximal)
			if found < 0 {
				break
			}
			at := p + found
			if at >= lastEnd {
				occ = append(occ, at)
				lastEnd = at + len(maximal)
			}
			p = at + 1
		}
		if len(occ) < minCount {
			continue
		}
		f := strings.Count(input, maximal)
		if f < minCount {
			continue
		}
		nS := tokenizer.Count(maximal)
		// Per-candidate gross saving gate (minSavings honored as before).
		if f*nS-((1+f)*1+nS) <= minSavings {
			continue
		}
		candidates = append(candidates, cand{sub: maximal, positions: occ, f: f, nS: nS})
	}
	if len(candidates) == 0 {
		return input, nil, false
	}
	// Greedy selection: highest total gross saving first; a candidate is
	// accepted only when none of its occurrences overlap an already accepted
	// one, so the envelope never pays for candidates whose text another
	// candidate already replaced. Ties break deterministically (length
	// descending, then lexicographic) so repeated encodes of the same input
	// produce byte-identical output.
	maxCodes := 5
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i].f*candidates[i].nS, candidates[j].f*candidates[j].nS
		if left != right {
			return left > right
		}
		if len(candidates[i].sub) != len(candidates[j].sub) {
			return len(candidates[i].sub) > len(candidates[j].sub)
		}
		return candidates[i].sub < candidates[j].sub
	})
	type interval struct {
		start, end int
	}
	var taken []interval
	dict := make(map[string]string, maxCodes)
	for _, c := range candidates {
		if len(dict) == maxCodes {
			break
		}
		disjoint := true
		for _, p := range c.positions {
			for _, iv := range taken {
				if p < iv.end && iv.start < p+len(c.sub) {
					disjoint = false
					break
				}
			}
			if !disjoint {
				break
			}
		}
		if !disjoint {
			continue
		}
		for _, p := range c.positions {
			taken = append(taken, interval{start: p, end: p + len(c.sub)})
		}
		dict[fmt.Sprintf("⟨M%d⟩", len(dict))] = c.sub
	}
	if len(dict) == 0 {
		return input, nil, false
	}
	// Replacement longest-first to avoid partial overlaps between accepted
	// candidates.
	subToMarker := make(map[string]string)
	for marker, sub := range dict {
		subToMarker[sub] = marker
	}
	// Build replacement order: sort distinct subs by len descending
	type repl struct {
		sub    string
		marker string
	}
	var repls []repl
	for sub, marker := range subToMarker {
		repls = append(repls, repl{sub: sub, marker: marker})
	}
	sort.Slice(repls, func(i, j int) bool {
		if len(repls[i].sub) != len(repls[j].sub) {
			return len(repls[i].sub) > len(repls[j].sub)
		}
		return repls[i].marker < repls[j].marker
	})
	body := input
	for _, r := range repls {
		if strings.Contains(body, r.sub) {
			// Avoid nested meta: ensure we don't replace inside already replaced markers (markers contain ⟨M, so sub won't contain that)
			body = strings.ReplaceAll(body, r.sub, r.marker)
		}
	}
	// Build header
	var parts []string
	for i := 0; i < len(dict); i++ {
		marker := fmt.Sprintf("⟨M%d⟩", i)
		// Need to find mapping; dict contains it
		if sub, ok := dict[marker]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", marker, sub))
		}
	}
	header := fmt.Sprintf("[Substrings: %s]", strings.Join(parts, " "))
	encoded := header + "\n" + body
	if body == input {
		return input, nil, false
	}
	if DecodeSubstrings(encoded, dict) != input {
		return input, nil, false
	}
	if codec.TokenSavings(input, encoded) <= codec.HintOverhead {
		return input, nil, false
	}
	return encoded, dict, true
}

// DecodeSubstrings reverses EncodeSubstrings.
func DecodeSubstrings(encoded string, dict map[string]string) string {
	if encoded == "" {
		return encoded
	}
	if len(dict) == 0 {
		if strings.HasPrefix(encoded, "[Substrings:") {
			if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
				return encoded[idx+2:]
			}
		}
		return encoded
	}
	body := encoded
	if strings.HasPrefix(encoded, "[Substrings:") {
		if idx := strings.LastIndex(encoded, "]\n"); idx != -1 {
			body = encoded[idx+2:]
		} else if nl := strings.Index(encoded, "\n"); nl != -1 {
			body = encoded[nl+1:]
		}
	} else if strings.HasPrefix(encoded, "[Paths:") || strings.HasPrefix(encoded, "[URLs:") {
		// fallback
		if nl := strings.Index(encoded, "\n"); nl != -1 {
			body = encoded[nl+1:]
		}
	}
	// Need to handle both ⟨M#⟩ and $S# markers; dict may contain either.
	type kv struct {
		marker string
		sub    string
	}
	var kvs []kv
	for m, s := range dict {
		kvs = append(kvs, kv{marker: m, sub: s})
	}
	// Sort longest marker first to avoid $S1 inside $S10 etc., also ⟨M10⟩ vs ⟨M1⟩
	sort.Slice(kvs, func(i, j int) bool {
		if len(kvs[i].marker) != len(kvs[j].marker) {
			return len(kvs[i].marker) > len(kvs[j].marker)
		}
		return kvs[i].marker > kvs[j].marker
	})
	for _, kv := range kvs {
		body = strings.ReplaceAll(body, kv.marker, kv.sub)
	}
	return body
}

// VerifySubstrings checks roundtrip.
func VerifySubstrings(original, encoded string, dict map[string]string) bool {
	return DecodeSubstrings(encoded, dict) == original
}

// Aliases for compatibility: Decode/Verify with alternative marker $S
// For marker $S support, EncodeSubstrings already uses ⟨M⟩ but Decode will handle $S if dict uses it.
// Provide helper functions that also accept $S markers? Already handled generically.

// Additional helpers for VerifyPaths/URLs with header parsing without dict
// Provide generic Verify that handles both
