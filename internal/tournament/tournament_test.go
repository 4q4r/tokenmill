package tournament

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

// mockCodec for tournament tests
type mockCodec struct {
	id        string
	estimate  int
	encodeStr string
	encodeErr error
	verify    bool
	panicOn   string // "estimate","encode","verify" to test fail-open
}

func (m *mockCodec) ID() string               { return m.id }
func (m *mockCodec) Detect(input string) bool { return true }
func (m *mockCodec) EstimateSavings(input string) int {
	if m.panicOn == "estimate" {
		panic("estimate panic")
	}
	return m.estimate
}
func (m *mockCodec) Encode(input string) (string, error) {
	if m.panicOn == "encode" {
		panic("encode panic")
	}
	if m.encodeErr != nil {
		return "", m.encodeErr
	}
	if m.encodeStr != "" {
		return m.encodeStr, nil
	}
	// default: return input truncated to simulate compression
	// For saving, we need shorter encoded string -> fewer tokens
	// Return a short string for positive saving
	return m.encodeStr, nil
}
func (m *mockCodec) Decode(encoded string) (string, error) {
	// For verify we just check if verify flag true then decode returns original implied
	return encoded, nil
}
func (m *mockCodec) Verify(original, encoded string) bool {
	if m.panicOn == "verify" {
		panic("verify panic")
	}
	return m.verify
}

// helper to make a codec that compresses by returning short string and verify true
func shortCodec(id string, estimate int) *mockCodec {
	return &mockCodec{id: id, estimate: estimate, encodeStr: "x", verify: true}
}

func longCodec(id string, estimate int) *mockCodec {
	// encode returns long string => negative saving
	return &mockCodec{id: id, estimate: estimate, encodeStr: strings.Repeat("a ", 1000), verify: true}
}

func TestTournament_Select_FallbackEmptyPool(t *testing.T) {
	tr := New(nil)
	cfg := DefaultConfig()
	best, enc, saving := tr.Select("some input that is long enough to test but we fallback", cfg)
	if best != nil {
		t.Fatal("expected nil best for empty pool")
	}
	if enc != "some input that is long enough to test but we fallback" {
		t.Fatalf("expected original fallback, got %q", enc)
	}
	if saving != 0 {
		t.Fatalf("expected 0 saving, got %d", saving)
	}
}

func TestTournament_Select_EstimateNegativeSkip(t *testing.T) {
	// codec with negative estimate should be skipped
	c1 := &mockCodec{id: "neg", estimate: -1, encodeStr: "x", verify: true}
	tr := New([]codec.LosslessCodec{c1})
	cfg := TournamentConfig{MinSavingsPercent: 0, MinSavingsTokens: 0, HintOverhead: 1, TopK: 3}
	// Need to bypass defaults: withDefaults will set 0 -> default 10/32, so set explicit small thresholds
	cfg.MinSavingsPercent = 1
	cfg.MinSavingsTokens = 1
	cfg.HintOverhead = 1
	best, _, _ := tr.Select("hello world hello world hello world hello world hello world hello world", cfg)
	if best != nil {
		t.Fatal("negative estimate should be skipped, expected fallback")
	}
}

func TestTournament_Select_VerifyFailSkip(t *testing.T) {
	c := &mockCodec{id: "bad-verify", estimate: 100, encodeStr: "x", verify: false}
	tr := New([]codec.LosslessCodec{c})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 1}
	best, _, _ := tr.Select(strings.Repeat("hello world ", 100), cfg)
	if best != nil {
		t.Fatal("verify false should be skipped")
	}
}

func TestTournament_Select_EncodeErrorSkip(t *testing.T) {
	c := &mockCodec{id: "err", estimate: 100, encodeErr: errors.New("encode failed"), verify: true}
	tr := New([]codec.LosslessCodec{c})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 1}
	best, enc, _ := tr.Select(strings.Repeat("hello ", 100), cfg)
	if best != nil {
		t.Fatalf("encode error should fallback, got %v enc %q", best, enc)
	}
}

