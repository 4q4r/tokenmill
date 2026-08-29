package tournament

import (
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec"
)

type undetectedCompressingCodec struct{}

func (undetectedCompressingCodec) ID() string { return "undetected" }

func (undetectedCompressingCodec) Detect(string) bool { return false }

func (undetectedCompressingCodec) EstimateSavings(string) int { return 100 }

func (undetectedCompressingCodec) Encode(string) (string, error) { return "x", nil }

func (undetectedCompressingCodec) Decode(encoded string) (string, error) { return encoded, nil }

func (undetectedCompressingCodec) Verify(string, string) bool { return true }

func TestTournamentSelectSkipsCodecWhenDetectIsFalse(t *testing.T) {
	var candidate codec.LosslessCodec = undetectedCompressingCodec{}
	tr := New([]codec.LosslessCodec{candidate})
	input := strings.Repeat("long input ", 100)
	cfg := TournamentConfig{MinSavingsPercent: 1, MinSavingsTokens: 1, HintOverhead: 1, TopK: 1}

	best, encoded, saving := tr.Select(input, cfg)
	if best != nil || encoded != input || saving != 0 {
		t.Fatalf("undetected codec must be skipped: best=%v encoded=%q saving=%d", best, encoded, saving)
	}
}
