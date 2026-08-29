package cache

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAddBreakpointIsMetadataOnlyAndDoesNotMutateInput(t *testing.T) {
	original := []Message{
		{Role: "system", Content: "keep this system payload"},
		{Role: "user", Content: "keep this user payload"},
		{Role: "assistant", Content: "keep this assistant payload", CacheControl: &Breakpoint{Type: "existing"}},
	}
	wantInput := append([]Message(nil), original...)
	wantInput[2].CacheControl = &Breakpoint{Type: "existing"}

	got := AddBreakpoint(original, 1)

	if !reflect.DeepEqual(original, wantInput) {
		t.Fatalf("AddBreakpoint mutated input: got %#v want %#v", original, wantInput)
	}
	if len(got) != len(original) {
		t.Fatalf("length changed: got %d want %d", len(got), len(original))
	}
	for i := range original {
		if got[i].Role != original[i].Role || got[i].Content != original[i].Content {
			t.Fatalf("payload changed at index %d: got %#v want %#v", i, got[i], original[i])
		}
	}
	if got[1].CacheControl == nil {
		t.Fatal("expected breakpoint metadata at requested position")
	}
	if got[1].CacheControl.Type != "ephemeral" || got[1].CacheControl.Position != 1 {
		t.Fatalf("unexpected breakpoint: %#v", got[1].CacheControl)
	}
	if got[2].CacheControl == original[2].CacheControl {
		t.Fatal("expected metadata pointers to be copied")
	}

	got[2].CacheControl.Type = "changed"
	if original[2].CacheControl.Type != "existing" {
		t.Fatal("mutating output metadata changed input metadata")
	}
}

func TestAddBreakpointOutOfBoundsAndEmptyInputs(t *testing.T) {
	for _, position := range []int{-1, 1} {
		input := []Message{{Role: "user", Content: "payload"}}
		got := AddBreakpoint(input, position)
		if !reflect.DeepEqual(got, input) {
			t.Fatalf("out-of-bounds position %d changed messages: got %#v want %#v", position, got, input)
		}
	}
	if got := AddBreakpoint(nil, 0); got == nil || len(got) != 0 {
		t.Fatalf("empty result must be a non-nil empty slice: %#v", got)
	}
}

func TestStablePrefixUsesLastBreakpointAndCopiesMessages(t *testing.T) {
	input := []Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "first", CacheControl: &Breakpoint{Type: "ephemeral", Position: 1}},
		{Role: "assistant", Content: "middle"},
		{Role: "user", Content: "last", CacheControl: &Breakpoint{Type: "ephemeral", Position: 3}},
	}

	got := StablePrefix(input)
	if len(got) != 4 {
		t.Fatalf("stable prefix length: got %d want 4", len(got))
	}
	if got[3].Content != "last" {
		t.Fatalf("stable prefix did not include last breakpoint: %#v", got)
	}
	if got[1].CacheControl == input[1].CacheControl {
		t.Fatal("expected StablePrefix to copy metadata pointers")
	}
	got[1].CacheControl.Type = "changed"
	if input[1].CacheControl.Type != "ephemeral" {
		t.Fatal("mutating stable prefix changed input metadata")
	}

	if got := StablePrefix([]Message{{Role: "user", Content: "no marker"}}); got == nil || len(got) != 0 {
		t.Fatalf("missing breakpoint must return non-nil empty prefix: %#v", got)
	}
}

func TestCacheMetadataSerializationIsCanonicalAndDeterministic(t *testing.T) {
	metadata := PrefixCache{
		Breakpoints: []Breakpoint{
			{Position: 2, Type: "ephemeral"},
			{Position: 0, Type: "ephemeral"},
		},
	}
	want := `{"breakpoints":[{"position":2,"type":"ephemeral"},{"position":0,"type":"ephemeral"}]}`

	got, err := MarshalMetadata(metadata)
	if err != nil {
		t.Fatalf("MarshalMetadata returned error: %v", err)
	}
	if string(got) != want {
		t.Fatalf("unexpected canonical metadata: got %q want %q", got, want)
	}
	serialized, err := SerializeMetadata(metadata)
	if err != nil {
		t.Fatalf("SerializeMetadata returned error: %v", err)
	}
	if serialized != want {
		t.Fatalf("SerializeMetadata mismatch: got %q want %q", serialized, want)
	}

	var decoded PrefixCache
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("serialized metadata is not JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, metadata) {
		t.Fatalf("metadata round-trip changed value: got %#v want %#v", decoded, metadata)
	}
}

