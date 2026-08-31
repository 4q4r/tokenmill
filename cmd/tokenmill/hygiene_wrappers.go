package main

import (
	"fmt"

	"github.com/tokenmill/tokenmill/internal/codec/textnorm"
	"github.com/tokenmill/tokenmill/internal/tokenizer"
)

func hygieneEstimate(s string, detect func(string) bool, encode func(string) string) int {
	if !detect(s) {
		return -1
	}
	enc := encode(s)
	saving := tokenizer.Count(s) - tokenizer.Count(enc)
	if saving <= 0 {
		return -1
	}
	return saving
}

// ---------- crlf-to-lf ----------

type crlfToLfWrapper struct{}

func (w *crlfToLfWrapper) ID() string           { return "crlf-to-lf" }
func (w *crlfToLfWrapper) Detect(s string) bool { return textnorm.HasCRLF(s) }
func (w *crlfToLfWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.NormalizeCRLF(x) })
}
func (w *crlfToLfWrapper) Encode(s string) (string, error) {
	return textnorm.NormalizeCRLF(s), nil
}
func (w *crlfToLfWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("crlf-to-lf strips invisible carriage returns")
}
func (w *crlfToLfWrapper) Verify(orig, enc string) bool {
	return textnorm.NormalizeCRLF(orig) == enc
}

// ---------- edge-blanks ----------

type edgeBlanksWrapper struct{}

func (w *edgeBlanksWrapper) ID() string           { return "edge-blanks" }
func (w *edgeBlanksWrapper) Detect(s string) bool { return textnorm.HasEdgeBlankLines(s) }
func (w *edgeBlanksWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripEdgeBlankLines(x) })
}
func (w *edgeBlanksWrapper) Encode(s string) (string, error) {
	return textnorm.StripEdgeBlankLines(s), nil
}
func (w *edgeBlanksWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("edge-blanks strips invisible whitespace")
}
func (w *edgeBlanksWrapper) Verify(orig, enc string) bool {
	return textnorm.StripEdgeBlankLines(orig) == enc
}

// ---------- unicode-lsep ----------

type unicodeLsepWrapper struct{}

func (w *unicodeLsepWrapper) ID() string           { return "unicode-lsep" }
func (w *unicodeLsepWrapper) Detect(s string) bool { return textnorm.HasUnicodeLineSeparators(s) }
func (w *unicodeLsepWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.NormalizeUnicodeLineSeparators(x) })
}
func (w *unicodeLsepWrapper) Encode(s string) (string, error) {
	return textnorm.NormalizeUnicodeLineSeparators(s), nil
}
func (w *unicodeLsepWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("unicode-lsep normalizes invisible separators")
}
func (w *unicodeLsepWrapper) Verify(orig, enc string) bool {
	return textnorm.NormalizeUnicodeLineSeparators(orig) == enc
}

// ---------- variation-sel ----------

type variationSelWrapper struct{}

func (w *variationSelWrapper) ID() string           { return "variation-sel" }
func (w *variationSelWrapper) Detect(s string) bool { return textnorm.HasVariationSelectors(s) }
func (w *variationSelWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripVariationSelectors(x) })
}
func (w *variationSelWrapper) Encode(s string) (string, error) {
	return textnorm.StripVariationSelectors(s), nil
}
func (w *variationSelWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("variation-sel strips invisible selectors")
}
func (w *variationSelWrapper) Verify(orig, enc string) bool {
	return textnorm.StripVariationSelectors(orig) == enc
}

// ---------- html-comments ----------

type htmlCommentsWrapper struct{}

func (w *htmlCommentsWrapper) ID() string           { return "html-comments" }
func (w *htmlCommentsWrapper) Detect(s string) bool { return textnorm.HasHTMLComments(s) }
func (w *htmlCommentsWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripHTMLCommentsText(x) })
}
func (w *htmlCommentsWrapper) Encode(s string) (string, error) {
	return textnorm.StripHTMLCommentsText(s), nil
}
func (w *htmlCommentsWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("html-comments strips invisible comments")
}
func (w *htmlCommentsWrapper) Verify(orig, enc string) bool {
	return textnorm.StripHTMLCommentsText(orig) == enc
}

// ---------- setext-to-atx ----------

type setextToAtxWrapper struct{}

func (w *setextToAtxWrapper) ID() string           { return "setext-to-atx" }
func (w *setextToAtxWrapper) Detect(s string) bool { return textnorm.HasSetextHeadings(s) }
func (w *setextToAtxWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.SetextToATX(x) })
}
func (w *setextToAtxWrapper) Encode(s string) (string, error) { return textnorm.SetextToATX(s), nil }
func (w *setextToAtxWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("setext-to-atx is display-lossless")
}
func (w *setextToAtxWrapper) Verify(orig, enc string) bool { return textnorm.SetextToATX(orig) == enc }

// ---------- list-markers ----------

