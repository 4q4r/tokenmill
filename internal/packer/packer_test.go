package packer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

func TestPackUnpackExactRepeatedBlocks(t *testing.T) {
	block := "event: request accepted\nrequest-id: 550e8400-e29b-41d4-a716-446655440000\nmetadata: " + strings.Repeat("x", 180) + "\n"
	input := []byte(block + block + "tail: done\n")

	p := New(Config{})
	packed, err := p.Pack(input)
	if err != nil {
		t.Fatalf("Pack error: %v", err)
	}
	if len(packed.Metadata.Dictionary) == 0 {
		t.Fatal("expected exact repeated block dictionary")
	}
	if len(packed.Metadata.References) == 0 {
		t.Fatal("expected block references")
	}
	if packed.Metadata.OriginalSize != len(input) {
		t.Fatalf("OriginalSize=%d want %d", packed.Metadata.OriginalSize, len(input))
	}
	wantHash := sha256.Sum256(input)
	if packed.Metadata.OriginalSHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("wrong provenance hash %q", packed.Metadata.OriginalSHA256)
	}
	decoded, err := Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack error: %v", err)
	}
	if !bytes.Equal(decoded, input) {
		t.Fatalf("unpack mismatch: got %q want %q", decoded, input)
	}
	if !Verify(input, packed) {
		t.Fatal("Verify should pass exact bytes")
	}
	if got := p.EstimateSavings(input); got <= 0 {
		t.Fatalf("EstimateSavings=%d want positive", got)
	}
	if !p.Detect(input) {
		t.Fatal("Detect should find repeated exact block")
	}
}

func TestCrossCallDictionaryAndProvenance(t *testing.T) {
	block := "row: " + strings.Repeat("same exact payload ", 20) + "\r\n"
	p := New(Config{})
	first, err := p.PackWithOptions([]byte(block+block), PackOptions{Source: "fixture-a"})
	if err != nil {
		t.Fatalf("first Pack error: %v", err)
	}
	if len(first.Metadata.Dictionary) == 0 {
		t.Fatal("first call should learn a dictionary entry")
	}
	second, err := p.PackWithOptions([]byte(block), PackOptions{Source: "fixture-b"})
	if err != nil {
		t.Fatalf("second Pack error: %v", err)
	}
	if len(second.Metadata.References) == 0 {
		t.Fatal("second call should use the cross-call exact dictionary")
	}
	if second.Metadata.Source != "fixture-b" {
		t.Fatalf("source provenance lost: %q", second.Metadata.Source)
	}
	decoded, err := p.Unpack(second)
	if err != nil {
		t.Fatalf("cross-call Unpack error: %v", err)
	}
	if !bytes.Equal(decoded, []byte(block)) {
		t.Fatal("cross-call unpack did not preserve CRLF bytes")
	}
}

func TestPackingIsDeterministicAndExactOnly(t *testing.T) {
	block := "same\n" + strings.Repeat("payload ", 50) + "\n"
	input := []byte(block + block + "different\n")
	p1 := New(Config{})
	p2 := New(Config{})
	one, err := p1.Pack(input)
	if err != nil {
		t.Fatalf("first Pack error: %v", err)
	}
	two, err := p2.Pack(input)
	if err != nil {
		t.Fatalf("second Pack error: %v", err)
	}
	if !bytes.Equal(one.Data, two.Data) {
		t.Fatalf("packed data is not deterministic")
	}
	if one.Metadata.Source != two.Metadata.Source || len(one.Metadata.Dictionary) != len(two.Metadata.Dictionary) {
		t.Fatalf("packed metadata is not deterministic")
	}
	near := []byte(block + strings.Replace(block, "payload", "payLoad", 1))
	nearPacked, err := New(Config{}).Pack(near)
	if err != nil {
		t.Fatalf("near-duplicate Pack error: %v", err)
	}
	if len(nearPacked.Metadata.References) != 0 {
		t.Fatal("near-duplicate blocks must not be packed as exact duplicates")
	}
}

