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
	"github.com/tokenmill/tokenmill/internal/codec/idmap"
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

// encodeHierarchical runs the dictionary pass at minLen and then, when the
// residual still carries repeats, a second pass at half the minimum length
// (hierarchical dictionary compression: longest patterns first, then
// shorter ones on the residual). Each pass self-verifies its own roundtrip;
// the deterministic composite makes Verify sound.
func (s *substringDictWrapper) encodeHierarchical(inp string) (string, bool) {
	first, ok := s.encodePass(inp, s.minLen, s.minCount)
	if !ok {
		return inp, false
	}
	secondMin := s.minLen / 2
	if secondMin < 8 {
		secondMin = 8
	}
	second, ok2 := s.encodePass(first, secondMin, s.minCount)
	if ok2 && len(second) < len(first) {
		return second, true
	}
	return first, true
}

func (s *substringDictWrapper) encodePass(inp string, minLen, minCount int) (string, bool) {
	enc, _, ok := dictionary.EncodeSubstrings(inp, minLen, minCount, 0)
	if !ok {
		return inp, false
	}
	return enc, true
}

func (s *substringDictWrapper) ID() string { return "substring-dict" }
func (s *substringDictWrapper) Detect(inp string) bool {
	return len(inp) >= s.minLen
}
func (s *substringDictWrapper) EstimateSavings(inp string) int {
	if !s.Detect(inp) {
		return -1
	}
	enc, ok := s.encodeHierarchical(inp)
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
	enc, ok := s.encodeHierarchical(inp)
	if !ok {
		return inp, fmt.Errorf("no substring dict saving")
	}
	return enc, nil
}
func (s *substringDictWrapper) Decode(enc string) (string, error) {
	// The dictionaries are recomputed deterministically by Verify; standalone
	// decoding of an encoded payload requires the embedded dictionary header.
	return enc, fmt.Errorf("substring dict decode requires dict")
}
func (s *substringDictWrapper) Verify(a, b string) bool {
	enc, _ := s.encodeHierarchical(a)
	return enc == b
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
	if cfg.Techniques.UrlDecode {
		pool = append(pool, &urlDecodeWrapper{})
	}
	if cfg.Techniques.HexCompact {
		pool = append(pool, &hexCompactWrapper{})
	}
	if cfg.Techniques.PrefixFold {
		pool = append(pool, &prefixFoldWrapper{})
	}
	if cfg.Techniques.UnicodeUnescape {
		pool = append(pool, &unicodeUnescapeWrapper{})
	}
	if cfg.Techniques.UUIDCompact {
		pool = append(pool, &uuidCompactWrapper{})
	}
	if cfg.Techniques.SmartPunct {
		pool = append(pool, &smartPunctWrapper{})
	}
	if cfg.Techniques.MojibakeFix {
		pool = append(pool, &mojibakeFixWrapper{})
	}
	if cfg.Techniques.IdnDecode {
		pool = append(pool, &idnDecodeWrapper{})
	}
	if cfg.Techniques.Ipv6Norm {
		pool = append(pool, &ipv6NormWrapper{})
	}
	if cfg.Techniques.CsvUnquote {
		pool = append(pool, &csvUnquoteWrapper{})
	}
	if cfg.Techniques.SqlMinify {
		pool = append(pool, &sqlMinifyWrapper{})
	}
	if cfg.Techniques.IsoNorm {
		pool = append(pool, &isoNormWrapper{})
	}
	if cfg.Techniques.EpochToISO {
		pool = append(pool, &epochToISOWrapper{})
	}
	if cfg.Techniques.MdLinkRef {
		pool = append(pool, &mdLinkRefWrapper{})
	}
	if cfg.Techniques.XmlCdata {
		pool = append(pool, &xmlCdataWrapper{})
	}
	if cfg.Techniques.HeaderNorm {
		pool = append(pool, &headerNormWrapper{})
	}
	if cfg.Techniques.NfkcFold {
		pool = append(pool, &nfkcFoldWrapper{})
	}
	if cfg.Techniques.TrailingWs {
		pool = append(pool, &trailingWsWrapper{})
	}
	if cfg.Techniques.BlankRun {
		pool = append(pool, &blankRunWrapper{})
	}
	if cfg.Techniques.ColorCompact {
		pool = append(pool, &colorCompactWrapper{})
	}
	if cfg.Techniques.XmlMinify {
		pool = append(pool, &xmlMinifyWrapper{})
	}
	if cfg.Techniques.RangeFold {
		pool = append(pool, &rangeFoldWrapper{})
	}
	if cfg.Techniques.UuidRemap {
		uuidRemapPool = idmap.New(idmap.DefaultMaxEntries)
		pool = append(pool, &uuidRemapWrapper{remapper: uuidRemapPool})
	}
	// dedup is separate store; not included in single-string tournament
	return pool
}

