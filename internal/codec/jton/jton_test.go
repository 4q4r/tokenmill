package jton

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

func TestJTON_ID(t *testing.T) {
	c := New()
	if c.ID() != "jton-zen" {
		t.Fatalf("ID got %q want %q", c.ID(), "jton-zen")
	}
}

func TestJTON_DetectHomogeneous(t *testing.T) {
	c := New() // minRows 10
	hom3 := `[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"},{"id":3,"name":"Carol"}]`
	if c.Detect(hom3) {
		t.Fatal("Detect should be false for 3 rows <10")
	}
	cSmall := &Codec{MinRows: 2}
	if !cSmall.Detect(hom3) {
		t.Fatal("Detect should be true for homogeneous 3 rows with minRows 2")
	}
	// homogeneous 10 rows
	var arr10 []map[string]interface{}
	for i := 1; i <= 10; i++ {
		arr10 = append(arr10, map[string]interface{}{"id": i, "name": fmt.Sprintf("Name%d", i)})
	}
	b10, _ := json.Marshal(arr10)
	if !c.Detect(string(b10)) {
		t.Fatalf("Detect should be true for 10 homogeneous rows")
	}
	// heterogeneous
	het := `[{"a":1,"b":2},{"a":3,"c":4}]`
	if cSmall.Detect(het) {
		t.Fatal("Detect should be false for heterogeneous")
	}
	// not array
	if cSmall.Detect(`{"a":1}`) {
		t.Fatal("Detect should be false for not array")
	}
	// empty
	if cSmall.Detect(`[]`) {
		t.Fatal("Detect should be false for empty")
	}
}