func TestEmptyUTF8AndLimits(t *testing.T) {
	p := New(Config{})
	for _, input := range [][]byte{nil, {}, []byte("Привет")} {
		packed, err := p.Pack(input)
		if err != nil {
			t.Fatalf("Pack %q error: %v", input, err)
		}
		decoded, err := Unpack(packed)
		if err != nil || !bytes.Equal(decoded, input) {
			t.Fatalf("round-trip failed for %q: decoded=%q err=%v", input, decoded, err)
		}
	}
	limited := New(Config{MaxInputBytes: 8})
	if _, err := limited.Pack(bytes.Repeat([]byte{'x'}, 9)); err == nil {
		t.Fatal("input over configured limit should fail loudly")
	}
}

func TestInvalidConfigurationFailsLoudly(t *testing.T) {
	p := New(Config{MinBlockBytes: 16, MaxBlockBytes: 8})
	if _, err := p.Pack(nil); err == nil {
		t.Fatal("invalid configuration should not silently fall back")
	}
	if p.Detect([]byte(strings.Repeat("x", 32))) {
		t.Fatal("invalid configuration should not report candidates")
	}
}

func TestUnpackRejectsMalformedStreamsAndMetadata(t *testing.T) {
	valid, err := New(Config{}).Pack([]byte("one\ntwo\none\ntwo\n" + strings.Repeat("x", 100)))
	if err != nil {
		t.Fatalf("setup Pack error: %v", err)
	}
	tests := []Packed{
		{Data: []byte("TBP1\x01\x80"), Metadata: valid.Metadata},
		{Data: []byte("TBP1\x01\x00\xff"), Metadata: valid.Metadata},
		{Data: valid.Data[:len(valid.Data)-1], Metadata: valid.Metadata},
		{Data: valid.Data, Metadata: Metadata{Version: valid.Metadata.Version, OriginalSize: valid.Metadata.OriginalSize + 1, Dictionary: valid.Metadata.Dictionary}},
		{Data: valid.Data, Metadata: Metadata{Version: valid.Metadata.Version, OriginalSize: valid.Metadata.OriginalSize, Dictionary: []DictionaryEntry{{ID: "b0", Data: []byte("wrong")}}}},
	}
	for i, packed := range tests {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			if _, err := Unpack(packed); err == nil {
				t.Fatal("Unpack should reject malformed stream/metadata")
			}
		})
	}
}

func TestEnabledCodecRoundTripAndOptIn(t *testing.T) {
	input := strings.Repeat("key: "+strings.Repeat("v", 80)+"\n", 4)
	disabled := NewCodec(false)
	encoded, err := disabled.Encode(input)
	if err != nil || encoded != input || disabled.Detect(input) {
		t.Fatal("disabled model-facing codec must pass through")
	}
	enabled := NewCodec(true)
	encoded, err = enabled.Encode(input)
	if err != nil {
		t.Fatalf("enabled codec Encode error: %v", err)
	}
	if encoded == input {
		t.Fatal("enabled codec should be able to encode repeated blocks")
	}
	decoded, err := enabled.Decode(encoded)
	if err != nil || decoded != input {
		t.Fatalf("codec round-trip failed: decoded=%q err=%v", decoded, err)
	}
	if !enabled.Verify(input, encoded) {
		t.Fatal("enabled codec Verify failed")
	}
	if got := enabled.EstimateSavings(input); got <= 0 {
		t.Fatalf("enabled codec EstimateSavings=%d want positive", got)
	}
	var _ codec.LosslessCodec = enabled
}

func TestModelCodecRejectsMalformedStreams(t *testing.T) {
	c := NewCodec(true)
	for _, encoded := range []string{
		"@tm-b:v1;b0:3:abc;@text",
		"@tm-b:v1;b0:3:abc;@@b1@",
		"@tm-b:v1;b0:3:abc;@@b0",
	} {
		if _, err := c.Decode(encoded); err == nil {
			t.Fatalf("Decode(%q) should reject malformed model stream", encoded)
		}
	}
}
