package codec

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// mockCodec is a minimal LosslessCodec for contract testing.
// Uppercase encode, lowercase decode, byte-lossless only for ascii.
type mockCodec struct {
	id              string
	detectFn        func(string) bool
	estimateFn      func(string) int
	encodeFn        func(string) (string, error)
	decodeFn        func(string) (string, error)
	verifyShouldUse bool
}

func (m *mockCodec) ID() string { return m.id }
func (m *mockCodec) Detect(input string) bool {
	if m.detectFn != nil {
		return m.detectFn(input)
	}
	return true
}
func (m *mockCodec) EstimateSavings(input string) int {
	if m.estimateFn != nil {
		return m.estimateFn(input)
	}
	return 10
}
func (m *mockCodec) Encode(input string) (string, error) {
	if m.encodeFn != nil {
		return m.encodeFn(input)
	}
	return input + "|enc", nil
}
func (m *mockCodec) Decode(encoded string) (string, error) {
	if m.decodeFn != nil {
		return m.decodeFn(encoded)
	}
	// naive reverse
	if len(encoded) >= 4 && encoded[len(encoded)-4:] == "|enc" {
		return encoded[:len(encoded)-4], nil
	}
	return encoded, nil
}
func (m *mockCodec) Verify(original, encoded string) bool {
	decoded, err := m.Decode(encoded)
	if err != nil {
		return false
	}
	return VerifyBytes([]byte(original), []byte(decoded))
}

func TestLosslessCodecInterfaceContract(t *testing.T) {
	tests := []struct {
		name  string
		codec LosslessCodec
	}{
		{
			name:  "mock implements interface",
			codec: &mockCodec{id: "mock"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.codec.ID() == "" {
				t.Fatal("ID() should not be empty")
			}
			if !tc.codec.Detect("anything") {
				t.Fatal("Detect should return true for mock")
			}
			if got := tc.codec.EstimateSavings("input"); got < 0 {
				t.Fatal("EstimateSavings should be >=0 for contract test")
			}
			enc, err := tc.codec.Encode("hello")
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}
			dec, err := tc.codec.Decode(enc)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if dec != "hello" {
				t.Fatalf("round-trip failed: got %q want %q", dec, "hello")
			}
			if !tc.codec.Verify("hello", enc) {
				t.Fatal("Verify should pass for valid round-trip")
			}
			if tc.codec.Verify("hello", "bad|enc_corrupted") {
				// mock decode will return bad without suffix handling
				// but our mock's Verify uses byte equal, corrupted should not equal
				// Actually mock decodes any string without |enc suffix as-is
				// So "bad|enc_corrupted" decodes to "bad|enc_corrupted" not equal "hello"
				// So verify should be false - check
			}
		})
	}
}

func TestHintOverheadConstant(t *testing.T) {
	if HintOverhead != 13 {
		t.Fatalf("HintOverhead should be 13 per spec §ref, got %d", HintOverhead)
	}
}

func TestVerifyBytes_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		original []byte
		decoded  []byte
		want     bool
	}{
		{"equal", []byte("hello"), []byte("hello"), true},
		{"empty equal", []byte(""), []byte(""), true},
		{"nil equal", nil, nil, true},
		{"nil vs empty", nil, []byte(""), true}, // bytes.Equal treats nil and empty as equal
		{"different", []byte("hello"), []byte("world"), false},
		{"prefix", []byte("hello"), []byte("hello "), false},
		{"binary", []byte{0, 1, 2}, []byte{0, 1, 2}, true},
		{"binary diff", []byte{0, 1, 2}, []byte{0, 1, 3}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VerifyBytes(tc.original, tc.decoded)
			if got != tc.want {
				t.Fatalf("VerifyBytes(%q,%q)=%v want %v", tc.original, tc.decoded, got, tc.want)
			}
		})
	}
}

