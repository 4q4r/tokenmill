package rle

import (
	"strings"
	"testing"
)

func TestExactRLE_MinRun3(t *testing.T) {
	// minRun=3: 2 repeats should NOT be encoded, 3 should
	input2 := "hello\nhello\n"
	enc2 := Encode(input2, 3)
	if enc2 != input2 {
		t.Fatalf("minRun3 with 2 repeats should not encode: got %q want %q", enc2, input2)
	}
	if IsRLEEncoded(enc2) {
		t.Fatalf("should not be detected as RLE")
	}

	input3 := "hello\nhello\nhello\n"
	enc3 := Encode(input3, 3)
	want3 := "hello [×3]\n"
	if enc3 != want3 {
		t.Fatalf("3 repeats: got %q want %q", enc3, want3)
	}
	if !IsRLEEncoded(enc3) {
		t.Fatal("should be detected as RLE")
	}
	dec := Decode(enc3)
	if dec != input3 {
		t.Fatalf("decode: got %q want %q", dec, input3)
	}
	if !Verify(input3, enc3) {
		t.Fatal("verify failed")
	}
}

func TestExactRLE_MinRun2(t *testing.T) {
	input := "a\na\n"
	enc := Encode(input, 2)
	want := "a [×2]\n"
	if enc != want {
		t.Fatalf("minRun2: got %q want %q", enc, want)
	}
	if dec := Decode(enc); dec != input {
		t.Fatalf("roundtrip minRun2: got %q want %q", dec, input)
	}
}

func TestExactRLE_DecodeRoundtrip(t *testing.T) {
	cases := []string{
		"line1\nline2\nline1\n",
		"foo\nfoo\nfoo\nbar\nbar\nbar\nbar\n",
		"single\n",
		"",
		"a\nb\nc\n",
		"x\nx\nx\nx\nx\n", // 5 repeats
	}
	for _, c := range cases {
		enc := Encode(c, 3)
		dec := Decode(enc)
		if dec != c {
			t.Fatalf("roundtrip failed for %q: enc %q dec %q", c, enc, dec)
		}
		if !Verify(c, enc) {
			t.Fatalf("verify failed for %q", c)
		}
	}
}

func TestExactRLE_Escaping(t *testing.T) {
	// Original already contains marker pattern
	orig := "foo [×3]\n"
	enc := Encode(orig, 3)
	// Should be escaped, not treated as RLE on decode
	dec := Decode(enc)
	if dec != orig {
		t.Fatalf("escaping roundtrip: got %q want %q enc %q", dec, orig, enc)
	}
	// Single line with marker should not be decoded as expansion
	orig2 := "hello [×2]\nhello [×2]\nhello [×2]\n"
	// 3 identical lines each containing marker
	enc2 := Encode(orig2, 3)
	dec2 := Decode(enc2)
	if dec2 != orig2 {
		t.Fatalf("escaping run: got %q want %q enc %q", dec2, orig2, enc2)
	}
	// Also test Contains check
	orig3 := "a [× test\nb\n"
	enc3 := Encode(orig3, 3)
	if dec := Decode(enc3); dec != orig3 {
		t.Fatalf("contains marker escaping: got %q want %q", dec, orig3)
	}
}

func TestExactRLE_IsRLEEncoded(t *testing.T) {
	if IsRLEEncoded("plain\ntext\n") {
		t.Fatal("false positive")
	}
	if !IsRLEEncoded("foo [×3]\n") {
		t.Fatal("should detect")
	}
	if IsRLEEncoded("") {
		t.Fatal("empty should be false")
	}
	// Escaped line should not be considered encoded
	orig := "foo [×3]\n"
	enc := Encode(orig, 3)
	if IsRLEEncoded(enc) {
		// Encoded escaped single line should NOT be detected as RLE (since it's escaped)
		// But our Encode for single escaped line will produce escaped version, not marker
		// So it should be false
		t.Fatalf("escaped single should not be RLE, enc %q", enc)
	}
}