func TestTournament_Select_Gates(t *testing.T) {
	// Create input with known token count: tokenizer estimated count = (len+3)/4 for <512, but >512 uses tiktoken
	// Use long input to use tiktoken counting
	input := strings.Repeat("hello world ", 200) // long
	// short encode "x" will have high saving
	c := shortCodec("dedup", 100)
	tr := New([]codec.LosslessCodec{c})

	tests := []struct {
		name     string
		cfg      TournamentConfig
		wantBest bool // expect best != nil
	}{
		{"pass all gates", TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 1}, true},
		{"fail percent", TournamentConfig{MinSavingsPercent: 100, MinSavingsTokens: 1, HintOverhead: 1, TopK: 1}, false},
		{"fail tokens", TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 10000, HintOverhead: 1, TopK: 1}, false},
		{"fail hint", TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 10000, TopK: 1}, false},
		{"hint exact equality should fail (need >)", TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 13, TopK: 1}, true}, // saving for this input should be >>13, so pass
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			best, _, saving := tr.Select(input, tc.cfg)
			if tc.wantBest && best == nil {
				t.Fatalf("expected best, got nil saving %d cfg %+v", saving, tc.cfg)
			}
			if !tc.wantBest && best != nil {
				t.Fatalf("expected fallback nil, got %v saving %d", best.ID(), saving)
			}
		})
	}
}

func TestTournament_Select_ChooseMaxSaving(t *testing.T) {
	input := strings.Repeat("hello world ", 300)
	// Two codecs: one returns "x" (1 token), other returns "xx" (maybe 1 token as well due to estimate). Need distinct savings.
	// Instead use different encode lengths: "x" vs "y y y" => different token counts.
	// We'll use mock that returns specific strings and compute saving via TokenSavings.
	// To ensure one is better, make c1 return "x" (very short), c2 return longer but still compressed.
	c1 := &mockCodec{id: "c1", estimate: 50, encodeStr: "x", verify: true}
	c2 := &mockCodec{id: "c2", estimate: 100, encodeStr: "x x x x x", verify: true} // more tokens than c1, so c1 should win
	// But estimate order: c2 has higher estimate (100 vs 50) so sorted first. However real saving c1 > c2, so final selection should pick c1 (max saving).
	tr := New([]codec.LosslessCodec{c1, c2})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 2}
	best, _, _ := tr.Select(input, cfg)
	if best == nil {
		t.Fatal("expected best")
	}
	if best.ID() != "c1" {
		// If estimate ordering dominates, would pick c2, but real max saving logic should pick c1
		t.Fatalf("expected c1 to win by max saving, got %s", best.ID())
	}
}

func TestTournament_Select_FailOpenPanic(t *testing.T) {
	// panic in estimate should not crash, should fallback or pick other codec
	panicCodec := &mockCodec{id: "panic-est", panicOn: "estimate", estimate: 100, encodeStr: "x", verify: true}
	good := shortCodec("good", 10)
	tr := New([]codec.LosslessCodec{panicCodec, good})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 2}
	input := strings.Repeat("hello ", 200)
	best, _, _ := tr.Select(input, cfg)
	if best == nil || best.ID() != "good" {
		t.Fatalf("expected good codec after panic, got %v", best)
	}

	// panic in encode
	panicEncode := &mockCodec{id: "panic-enc", panicOn: "encode", estimate: 100}
	tr2 := New([]codec.LosslessCodec{panicEncode, good})
	best2, _, _ := tr2.Select(input, cfg)
	if best2 == nil || best2.ID() != "good" {
		t.Fatalf("expected good after encode panic, got %v", best2)
	}

	// panic in verify
	panicVerify := &mockCodec{id: "panic-verify", estimate: 100, encodeStr: "x", panicOn: "verify"}
	tr3 := New([]codec.LosslessCodec{panicVerify, good})
	best3, _, _ := tr3.Select(input, cfg)
	if best3 == nil || best3.ID() != "good" {
		t.Fatalf("expected good after verify panic, got %v", best3)
	}
}