type listMarkersWrapper struct{}

func (w *listMarkersWrapper) ID() string           { return "list-markers" }
func (w *listMarkersWrapper) Detect(s string) bool { return textnorm.HasAsteriskBullets(s) }
func (w *listMarkersWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StandardizeListMarkers(x) })
}
func (w *listMarkersWrapper) Encode(s string) (string, error) {
	return textnorm.StandardizeListMarkers(s), nil
}
func (w *listMarkersWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("list-markers is display-lossless")
}
func (w *listMarkersWrapper) Verify(orig, enc string) bool {
	return textnorm.StandardizeListMarkers(orig) == enc
}

// ---------- horiz-rules ----------

type horizRulesWrapper struct{}

func (w *horizRulesWrapper) ID() string           { return "horiz-rules" }
func (w *horizRulesWrapper) Detect(s string) bool { return textnorm.HasHorizontalRules(s) }
func (w *horizRulesWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripHorizontalRules(x) })
}
func (w *horizRulesWrapper) Encode(s string) (string, error) {
	return textnorm.StripHorizontalRules(s), nil
}
func (w *horizRulesWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("horiz-rules strips decorative lines")
}
func (w *horizRulesWrapper) Verify(orig, enc string) bool {
	return textnorm.StripHorizontalRules(orig) == enc
}

// ---------- toc-strip ----------

type tocStripWrapper struct{}

func (w *tocStripWrapper) ID() string           { return "toc-strip" }
func (w *tocStripWrapper) Detect(s string) bool { return textnorm.HasTOC(s) }
func (w *tocStripWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripTOC(x) })
}
func (w *tocStripWrapper) Encode(s string) (string, error) { return textnorm.StripTOC(s), nil }
func (w *tocStripWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("toc-strip removes redundant TOC links")
}
func (w *tocStripWrapper) Verify(orig, enc string) bool { return textnorm.StripTOC(orig) == enc }

// ---------- badge-strip ----------

type badgeStripWrapper struct{}

func (w *badgeStripWrapper) ID() string           { return "badge-strip" }
func (w *badgeStripWrapper) Detect(s string) bool { return textnorm.HasBadges(s) }
func (w *badgeStripWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripBadges(x) })
}
func (w *badgeStripWrapper) Encode(s string) (string, error) { return textnorm.StripBadges(s), nil }
func (w *badgeStripWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("badge-strip removes decorative badges")
}
func (w *badgeStripWrapper) Verify(orig, enc string) bool { return textnorm.StripBadges(orig) == enc }

// ---------- frontmatter-strip ----------

type frontmatterStripWrapper struct{}

func (w *frontmatterStripWrapper) ID() string           { return "frontmatter-strip" }
func (w *frontmatterStripWrapper) Detect(s string) bool { return textnorm.HasFrontmatter(s) }
func (w *frontmatterStripWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.StripFrontmatter(x) })
}
func (w *frontmatterStripWrapper) Encode(s string) (string, error) {
	return textnorm.StripFrontmatter(s), nil
}
func (w *frontmatterStripWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("frontmatter-strip removes metadata")
}
func (w *frontmatterStripWrapper) Verify(orig, enc string) bool {
	return textnorm.StripFrontmatter(orig) == enc
}

// ---------- empty-headings ----------

type emptyHeadingsWrapper struct{}

func (w *emptyHeadingsWrapper) ID() string           { return "empty-headings" }
func (w *emptyHeadingsWrapper) Detect(s string) bool { return textnorm.HasEmptyHeadings(s) }
func (w *emptyHeadingsWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.FoldEmptyHeadings(x) })
}
func (w *emptyHeadingsWrapper) Encode(s string) (string, error) {
	return textnorm.FoldEmptyHeadings(s), nil
}
func (w *emptyHeadingsWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("empty-headings removes zero-content headings")
}
func (w *emptyHeadingsWrapper) Verify(orig, enc string) bool {
	return textnorm.FoldEmptyHeadings(orig) == enc
}

// ---------- doubled-words ----------

type doubledWordsWrapper struct{}

func (w *doubledWordsWrapper) ID() string           { return "doubled-words" }
func (w *doubledWordsWrapper) Detect(s string) bool { return textnorm.HasDoubledWords(s) }
func (w *doubledWordsWrapper) EstimateSavings(s string) int {
	return hygieneEstimate(s, w.Detect, func(x string) string { return textnorm.CollapseDoubledWords(x) })
}
func (w *doubledWordsWrapper) Encode(s string) (string, error) {
	return textnorm.CollapseDoubledWords(s), nil
}
func (w *doubledWordsWrapper) Decode(s string) (string, error) {
	return s, fmt.Errorf("doubled-words collapses transcription artifacts")
}
func (w *doubledWordsWrapper) Verify(orig, enc string) bool {
	return textnorm.CollapseDoubledWords(orig) == enc
}