func TestExactRLE_SingleLine(t *testing.T) {
	in := "single\n"
	enc := Encode(in, 3)
	if enc != in {
		t.Fatalf("single line should not be encoded: got %q", enc)
	}
	if dec := Decode(enc); dec != in {
		t.Fatalf("single line decode: got %q", dec)
	}
}

func TestExactRLE_VerifyNegative(t *testing.T) {
	orig := "a\na\na\n"
	enc := Encode(orig, 3)
	if !Verify(orig, enc) {
		t.Fatal("should verify")
	}
	if Verify(orig, "wrong") {
		t.Fatal("should not verify wrong encoding")
	}
}

func TestExactRLE_DefaultMinRun(t *testing.T) {
	// minRun 0 should default to 3
	input := "x\nx\nx\n"
	enc := Encode(input, 0)
	want := "x [×3]\n"
	if enc != want {
		t.Fatalf("default minRun: got %q want %q", enc, want)
	}
}

func TestExactRLE_NoTrailingNewline(t *testing.T) {
	in := "a\na\na"
	enc := Encode(in, 3)
	// Should preserve lack of trailing newline
	if !strings.HasSuffix(enc, "]") && strings.HasSuffix(enc, "\n") {
		t.Fatalf("should not add trailing newline")
	}
	dec := Decode(enc)
	if dec != in {
		t.Fatalf("no trailing newline roundtrip: got %q want %q enc %q", dec, in, enc)
	}
}

func TestBlockFactoring_Basic(t *testing.T) {
	// block size 2 repeating 3 times
	input := "a\nb\na\nb\na\nb\nc\n"
	enc := EncodeBlocks(input, 2, 20)
	if !IsBlockEncoded(enc) {
		t.Fatalf("should be block encoded, got %q", enc)
	}
	dec := DecodeBlocks(enc)
	if dec != input {
		t.Fatalf("block roundtrip: got %q want %q enc %q", dec, input, enc)
	}
	if !VerifyBlocks(input, enc) {
		t.Fatal("verify blocks failed")
	}
}

func TestBlockFactoring_NonExactNotFactored(t *testing.T) {
	input := "a\nb\na\nc\na\nb\n"
	enc := EncodeBlocks(input, 2, 20)
	if IsBlockEncoded(enc) {
		t.Fatalf("non-exact should not be factored, got %q", enc)
	}
	if enc != input {
		t.Fatalf("non-exact should remain unchanged: got %q want %q", enc, input)
	}
}

func TestBlockFactoring_SingleLineNotFactored(t *testing.T) {
	input := "single\n"
	enc := EncodeBlocks(input, 2, 20)
	if enc != input {
		t.Fatalf("single line should not be block encoded: got %q", enc)
	}
	if IsBlockEncoded(enc) {
		t.Fatal("single should not be detected")
	}
	dec := DecodeBlocks(enc)
	if dec != input {
		t.Fatalf("single decode: got %q", dec)
	}
}

func TestBlockFactoring_MinMax(t *testing.T) {
	// test minBlock 2 maxBlock 20 boundary: block size 2 should be factored, size 1 not
	input2 := "x\ny\nx\ny\n"
	enc2 := EncodeBlocks(input2, 2, 20)
	if !IsBlockEncoded(enc2) {
		t.Fatalf("2-block should be factored: got %q", enc2)
	}
	// with minBlock 3, same input should NOT be factored (needs 3)
	enc3 := EncodeBlocks(input2, 3, 20)
	if IsBlockEncoded(enc3) {
		t.Fatalf("with minBlock 3, 2-size block should not be factored: got %q", enc3)
	}
	// maxBlock test: 4 lines block
	input4 := "1\n2\n3\n4\n1\n2\n3\n4\n"
	enc4 := EncodeBlocks(input4, 2, 20)
	if !IsBlockEncoded(enc4) {
		t.Fatalf("4-block should be factored: got %q", enc4)
	}
	if dec := DecodeBlocks(enc4); dec != input4 {
		t.Fatalf("4-block roundtrip: got %q", dec)
	}
}

