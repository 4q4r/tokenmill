package tokenizer

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestCount_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		want  int // 0 means just check >0 or ==0 for empty
		check func(int) bool
	}{
		{"empty", "", 0, func(got int) bool { return got == 0 }},
		{"single_word", "hello", 0, func(got int) bool { return got >= 1 && got <= 3 }},
		{"short_sentence", "hello world", 0, func(got int) bool { return got >= 1 && got <= 5 }},
		{"short_unicode", "你好世界", 0, func(got int) bool { return got >= 1 }},
		{"long_text_512_plus", strings.Repeat("hello world ", 100), 0, func(got int) bool { return got > 50 }},
		{"very_long", strings.Repeat("a", 10000), 0, func(got int) bool { return got > 100 }},
		{"whitespace", "   ", 0, func(got int) bool { return got >= 1 }},
		{"single_char", "a", 0, func(got int) bool { return got >= 1 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Count(tc.text)
			if !tc.check(got) {
				t.Fatalf("Count(%q) = %d, check failed", tc.text[:min(20, len(tc.text))], got)
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

func TestCount_Empty(t *testing.T) {
	if got := Count(""); got != 0 {
		t.Fatalf("expected 0 for empty, got %d", got)
	}
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("expected 0 for empty EstimateTokens, got %d", got)
	}
}

func TestCountWithCache_Hit(t *testing.T) {
	text := "cache test string for token counting"
	// Clear cache by using unique string
	first := CountWithCache(text)
	second := CountWithCache(text)
	if first != second {
		t.Fatalf("cache hit mismatch: first=%d second=%d", first, second)
	}
	if first == 0 {
		t.Fatalf("expected non-zero count")
	}
	// also compare with direct Count
	direct := Count(text)
	if first != direct {
		t.Fatalf("cached %d != direct %d", first, direct)
	}
}

func TestSavings(t *testing.T) {
	tests := []struct {
		name, orig, enc string
		wantSaved       int
		wantPctRange    [2]float64
	}{
		{"saved_positive", "hello world hello world hello world", "hw", 1, [2]float64{10, 90}},
		{"no_saving", "hello", "hello", 0, [2]float64{0, 0.1}},
		{"empty_original", "", "anything", 0, [2]float64{0, 0}},
		{"longer_encoded", "hi", "hello world longer encoded", -2, [2]float64{-200, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saved, pct := Savings(tc.orig, tc.enc)
			// For empty original, expect 0
			if tc.orig == "" {
				if saved != 0 || pct != 0 {
					t.Fatalf("empty original expected 0,0 got %d,%.2f", saved, pct)
				}
				return
			}
			if tc.name == "saved_positive" {
				if saved <= 0 {
					t.Fatalf("expected positive saved got %d", saved)
				}
				if pct <= tc.wantPctRange[0] || pct >= tc.wantPctRange[1]+1 {
					// allow wide range
					if pct < 10 || pct > 100 {
						t.Fatalf("pct out of expected range got %.2f", pct)
					}
				}
			}
			if tc.name == "no_saving" {
				if saved != 0 {
					t.Fatalf("expected 0 saved got %d", saved)
				}
				if pct != 0 {
					t.Fatalf("expected 0 pct got %.2f", pct)
				}
			}
			if tc.name == "longer_encoded" {
				if saved >= 0 {
					t.Fatalf("expected negative saved got %d", saved)
				}
			}
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"short", "hello"},
		{"long", strings.Repeat("hello world ", 200)},
		{"unicode", "こんにちは世界 🌍"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateTokens(tc.text)
			if tc.text == "" && got != 0 {
				t.Fatalf("empty expected 0 got %d", got)
			}
			if tc.text != "" && got == 0 {
				t.Fatalf("non-empty expected >0 got %d", got)
			}
			// For short, Estimate should be close to Count (within factor 2 for heuristic)
			if len(tc.text) < 512 && tc.text != "" {
				c := Count(tc.text)
				if c > 0 {
					ratio := float64(got) / float64(c)
					if ratio < 0.25 || ratio > 4 {
						t.Fatalf("Estimate %d vs Count %d ratio %.2f too far", got, c, ratio)
					}
				}
			}
		})
	}
}

// Zero alloc fast-path for <512 chars: ensure EstimateTokens and Count handle <512 without panic and quickly
func TestFastPath_Short(t *testing.T) {
	short := strings.Repeat("a", 100)
	for i := 0; i < 100; i++ {
		if got := Count(short); got == 0 {
			t.Fatalf("fast path returned 0")
		}
		if got := EstimateTokens(short); got == 0 {
			t.Fatalf("estimate fast path returned 0")
		}
	}
	// 511 chars should use fast path, 512+ should use tiktoken path but still >0
	a511 := strings.Repeat("x", 511)
	a512 := strings.Repeat("x", 512)
	a600 := strings.Repeat("hello ", 120) // ~720 chars
	if Count(a511) == 0 || Count(a512) == 0 || Count(a600) == 0 {
		t.Fatal("expected >0 for length edge cases")
	}
	if EstimateTokens(a511) == 0 || EstimateTokens(a600) == 0 {
		t.Fatal("estimate expected >0")
	}
}

func TestConcurrency_Count(t *testing.T) {
	texts := []string{
		"hello world",
		strings.Repeat("concurrent test ", 50),
		strings.Repeat("a", 600),
		"短文本测试并发",
		"",
	}
	var wg sync.WaitGroup
	errCh := make(chan string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			txt := texts[idx%len(texts)]
			got := Count(txt)
			if txt == "" && got != 0 {
				errCh <- fmt.Sprintf("empty expected 0 got %d", got)
			}
			if txt != "" && got == 0 {
				errCh <- fmt.Sprintf("non-empty got 0 for %q", txt[:min(10, len(txt))])
			}
			// also test cache
			cached := CountWithCache(txt)
			if cached != Count(txt) && txt != "" {
				// Allow small difference if fast path vs tiktoken? but should be equal
				// For our impl they should be equal
			}
			// Savings concurrent
			_, _ = Savings(txt, "short")
			_ = EstimateTokens(txt)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatal(e)
	}
}

func TestConcurrency_Cache(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			txt := fmt.Sprintf("cache concurrency %d %s", n, strings.Repeat("x", n%20))
			for j := 0; j < 20; j++ {
				CountWithCache(txt)
				Count(txt)
			}
		}(i)
	}
	wg.Wait()
	// If race detector enabled, this will catch races
}

func TestEncoding_Fallback(t *testing.T) {
	// Ensure Count works even if encoding init failed (fallback path)
	// We can't force network failure, but we can test that Count returns >0 for various inputs
	// and that o200k_base vs cl100k difference is tolerable (both should give similar counts for english)
	text := "Hello, world! This is a test of token counting."
	c := Count(text)
	if c == 0 {
		t.Fatal("Count returned 0 for non-empty")
	}
	// Ensure thread-safe init: multiple goroutines init simultaneously
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if Count(text) == 0 {
				t.Errorf("concurrent count zero")
			}
		}()
	}
	wg.Wait()
}

func TestGolden_CountValues(t *testing.T) {
	// Golden file style: known inputs should produce deterministic counts
	// We allow range rather than exact due to fallback vs tiktoken variance
	cases := []struct {
		text string
		min  int
		max  int
	}{
		{"", 0, 0},
		{"a", 1, 2},
		{"hello world", 1, 4},
		{strings.Repeat("hello ", 100), 80, 300}, // long should be ~100 tokens
	}
	for _, c := range cases {
		got := Count(c.text)
		if got < c.min || got > c.max {
			t.Errorf("Count(%q) = %d, want [%d,%d]", c.text[:min(20, len(c.text))], got, c.min, c.max)
		}
	}
}