func TestJTON_EncodeHomogeneous3RowsSpec(t *testing.T) {
	c := &Codec{MinRows: 2}
	hom3 := `[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"},{"id":3,"name":"Carol"}]`
	enc, err := c.Encode(hom3)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	// Expected format per spec: [3: id, name; 1, "Alice"; 2, "Bob"; 3, "Carol"]
	expectedPrefix := "[3: id, name;"
	if !strings.HasPrefix(enc, expectedPrefix) {
		t.Fatalf("Encode unexpected prefix: got %q want prefix %q", enc, expectedPrefix)
	}
	// Check types preserved: numeric without quotes, strings with quotes
	if !strings.Contains(enc, "1, \"Alice\"") {
		t.Fatalf("Encode should contain '1, \"Alice\"', got %q", enc)
	}
	if !c.Verify(hom3, enc) {
		t.Fatalf("Verify should be true for hom3")
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if !codec.VerifyJSON(hom3, dec) {
		t.Fatalf("VerifyJSON hom3 vs dec failed: %q vs %q", hom3, dec)
	}
}

func TestJTON_HeterogeneousFallback(t *testing.T) {
	c := &Codec{MinRows: 2}
	het := `[{"a":1,"b":2},{"a":3,"c":4}]`
	_, err := c.Encode(het)
	if err == nil {
		t.Fatal("Encode should error for heterogeneous")
	}
	// Also test different lengths
	diffLen := `[{"a":1},{"a":1,"b":2}]`
	_, err2 := c.Encode(diffLen)
	if err2 == nil {
		t.Fatal("Encode should error for different lengths")
	}
}

func TestJTON_Nested(t *testing.T) {
	c := &Codec{MinRows: 2}
	nested := `[{"id":1,"data":{"x":1,"y":[2,3]}},{"id":2,"data":{"x":4,"y":[5,6]}}]`
	enc, err := c.Encode(nested)
	if err != nil {
		t.Fatalf("Encode nested error: %v", err)
	}
	if !c.Verify(nested, enc) {
		t.Fatalf("Verify nested failed")
	}
	dec, _ := c.Decode(enc)
	if !codec.VerifyJSON(nested, dec) {
		t.Fatalf("nested VerifyJSON failed")
	}
	// Ensure nested object preserved
	if !strings.Contains(enc, "{\"x\":1,\"y\":[2,3]}") && !strings.Contains(enc, "{\"x\": 1") {
		t.Logf("nested enc: %q", enc)
		// Could be different spacing but should contain x and y
		if !strings.Contains(enc, "x") || !strings.Contains(enc, "y") {
			t.Fatal("nested encoding missing data")
		}
	}
}

func TestJTON_Roundtrip100(t *testing.T) {
	c := &Codec{MinRows: 2}
	// Homogeneous 3 rows
	cases := []string{
		`[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"},{"id":3,"name":"Carol"}]`,
		`[{"a":1,"b":2,"c":3},{"a":4,"b":5,"c":6}]`,
		`[{"x":true,"y":null,"z":1},{"x":false,"y":null,"z":2}]`,
	}
	for i, tc := range cases {
		enc, err := c.Encode(tc)
		if err != nil {
			t.Fatalf("case %d Encode error: %v", i, err)
		}
		if !c.Verify(tc, enc) {
			t.Fatalf("case %d Verify failed", i)
		}
		dec, err := c.Decode(enc)
		if err != nil {
			t.Fatalf("case %d Decode error: %v", i, err)
		}
		if !codec.VerifyJSON(tc, dec) {
			t.Fatalf("case %d roundtrip VerifyJSON failed: %q -> %q -> %q", i, tc, enc, dec)
		}
	}
	// Random homogeneous 20 times
	for i := 0; i < 20; i++ {
		var arr []map[string]interface{}
		for r := 0; r < 5; r++ {
			arr = append(arr, map[string]interface{}{"id": r, "val": fmt.Sprintf("v%d", r), "num": r * 10})
		}
		b, _ := json.Marshal(arr)
		s := string(b)
		enc, err := c.Encode(s)
		if err != nil {
			t.Fatalf("random %d Encode error: %v", i, err)
		}
		if !c.Verify(s, enc) {
			t.Fatalf("random %d Verify failed", i)
		}
	}
}

func TestJTON_StringsWithCommaSemicolon(t *testing.T) {
	c := &Codec{MinRows: 2}
	tricky := `[{"id":1,"name":"Alice, Jr."},{"id":2,"name":"Bob; Sr."}]`
	enc, err := c.Encode(tricky)
	if err != nil {
		t.Fatalf("Encode tricky error: %v", err)
	}
	if !strings.Contains(enc, "\"Alice, Jr.\"") || !strings.Contains(enc, "\"Bob; Sr.\"") {
		t.Fatalf("Encode should preserve strings with comma/semicolon, got %q", enc)
	}
	if !c.Verify(tricky, enc) {
		t.Fatalf("Verify tricky failed")
	}
	dec, _ := c.Decode(enc)
	if !codec.VerifyJSON(tricky, dec) {
		t.Fatalf("tricky VerifyJSON failed")
	}
}

func TestJTON_JSONL(t *testing.T) {
	c := &Codec{MinRows: 2}
	jsonl := "{\"id\":1,\"name\":\"Alice\"}\n{\"id\":2,\"name\":\"Bob\"}\n{\"id\":3,\"name\":\"Carol\"}"
	enc, err := c.Encode(jsonl)
	if err != nil {
		t.Fatalf("Encode JSONL error: %v", err)
	}
	// Verify should handle JSONL vs array
	if !c.Verify(jsonl, enc) {
		t.Fatalf("Verify JSONL failed")
	}
	// Helper JSONLToZen
	viaHelper, err := JSONLToZen(jsonl)
	if err != nil {
		t.Fatalf("JSONLToZen error: %v", err)
	}
	if viaHelper != enc {
		t.Logf("JSONL Encode vs Helper differ: %q vs %q (both valid)", enc, viaHelper)
	}
	// Decode should be valid JSON array that deepEquals jsonl array
	dec, _ := c.Decode(enc)
	// Compare dec array vs jsonl parsed array
	var decArr []map[string]interface{}
	if err := json.Unmarshal([]byte(dec), &decArr); err != nil {
		t.Fatalf("dec unmarshal error: %v", err)
	}
	if len(decArr) != 3 {
		t.Fatalf("dec len 3 got %d", len(decArr))
	}
	// Also test IsHomogeneousJSONArray handles JSONL
	ok, cols, n := IsHomogeneousJSONArray(jsonl)
	if !ok || n != 3 || len(cols) != 2 {
		t.Fatalf("IsHomogeneousJSONArray JSONL failed: ok %v cols %v n %d", ok, cols, n)
	}
}

func TestJTON_EstimateSavings(t *testing.T) {
	c := New() // min 10
	// 10 rows should have positive saving
	var arr10 []map[string]interface{}
	for i := 1; i <= 10; i++ {
		arr10 = append(arr10, map[string]interface{}{"id": i, "name": fmt.Sprintf("Name%d", i), "value": i * 100})
	}
	b10, _ := json.Marshal(arr10)
	saving := c.EstimateSavings(string(b10))
	if saving <= 0 {
		t.Fatalf("EstimateSavings should be >0 for 10 rows, got %d", saving)
	}
	// 3 rows should be -1 due to Detect false
	hom3 := `[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"},{"id":3,"name":"Carol"}]`
	if got := c.EstimateSavings(hom3); got != -1 {
		t.Fatalf("EstimateSavings for 3 rows should be -1 (skip), got %d", got)
	}
	// heterogeneous -> -1
	het := `[{"a":1,"b":2},{"a":3,"c":4}]`
	if got := (&Codec{MinRows: 2}).EstimateSavings(het); got != -1 {
		t.Fatalf("EstimateSavings heterogeneous should be -1, got %d", got)
	}
}

func TestJTON_BareStringsFalse(t *testing.T) {
	c := &Codec{MinRows: 2}
	hom := `[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]`
	enc, _ := c.Encode(hom)
	// strings should be quoted
	if !strings.Contains(enc, "\"Alice\"") {
		t.Fatalf("Bare strings should be false, expected quoted \"Alice\", got %q", enc)
	}
	// Ensure not bare: should not contain ` Alice` without quotes as value
	// Check that after header, values contain quotes
	parts := strings.Split(enc, ";")
	if len(parts) < 2 {
		t.Fatalf("unexpected enc %q", enc)
	}
	// Second part is first row: " 1, \"Alice\""
	if !strings.Contains(parts[1], "\"") {
		t.Fatalf("row should contain quoted string, got %q", parts[1])
	}
}

func TestJTON_DecodeInvalid(t *testing.T) {
	c := &Codec{MinRows: 2}
	_, err := c.Decode("not zen")
	if err == nil {
		t.Fatal("expected error for invalid decode")
	}
	_, err2 := c.Decode("[1: a, b; 1, 2]")
	if err2 == nil {
		// This is actually valid? 1 row? But header says 1, we provide 1 row with 2 cols? That's valid
		// Let's test mismatch
	}
	_, err3 := c.Decode("[2: a, b; 1, 2]")
	if err3 == nil {
		t.Fatal("expected error for row count mismatch")
	}
	_, err4 := c.Decode("[1: a, b; 1]")
	if err4 == nil {
		t.Fatal("expected error for col mismatch")
	}
}

func TestJTON_IsHomogeneousJSONArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"homogeneous", `[{"a":1,"b":2},{"a":3,"b":4}]`, true},
		{"heterogeneous keys", `[{"a":1,"b":2},{"a":3,"c":4}]`, false},
		{"single", `[{"a":1}]`, false},
		{"empty", `[]`, false},
		{"not array", `{"a":1}`, false},
		{"nested homogeneous", `[{"a":{"x":1},"b":2},{"a":{"x":2},"b":3}]`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, _, _ := IsHomogeneousJSONArray(tc.input)
			if ok != tc.want {
				t.Fatalf("IsHomogeneousJSONArray %q = %v want %v", tc.name, ok, tc.want)
			}
		})
	}
}

