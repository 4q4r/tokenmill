package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestPutGet_HappyPath(t *testing.T) {
	s := New(20, "")
	content := "hello world block"
	hash := s.Put(content, 1)
	if len(hash) != 64 {
		t.Fatalf("Put hash len want 64 got %d (%q)", len(hash), hash)
	}
	got, ok := s.Get(hash)
	if !ok || got != content {
		t.Fatalf("Get failed: ok=%v got=%q want=%q", ok, got, content)
	}
	// Get via truncated 8
	trunc := hash[:8]
	got2, ok2 := s.Get(trunc)
	if !ok2 || got2 != content {
		t.Fatalf("Get trunc failed: %v %q", ok2, got2)
	}
	// Expand alias
	got3, ok3 := s.Expand(hash)
	if !ok3 || got3 != content {
		t.Fatalf("Expand failed")
	}
	got4, ok4 := s.Expand(trunc)
	if !ok4 || got4 != content {
		t.Fatalf("Expand trunc failed")
	}
}

func TestEncode_CacheSafe_CanonicalFirst(t *testing.T) {
	s := New(20, "")
	content := "canonical block for cache safety test with sufficient length to be realistic"
	// First Encode returns original (canonical-first)
	first := s.Encode(content, 1)
	if first != content {
		t.Fatalf("first Encode should return original, got %q", first)
	}
	if s.IsRef(first) {
		t.Fatalf("first Encode should not be ref")
	}
	// Second Encode should return ref
	second := s.Encode(content, 2)
	if !s.IsRef(second) {
		t.Fatalf("second Encode should be ref, got %q", second)
	}
	if second == content {
		t.Fatalf("second Encode should not be original")
	}
	// Verify that canonical stored entry is still original content via Get
	// Get via full hash should still return original
	full, _ := hashContent(content)
	got, ok := s.Get(full)
	if !ok || got != content {
		t.Fatalf("canonical after dedup should still be retrievable")
	}
	// Decode second should return original
	decoded, ok := s.Decode(second)
	if !ok || decoded != content {
		t.Fatalf("Decode failed: %v %q", ok, decoded)
	}
	// Verify byte-equality
	if !s.Verify(content, second) {
		t.Fatalf("Verify should be true for ref")
	}
	if !s.Verify(content, content) {
		t.Fatalf("Verify should be true for identical")
	}
	// Third Encode also ref, cache-safe still
	third := s.Encode(content, 3)
	if !s.IsRef(third) {
		t.Fatalf("third Encode should also be ref")
	}
}

func TestEncode_DedupVsFirst_StatsSuffix(t *testing.T) {
	s := New(20, "")
	content := "test content for stats suffix verification 2143 chars placeholder"
	_ = s.Encode(content, 1) // first
	ref := s.Encode(content, 2)
	// ref should contain §ref:XXXXXXXX§ and suffix
	if !IsRef(ref) {
		t.Fatalf("ref should be IsRef")
	}
	// check suffix contains use tokenmill_expand
	if !contains(ref, "tokenmill_expand") {
		t.Fatalf("ref should contain tokenmill_expand, got %q", ref)
	}
	if !contains(ref, "original") {
		t.Fatalf("ref should contain original, got %q", ref)
	}
	// check hash length 8 or 64 inside
	m := refRegex.FindStringSubmatch(ref)
	if len(m) != 2 {
		t.Fatalf("ref regex failed on %q", ref)
	}
	hash := m[1]
	if len(hash) != 8 && len(hash) != 64 {
		t.Fatalf("hash len should be 8 or 64, got %d", len(hash))
	}
	// ensure Verify passes
	if !s.Verify(content, ref) {
		t.Fatalf("Verify failed for ref")
	}
}

