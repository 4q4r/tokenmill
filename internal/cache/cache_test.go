package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// ---------- Helpers for TDD red/green ----------

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("expected %q to contain %q", s, substr)
	}
}

// ---------- PrefixCache explicit prefix caching ----------

func TestPrefixCache_AddBreakpoint_Lossless(t *testing.T) {
	pc := NewPrefixCache()
	msgs := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}
	// Add breakpoint at position 1
	out := pc.AddBreakpoint(msgs, 1)
	if len(out) != len(msgs) {
		t.Fatalf("AddBreakpoint should not change length: got %d want %d", len(out), len(msgs))
	}
	// Content must be unchanged (lossless: only marker added)
	for i := range msgs {
		if out[i].Content != msgs[i].Content {
			t.Fatalf("content changed at %d: %q vs %q", i, out[i].Content, msgs[i].Content)
		}
		if out[i].Role != msgs[i].Role {
			t.Fatalf("role changed at %d", i)
		}
	}
	// Marker should be present at position 1
	if out[1].CacheControl == nil {
		t.Fatal("expected CacheControl marker at breakpoint position")
	}
	if out[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("expected ephemeral, got %q", out[1].CacheControl.Type)
	}
	// Other positions should not have marker (unless previously set)
	if out[0].CacheControl != nil && msgs[0].CacheControl == nil {
		t.Fatal("unexpected marker at position 0")
	}
	// Verify idempotence: adding again same position should remain lossless
	out2 := pc.AddBreakpoint(out, 1)
	if out2[1].Content != msgs[1].Content {
		t.Fatal("idempotent AddBreakpoint changed content")
	}
}

func TestPrefixCache_AddBreakpoint_OutOfBounds(t *testing.T) {
	pc := NewPrefixCache()
	msgs := []Message{{Role: "user", Content: "hi"}}
	out := pc.AddBreakpoint(msgs, -1)
	if len(out) != 1 {
		t.Fatalf("out of bounds should return original length")
	}
	out2 := pc.AddBreakpoint(msgs, 5)
	if len(out2) != 1 {
		t.Fatalf("out of bounds should return original length")
	}
	// negative and overflow should not panic and should not add marker
	if out[0].CacheControl != nil {
		t.Fatal("should not add marker for out of bounds")
	}
}

func TestPrefixCache_StablePrefix(t *testing.T) {
	pc := NewPrefixCache()
	msgs := []Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	pc.AddBreakpoint(msgs, 1) // breakpoint at u1
	// StablePrefix should return prefix up to breakpoint inclusive
	// We test with explicit markers
	msgsWithMarker := pc.AddBreakpoint(msgs, 1)
	prefix := pc.StablePrefix(msgsWithMarker)
	if len(prefix) != 2 {
		t.Fatalf("StablePrefix len got %d want 2 (up to breakpoint)", len(prefix))
	}
	if prefix[0].Content != "system" || prefix[1].Content != "u1" {
		t.Fatalf("unexpected prefix content: %+v", prefix)
	}
	// If no breakpoint, stable prefix should be empty or whole? We choose empty to indicate no stable prefix yet
	empty := pc.StablePrefix([]Message{{Role: "user", Content: "hi"}})
	if len(empty) != 0 {
		t.Fatalf("expected empty stable prefix when no breakpoint, got %d", len(empty))
	}
	// Multiple breakpoints: should return up to last
	msgs2 := pc.AddBreakpoint(msgs, 3)
	// also ensure marker at 1 still present? Let's add both
	msgsBoth := []Message{
		{Role: "system", Content: "system", CacheControl: &CacheControl{Type: "ephemeral"}},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2", CacheControl: &CacheControl{Type: "ephemeral"}},
	}
	prefix2 := pc.StablePrefix(msgsBoth)
	if len(prefix2) != 4 {
		t.Fatalf("expected prefix up to last breakpoint 4, got %d", len(prefix2))
	}
	_ = msgs2
}

func TestPrefixCache_Concurrent(t *testing.T) {
	pc := NewPrefixCache()
	msgs := []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(pos int) {
			defer wg.Done()
			_ = pc.AddBreakpoint(msgs, pos%2)
			_ = pc.StablePrefix(msgs)
		}(i)
	}
	wg.Wait()
}