// uuidRemapPool holds the session-scoped identifier mapping shared by every
// payload processed through the pool instance.
var uuidRemapPool *idmap.Remapper

type uuidRemapWrapper struct{ remapper *idmap.Remapper }

func (w *uuidRemapWrapper) ID() string           { return "uuid-remap" }
func (w *uuidRemapWrapper) Detect(s string) bool { return w.remapper.Preview(s) > 0 }
func (w *uuidRemapWrapper) EstimateSavings(s string) int {
	repeats := w.remapper.Preview(s)
	if repeats == 0 {
		return -1
	}
	// Pure estimate: known repeats collapse a full UUID (~20 tokens) into a
	// short marker (~4 tokens).
	saving := repeats * 16
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *uuidRemapWrapper) Encode(s string) (string, error) {
	enc, replacements := w.remapper.Remap(s)
	if replacements == 0 {
		return s, fmt.Errorf("no repeat identifiers to remap")
	}
	return enc, nil
}
func (w *uuidRemapWrapper) Decode(enc string) (string, error) {
	expanded, ok := w.remapper.Expand(enc)
	if !ok {
		return enc, fmt.Errorf("no remap markers to expand")
	}
	return expanded, nil
}
func (w *uuidRemapWrapper) Verify(orig, enc string) bool {
	return w.remapper.Verify(orig, enc)
}

type rangeFoldWrapper struct{}