func TestIsRef_Decode_EdgeCases(t *testing.T) {
	s := New(20, "")
	tests := []struct {
		input    string
		isRef    bool
		decodeOk bool
	}{
		{"hello", false, false},
		{"§ref:abcd1234§", true, false}, // not stored
		{"§ref:abcd1234§ (original 10 chars, use tokenmill_expand to retrieve)", true, false},
		{"§ref:ABCDEF12§", false, false}, // uppercase should not match [0-9a-f] - lower only
		{"§ref:abc§", false, false},      // too short
		{"", false, false},
		{"§ref:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef§", true, false}, // 64
	}
	for i, tc := range tests {
		if got := s.IsRef(tc.input); got != tc.isRef {
			t.Fatalf("case %d IsRef(%q)=%v want %v", i, tc.input, got, tc.isRef)
		}
		if got := IsRef(tc.input); got != tc.isRef {
			t.Fatalf("case %d pkg IsRef mismatch", i)
		}
		_, ok := s.Decode(tc.input)
		if ok != tc.decodeOk {
			t.Fatalf("case %d Decode ok=%v want %v for %q", i, ok, tc.decodeOk, tc.input)
		}
	}
	// Valid decode after put
	content := "decodeable content"
	s.Put(content, 1)
	full, _ := hashContent(content)
	ref := formatRef(full[:8], len(content))
	dec, ok := s.Decode(ref)
	if !ok || dec != content {
		t.Fatalf("Decode after Put failed: %v %q", ok, dec)
	}
	// Decode with surrounding text should still work (regex finds)
	wrapped := "prefix " + ref + " suffix"
	dec2, ok2 := s.Decode(wrapped)
	if !ok2 || dec2 != content {
		t.Fatalf("Decode wrapped failed")
	}
}

func TestVerify_ByteEquality(t *testing.T) {
	s := New(20, "")
	original := "byte equality test content"
	// first encode
	s.Encode(original, 1)
	ref := s.Encode(original, 2)
	if !s.Verify(original, ref) {
		t.Fatalf("Verify original vs ref should be true")
	}
	if !s.Verify(original, original) {
		t.Fatalf("Verify identical should be true")
	}
	if s.Verify(original, "different") {
		t.Fatalf("Verify different should be false")
	}
	// Verify with ref that decodes to different should be false
	other := "other content"
	s.Put(other, 10)
	otherFull, _ := hashContent(other)
	otherRef := formatRef(otherFull[:8], len(other))
	if s.Verify(original, otherRef) {
		t.Fatalf("Verify should be false for mismatched ref")
	}
	// Verify empty
	if s.Verify("", "") != true {
		t.Fatalf("empty verify")
	}
}

func TestVerifyRejectsReferenceSurroundedByText(t *testing.T) {
	s := New(20, "")
	content := "canonical content"
	full := s.Put(content, 1)
	ref := formatRef(full[:8], len(content))
	if s.Verify(content, "prefix "+ref+" suffix") {
		t.Fatal("Verify must reject a reference embedded in surrounding text")
	}
}

func TestCloseConcurrentWithPersistenceIsRaceFree(t *testing.T) {
	s := New(20, filepath.Join(t.TempDir(), "dedup.db"))
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for turn := 0; turn < 100; turn++ {
				s.Put(fmt.Sprintf("worker-%d-%d", worker, turn), turn)
			}
		}(worker)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.Close()
	}()
	wg.Wait()
}

func TestPersistenceErrorIsObservable(t *testing.T) {
	s := New(20, filepath.Join(t.TempDir(), "dedup.db"))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s.Put("cannot persist after close", 1)
	if s.Err() == nil {
		t.Fatal("persistence failure after Close must be observable via Err")
	}
}

func TestFreshnessExpiry_AndCleanup(t *testing.T) {
	s := New(5, "") // freshness 5 for quick test
	content := "freshness test block"
	s.Put(content, 1)
	full, _ := hashContent(content)
	// still present at turn 4
	if _, ok := s.Get(full); !ok {
		t.Fatalf("should be present before expiry")
	}
	// Cleanup at turn 6 should expire (6-1=5 >=5)
	removed := s.Cleanup(6)
	if removed != 1 {
		t.Fatalf("Cleanup should remove 1, got %d", removed)
	}
	if _, ok := s.Get(full); ok {
		t.Fatalf("should be gone after Cleanup")
	}
	// After expiry, Encode should treat as new canonical (return original, not ref)
	// Put again at turn 6 already removed, now Encode at turn 7 should be original
	enc := s.Encode(content, 7)
	if s.IsRef(enc) {
		t.Fatalf("after expiry, Encode should return original, got ref %q", enc)
	}
	if enc != content {
		t.Fatalf("after expiry, Encode should be original")
	}
	// Next Encode at 8 should be ref (new dedup)
	ref2 := s.Encode(content, 8)
	if !s.IsRef(ref2) {
		t.Fatalf("second after re-canonical should be ref")
	}
}

