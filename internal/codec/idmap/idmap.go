// Package idmap implements a session-scoped identifier remapper: the first
// sighting of an identifier stays verbatim (canonical-first), and every
// later sighting in the same session collapses to a short marker that
// decodes back byte-for-byte.
package idmap

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// canonicalUUID matches the dashed UUID shape.
var canonicalUUID = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)

// marker matches the remap markers this package emits (§uid:N§).
var markerPattern = regexp.MustCompile(`§uid:(\d+)§`)

// DefaultMaxEntries bounds the mapping table. Over the limit, new
// identifiers stay verbatim — the remapper degrades to a no-op instead of
// growing without bounds.
const DefaultMaxEntries = 10_000

// Remapper is a session-scoped, canonical-first identifier remapper. It is
// safe for concurrent use.
type Remapper struct {
	mu       sync.Mutex
	byLower  map[string]string // lower-case uuid -> marker
	byMarker map[string]string // marker -> first-seen spelling
	count    int
	max      int
}

// New creates a remapper with the given entry bound (<=0 selects the
// default).
func New(maxEntries int) *Remapper {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &Remapper{
		byLower:  make(map[string]string),
		byMarker: make(map[string]string),
		max:      maxEntries,
	}
}

// Reset clears the mapping (new session).
func (r *Remapper) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byLower = make(map[string]string)
	r.byMarker = make(map[string]string)
	r.count = 0
}

// Len returns the number of mapped identifiers.
func (r *Remapper) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// Preview counts, without mutating state, how many identifier occurrences in
// s would collapse to markers (repeat sightings of known identifiers).
func (r *Remapper) Preview(s string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	known := 0
	for _, uuid := range canonicalUUID.FindAllString(s, -1) {
		if _, isKnown := r.byLower[strings.ToLower(uuid)]; isKnown {
			known++
		}
	}
	return known
}

// Remap replaces every repeat sighting of a known identifier with its
// §uid:N§ marker. First sightings stay verbatim so the model always sees
// the full form at least once. Returns the rewritten text and the number
// of replacements made.
func (r *Remapper) Remap(s string) (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	replacements := 0
	out := canonicalUUID.ReplaceAllStringFunc(s, func(uuid string) string {
		key := strings.ToLower(uuid)
		if ref, known := r.byLower[key]; known {
			replacements++
			return ref
		}
		if r.count >= r.max {
			return uuid
		}
		r.count++
		ref := fmt.Sprintf("§uid:%d§", r.count)
		r.byLower[key] = ref
		r.byMarker[ref] = uuid
		return uuid
	})
	return out, replacements
}

// Expand replaces every §uid:N§ marker with the identifier it references.
// ok is false when the text contains no markers or an unknown marker
// (nothing is rewritten in either case).
func (r *Remapper) Expand(s string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.Contains(s, "§uid:") {
		return s, false
	}
	unknown := false
	expanded := markerPattern.ReplaceAllStringFunc(s, func(m string) string {
		match := markerPattern.FindStringSubmatch(m)
		original, known := r.byMarker["§uid:"+match[1]+"§"]
		if !known {
			unknown = true
			return m
		}
		return original
	})
	if unknown {
		return s, false
	}
	return expanded, true
}

// Verify reports whether expanding the encoded form reproduces the original.
func (r *Remapper) Verify(original, encoded string) bool {
	expanded, ok := r.Expand(encoded)
	return ok && expanded == original
}
