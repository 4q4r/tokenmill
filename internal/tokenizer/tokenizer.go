package tokenizer

import (
	"sync"
	"unicode/utf8"

	"github.com/pkoukk/tiktoken-go"
)

var (
	encOnce       sync.Once
	globalEnc     *tiktoken.Tiktoken
	globalEncName string

	cacheMu    sync.RWMutex
	cache      = make(map[string]int)
	cacheOrder []string
	cacheCap   = 2048
)

func getEncoding() *tiktoken.Tiktoken {
	encOnce.Do(func() {
		if e, err := tiktoken.GetEncoding("o200k_base"); err == nil {
			globalEnc = e
			globalEncName = "o200k_base"
			return
		}
		if e, err := tiktoken.GetEncoding("cl100k_base"); err == nil {
			globalEnc = e
			globalEncName = "cl100k_base"
			return
		}
		// remain nil -> fallback to estimate
	})
	return globalEnc
}

// EncodingName returns the name of the encoding in use, or "fallback" if none.
func EncodingName() string {
	enc := getEncoding()
	if enc == nil {
		return "fallback"
	}
	return globalEncName
}

func estimateFast(text string) int {
	if text == "" {
		return 0
	}
	n := 0
	for range text {
		n++
	}
	est := (n + 3) / 4
	if est == 0 && n > 0 {
		est = 1
	}
	return est
}

// Count returns token count for text.
// Thread-safe. Accurate path via tiktoken; fallback to rune estimate only if encoding unavailable.
func Count(text string) int {
	if text == "" {
		return 0
	}
	enc := getEncoding()
	if enc != nil {
		return len(enc.EncodeOrdinary(text))
	}
	return estimateFast(text)
}

// CountWithCache returns token count using a simple FIFO cache (bounded, 2048 entries).
// Thread-safe. FIFO eviction: documented as FIFO (not true LRU) for minimal overhead.
// If true LRU needed, promote on hit; currently FIFO for simplicity.
func CountWithCache(text string) int {
	if text == "" {
		return 0
	}
	cacheMu.RLock()
	if v, ok := cache[text]; ok {
		cacheMu.RUnlock()
		return v
	}
	cacheMu.RUnlock()

	c := Count(text)

	cacheMu.Lock()
	// double-check
	if v, ok := cache[text]; ok {
		cacheMu.Unlock()
		return v
	}
	cache[text] = c
	cacheOrder = append(cacheOrder, text)
	if len(cacheOrder) > cacheCap {
		oldest := cacheOrder[0]
		cacheOrder = cacheOrder[1:]
		delete(cache, oldest)
	}
	cacheMu.Unlock()
	return c
}

// Savings calculates token savings between original and encoded text.
// Returns saved tokens and percentage saved.
func Savings(original, encoded string) (int, float64) {
	orig := Count(original)
	if orig == 0 {
		return 0, 0
	}
	enc := Count(encoded)
	saved := orig - enc
	pct := float64(saved) / float64(orig) * 100
	return saved, pct
}

// EstimateTokens provides fast token estimation.
// Thread-safe, zero alloc fast-path for <512 runes.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	if utf8.RuneCountInString(text) < 512 {
		return estimateFast(text)
	}
	// For longer texts, try accurate count but fallback to estimate
	enc := getEncoding()
	if enc != nil {
		return len(enc.EncodeOrdinary(text))
	}
	return estimateFast(text)
}
