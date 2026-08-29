package rle

import "testing"

func TestDecode_UnboundedGuard_Red(t *testing.T) {
	// n > maxRLEExpand (10000) must NOT expand — treat as plain to prevent OOM on x [×9999999]
	input := "x [×9999999]\n"
	got := Decode(input)
	if got != input {
		t.Fatalf("Decode unbounded should fallback to plain: got len %d want len %d (plain)", len(got), len(input))
	}
	// also test boundary 10001
	input2 := "x [×10001]\n"
	got2 := Decode(input2)
	if got2 != input2 {
		t.Fatalf("Decode 10001 should fallback: got len %d", len(got2))
	}
	// 10000 should still expand (at limit)
	input3 := "x [×10000]\n"
	got3 := Decode(input3)
	if got3 == input3 {
		t.Fatalf("Decode 10000 should expand, not fallback")
	}
	if len(got3) == 0 {
		t.Fatal("unexpected empty")
	}
}

func TestDecodeBlocks_UnboundedGuard_Red(t *testing.T) {
	// n*len(block) > 10000 must fallback to plain
	input := "[block ×5001: [\"a\",\"b\"]]\n" // 5001*2=10002 >10000
	got := DecodeBlocks(input)
	if got != input {
		t.Fatalf("DecodeBlocks unbounded should fallback plain: got len %d want len %d", len(got), len(input))
	}
	// also huge
	input2 := "[block ×999999: [\"a\"]]\n"
	got2 := DecodeBlocks(input2)
	if got2 != input2 {
		t.Fatalf("DecodeBlocks 999999 fallback failed")
	}
	// at limit should expand: 5000*2=10000 -> allowed
	input3 := "[block ×5000: [\"a\",\"b\"]]\n"
	got3 := DecodeBlocks(input3)
	if got3 == input3 {
		t.Fatalf("DecodeBlocks at limit 10000 should expand")
	}
}