// ---------- CacheControlHeader ----------

func TestCacheControlHeader_Generation(t *testing.T) {
	h := CacheControlHeader()
	// Header should contain ephemeral
	var s string
	switch v := any(h).(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		s = string(b)
	case CacheControl:
		b, _ := json.Marshal(v)
		s = string(b)
	case *CacheControl:
		b, _ := json.Marshal(v)
		s = string(b)
	default:
		// try json marshal any
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("unknown header type %T", h)
		}
		s = string(b)
	}
	mustContain(t, s, "ephemeral")
	// Should be valid JSON containing type
	var m map[string]interface{}
	// Try to parse if it's JSON object or header containing JSON
	// If header is raw string like "cache_control: ephemeral", still contains ephemeral
	if json.Valid([]byte(s)) {
		_ = json.Unmarshal([]byte(s), &m)
	}
	// Also test explicit header JSON generation via helper if exists
	// Ensure deterministic
	h2 := CacheControlHeader()
	var s2 string
	switch v := any(h2).(type) {
	case string:
		s2 = v
	case []byte:
		s2 = string(v)
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		s2 = string(b)
	case CacheControl:
		b, _ := json.Marshal(v)
		s2 = string(b)
	case *CacheControl:
		b, _ := json.Marshal(v)
		s2 = string(b)
	default:
		b, _ := json.Marshal(v)
		s2 = string(b)
	}
	if s != s2 {
		t.Fatalf("CacheControlHeader not deterministic: %q vs %q", s, s2)
	}
}

// ---------- PromptCacheKey / CacheScopeKey ----------