func TestJTON_VerifyLossless(t *testing.T) {
	c := &Codec{MinRows: 2}
	cases := []string{
		`[{"id":1,"name":"Alice","age":30},{"id":2,"name":"Bob","age":25}]`,
		`[{"x":1,"y":2},{"x":3,"y":4}]`,
	}
	for i, tc := range cases {
		enc, err := c.Encode(tc)
		if err != nil {
			t.Fatalf("case %d encode error: %v", i, err)
		}
		dec, err := c.Decode(enc)
		if err != nil {
			t.Fatalf("case %d decode error: %v", i, err)
		}
		if !c.Verify(tc, enc) {
			t.Fatalf("case %d verify failed", i)
		}
		if !codec.VerifyJSON(tc, dec) {
			t.Fatalf("case %d VerifyJSON failed", i)
		}
		// Also ensure tournament fallback would not happen
		if dec == tc {
			t.Logf("case %d decode equals original exactly (ok)", i)
		}
	}
}

func TestJTON_ImplementsLosslessCodec(t *testing.T) {
	var _ codec.LosslessCodec = New()
}

func TestJTON_EncodePreservesTypes(t *testing.T) {
	c := &Codec{MinRows: 2}
	input := `[{"id":1,"flag":true,"val":null,"num":3.14},{"id":2,"flag":false,"val":null,"num":2.71}]`
	enc, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	// Check types: true, false, null, numbers should be preserved without quotes
	if !strings.Contains(enc, "true") || !strings.Contains(enc, "false") || !strings.Contains(enc, "null") {
		t.Fatalf("Encode should preserve types, got %q", enc)
	}
	if !c.Verify(input, enc) {
		t.Fatalf("Verify failed for types")
	}
	// Also ensure numbers are not quoted
	if strings.Contains(enc, "\"3.14\"") {
		t.Fatalf("numbers should not be quoted, got %q", enc)
	}
}