func (w *rangeFoldWrapper) ID() string           { return "range-fold" }
func (w *rangeFoldWrapper) Detect(s string) bool { return textnorm.HasFoldableRanges(s) }
func (w *rangeFoldWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.FoldRanges(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *rangeFoldWrapper) Encode(s string) (string, error) {
	return textnorm.FoldRanges(s), nil
}
func (w *rangeFoldWrapper) Decode(enc string) (string, error) {
	return textnorm.UnfoldRanges(enc), nil
}
func (w *rangeFoldWrapper) Verify(orig, enc string) bool {
	return textnorm.UnfoldRanges(enc) == orig
}

type nfkcFoldWrapper struct{}

func (w *nfkcFoldWrapper) ID() string           { return "nfkc-fold" }
func (w *nfkcFoldWrapper) Detect(s string) bool { return textnorm.HasCompatibilityForms(s) }
func (w *nfkcFoldWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.FoldCompatibility(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *nfkcFoldWrapper) Encode(s string) (string, error) {
	return textnorm.FoldCompatibility(s), nil
}
func (w *nfkcFoldWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("nfkc-fold is display-lossless; compatibility forms are not recoverable")
}
func (w *nfkcFoldWrapper) Verify(orig, enc string) bool {
	return textnorm.FoldCompatibility(orig) == enc
}

type trailingWsWrapper struct{}

func (w *trailingWsWrapper) ID() string           { return "trailing-ws" }
func (w *trailingWsWrapper) Detect(s string) bool { return textnorm.HasTrailingWhitespace(s) }
func (w *trailingWsWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.StripTrailingWhitespace(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *trailingWsWrapper) Encode(s string) (string, error) {
	return textnorm.StripTrailingWhitespace(s), nil
}
func (w *trailingWsWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("trailing-ws strips invisible whitespace; no decode")
}
func (w *trailingWsWrapper) Verify(orig, enc string) bool {
	return textnorm.StripTrailingWhitespace(orig) == enc
}

type blankRunWrapper struct{}

func (w *blankRunWrapper) ID() string           { return "blank-run" }
func (w *blankRunWrapper) Detect(s string) bool { return textnorm.HasBlankRuns(s) }
func (w *blankRunWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CollapseBlankRuns(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *blankRunWrapper) Encode(s string) (string, error) {
	return textnorm.CollapseBlankRuns(s), nil
}
func (w *blankRunWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("blank-run collapses invisible separators; no decode")
}
func (w *blankRunWrapper) Verify(orig, enc string) bool {
	return textnorm.CollapseBlankRuns(orig) == enc
}

type colorCompactWrapper struct{}

func (w *colorCompactWrapper) ID() string           { return "color-compact" }
func (w *colorCompactWrapper) Detect(s string) bool { return textnorm.HasCompactableColors(s) }
func (w *colorCompactWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CompactHexColors(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *colorCompactWrapper) Encode(s string) (string, error) {
	return textnorm.CompactHexColors(s), nil
}
func (w *colorCompactWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("color-compact is display-lossless; expanded form is not recoverable")
}
func (w *colorCompactWrapper) Verify(orig, enc string) bool {
	return textnorm.CompactHexColors(orig) == enc
}

type xmlMinifyWrapper struct{}

func (w *xmlMinifyWrapper) ID() string           { return "xml-minify" }
func (w *xmlMinifyWrapper) Detect(s string) bool { return textnorm.HasMinifiableXML(s) }
func (w *xmlMinifyWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.MinifyXML(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *xmlMinifyWrapper) Encode(s string) (string, error) {
	return textnorm.MinifyXML(s), nil
}
func (w *xmlMinifyWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("xml-minify collapses insignificant indentation; no byte decode")
}
func (w *xmlMinifyWrapper) Verify(orig, enc string) bool {
	return textnorm.MinifyXML(orig) == enc
}

type mojibakeFixWrapper struct{}

func (w *mojibakeFixWrapper) ID() string           { return "mojibake-fix" }
func (w *mojibakeFixWrapper) Detect(s string) bool { return textnorm.HasMojibake(s) }
func (w *mojibakeFixWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.RepairMojibake(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *mojibakeFixWrapper) Encode(s string) (string, error) {
	return textnorm.RepairMojibake(s), nil
}
func (w *mojibakeFixWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("mojibake-fix restores intended characters; no byte decode")
}
func (w *mojibakeFixWrapper) Verify(orig, enc string) bool {
	return textnorm.RepairMojibake(orig) == enc
}

type idnDecodeWrapper struct{}

func (w *idnDecodeWrapper) ID() string           { return "idn-decode" }
func (w *idnDecodeWrapper) Detect(s string) bool { return textnorm.HasPunycodeLabels(s) }
func (w *idnDecodeWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.DecodeIDNLabels(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *idnDecodeWrapper) Encode(s string) (string, error) {
	return textnorm.DecodeIDNLabels(s), nil
}
func (w *idnDecodeWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("idn-decode is display-lossless; re-encoding needs IDNA rules")
}
func (w *idnDecodeWrapper) Verify(orig, enc string) bool {
	return textnorm.DecodeIDNLabels(orig) == enc
}

type ipv6NormWrapper struct{}

func (w *ipv6NormWrapper) ID() string           { return "ipv6-norm" }
func (w *ipv6NormWrapper) Detect(s string) bool { return textnorm.HasNonCanonicalIPv6(s) }
func (w *ipv6NormWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CanonicalizeIPv6(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *ipv6NormWrapper) Encode(s string) (string, error) {
	return textnorm.CanonicalizeIPv6(s), nil
}
func (w *ipv6NormWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("ipv6-norm is canonicalization of the same address")
}
func (w *ipv6NormWrapper) Verify(orig, enc string) bool {
	return textnorm.CanonicalizeIPv6(orig) == enc
}

type csvUnquoteWrapper struct{}

func (w *csvUnquoteWrapper) ID() string           { return "csv-unquote" }
func (w *csvUnquoteWrapper) Detect(s string) bool { return textnorm.HasOverQuotedCSV(s) }
func (w *csvUnquoteWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.UnquoteCSV(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *csvUnquoteWrapper) Encode(s string) (string, error) {
	return textnorm.UnquoteCSV(s), nil
}
func (w *csvUnquoteWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("csv-unquote preserves field values; original quoting is not reproducible")
}
func (w *csvUnquoteWrapper) Verify(orig, enc string) bool {
	return textnorm.UnquoteCSV(orig) == enc
}

type sqlMinifyWrapper struct{}

func (w *sqlMinifyWrapper) ID() string           { return "sql-minify" }
func (w *sqlMinifyWrapper) Detect(s string) bool { return textnorm.HasMinifiableSQL(s) }
func (w *sqlMinifyWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.MinifySQL(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *sqlMinifyWrapper) Encode(s string) (string, error) {
	return textnorm.MinifySQL(s), nil
}
func (w *sqlMinifyWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("sql-minify collapses formatting whitespace; no byte decode")
}
func (w *sqlMinifyWrapper) Verify(orig, enc string) bool {
	return textnorm.MinifySQL(orig) == enc
}

type isoNormWrapper struct{}

func (w *isoNormWrapper) ID() string           { return "iso-norm" }
func (w *isoNormWrapper) Detect(s string) bool { return textnorm.HasNonCanonicalTimestamp(s) }
func (w *isoNormWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CanonicalizeTimestamps(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *isoNormWrapper) Encode(s string) (string, error) {
	return textnorm.CanonicalizeTimestamps(s), nil
}
func (w *isoNormWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("iso-norm canonicalizes the same instant")
}
func (w *isoNormWrapper) Verify(orig, enc string) bool {
	return textnorm.CanonicalizeTimestamps(orig) == enc
}

type epochToISOWrapper struct{}

func (w *epochToISOWrapper) ID() string           { return "epoch-to-iso" }
func (w *epochToISOWrapper) Detect(s string) bool { return textnorm.HasEpochTimestamps(s) }
func (w *epochToISOWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.EpochToISO(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *epochToISOWrapper) Encode(s string) (string, error) {
	return textnorm.EpochToISO(s), nil
}
func (w *epochToISOWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("epoch-to-iso denotes the same instant; epoch form is not recoverable")
}
func (w *epochToISOWrapper) Verify(orig, enc string) bool {
	return textnorm.EpochToISO(orig) == enc
}

type mdLinkRefWrapper struct{}

func (w *mdLinkRefWrapper) ID() string           { return "md-link-ref" }
func (w *mdLinkRefWrapper) Detect(s string) bool { return textnorm.HasDuplicatedMarkdownLinks(s) }
func (w *mdLinkRefWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.FoldMarkdownLinks(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *mdLinkRefWrapper) Encode(s string) (string, error) {
	return textnorm.FoldMarkdownLinks(s), nil
}
func (w *mdLinkRefWrapper) Decode(enc string) (string, error) {
	return textnorm.UnfoldMarkdownLinks(enc), nil
}
func (w *mdLinkRefWrapper) Verify(orig, enc string) bool {
	return textnorm.UnfoldMarkdownLinks(enc) == orig
}

type xmlCdataWrapper struct{}

func (w *xmlCdataWrapper) ID() string           { return "xml-cdata" }
func (w *xmlCdataWrapper) Detect(s string) bool { return textnorm.HasCDATA(s) }
func (w *xmlCdataWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.UnwrapCDATA(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *xmlCdataWrapper) Encode(s string) (string, error) {
	return textnorm.UnwrapCDATA(s), nil
}
func (w *xmlCdataWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("xml-cdata unwrap is value-lossless; original wrapper is not reproducible")
}
func (w *xmlCdataWrapper) Verify(orig, enc string) bool {
	return textnorm.UnwrapCDATA(orig) == enc
}

type headerNormWrapper struct{}

func (w *headerNormWrapper) ID() string           { return "header-norm" }
func (w *headerNormWrapper) Detect(s string) bool { return textnorm.HasLooseHeaders(s) }
func (w *headerNormWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.NormalizeHeaders(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *headerNormWrapper) Encode(s string) (string, error) {
	return textnorm.NormalizeHeaders(s), nil
}
func (w *headerNormWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("header-norm canonicalizes case-insensitive names")
}
func (w *headerNormWrapper) Verify(orig, enc string) bool {
	return textnorm.NormalizeHeaders(orig) == enc
}

type unicodeUnescapeWrapper struct{}

func (w *unicodeUnescapeWrapper) ID() string { return "unicode-unescape" }
func (w *unicodeUnescapeWrapper) Detect(s string) bool {
	return textnorm.HasUnicodeEscapes(s) || textnorm.HasHexEscapes(s)
}
func (w *unicodeUnescapeWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.UnfoldHexEscapes(textnorm.UnfoldUnicode(s))
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *unicodeUnescapeWrapper) Encode(s string) (string, error) {
	return textnorm.UnfoldHexEscapes(textnorm.UnfoldUnicode(s)), nil
}
func (w *unicodeUnescapeWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("unicode-unescape is value-lossless; re-escaping is lossy by design")
}
func (w *unicodeUnescapeWrapper) Verify(orig, enc string) bool {
	return textnorm.UnfoldHexEscapes(textnorm.UnfoldUnicode(orig)) == enc
}

type uuidCompactWrapper struct{}

func (w *uuidCompactWrapper) ID() string           { return "uuid-compact" }
func (w *uuidCompactWrapper) Detect(s string) bool { return textnorm.HasDashedUUIDs(s) }
func (w *uuidCompactWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CompactUUIDs(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *uuidCompactWrapper) Encode(s string) (string, error) {
	return textnorm.CompactUUIDs(s), nil
}
func (w *uuidCompactWrapper) Decode(enc string) (string, error) {
	return textnorm.RedashUUIDs(enc), nil
}
func (w *uuidCompactWrapper) Verify(orig, enc string) bool {
	return textnorm.CompactUUIDs(orig) == enc && textnorm.RedashUUIDs(enc) == orig
}

type smartPunctWrapper struct{}

func (w *smartPunctWrapper) ID() string           { return "smart-punct" }
func (w *smartPunctWrapper) Detect(s string) bool { return textnorm.HasSmartPunctuation(s) }
func (w *smartPunctWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.NormalizeSmartPunctuation(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *smartPunctWrapper) Encode(s string) (string, error) {
	return textnorm.NormalizeSmartPunctuation(s), nil
}
func (w *smartPunctWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("smart-punct is display-lossless, no decode")
}
func (w *smartPunctWrapper) Verify(orig, enc string) bool {
	return textnorm.NormalizeSmartPunctuation(orig) == enc
}

type urlDecodeWrapper struct{}

func (w *urlDecodeWrapper) ID() string           { return "url-decode" }
func (w *urlDecodeWrapper) Detect(s string) bool { return textnorm.HasPercentEncodings(s) }
func (w *urlDecodeWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.DecodePercent(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *urlDecodeWrapper) Encode(s string) (string, error) {
	return textnorm.DecodePercent(s), nil
}
func (w *urlDecodeWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("url-decode is display-lossless")
}
func (w *urlDecodeWrapper) Verify(orig, enc string) bool {
	return textnorm.DecodePercent(orig) == enc
}

type hexCompactWrapper struct{}

func (w *hexCompactWrapper) ID() string           { return "hex-compact" }
func (w *hexCompactWrapper) Detect(s string) bool { return textnorm.HasCompactableHex(s) }
func (w *hexCompactWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CompactHex(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *hexCompactWrapper) Encode(s string) (string, error) {
	return textnorm.CompactHex(s), nil
}
func (w *hexCompactWrapper) Decode(enc string) (string, error) {
	return enc, fmt.Errorf("hex-compact only removes separator whitespace")
}
func (w *hexCompactWrapper) Verify(orig, enc string) bool {
	return textnorm.CompactHex(orig) == enc
}

type prefixFoldWrapper struct{}

func (w *prefixFoldWrapper) ID() string           { return "prefix-fold" }
func (w *prefixFoldWrapper) Detect(s string) bool { return textnorm.FoldLinePrefixes(s, 5, 8).Changed }
func (w *prefixFoldWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.FoldLinePrefixes(s, 5, 8).Content
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *prefixFoldWrapper) Encode(s string) (string, error) {
	return textnorm.FoldLinePrefixes(s, 5, 8).Content, nil
}
func (w *prefixFoldWrapper) Decode(enc string) (string, error) {
	return textnorm.UnfoldLinePrefixes(enc), nil
}
func (w *prefixFoldWrapper) Verify(orig, enc string) bool {
	return textnorm.UnfoldLinePrefixes(enc) == orig
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

func (w *htmlEntityWrapper) ID() string { return "html-entity" }
func (w *htmlEntityWrapper) Detect(s string) bool {
	return textnorm.HasHTMLEntities(s) || textnorm.HasDeepEntities(s)
}
func (w *htmlEntityWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.DeepUnescapeEntities(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *htmlEntityWrapper) Encode(s string) (string, error) {
	return textnorm.DeepUnescapeEntities(s), nil
}
func (w *htmlEntityWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("html-entity decode is display-lossless")
}
func (w *htmlEntityWrapper) Verify(orig, enc string) bool {
	return textnorm.DeepUnescapeEntities(orig) == enc
}

type base64CompactWrapper struct{}

func (w *base64CompactWrapper) ID() string { return "base64-compact" }
func (w *base64CompactWrapper) Detect(s string) bool {
	return textnorm.HasCompactableBase64(s) || textnorm.HasCompactableBase32(s)
}
func (w *base64CompactWrapper) EstimateSavings(s string) int {
	if !w.Detect(s) {
		return -1
	}
	enc := textnorm.CompactBase32(textnorm.CompactBase64(s))
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}
func (w *base64CompactWrapper) Encode(s string) (string, error) {
	return textnorm.CompactBase32(textnorm.CompactBase64(s)), nil
}
func (w *base64CompactWrapper) Decode(enc string) (string, error) {
	return enc, fmt.Errorf("base64-compact only removes decoder-ignored whitespace")
}
func (w *base64CompactWrapper) Verify(orig, enc string) bool {
	return textnorm.CompactBase32(textnorm.CompactBase64(orig)) == enc
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