func TestSortToolsStableAndNonMutating(t *testing.T) {
	input := []Tool{
		{Name: "same", Description: "first"},
		{Name: "z"},
		{Name: "same", Description: "second"},
		{Name: "a"},
	}
	wantInput := append([]Tool(nil), input...)

	got := SortTools(input)
	want := []Tool{
		{Name: "a"},
		{Name: "same", Description: "first"},
		{Name: "same", Description: "second"},
		{Name: "z"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected stable order: got %#v want %#v", got, want)
	}
	if !reflect.DeepEqual(input, wantInput) {
		t.Fatalf("SortTools mutated input: got %#v want %#v", input, wantInput)
	}
	if got := SortTools(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil tools must produce non-nil empty result: %#v", got)
	}
}

func TestCacheScopeKeyAndFreezeSchemaAreDeterministic(t *testing.T) {
	unsorted := []Tool{
		{Name: "b", InputSchema: map[string]any{"z": 2, "a": 1}},
		{Name: "a", Description: "first"},
		{Name: "same", Description: "one"},
		{Name: "same", Description: "two"},
	}
	sorted := []Tool{
		{Name: "a", Description: "first"},
		{Name: "b", InputSchema: map[string]any{"a": 1, "z": 2}},
		{Name: "same", Description: "one"},
		{Name: "same", Description: "two"},
	}

	key1 := CacheScopeKey("system", unsorted, "model")
	key2 := CacheScopeKey("system", sorted, "model")
	if key1 == "" || key1 != key2 {
		t.Fatalf("CacheScopeKey is not stable under tool permutation: %q vs %q", key1, key2)
	}
	if len(key1) != 64 {
		t.Fatalf("CacheScopeKey must be SHA-256 hex: %q", key1)
	}
	if key1 == CacheScopeKey("other system", sorted, "model") || key1 == CacheScopeKey("system", sorted, "other-model") {
		t.Fatal("scope key ignored system or model")
	}

	hash1 := FreezeSchema(unsorted)
	hash2 := FreezeSchema(sorted)
	if hash1 == "" || hash1 != hash2 {
		t.Fatalf("FreezeSchema is not stable under tool permutation: %q vs %q", hash1, hash2)
	}
	if len(hash1) != 64 {
		t.Fatalf("FreezeSchema must be SHA-256 hex: %q", hash1)
	}
}

func TestStabilizePrefixIsConservativeAndReversible(t *testing.T) {
	input := "system text\r\n" +
		"Timestamp: 2026-08-27T12:00:00Z\r\n" +
		"Request_ID = 550e8400-e29b-41d4-a716-446655440000\r\n" +
		"This prose contains UUID 123e4567-e89b-12d3-a456-426614174000.\r\n" +
		"URL: https://example.test/2026-08-27\r\n" +
		"```text\r\n" +
		"Timestamp: 1999-01-01T00:00:00Z\r\n" +
		"```\r\n" +
		"Date: 2026-08-27"

	stable, volatile := StabilizePrefix(input)
	if stable == input || volatile == "" {
		t.Fatalf("expected volatile metadata extraction: stable=%q volatile=%q", stable, volatile)
	}
	if strings.Contains(stable, "2026-08-27T12:00:00Z") || strings.Contains(stable, "550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("volatile values remained in stable prefix: %q", stable)
	}
	for _, preserved := range []string{
		"system text\r\n",
		"This prose contains UUID 123e4567-e89b-12d3-a456-426614174000.\r\n",
		"URL: https://example.test/2026-08-27\r\n",
		"Timestamp: 1999-01-01T00:00:00Z\r\n",
	} {
		if !strings.Contains(stable, preserved) {
			t.Fatalf("conservative stabilization changed or removed %q from stable text %q", preserved, stable)
		}
	}
	if !strings.HasPrefix(volatile, "tokenmill-volatile-v1\n") {
		t.Fatalf("volatile suffix has unstable format: %q", volatile)
	}
	if !strings.Contains(volatile, "2026-08-27T12:00:00Z") || !strings.Contains(volatile, "550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("volatile suffix does not preserve extracted values: %q", volatile)
	}

	other, otherVolatile := StabilizePrefix(strings.ReplaceAll(strings.ReplaceAll(input, "2026-08-27T12:00:00Z", "2026-08-28T13:30:00Z"), "550e8400-e29b-41d4-a716-446655440000", "123e4567-e89b-12d3-a456-426614174000"))
	if stable != other {
		t.Fatalf("stable prefixes differ only because volatile values changed:\n%q\n%q", stable, other)
	}
	if volatile == otherVolatile {
		t.Fatal("volatile suffix did not change with volatile values")
	}

	restored, err := restoreStabilizedPrefix(stable, volatile)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if restored != input {
		t.Fatalf("stabilization round-trip changed input:\ngot  %q\nwant %q", restored, input)
	}
}

func TestStabilizePrefixLeavesUnmatchedAndEmptyInputsByteExact(t *testing.T) {
	for _, input := range []string{"", "plain prose\nhttps://example.test/2026-08-27\nUUID in prose: 550e8400-e29b-41d4-a716-446655440000"} {
		stable, volatile := StabilizePrefix(input)
		if stable != input || volatile != "" {
			t.Fatalf("unexpected change for input %q: stable=%q volatile=%q", input, stable, volatile)
		}
	}
}

func restoreStabilizedPrefix(stable, volatile string) (string, error) {
	const header = "tokenmill-volatile-v1\n"
	if volatile == "" {
		return stable, nil
	}
	if !strings.HasPrefix(volatile, header) {
		return "", &formatError{"missing volatile header"}
	}
	var records []struct {
		Index  int    `json:"index"`
		Line   string `json:"line"`
		Ending string `json:"ending"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(volatile, header)), &records); err != nil {
		return "", err
	}
	lines := splitTestPreservedLines(stable)
	for _, record := range records {
		if record.Index < 0 || record.Index >= len(lines) {
			return "", &formatError{"volatile index out of range"}
		}
		wantPlaceholder := "[tokenmill:volatile:" + testItoa(record.Index) + "]"
		if lines[record.Index].content != wantPlaceholder {
			return "", &formatError{"volatile placeholder mismatch"}
		}
		lines[record.Index] = testPreservedLine{content: record.Line, ending: record.Ending}
	}
	return joinTestPreservedLines(lines), nil
}

type formatError struct{ message string }

func (e *formatError) Error() string { return e.message }

type testPreservedLine struct {
	content string
	ending  string
}

func splitTestPreservedLines(input string) []testPreservedLine {
	if input == "" {
		return nil
	}
	lines := make([]testPreservedLine, 0, strings.Count(input, "\n")+1)
	start := 0
	for i := 0; i < len(input); i++ {
		if input[i] != '\n' {
			continue
		}
		content := input[start:i]
		ending := "\n"
		if strings.HasSuffix(content, "\r") {
			content = strings.TrimSuffix(content, "\r")
			ending = "\r\n"
		}
		lines = append(lines, testPreservedLine{content: content, ending: ending})
		start = i + 1
	}
	if start < len(input) {
		lines = append(lines, testPreservedLine{content: input[start:]})
	}
	return lines
}

func joinTestPreservedLines(lines []testPreservedLine) string {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.content)
		builder.WriteString(line.ending)
	}
	return builder.String()
}

func testItoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}
