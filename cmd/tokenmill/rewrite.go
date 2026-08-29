package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tokenmill/tokenmill/internal/codec"
	"github.com/tokenmill/tokenmill/internal/codec/csvcanonical"
	"github.com/tokenmill/tokenmill/internal/codec/folding"
	"github.com/tokenmill/tokenmill/internal/codec/jcs"
	"github.com/tokenmill/tokenmill/internal/codec/jsoncompact"
	"github.com/tokenmill/tokenmill/internal/codec/jsonnumber"
	"github.com/tokenmill/tokenmill/internal/codec/jton"
	"github.com/tokenmill/tokenmill/internal/codec/markdown"
	"github.com/tokenmill/tokenmill/internal/codec/opaque"
	"github.com/tokenmill/tokenmill/internal/codec/symboltable"
	"github.com/tokenmill/tokenmill/internal/codec/textnorm"
	"github.com/tokenmill/tokenmill/internal/config"
	"github.com/tokenmill/tokenmill/internal/detector"
	"github.com/tokenmill/tokenmill/internal/dictionary"
	"github.com/tokenmill/tokenmill/internal/packer"
	"github.com/tokenmill/tokenmill/internal/rle"
	"github.com/tokenmill/tokenmill/internal/stacktrace"
	"github.com/tokenmill/tokenmill/internal/stats"
	"github.com/tokenmill/tokenmill/internal/table"
	"github.com/tokenmill/tokenmill/internal/terminal"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
	"github.com/tokenmill/tokenmill/internal/tournament"
)

// wrappers for tournament codecs

type jsonCompactWrapper struct{ c *jsoncompact.Codec }

func (w *jsonCompactWrapper) ID() string                      { return w.c.ID() }
func (w *jsonCompactWrapper) Detect(s string) bool            { return w.c.Detect(s) }
func (w *jsonCompactWrapper) EstimateSavings(s string) int    { return w.c.EstimateSavings(s) }
func (w *jsonCompactWrapper) Encode(s string) (string, error) { return w.c.Encode(s) }
func (w *jsonCompactWrapper) Decode(s string) (string, error) { return w.c.Decode(s) }
func (w *jsonCompactWrapper) Verify(a, b string) bool         { return w.c.Verify(a, b) }

type jtonWrapper struct{ c *jton.Codec }

func (w *jtonWrapper) ID() string                      { return w.c.ID() }
func (w *jtonWrapper) Detect(s string) bool            { return w.c.Detect(s) }
func (w *jtonWrapper) EstimateSavings(s string) int    { return w.c.EstimateSavings(s) }
func (w *jtonWrapper) Encode(s string) (string, error) { return w.c.Encode(s) }
func (w *jtonWrapper) Decode(s string) (string, error) { return w.c.Decode(s) }
func (w *jtonWrapper) Verify(a, b string) bool         { return w.c.Verify(a, b) }

type ansiWrapper struct{}