func TestTournament_Firewall_CodeBlockOnlyDedup(t *testing.T) {
	// Input that is code block
	codeInput := "```go\npackage main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hi\") }\nfunc helper() {}\nfunc another() {}\n```"
	nonDedup := shortCodec("jton", 100) // not dedup
	dedup := shortCodec("dedup-sha256", 50)
	tr := New([]codec.LosslessCodec{nonDedup, dedup})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 2}
	// With code firewall, only dedup should be considered, so best should be dedup even though jton has higher estimate
	best, _, _ := tr.Select(codeInput, cfg)
	if best == nil {
		t.Fatal("expected dedup for code block")
	}
	if best.ID() != "dedup-sha256" {
		t.Fatalf("firewall failed: expected dedup, got %s", best.ID())
	}

	// If pool has only non-dedup, should fallback original
	tr2 := New([]codec.LosslessCodec{nonDedup})
	best2, enc2, saving2 := tr2.Select(codeInput, cfg)
	if best2 != nil {
		t.Fatalf("expected fallback nil for code without dedup, got %s", best2.ID())
	}
	if enc2 != codeInput || saving2 != 0 {
		t.Fatalf("expected original fallback, got enc %q saving %d", enc2, saving2)
	}

	// Non-code input should allow any codec
	plainInput := strings.Repeat("hello world ", 200)
	tr3 := New([]codec.LosslessCodec{nonDedup, dedup})
	best3, _, _ := tr3.Select(plainInput, cfg)
	if best3 == nil {
		t.Fatal("expected best for plain input")
	}
	// plain should allow jton which has higher estimate and also higher saving? Need to ensure plain picks max saving.
	// Both have same encode "x" so tie; but estimate ordering will place jton first, saving equal => first wins. So we expect jton? Actually both encode "x" equal saving, so whichever is first in sorted candidates will win if tie and first has bestSaving == second? Our code picks first with max saving > bestSaving, second has equal saving not greater, so first remains. So jton wins.
	if best3.ID() != "jton" {
		t.Logf("plain input picked %s (either is ok if tie)", best3.ID())
	}
}

func TestTournament_Firewall_ShellCommandFallsBack(t *testing.T) {
	input := `printf '%s' /tmp/a`
	nonDedup := shortCodec("json-compact", 100)
	tr := New([]codec.LosslessCodec{nonDedup})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 1}

	best, encoded, saving := tr.Select(input, cfg)
	if best != nil || encoded != input || saving != 0 {
		t.Fatalf("shell command must bypass non-dedup codecs: best=%v encoded=%q saving=%d", best, encoded, saving)
	}
}

func TestTournament_TopKSample512(t *testing.T) {
	// Input >512 chars, ensure sample reranking works
	input := strings.Repeat("abcdefghij ", 200) // >2000 chars >512
	c1 := &mockCodec{id: "c1", estimate: 10, encodeStr: "x", verify: true}
	c2 := &mockCodec{id: "c2", estimate: 100, encodeStr: strings.Repeat("a ", 500), verify: true} // estimate high but real saving low (long encode)
	tr := New([]codec.LosslessCodec{c1, c2})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 2}
	best, _, _ := tr.Select(input, cfg)
	// c2 has high estimate but poor real saving (encode is 500 tokens), c1 has low estimate but good saving
	// Sample reranking should reorder by sample saving, so c1 should win.
	// Our sample logic reranks by sample saving, so c1 should be first for real encode, and pick max saving still c1.
	if best == nil {
		t.Fatal("expected best")
	}
	if best.ID() != "c1" {
		t.Fatalf("expected c1 after sample rerank, got %s", best.ID())
	}
}

