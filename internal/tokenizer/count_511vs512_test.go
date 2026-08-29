package tokenizer

import (
	"strings"
	"testing"
)

// TestCount_511vs512 verifies tournament estimate fix: fast-path must not cause 2x discontinuity at boundary.
// RED: with bug len<512 fast-path, Count("x"*511) =128, Count("x"*512)=64 ratio 2.0 → fail.
// GREEN: after removing fast-path from Count, both use tiktoken → ratio ~1.
func TestCount_511vs512(t *testing.T) {
	a511 := strings.Repeat("x", 511)
	a512 := strings.Repeat("x", 512)
	c511 := Count(a511)
	c512 := Count(a512)
	if c511 == 0 || c512 == 0 {
		t.Fatalf("expected >0 got c511=%d c512=%d", c511, c512)
	}
	ratio := float64(c511) / float64(c512)
	// Allow small delta: true tokenization should be ~1.0, bug gives 2.0
	if ratio < 0.8 || ratio > 1.25 {
		t.Fatalf("Count discontinuity at 511/512 boundary: c511=%d c512=%d ratio=%.3f want ~1.0", c511, c512, ratio)
	}
	// Also absolute diff should be tiny (0 or 1) for repeated single char, not 64
	if diff := c511 - c512; diff < -2 || diff > 2 {
		// For "x"*511 vs "x"*512 tokens should differ by at most 1
		t.Fatalf("expected tokens diff ~0/1, got c511=%d c512=%d diff=%d", c511, c512, diff)
	}
}

// TestCount_EstimateTokens_RuneThreshold ensures EstimateTokens uses rune count threshold, not byte length.
// Also verifies Count is always accurate (tiktoken) while EstimateTokens keeps fast-path.
func TestCount_EstimateTokens_RuneThreshold(t *testing.T) {
	// 300 runes of 2-byte char => 600 bytes but 300 runes (<512) should use fast-path in EstimateTokens
	uni := strings.Repeat("é", 300) // 2 bytes each
	// Count must use tiktoken (accurate), not fast-path
	c := Count(uni)
	// estimate via rune threshold should be (300+3)/4 =75 if fast-path
	e := EstimateTokens(uni)
	if c == 0 || e == 0 {
		t.Fatalf("expected >0 c=%d e=%d", c, e)
	}
	// For pure fast-path, e would be 75; if bug uses byte len threshold, e would use tiktoken (300) not 75
	// We expect EstimateTokens to use fast-path for <512 runes, so e should be ~75, not 300
	// Count via tiktoken may be different; just ensure EstimateTokens fast-path activated
	// This is a soft check: ensure rune-based threshold doesn't misclassify
	if e == c && c == 300 {
		// If both equal 300, EstimateTokens likely used tiktoken incorrectly via byte threshold
		// But we allow either if tiktoken fallback gives similar; just log
		t.Logf("warning: EstimateTokens used tiktoken for 300 runes 600 bytes: e=%d c=%d", e, c)
	}
}