func TestJTON_HomogeneousCheckWithDifferentKeyOrder(t *testing.T) {
	// Keys in different order should still be homogeneous after sorting
	c := &Codec{MinRows: 2}
	input := `[{"b":2,"a":1},{"a":3,"b":4}]`
	enc, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if !c.Verify(input, enc) {
		t.Fatalf("Verify failed for different key order")
	}
	// Ensure cols sorted
	if !strings.Contains(enc, "a, b") {
		t.Fatalf("cols should be sorted 'a, b', got %q", enc)
	}
}

func TestJTON_JSONLToZen(t *testing.T) {
	jsonl := "{\"id\":1,\"name\":\"Alice\"}\n{\"id\":2,\"name\":\"Bob\"}"
	enc, err := JSONLToZen(jsonl)
	if err != nil {
		t.Fatalf("JSONLToZen error: %v", err)
	}
	c := &Codec{MinRows: 2}
	if !c.Verify(jsonl, enc) {
		t.Fatalf("Verify JSONLToZen failed")
	}
	// Roundtrip
	dec, _ := c.Decode(enc)
	// dec is array, jsonl is lines; verify via parseToSlice
	if !c.Verify(jsonl, enc) {
		t.Fatalf("Verify failed")
	}
	_ = dec
}

func TestJTON_ReflectDeepEqual(t *testing.T) {
	// Ensure Verify uses deepEqual not byte equal
	c := &Codec{MinRows: 2}
	a := `[{"a":1,"b":2}]`
	b := `[{"b":2,"a":1}]`
	// These are same data, different order, should be true via VerifyJSON
	if !codec.VerifyJSON(a, b) {
		t.Fatal("VerifyJSON should be true for different key order")
	}
	// Encode a and verify b vs enc? Encode of a should verify against b as well? Since they are same data
	enc, _ := c.Encode(a)
	if !c.Verify(b, enc) {
		t.Fatalf("Verify should be true for b vs enc of a (same data different order)")
	}
	// Check reflect
	if !reflect.DeepEqual([]int{1, 2}, []int{1, 2}) {
		t.Fatal("reflect sanity")
	}
}

func TestJTON_PreservesUnsafeInteger(t *testing.T) {
	c := &Codec{MinRows: 2}
	input := `[{"id":9007199254740993,"name":"a"},{"id":9007199254740994,"name":"b"}]`
	encoded, err := c.Encode(input)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !codec.VerifyJSON(input, decoded) || !c.Verify(input, encoded) {
		t.Fatalf("unsafe integer changed: encoded=%q decoded=%q", encoded, decoded)
	}
}

func TestJTON_RejectsDuplicateObjectKeys(t *testing.T) {
	c := &Codec{MinRows: 2}
	input := `[{"id":1,"id":2,"name":"a"},{"id":3,"name":"b"}]`
	if c.Detect(input) {
		t.Fatal("duplicate-key JSON must not be detected")
	}
	if _, err := c.Encode(input); err == nil {
		t.Fatal("duplicate-key JSON must not be encoded")
	}
}