func TestCleanup_MultipleEntries(t *testing.T) {
	s := New(10, "")
	for i := 0; i < 5; i++ {
		s.Put(fmt.Sprintf("content-%d", i), i*2) // turns 0,2,4,6,8
	}
	if s.Size() != 5 {
		t.Fatalf("size 5 got %d", s.Size())
	}
	removed := s.Cleanup(12) // threshold 12-10=2, entries with turn<2? Actually cleanup deletes where current-turn >= freshness: 12-0=12>=10 yes, 12-2=10>=10 yes, 12-4=8<10 no, so 2 removed
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	if s.Size() != 3 {
		t.Fatalf("size after cleanup want 3 got %d", s.Size())
	}
}

func TestNotifyCompaction(t *testing.T) {
	s := New(20, "")
	// Put entries at turns 1..10
	for i := 1; i <= 10; i++ {
		s.Put(fmt.Sprintf("block-%d", i), i)
	}
	// keep last 3 => should keep 8,9,10 (3 most recent), remove 1..7 (7 entries) count-based
	removed := s.NotifyCompaction(3)
	if removed != 7 {
		t.Fatalf("NotifyCompaction keep 3 removed want 7 got %d", removed)
	}
	if s.Size() != 3 {
		t.Fatalf("size after compaction want 3 (8-10) got %d", s.Size())
	}
	// Check Get for old should fail
	for i := 1; i <= 7; i++ {
		full, _ := hashContent(fmt.Sprintf("block-%d", i))
		if _, ok := s.Get(full); ok {
			t.Fatalf("block %d should be compacted", i)
		}
	}
	for i := 8; i <= 10; i++ {
		full, _ := hashContent(fmt.Sprintf("block-%d", i))
		if _, ok := s.Get(full); !ok {
			t.Fatalf("block %d should remain", i)
		}
	}
	// keepTurns >= size should keep all
	removed2 := s.NotifyCompaction(15)
	if removed2 != 0 {
		t.Fatalf("keepTurns > size should keep all, removed %d", removed2)
	}
}