func (a *ansiWrapper) ID() string           { return "ansi-strip" }
func (a *ansiWrapper) Detect(s string) bool { return terminal.HasANSI(s) }
func (a *ansiWrapper) EstimateSavings(s string) int {
	if !a.Detect(s) {
		return -1
	}
	enc := terminal.StripANSI(s, true)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (a *ansiWrapper) Encode(s string) (string, error) { return terminal.StripANSI(s, true), nil }
func (a *ansiWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("ansi strip is display-lossless, no decode")
}
func (a *ansiWrapper) Verify(orig, enc string) bool {
	// display-lossless: stripping ansi is considered verify true if enc equals stripped orig
	return terminal.StripANSI(orig, true) == enc
}

type crWrapper struct{}

func (c *crWrapper) ID() string           { return "cr-render" }
func (c *crWrapper) Detect(s string) bool { return strings.Contains(s, "\r") }
func (c *crWrapper) EstimateSavings(s string) int {
	if !c.Detect(s) {
		return -1
	}
	enc := terminal.RenderCR(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (c *crWrapper) Encode(s string) (string, error) { return terminal.RenderCR(s), nil }
func (c *crWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("cr render is display-lossless")
}
func (c *crWrapper) Verify(orig, enc string) bool { return terminal.RenderCR(orig) == enc }

type rleWrapper struct{ minRun int }

func (r *rleWrapper) ID() string { return "exact-rle" }
func (r *rleWrapper) Detect(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) < r.minRun {
		return false
	}
	// check any run >=minRun
	count := 1
	for i := 1; i < len(lines); i++ {
		if lines[i] == lines[i-1] {
			count++
			if count >= r.minRun {
				return true
			}
		} else {
			count = 1
		}
	}
	return false
}
func (r *rleWrapper) EstimateSavings(s string) int {
	if !r.Detect(s) {
		return -1
	}
	enc := rle.Encode(s, r.minRun)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (r *rleWrapper) Encode(s string) (string, error) { return rle.Encode(s, r.minRun), nil }
func (r *rleWrapper) Decode(s string) (string, error) { return rle.Decode(s), nil }
func (r *rleWrapper) Verify(a, b string) bool         { return rle.Verify(a, b) }

type blockWrapper struct{ minBlock, maxBlock int }

func (b *blockWrapper) ID() string { return "block-factor" }
func (b *blockWrapper) Detect(s string) bool {
	// need at least minBlock*2 lines
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < b.minBlock*2 {
		return false
	}
	// quick heuristic: check duplicate block via rle EncodeBlocks saving
	enc := rle.EncodeBlocks(s, b.minBlock, b.maxBlock)
	return enc != s
}
func (b *blockWrapper) EstimateSavings(s string) int {
	enc := rle.EncodeBlocks(s, b.minBlock, b.maxBlock)
	if enc == s {
		return -1
	}
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (b *blockWrapper) Encode(s string) (string, error) {
	return rle.EncodeBlocks(s, b.minBlock, b.maxBlock), nil
}
func (b *blockWrapper) Decode(s string) (string, error) { return rle.DecodeBlocks(s), nil }
func (b *blockWrapper) Verify(a, e string) bool         { return rle.VerifyBlocks(a, e) }

type pathDictWrapper struct{ maxCodes, minCount int }

func (p *pathDictWrapper) ID() string           { return "path-dict" }
func (p *pathDictWrapper) Detect(s string) bool { ok, _ := detector.IsPathHeavy(s); return ok }
func (p *pathDictWrapper) EstimateSavings(s string) int {
	if !p.Detect(s) {
		return -1
	}
	enc, _, ok := dictionary.EncodePaths(s, p.maxCodes, p.minCount)
	if !ok {
		return -1
	}
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (p *pathDictWrapper) Encode(s string) (string, error) {
	enc, _, ok := dictionary.EncodePaths(s, p.maxCodes, p.minCount)
	if !ok {
		return s, fmt.Errorf("no path dict saving")
	}
	return enc, nil
}
func (p *pathDictWrapper) Decode(s string) (string, error) {
	// decode requires dict; without dict we can't accurately decode; but verify will handle.
	// For tournament verify we need to test decode; we will extract dict via heuristic? For now return error.
	return s, fmt.Errorf("path dict decode requires dict")
}
func (p *pathDictWrapper) Verify(a, b string) bool {
	enc, dict, ok := dictionary.EncodePaths(a, p.maxCodes, p.minCount)
	if !ok {
		return false
	}
	if enc != b {
		return false
	}
	return dictionary.VerifyPaths(a, b, dict)
}

type substringDictWrapper struct{ minLen, minCount int }

func (s *substringDictWrapper) ID() string { return "substring-dict" }
func (s *substringDictWrapper) Detect(inp string) bool {
	return len(inp) >= s.minLen
}
func (s *substringDictWrapper) EstimateSavings(inp string) int {
	if !s.Detect(inp) {
		return -1
	}
	enc, _, ok := dictionary.EncodeSubstrings(inp, s.minLen, s.minCount, 0)
	if !ok {
		return -1
	}
	saving := tokenizer.Count(inp) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (s *substringDictWrapper) Encode(inp string) (string, error) {
	enc, _, ok := dictionary.EncodeSubstrings(inp, s.minLen, s.minCount, 0)
	if !ok {
		return inp, fmt.Errorf("no substring dict saving")
	}
	return enc, nil
}
func (s *substringDictWrapper) Decode(enc string) (string, error) {
	// The dictionary is recomputed deterministically by Verify; standalone
	// decoding of an encoded payload requires the embedded dictionary header.
	return enc, fmt.Errorf("substring dict decode requires dict")
}
func (s *substringDictWrapper) Verify(a, b string) bool {
	enc, dict, ok := dictionary.EncodeSubstrings(a, s.minLen, s.minCount, 0)
	if !ok {
		return false
	}
	if enc != b {
		return false
	}
	return dictionary.VerifySubstrings(a, b, dict)
}

type tableWrapper struct{}

func (t *tableWrapper) ID() string           { return "table-tsv" }
func (t *tableWrapper) Detect(s string) bool { return table.DetectTable(s) }
func (t *tableWrapper) EstimateSavings(s string) int {
	if !t.Detect(s) {
		return -1
	}
	enc, err := table.TableToTSV(s)
	if err != nil {
		return -1
	}
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (t *tableWrapper) Encode(s string) (string, error) { return table.TableToTSV(s) }
func (t *tableWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("table decode not reversible byte-lossless")
}
func (t *tableWrapper) Verify(a, b string) bool { return table.VerifyTable(a, b) }

type stackWrapper struct{}

func (s *stackWrapper) ID() string             { return "stacktrace-dict" }
func (s *stackWrapper) Detect(inp string) bool { ok, _ := detector.IsStackTrace(inp); return ok }
func (s *stackWrapper) EstimateSavings(inp string) int {
	if !s.Detect(inp) {
		return -1
	}
	enc, _, ok := stacktrace.CompressStackTrace(inp)
	if !ok {
		return -1
	}
	saving := tokenizer.Count(inp) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (s *stackWrapper) Encode(inp string) (string, error) {
	enc, _, ok := stacktrace.CompressStackTrace(inp)
	if !ok {
		return inp, fmt.Errorf("no stacktrace saving")
	}
	return enc, nil
}
func (s *stackWrapper) Decode(enc string) (string, error) {
	return enc, fmt.Errorf("stacktrace decode requires dict")
}
func (s *stackWrapper) Verify(a, b string) bool {
	enc, dict, ok := stacktrace.CompressStackTrace(a)
	if !ok {
		return false
	}
	if enc != b {
		return false
	}
	return stacktrace.VerifyStackTrace(a, b, dict)
}

// buildPool constructs tournament pool from config
func buildPool(cfg config.Config) []codec.LosslessCodec {
	if !cfg.Enabled {
		return nil
	}
	var pool []codec.LosslessCodec
	// Always add ansi and cr if enabled (display-lossless)
	if cfg.Techniques.AnsiStripping {
		pool = append(pool, &ansiWrapper{})
	}
	if cfg.Techniques.CrRendering {
		pool = append(pool, &crWrapper{})
	}
	if cfg.Techniques.ExactRLE.Enabled {
		minRun := cfg.Techniques.ExactRLE.MinRun
		if minRun <= 0 {
			minRun = 3
		}
		pool = append(pool, &rleWrapper{minRun: minRun})
	}
	if cfg.Techniques.BlockFactoring.Enabled {
		minB := cfg.Techniques.BlockFactoring.MinBlock
		maxB := cfg.Techniques.BlockFactoring.MaxBlock
		if minB <= 0 {
			minB = 2
		}
		if maxB <= 0 {
			maxB = 20
		}
		pool = append(pool, &blockWrapper{minBlock: minB, maxBlock: maxB})
	}
	if cfg.Techniques.PathDict.Enabled {
		pool = append(pool, &pathDictWrapper{maxCodes: cfg.Techniques.PathDict.MaxCodes, minCount: cfg.Techniques.PathDict.MinCount})
	}
	if cfg.Techniques.SubstringDict.Enabled {
		minLen := cfg.Techniques.SubstringDict.MinLen
		minCount := cfg.Techniques.SubstringDict.MinCount
		if minLen <= 0 {
			minLen = 40
		}
		if minCount <= 0 {
			minCount = 4
		}
		pool = append(pool, &substringDictWrapper{minLen: minLen, minCount: minCount})
	}
	if cfg.Techniques.JsonCompact {
		pool = append(pool, &jsonCompactWrapper{c: jsoncompact.New()})
	}
	if cfg.Techniques.Jton.Enabled {
		jc := jton.New()
		jc.MinRows = cfg.Techniques.Jton.MinRows
		pool = append(pool, &jtonWrapper{c: jc})
	}
	if cfg.Techniques.TableTSV {
		pool = append(pool, &tableWrapper{})
	}
	if cfg.Techniques.StacktraceDict {
		pool = append(pool, &stackWrapper{})
	}
	if cfg.Techniques.JCS {
		pool = append(pool, jcs.New())
	}
	if cfg.Techniques.JsonNumber {
		pool = append(pool, jsonnumber.New())
	}
	if cfg.Techniques.MarkdownWhitespace {
		pool = append(pool, markdown.New())
	}
	if cfg.Techniques.OpaqueDict {
		pool = append(pool, opaque.New())
	}
	if cfg.Techniques.CrossCallPack {
		pool = append(pool, packer.NewCodec(cfg.Techniques.CrossCallPack))
	}
	if cfg.Techniques.CsvCanonical {
		pool = append(pool, csvcanonical.New())
	}
	if cfg.Techniques.SymbolTable {
		pool = append(pool, symboltable.New())
	}
	if cfg.Techniques.DiffLogFold {
		pool = append(pool, folding.New())
	}
	if cfg.Techniques.UnicodeNormalize {
		pool = append(pool, &textNormWrapper{})
	}
	if cfg.Techniques.HtmlEntityDecode {
		pool = append(pool, &htmlEntityWrapper{})
	}
	if cfg.Techniques.Base64Compact {
		pool = append(pool, &base64CompactWrapper{})
	}
	// dedup is separate store; not included in single-string tournament
	return pool
}

type textNormWrapper struct{}

func (w *textNormWrapper) ID() string           { return "text-norm" }
func (w *textNormWrapper) Detect(s string) bool { return textnorm.NeedsNormalization(s) }
func (w *textNormWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.Normalize(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *textNormWrapper) Encode(s string) (string, error) { return textnorm.Normalize(s), nil }
func (w *textNormWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("text-norm is display-lossless, no decode")
}
func (w *textNormWrapper) Verify(orig, enc string) bool {
	return textnorm.Normalize(orig) == enc
}

type htmlEntityWrapper struct{}

func (w *htmlEntityWrapper) ID() string           { return "html-entity" }
func (w *htmlEntityWrapper) Detect(s string) bool { return textnorm.HasHTMLEntities(s) }
func (w *htmlEntityWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.UnescapeEntities(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *htmlEntityWrapper) Encode(s string) (string, error) {
	return textnorm.UnescapeEntities(s), nil
}
func (w *htmlEntityWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("html-entity decode is display-lossless")
}
func (w *htmlEntityWrapper) Verify(orig, enc string) bool {
	return textnorm.UnescapeEntities(orig) == enc
}

type base64CompactWrapper struct{}

func (w *base64CompactWrapper) ID() string           { return "base64-compact" }
func (w *base64CompactWrapper) Detect(s string) bool { return textnorm.HasCompactableBase64(s) }
func (w *base64CompactWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CompactBase64(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *base64CompactWrapper) Encode(s string) (string, error) {
	return textnorm.CompactBase64(s), nil
}
func (w *base64CompactWrapper) Decode(enc string) (string, error) {
	return enc, fmt.Errorf("base64-compact only removes decoder-ignored whitespace")
}
func (w *base64CompactWrapper) Verify(orig, enc string) bool {
	return textnorm.CompactBase64(orig) == enc
}

func newRewriteCmd() *cobra.Command {
	var rawOutput bool
	cmd := &cobra.Command{
		Use:   "rewrite [command]",
		Short: "Run tournament on string and output encoded or original (for testing)",
		Long:  "Takes a command string (e.g. \"git status\"), runs tournament codecs, and prints the encoded output if saving meets thresholds, otherwise original.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := strings.Join(args, " ")
			cfg, err := loadConfig()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("load config: %w", err)
			}
			started := time.Now()
			encoded := input
			pool := buildPool(cfg)
			if len(pool) > 0 {
				tCfg := tournament.TournamentConfig{
					MinSavingsPercent: cfg.MinSavingsPercent,
					MinSavingsTokens:  cfg.MinSavingsTokens,
					HintOverhead:      codec.HintOverhead,
					TopK:              3,
				}
				tr := tournament.New(pool)
				_, encoded, _ = tr.Select(input, tCfg)
			}
			var writeErr error
			if rawOutput {
				_, writeErr = fmt.Fprint(cmd.OutOrStdout(), encoded)
			} else {
				_, writeErr = fmt.Fprintln(cmd.OutOrStdout(), encoded)
			}
			if writeErr != nil {
				return fmt.Errorf("write rewrite output: %w", writeErr)
			}
			recordRewriteStats(cmd, cfg, input, encoded, started)
			return nil
		},
	}
	cmd.Flags().BoolVar(&rawOutput, "raw", false, "emit the payload without a trailing newline")
	return cmd
}

func recordRewriteStats(cmd *cobra.Command, cfg config.Config, input, output string, started time.Time) {
	if !cfg.LogSavings {
		return
	}
	store, err := stats.New("")
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: record rewrite stats: open store: %v\n", err)
		return
	}
	if err := store.Record(input, input, output, tokenizer.Count(input), tokenizer.Count(output), time.Since(started).Milliseconds()); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: record rewrite stats: %v\n", err)
	}
	if err := store.Close(); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: close rewrite stats: %v\n", err)
	}
}