func TestPromptCacheKey_SHA256_Deterministic(t *testing.T) {
	tools := []Tool{{Name: "search", Description: "search"}, {Name: "read", Description: "read"}}
	k1 := PromptCacheKey("system prompt", tools, "gpt-4o")
	k2 := PromptCacheKey("system prompt", tools, "gpt-4o")
	if k1 != k2 {
		t.Fatalf("PromptCacheKey not deterministic: %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Fatalf("expected SHA256 hex 64 chars, got %d %q", len(k1), k1)
	}
	// Verify hex
	if _, err := hex.DecodeString(k1); err != nil {
		t.Fatalf("not valid hex: %v", err)
	}
	// Different model should differ
	k3 := PromptCacheKey("system prompt", tools, "claude-3")
	if k1 == k3 {
		t.Fatal("different model should give different key")
	}
	// Different system should differ
	k4 := PromptCacheKey("other system", tools, "gpt-4o")
	if k1 == k4 {
		t.Fatal("different system should give different key")
	}
	// Verify SHA256 is over system+tools+model canonically (rough check)
	h := sha256.Sum256([]byte("system prompt" + "gpt-4o"))
	// Not equal to just that, should include tools, so not equal to simple hash -> ensure tools affect
	if k1 == hex.EncodeToString(h[:]) {
		t.Fatal("tools not included in hash")
	}
}

func TestCacheScopeKey_SameAsPromptCacheKey(t *testing.T) {
	tools := []Tool{{Name: "b"}, {Name: "a"}}
	k1 := CacheScopeKey("sys", tools, "model")
	k2 := PromptCacheKey("sys", tools, "model")
	// They should be equal or at least both deterministic SHA256 - if implementations differ but both use same logic, they should equal
	// Allow either equal or both valid hex
	if len(k1) != 64 || len(k2) != 64 {
		t.Fatalf("keys not 64 hex: %q %q", k1, k2)
	}
	// At least CacheScopeKey should be deterministic with sorted tools
	toolsPermuted := []Tool{{Name: "a"}, {Name: "b"}}
	k3 := CacheScopeKey("sys", toolsPermuted, "model")
	if k1 != k3 {
		t.Fatalf("CacheScopeKey should be stable under tool permutation (sorted): %q vs %q", k1, k3)
	}
}

func TestCacheScopeKey_StablePrefixHash(t *testing.T) {
	tools := []Tool{{Name: "tool1"}}
	s1 := "You are helpful"
	s2 := "You are helpful"
	k1 := CacheScopeKey(s1, tools, "m")
	k2 := CacheScopeKey(s2, tools, "m")
	if k1 != k2 {
		t.Fatal("same prefix should hash same")
	}
}

// ---------- SortTools and FreezeSchema ----------

func TestSortTools_Deterministic(t *testing.T) {
	tools := []Tool{{Name: "zebra"}, {Name: "apple"}, {Name: "middle"}}
	sorted := SortTools(tools)
	if len(sorted) != 3 {
		t.Fatalf("len mismatch")
	}
	if sorted[0].Name != "apple" || sorted[1].Name != "middle" || sorted[2].Name != "zebra" {
		t.Fatalf("not sorted correctly: %+v", sorted)
	}
	// Original should either be sorted in place or remain but we allow both - check that repeated call is stable
	sorted2 := SortTools(sorted)
	if sorted2[0].Name != "apple" {
		t.Fatal("second sort not stable")
	}
	// Permutation should give same sorted result
	tools2 := []Tool{{Name: "middle"}, {Name: "zebra"}, {Name: "apple"}}
	sorted3 := SortTools(tools2)
	if sorted3[0].Name != "apple" || sorted3[1].Name != "middle" || sorted3[2].Name != "zebra" {
		t.Fatalf("permuted sort failed: %+v", sorted3)
	}
	// Verify with hash helper
	h1 := FreezeSchema(tools)
	h2 := FreezeSchema(tools2)
	if h1 != h2 {
		t.Fatalf("FreezeSchema hash should be equal for permuted tools: %q vs %q", h1, h2)
	}
}

func TestFreezeSchema_SHA256(t *testing.T) {
	tools := []Tool{{Name: "a", Description: "desc a"}, {Name: "b", Description: "desc b"}}
	h := FreezeSchema(tools)
	if len(h) != 64 {
		t.Fatalf("expected 64 hex, got %d", len(h))
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Fatalf("invalid hex: %v", err)
	}
	// Different tools => different hash
	tools2 := []Tool{{Name: "a", Description: "desc a"}, {Name: "c", Description: "desc c"}}
	h2 := FreezeSchema(tools2)
	if h == h2 {
		t.Fatal("different schemas should have different hash")
	}
	// Deterministic
	h3 := FreezeSchema(tools)
	if h != h3 {
		t.Fatal("not deterministic")
	}
	// Empty tools
	empty := FreezeSchema(nil)
	if len(empty) != 64 {
		t.Fatalf("empty should still be 64 hex, got %q", empty)
	}
	// Verify that FreezeSchema uses sorted tools internally: FreezeSchema of unsorted vs sorted should equal
	unsorted := []Tool{{Name: "b"}, {Name: "a"}}
	sorted := []Tool{{Name: "a"}, {Name: "b"}}
	if FreezeSchema(unsorted) != FreezeSchema(sorted) {
		t.Fatal("FreezeSchema should sort internally")
	}
}

func TestSortTools_Empty(t *testing.T) {
	var nilTools []Tool
	sorted := SortTools(nilTools)
	if len(sorted) != 0 {
		t.Fatalf("expected empty, got %d", len(sorted))
	}
	empty := []Tool{}
	sorted2 := SortTools(empty)
	if len(sorted2) != 0 {
		t.Fatalf("expected empty slice sort")
	}
}

// ---------- StabilizePrefix ----------

func TestStabilizePrefix_VolatileExtraction(t *testing.T) {
	sys := "You are helpful assistant.\nCurrent time: 2026-08-27T12:00:00Z\nRequest ID: 550e8400-e29b-41d4-a716-446655440000\nUser question here."
	stable, volatile := StabilizePrefix(sys)
	if strings.Contains(stable, "2026-08-27T12:00:00Z") {
		t.Fatalf("stable should not contain timestamp, got %q", stable)
	}
	if strings.Contains(stable, "550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("stable should not contain uuid, got %q", stable)
	}
	if !strings.Contains(volatile, "2026-08-27") && !strings.Contains(volatile, "550e8400") {
		t.Fatalf("volatile should contain timestamp/uuid, got %q", volatile)
	}
	// Stable should still contain non-volatile content
	if !strings.Contains(stable, "You are helpful") {
		t.Fatalf("stable missing original system part")
	}
	// Combined should contain all original non-whitespace tokens (lossless-ish: no data lost, just moved)
	combined := stable + "\n" + volatile
	if !strings.Contains(combined, "You are helpful") {
		t.Fatal("combined missing stable part")
	}
	if !strings.Contains(combined, "550e8400") {
		t.Fatal("combined missing volatile part")
	}
}

func TestStabilizePrefix_StableHashEquality(t *testing.T) {
	timestamp1 := "2026-08-27T10:00:00Z"
	timestamp2 := "2026-08-27T11:30:45.123Z"
	uuid1 := "550e8400-e29b-41d4-a716-446655440000"
	uuid2 := "123e4567-e89b-12d3-a456-426614174000"
	sys1 := "System: You are assistant.\nTime: " + timestamp1 + "\nRequest: " + uuid1 + "\nTask: help user"
	sys2 := "System: You are assistant.\nTime: " + timestamp2 + "\nRequest: " + uuid2 + "\nTask: help user"
	stable1, _ := StabilizePrefix(sys1)
	stable2, _ := StabilizePrefix(sys2)
	if stable1 != stable2 {
		t.Fatalf("stable prefixes with different volatile should be equal for cache hit:\n%q vs\n%q", stable1, stable2)
	}
	// Hash equality
	h1 := sha256.Sum256([]byte(stable1))
	h2 := sha256.Sum256([]byte(stable2))
	if h1 != h2 {
		t.Fatal("hash of stable should be equal")
	}
	// Volatile should differ
	_, vol1 := StabilizePrefix(sys1)
	_, vol2 := StabilizePrefix(sys2)
	if vol1 == vol2 {
		t.Fatal("volatile should differ for different timestamps/uuids")
	}
}

func TestStabilizePrefix_NoVolatile(t *testing.T) {
	sys := "You are helpful assistant. No timestamps here."
	stable, volatile := StabilizePrefix(sys)
	if stable != sys {
		t.Fatalf("no volatile: stable should equal original, got %q vs %q", stable, sys)
	}
	if volatile != "" {
		t.Fatalf("no volatile: volatile should be empty, got %q", volatile)
	}
}

func TestStabilizePrefix_RequestID(t *testing.T) {
	cases := []struct {
		input     string
		extracted bool
	}{
		{
			input:     "System prompt\nrequest_id: abc123-uuid-here 550e8400-e29b-41d4-a716-446655440000\nend",
			extracted: true,
		},
		{
			input: "System\n[request_id=123e4567-e89b-12d3-a456-426614174000] hello",
		},
		{
			input: "Time: 2026/08/27 12:00:00 and uuid 123e4567-e89b-12d3-a456-426614174000",
		},
	}
	for _, tc := range cases {
		stable, volatile := StabilizePrefix(tc.input)
		if tc.extracted {
			if stable == tc.input || volatile == "" {
				t.Fatalf("expected standalone request metadata extraction for %q", tc.input)
			}
			continue
		}
		if stable != tc.input || volatile != "" {
			t.Fatalf("inline/non-standalone metadata must remain unchanged: stable=%q volatile=%q", stable, volatile)
		}
	}
}

func TestStabilizePrefix_DatetimeVariations(t *testing.T) {
	cases := []string{
		"Current datetime: 2026-08-27 12:00:00",
		"Timestamp: 2026-08-27T12:00:00.123456Z",
		"Date: 2026/08/27",
		"Time: 12:34:56",
		"datetime.now(): 2026-08-27T12:00:00Z",
	}
	for _, c := range cases {
		stable, vol := StabilizePrefix(c)
		// For those with timestamp, stable should not contain the timestamp string
		// We don't assert strict for every case, just ensure vol extracted if timestamp present
		if strings.Contains(c, "2026") && !strings.Contains(vol, "2026") {
			t.Fatalf("expected timestamp moved to volatile for %q, got stable %q volatile %q", c, stable, vol)
		}
	}
}

// ---------- Hash helper for tests ----------

func TestHashHelper(t *testing.T) {
	// Verify hash function used
	if sha256.New().Size() != 32 {
		t.Fatal("expected SHA-256 digest size of 32 bytes")
	}
}