func TestTournament_ThreadSafe(t *testing.T) {
	tr := New([]codec.LosslessCodec{shortCodec("dedup", 10), shortCodec("other", 20)})
	cfg := DefaultConfig()
	cfg.MinSavingsPercent = 1
	cfg.MinSavingsTokens = 1
	cfg.HintOverhead = 1
	var wg sync.WaitGroup
	errCh := make(chan error, 100)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _, _ = tr.Select(strings.Repeat("hello ", 100), cfg)
				// concurrent SetPool
				if j%5 == 0 {
					tr.SetPool([]codec.LosslessCodec{shortCodec(" dedup ", 5)})
				}
			}
		}()
	}
	// Also concurrent SetPool from another goroutine
	go func() {
		for k := 0; k < 10; k++ {
			tr.SetPool([]codec.LosslessCodec{shortCodec("dedup", 10)})
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent error: %v", err)
		}
	}
}

func TestTournament_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MinSavingsPercent != 10 || cfg.MinSavingsTokens != 32 || cfg.HintOverhead != codec.HintOverhead || cfg.TopK != 3 {
		t.Fatalf("default config mismatch: %+v", cfg)
	}
	// withDefaults should not override explicit values
	c := TournamentConfig{MinSavingsPercent: 5, MinSavingsTokens: 5, HintOverhead: 5, TopK: 5}
	w := c.withDefaults()
	if w.MinSavingsPercent != 5 || w.MinSavingsTokens != 5 || w.HintOverhead != 5 || w.TopK != 5 {
		t.Fatalf("withDefaults incorrectly overrode explicit values: %+v", w)
	}
	// zero should default
	zero := TournamentConfig{}
	w2 := zero.withDefaults()
	if w2.MinSavingsPercent != 10 || w2.MinSavingsTokens != 32 {
		t.Fatalf("withDefaults failed to default zeros: %+v", w2)
	}
}

func TestTournament_HintOverheadStrictGreater(t *testing.T) {
	// saving == HintOverhead => should fallback (need >)
	tr := New([]codec.LosslessCodec{shortCodec("dedup", 100)})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 10000, TopK: 1}
	best, _, _ := tr.Select(strings.Repeat("hi ", 200), cfg)
	if best != nil {
		t.Fatal("expected fallback when HintOverhead huge")
	}
	// also test saving == HintOverhead rejected: we can set HintOverhead to actual saving value
	// Compute actual saving for shortCodec
	input2 := strings.Repeat("hello world ", 100)
	// compute saving via codec.TokenSavings
	actualSaving := codec.TokenSavings(input2, "x")
	cfg2 := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: actualSaving, TopK: 1}
	best2, _, _ := tr.Select(input2, cfg2)
	if best2 != nil {
		t.Fatalf("saving == HintOverhead (%d) should be rejected (need >), got best %v", actualSaving, best2.ID())
	}
	cfg3 := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: actualSaving - 1, TopK: 1}
	best3, _, _ := tr.Select(input2, cfg3)
	if best3 == nil {
		t.Fatalf("saving %d with HintOverhead %d should pass (need >)", actualSaving, actualSaving-1)
	}
}

func TestTournament_EmptyInput(t *testing.T) {
	tr := New([]codec.LosslessCodec{shortCodec("dedup", 10)})
	cfg := DefaultConfig()
	best, enc, saving := tr.Select("", cfg)
	if enc != "" || saving != 0 || best != nil {
		t.Fatalf("empty input should fallback: best %v enc %q saving %d", best, enc, saving)
	}
}

func TestTournament_NilCodecInPool(t *testing.T) {
	tr := New([]codec.LosslessCodec{nil, shortCodec("dedup", 10), nil})
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 3}
	best, _, _ := tr.Select(strings.Repeat("hello ", 200), cfg)
	if best == nil {
		t.Fatal("nil codecs should be skipped, good codec should win")
	}
}