func TestTruncatedCollisionFallback(t *testing.T) {
	s := New(20, "")
	// Find two strings with same hash8 via brute force (expected ~ 2^32 search, but small sample we can brute up to ~ 200k)
	str1, str2 := findCollision(t)
	if str1 == "" {
		t.Skip("could not find collision in time")
	}
	full1, h8_1 := hashContent(str1)
	full2, h8_2 := hashContent(str2)
	if h8_1 != h8_2 {
		t.Fatalf("collision helper failed")
	}
	if full1 == full2 {
		t.Fatalf("full should differ for collision test")
	}
	// Put first
	hash1 := s.Put(str1, 1)
	if hash1 != full1 {
		t.Fatalf("Put hash mismatch")
	}
	// Put second with same trunc but different full+content
	hash2 := s.Put(str2, 2)
	if hash2 != full2 {
		t.Fatalf("second Put hash mismatch")
	}
	if s.Size() != 2 {
		t.Fatalf("both should be stored despite trunc collision, size 2 got %d", s.Size())
	}
	// Get via full should return correct respective content (byte-compare fallback)
	got1, ok1 := s.Get(full1)
	if !ok1 || got1 != str1 {
		t.Fatalf("Get full1 failed")
	}
	got2, ok2 := s.Get(full2)
	if !ok2 || got2 != str2 {
		t.Fatalf("Get full2 failed, got %q want %q", got2, str2)
	}
	// Get via trunc should return first (canonical-first)
	gotTrunc, okTrunc := s.Get(h8_1)
	if !okTrunc {
		t.Fatalf("Get trunc should succeed")
	}
	if gotTrunc != str1 {
		t.Fatalf("Get trunc should return first canonical, got %q want %q", gotTrunc, str1)
	}
	// Encode handling: first content's duplicate should still be dedup via full check even though trunc collision exists
	// Put str1 again via Encode at turn 3 should be ref with full hash? Our logic uses full hash if trunc collision.
	// For str1 duplicate, truncIndex points to full1, so Encode should return ref with h8 (since mapped == full1)
	ref1 := s.Encode(str1, 3)
	if !s.IsRef(ref1) {
		t.Fatalf("Encode str1 duplicate should be ref")
	}
	m := refRegex.FindStringSubmatch(ref1)
	if len(m) != 2 || m[1] != h8_1 {
		// If collision exists but this is first entry, it should be h8, not full
		t.Fatalf("ref for str1 should be h8, got %q", ref1)
	}
	// For str2 duplicate, since its trunc maps to first's full, Encode should use full hash to avoid ambiguity
	ref2 := s.Encode(str2, 4)
	if !s.IsRef(ref2) {
		t.Fatalf("Encode str2 duplicate should be ref")
	}
	m2 := refRegex.FindStringSubmatch(ref2)
	if len(m2) != 2 {
		t.Fatalf("ref2 regex fail")
	}
	if m2[1] != full2 {
		// Expect full hash for colliding second
		t.Fatalf("ref for colliding str2 should be full hash to avoid ambiguity, got %q want %q", m2[1], full2)
	}
	// Decode both refs should return correct content
	dec1, ok := s.Decode(ref1)
	if !ok || dec1 != str1 {
		t.Fatalf("Decode ref1 failed %q", dec1)
	}
	dec2, ok := s.Decode(ref2)
	if !ok || dec2 != str2 {
		t.Fatalf("Decode ref2 failed got %q want %q", dec2, str2)
	}
	// Verify byte-equality
	if !s.Verify(str1, ref1) || s.Verify(str1, ref2) {
		t.Fatalf("Verify collision fallback failed")
	}
}

func TestConcurrency(t *testing.T) {
	s := New(20, "")
	var wg sync.WaitGroup
	content := "concurrent block content for race test"
	// Concurrent Put/Get/Encode
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(turn int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				h := s.Put(content, turn)
				if _, ok := s.Get(h); !ok {
					t.Errorf("concurrent Get failed")
				}
				enc := s.Encode(content, turn+1)
				_ = s.IsRef(enc)
				_, _ = s.Decode(enc)
				_ = s.Verify(content, enc)
			}
		}(i)
	}
	wg.Wait()
	// Also test concurrent Cleanup and NotifyCompaction
	wg2 := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			s.Cleanup(100)
			s.NotifyCompaction(5)
		}()
	}
	wg2.Wait()
}

func TestSQLitePersist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "dedup.db")
	s1 := New(20, dbPath)
	content := "persisted content via sqlite"
	hash := s1.Put(content, 5)
	if _, ok := s1.Get(hash); !ok {
		t.Fatalf("s1 Get failed")
	}
	// Encode also persists
	content2 := "second persisted"
	s1.Encode(content2, 6)
	s1.Close()

	// New store with same dbPath should load previous entries
	s2 := New(20, dbPath)
	defer s2.Close()
	if got, ok := s2.Get(hash); !ok || got != content {
		t.Fatalf("persisted Get after reopen failed: %v %q", ok, got)
	}
	// Check truncated also
	full2, h8_2 := hashContent(content2)
	if got, ok := s2.Get(h8_2); !ok || got != content2 {
		t.Fatalf("persisted second trunc Get failed")
	}
	if got, ok := s2.Get(full2); !ok || got != content2 {
		t.Fatalf("persisted second full Get failed")
	}
	// Cleanup should delete from DB as well
	s2.Cleanup(30) // 30-5=25 >=20 expire both
	if _, ok := s2.Get(hash); ok {
		t.Fatalf("after cleanup, should be gone")
	}
	s2.Close()
	// Reopen again, should still be gone
	s3 := New(20, dbPath)
	defer s3.Close()
	if _, ok := s3.Get(hash); ok {
		t.Fatalf("after cleanup persist, should remain gone after reopen")
	}
}