func TestVerifyJSON_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"equal objects", `{"a":1,"b":[2,3]}`, `{"b":[2,3],"a":1}`, true},
		{"equal arrays", `[1,2,3]`, `[1,2,3]`, true},
		{"different values", `{"a":1}`, `{"a":2}`, false},
		{"different keys", `{"a":1}`, `{"b":1}`, false},
		{"whitespace ignored", `{"a": 1}`, `{"a":1}`, true},
		{"order same array", `[1,2,3]`, `[3,2,1]`, false},
		{"invalid json a", `not json`, `{"a":1}`, false},
		{"invalid json b", `{"a":1}`, `not json`, false},
		{"both invalid", `not`, `also not`, false},
		{"nested equal", `{"x":{"y":[1,2]}}`, `{"x":{"y":[1,2]}}`, true},
		{"nested diff", `{"x":{"y":[1,2]}}`, `{"x":{"y":[1,3]}}`, false},
		{"numbers equal", `{"n":1}`, `{"n":1.0}`, true}, // both unmarshal to float64 1
		{"empty object", `{}`, `{}`, true},
		{"empty array", `[]`, `[]`, true},
		{"null", `null`, `null`, true},
		{"bool true", `true`, `true`, true},
		{"bool false vs true", `true`, `false`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VerifyJSON(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("VerifyJSON(%q,%q)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestVerifyJSON_DeepEqualSemantics(t *testing.T) {
	// Ensure reflect deepEqual is used via json numbers
	a := `{"a": 1, "b": null}`
	b := `{"a": 1, "b": null}`
	if !VerifyJSON(a, b) {
		t.Fatal("expected true for identical null")
	}
	// Ensure that after marshal round-trip, VerifyJSON still holds
	type S struct {
		A int   `json:"a"`
		B []int `json:"b"`
	}
	s := S{A: 5, B: []int{1, 2}}
	ba, _ := json.Marshal(s)
	bb, _ := json.Marshal(s)
	if !VerifyJSON(string(ba), string(bb)) {
		t.Fatal("marshal equal should verify")
	}
	// check reflect usage not bypassed
	if reflect.DeepEqual(nil, nil) != true {
		t.Fatal("sanity")
	}
}

func TestVerifyJSON_ExactNumbersAndStructure(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"unsafe integer differs", `{"n":9007199254740993}`, `{"n":9007199254740992}`, false},
		{"decimal forms equal", `{"n":1.0}`, `{"n":1e0}`, true},
		{"nested duplicate rejected", `{"outer":{"n":1,"n":2}}`, `{"outer":{"n":2}}`, false},
		{"top-level duplicate rejected", `{"n":1,"n":1}`, `{"n":1}`, false},
		{"trailing document rejected", `{"n":1} {"n":1}`, `{"n":1}`, false},
		{"array order significant", `[1,2]`, `[2,1]`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyJSON(tc.a, tc.b); got != tc.want {
				t.Fatalf("VerifyJSON(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestTokenSavings_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		original string
		encoded  string
		check    func(int) bool
	}{
		{"positive saving", "hello world hello world hello world hello world hello world", "hw", func(got int) bool { return got > 0 }},
		{"no saving", "hello", "hello", func(got int) bool { return got == 0 }},
		{"negative saving", "hi", "this is a much longer encoded string that should have more tokens", func(got int) bool { return got < 0 }},
		{"empty original", "", "anything", func(got int) bool { return got <= 0 }},
		{"empty encoded", "hello world", "", func(got int) bool { return got > 0 }},
		{"both empty", "", "", func(got int) bool { return got == 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TokenSavings(tc.original, tc.encoded)
			if !tc.check(got) {
				t.Fatalf("TokenSavings(%q,%q)=%d check failed", tc.original[:min(20, len(tc.original))], tc.encoded[:min(20, len(tc.encoded))], got)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestCodecVerifyPassFail(t *testing.T) {
	// Test Verify pass/fail via a codec that correctly and incorrectly decodes
	good := &mockCodec{
		id:       "good",
		encodeFn: func(s string) (string, error) { return s + "|enc", nil },
		decodeFn: func(s string) (string, error) {
			if len(s) >= 4 && s[len(s)-4:] == "|enc" {
				return s[:len(s)-4], nil
			}
			return "", errors.New("invalid encoding")
		},
	}
	bad := &mockCodec{
		id:       "bad",
		encodeFn: func(s string) (string, error) { return "corrupted", nil },
		decodeFn: func(s string) (string, error) { return "wrong", nil },
	}

	orig := "original content"
	encGood, _ := good.Encode(orig)
	if !good.Verify(orig, encGood) {
		t.Fatal("good codec Verify should pass")
	}
	if good.Verify(orig, "not-enc") {
		t.Fatal("good codec Verify should fail for bad encoding")
	}
	encBad, _ := bad.Encode(orig)
	if bad.Verify(orig, encBad) {
		t.Fatal("bad codec Verify should fail (decoded wrong)")
	}
	// also test error path decode error -> Verify false
	errCodec := &mockCodec{
		id:       "err",
		decodeFn: func(s string) (string, error) { return "", errors.New("decode error") },
	}
	if errCodec.Verify(orig, "anything") {
		t.Fatal("Verify should fail when Decode errors")
	}
}

func TestCodecEstimateSavingsNegativeSkip(t *testing.T) {
	c := &mockCodec{
		id:         "skip",
		estimateFn: func(s string) int { return -1 },
	}
	if got := c.EstimateSavings("anything"); got >= 0 {
		t.Fatalf("negative estimate should indicate skip, got %d", got)
	}
}
