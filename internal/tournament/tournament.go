package tournament

import (
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/detector"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

// TournamentConfig holds thresholds for candidate selection.
type TournamentConfig struct {
	MinSavingsPercent int // default 10
	MinSavingsTokens  int // default 32
	HintOverhead      int // default codec.HintOverhead (13)
	TopK              int // default 3
}

// DefaultConfig returns config with spec defaults.
func DefaultConfig() TournamentConfig {
	return TournamentConfig{
		MinSavingsPercent: 10,
		MinSavingsTokens:  32,
		HintOverhead:      codec.HintOverhead,
		TopK:              3,
	}
}

func (c TournamentConfig) withDefaults() TournamentConfig {
	if c.MinSavingsPercent <= 0 {
		c.MinSavingsPercent = 10
	}
	if c.MinSavingsTokens <= 0 {
		c.MinSavingsTokens = 32
	}
	if c.HintOverhead <= 0 {
		c.HintOverhead = codec.HintOverhead
	}
	if c.TopK <= 0 {
		c.TopK = 3
	}
	return c
}

// Tournament is a ClickHouse-Adaptive style candidate selector.
// Thread-safe, fail-open: panics in codecs are recovered and logged.
type Tournament struct {
	mu   sync.RWMutex
	Pool []codec.LosslessCodec
}

// New creates a Tournament with given pool.
func New(pool []codec.LosslessCodec) *Tournament {
	cp := make([]codec.LosslessCodec, len(pool))
	copy(cp, pool)
	return &Tournament{Pool: cp}
}

// SetPool replaces pool atomically (thread-safe).
func (t *Tournament) SetPool(pool []codec.LosslessCodec) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]codec.LosslessCodec, len(pool))
	copy(cp, pool)
	t.Pool = cp
}

func (t *Tournament) getPoolCopy() []codec.LosslessCodec {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make([]codec.LosslessCodec, len(t.Pool))
	copy(cp, t.Pool)
	return cp
}

// Select chooses the best codec for input per tournament rules.
// Returns (nil, original, 0) on fallback (preferOriginal).
func (t *Tournament) Select(input string, cfg TournamentConfig) (codec.LosslessCodec, string, int) {
	cfg = cfg.withDefaults()

	// Code firewall: only dedup allowed for code blocks.
	if ok, _ := detector.IsCodeBlock(input); ok {
		pool := t.getPoolCopy()
		filtered := filterDedupOnly(pool)
		if len(filtered) == 0 {
			return nil, input, 0
		}
		return t.selectWithPool(input, filtered, cfg)
	}
	pool := t.getPoolCopy()
	return t.selectWithPool(input, pool, cfg)
}

func filterDedupOnly(pool []codec.LosslessCodec) []codec.LosslessCodec {
	var out []codec.LosslessCodec
	for _, c := range pool {
		if c == nil {
			continue
		}
		id := strings.ToLower(c.ID())
		if strings.Contains(id, "dedup") {
			out = append(out, c)
		}
	}
	return out
}

type candidate struct {
	codec    codec.LosslessCodec
	estimate int
}

func (t *Tournament) selectWithPool(input string, pool []codec.LosslessCodec, cfg TournamentConfig) (codec.LosslessCodec, string, int) {
	if len(pool) == 0 {
		return nil, input, 0
	}
	// Fast-path estimates
	var candidates []candidate
	for _, c := range pool {
		if c == nil {
			continue
		}
		if !safeDetect(c, input) {
			continue
		}
		est := safeEstimate(c, input)
		if est < 0 {
			continue
		}
		candidates = append(candidates, candidate{codec: c, estimate: est})
	}
	if len(candidates) == 0 {
		return nil, input, 0
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].estimate > candidates[j].estimate
	})
	topK := cfg.TopK
	if topK > len(candidates) {
		topK = len(candidates)
	}
	candidates = candidates[:topK]

	// Sample encode on first 512 chars to rerank (rune-safe, verified)
	if len([]rune(input)) > 512 {
		sample := string([]rune(input)[:512])
		type sampleRes struct {
			idx    int
			saving int
			ok     bool
		}
		var sampleResults []sampleRes
		for idx, cand := range candidates {
			enc, ok, saving := safeSampleEncode(cand.codec, sample)
			if !ok {
				sampleResults = append(sampleResults, sampleRes{idx: idx, saving: cand.estimate, ok: false})
				continue
			}
			if !safeVerify(cand.codec, sample, enc) {
				sampleResults = append(sampleResults, sampleRes{idx: idx, saving: cand.estimate, ok: false})
				continue
			}
			sampleResults = append(sampleResults, sampleRes{idx: idx, saving: saving, ok: true})
		}
		sort.Slice(sampleResults, func(i, j int) bool {
			return sampleResults[i].saving > sampleResults[j].saving
		})
		reordered := make([]candidate, len(candidates))
		for i, sr := range sampleResults {
			reordered[i] = candidates[sr.idx]
		}
		candidates = reordered
	}

	origTokens := tokenizer.Count(input)
	if origTokens == 0 {
		return nil, input, 0
	}

	var best codec.LosslessCodec
	var bestEncoded string
	bestSaving := -1

	for _, cand := range candidates {
		enc, ok := safeEncode(cand.codec, input)
		if !ok {
			continue
		}
		if !safeVerify(cand.codec, input, enc) {
			continue
		}
		saving := codec.TokenSavings(input, enc)
		if saving <= 0 {
			continue
		}
		percent := float64(saving) / float64(origTokens) * 100
		if saving < cfg.MinSavingsTokens {
			continue
		}
		if percent < float64(cfg.MinSavingsPercent) {
			continue
		}
		if saving <= cfg.HintOverhead {
			continue
		}
		if saving > bestSaving {
			bestSaving = saving
			best = cand.codec
			bestEncoded = enc
		}
	}

	if best == nil {
		return nil, input, 0
	}
	return best, bestEncoded, bestSaving
}

func safeDetect(c codec.LosslessCodec, input string) (detected bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("codec Detect panic, skipping candidate", "id", safeID(c), "panic", r)
			detected = false
		}
	}()
	return c.Detect(input)
}

func safeEstimate(c codec.LosslessCodec, input string) (ret int) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("codec EstimateSavings panic, skipping candidate", "id", safeID(c), "panic", r)
			ret = -1
		}
	}()
	ret = c.EstimateSavings(input)
	return
}

func safeEncode(c codec.LosslessCodec, input string) (enc string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("codec Encode panic, skipping candidate", "id", safeID(c), "panic", r)
			enc = ""
			ok = false
		}
	}()
	e, err := c.Encode(input)
	if err != nil {
		return "", false
	}
	return e, true
}

func safeSampleEncode(c codec.LosslessCodec, sample string) (enc string, ok bool, saving int) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("codec sample Encode panic", "id", safeID(c), "panic", r)
			enc = ""
			ok = false
			saving = -1
		}
	}()
	e, err := c.Encode(sample)
	if err != nil {
		return "", false, -1
	}
	s := codec.TokenSavings(sample, e)
	return e, true, s
}

func safeVerify(c codec.LosslessCodec, original, encoded string) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("codec Verify panic, skipping candidate", "id", safeID(c), "panic", r)
			ok = false
		}
	}()
	return c.Verify(original, encoded)
}

func safeID(c codec.LosslessCodec) string {
	defer func() { recover() }()
	if c == nil {
		return "<nil>"
	}
	return c.ID()
}