func TestFreshnessEncodeExpiry(t *testing.T) {
	s := New(3, "")
	content := "freshness encode test"
	// Encode at turn 1 is canonical
	if enc := s.Encode(content, 1); s.IsRef(enc) {
		t.Fatalf("first should not be ref")
	}
	// Encode at turn 2 within freshness (2-1=1<3) should be ref
	if enc := s.Encode(content, 2); !s.IsRef(enc) {
		t.Fatalf("second within freshness should be ref")
	}
	// Encode at turn 5: 5-1=4 >=3 expired, should be original (new canonical)
	if enc := s.Encode(content, 5); s.IsRef(enc) {
		t.Fatalf("after expiry should be original, got ref %q", enc)
	}
	// Next at 6 should be ref again (new canonical at 5)
	if enc := s.Encode(content, 6); !s.IsRef(enc) {
		t.Fatalf("after new canonical, next should be ref")
	}
}

func TestExpand_Alias(t *testing.T) {
	s := New(20, "")
	c := "expand test"
	h := s.Put(c, 1)
	if got, ok := s.Expand(h); !ok || got != c {
		t.Fatalf("Expand full failed")
	}
	if got, ok := s.Expand(h[:8]); !ok || got != c {
		t.Fatalf("Expand trunc failed")
	}
	wrapped := formatRef(h[:8], len(c))
	if got, ok := s.Expand(wrapped); !ok || got != c {
		t.Fatalf("Expand wrapped failed")
	}
}

func TestPut_PreservesCanonicalTurn(t *testing.T) {
	s := New(20, "")
	content := "preserve turn test"
	s.Put(content, 1)
	// Put same content at turn 2 should not update turn
	s.Put(content, 2)
	// Cleanup at turn 21 should still expire based on original turn 1 (21-1=20 >=20 => expired)
	// If Put had updated turn to 2, then 21-2=19 <20 not expired
	removed := s.Cleanup(21)
	if removed != 1 {
		t.Fatalf("should be expired based on original turn, removed %d", removed)
	}
}

func TestEmptyContent(t *testing.T) {
	s := New(20, "")
	empty := ""
	hash := s.Put(empty, 1)
	if hash == "" {
		t.Fatalf("empty hash empty")
	}
	if got, ok := s.Get(hash); !ok || got != empty {
		t.Fatalf("empty Get failed")
	}
	// For empty, test fresh store without prior Put
	s2 := New(20, "")
	encFirst := s2.Encode(empty, 1)
	if s2.IsRef(encFirst) {
		t.Fatalf("empty first Encode should be original (empty is still content)")
	}
	encSecond := s2.Encode(empty, 2)
	if !s2.IsRef(encSecond) {
		t.Fatalf("empty second should be ref")
	}
	// Verify
	if !s2.Verify(empty, encSecond) {
		t.Fatalf("empty Verify failed")
	}
	if _, ok := s2.Decode(encFirst); ok {
		t.Fatalf("Decode empty original should not be ref")
	}
}

// Helpers

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func findCollision(t *testing.T) (string, string) {
	t.Helper()
	seen := make(map[string]string) // h8 -> str
	for i := 0; i < 300000; i++ {
		candidate := fmt.Sprintf("collision-payload-%d-%d", i, i*7)
		_, h8 := hashContent(candidate)
		// Also compute via direct sha to ensure same as hashContent
		// Use helper to confirm
		if prev, ok := seen[h8]; ok {
			// found collision
			// verify they are different strings and different full
			f1, _ := hashContent(prev)
			f2, h8_2 := hashContent(candidate)
			if f1 != f2 && h8_2 == h8 {
				return prev, candidate
			}
		} else {
			seen[h8] = candidate
		}
	}
	return "", ""
}

// Ensure hashContent matches direct sha
func TestHashContentConsistency(t *testing.T) {
	content := "test"
	full, h8 := hashContent(content)
	h := sha256.Sum256([]byte(content))
	expFull := hex.EncodeToString(h[:])
	if full != expFull || h8 != expFull[:8] {
		t.Fatalf("hashContent mismatch")
	}
}
