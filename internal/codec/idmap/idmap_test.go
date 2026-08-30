package idmap

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

const testUUID = "550e8400-e29b-41d4-a716-446655440000"

func TestRemapFirstVerbatimRepeatMarked(t *testing.T) {
	r := New(0)
	first := "session " + testUUID + " started"
	out, replacements := r.Remap(first)
	if replacements != 0 || out != first {
		t.Fatalf("first sighting changed: replacements=%d out=%q", replacements, out)
	}
	second := "session " + testUUID + " finished"
	out, replacements = r.Remap(second)
	if replacements != 1 {
		t.Fatalf("replacements = %d, want 1", replacements)
	}
	if !strings.Contains(out, "§uid:1§") {
		t.Fatalf("marker missing: %q", out)
	}
	expanded, ok := r.Expand(out)
	if !ok || expanded != second {
		t.Fatalf("expand = %q ok=%v, want original", expanded, ok)
	}
	if !r.Verify(second, out) {
		t.Fatal("Verify failed for its own encoding")
	}
}

func TestRemapCaseInsensitiveDistinctSpelling(t *testing.T) {
	r := New(0)
	upper := "ID " + strings.ToUpper(testUUID)
	r.Remap(upper)
	lower := "ID " + strings.ToLower(testUUID) + " again"
	out, replacements := r.Remap(lower)
	if replacements != 1 {
		t.Fatalf("case-insensitive repeat not marked: replacements=%d out=%q", replacements, out)
	}
	expanded, ok := r.Expand(out)
	if !ok {
		t.Fatal("expand failed")
	}
	// Canonical-first: the first-seen spelling (upper) is what markers
	// expand to.
	if expanded != upper+" again" {
		t.Fatalf("expand = %q, want first-seen spelling %q", expanded, upper+" again")
	}
}

func TestRemapBounded(t *testing.T) {
	r := New(2)
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < 5; i++ {
			uuid := strings.ReplaceAll(testUUID, "550e", fmt.Sprintf("%04x", i))
			out, _ := r.Remap("x " + uuid + " y")
			if pass == 1 && i < 2 && !strings.Contains(out, "§uid:") {
				t.Fatalf("bounded identifier %d not remapped on repeat: %q", i, out)
			}
			if pass == 1 && i >= 2 && strings.Contains(out, "§uid:") {
				t.Fatalf("over-limit identifier %d was remapped: %q", i, out)
			}
		}
	}
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (bounded)", r.Len())
	}
}

func TestReset(t *testing.T) {
	r := New(0)
	r.Remap("x " + testUUID + " y")
	r.Reset()
	if r.Len() != 0 {
		t.Fatalf("Len = %d after reset", r.Len())
	}
	out, replacements := r.Remap("x " + testUUID + " y")
	if replacements != 0 || out != "x "+testUUID+" y" {
		t.Fatal("after reset the identifier should be first-seen again")
	}
}

func TestConcurrentRemap(t *testing.T) {
	r := New(0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uuid := strings.ReplaceAll(testUUID, "550e", fmt.Sprintf("%04x", i))
			r.Remap("x " + uuid + " y")
			r.Remap("x " + uuid + " y")
		}(i)
	}
	wg.Wait()
	if r.Len() != 8 {
		t.Fatalf("Len = %d, want 8 distinct identifiers", r.Len())
	}
}