func TestBlockFactoring_LargeBlock(t *testing.T) {
	// test block size near max 20
	var b strings.Builder
	block := []string{"l1", "l2", "l3", "l4", "l5"}
	for i := 0; i < 2; i++ {
		for _, l := range block {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	input := b.String()
	enc := EncodeBlocks(input, 2, 20)
	if !IsBlockEncoded(enc) {
		t.Fatalf("5-block should be factored: got %q", enc)
	}
	if dec := DecodeBlocks(enc); dec != input {
		t.Fatalf("large block roundtrip failed")
	}
}

func TestBlockFactoring_Verify(t *testing.T) {
	input := "a\nb\na\nb\na\nb\n"
	enc := EncodeBlocks(input, 2, 20)
	if !VerifyBlocks(input, enc) {
		t.Fatal("verify blocks should pass")
	}
	if VerifyBlocks(input, "wrong") {
		t.Fatal("verify wrong should fail")
	}
}

func TestBlockFactoring_OverlappingAndExactEquality(t *testing.T) {
	// near-duplicate but not exact should not factor
	_ = "error: foo 1\nerror: foo 2\nerror: foo 1\nerror: foo 2\n"
	// Wait that's exact repeats of 2? Actually first block "error: foo 1", "error: foo 2" exact repeat, so should factor
	// Let's make near duplicate
	_ = "id=12\nid=13\nid=12\nid=13\n"
	// Actually these are also exact repeats of block "id=12","id=13"? No first block is id12,id13, second block same id12,id13 => exact, so should factor, but spec says non-exact not factored. Need truly non-exact: block differs by one byte
	input3 := "a\nb\na\nB\n" // second block "a","B" differs case
	enc := EncodeBlocks(input3, 2, 20)
	if IsBlockEncoded(enc) {
		t.Fatalf("near dup should not factor: got %q", enc)
	}
	// Ensure exact still factors
	input4 := "a\nb\na\nb\n"
	enc4 := EncodeBlocks(input4, 2, 2)
	if !IsBlockEncoded(enc4) {
		t.Fatalf("exact should factor: got %q", enc4)
	}
}

func TestBlockFactoring_DecodeIsRLEIndependent(t *testing.T) {
	// Ensure block factoring doesn't interfere with exact RLE
	input := "x\nx\nx\na\nb\na\nb\na\nb\n"
	encRLE := Encode(input, 3)
	encBlock := EncodeBlocks(input, 2, 20)
	// Both should roundtrip via their own decoders
	if dec := Decode(encRLE); dec != input {
		t.Fatalf("RLE roundtrip mixed: got %q", dec)
	}
	if dec := DecodeBlocks(encBlock); dec != input {
		t.Fatalf("block roundtrip mixed: got %q want %q enc %q", dec, input, encBlock)
	}
}

func TestBlockFactoring_Escaping(t *testing.T) {
	orig := "[block ×2: [\"a\"]]\n"
	enc := EncodeBlocks(orig, 2, 20)
	dec := DecodeBlocks(enc)
	if dec != orig {
		t.Fatalf("block escaping: got %q want %q enc %q", dec, orig, enc)
	}
	// Block marker inside block content
	_ = "[block ×2: test]\n[block ×2: test]\n[block ×2: test]\n[block ×2: test]\n"
	// This is block of size1 repeated 4 times but minBlock 2, so shouldn't factor as block=1, but ExactRLE would
	// For block test, use size2 with marker inside: create 2-line block each containing marker
	input2 := "a [block ×2: foo]\nb\n" + "a [block ×2: foo]\nb\n"
	enc2 := EncodeBlocks(input2, 2, 20)
	dec2 := DecodeBlocks(enc2)
	if dec2 != input2 {
		t.Fatalf("block with marker inside: got %q want %q enc %q", dec2, input2, enc2)
	}
}
