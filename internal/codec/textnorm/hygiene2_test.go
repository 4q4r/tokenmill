package textnorm

import (
	"strings"
	"testing"
)

func TestNormalizeCRLF(t *testing.T) {
	input := "line1\r\nline2\rline3\r\n"
	want := "line1\nline2\nline3\n"
	if !HasCRLF(input) {
		t.Fatal("HasCRLF missed")
	}
	if got := NormalizeCRLF(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if NormalizeCRLF(want) != want {
		t.Fatal("idempotency broken")
	}
	if HasCRLF(want) {
		t.Fatal("clean text flagged")
	}
}

func TestStripEdgeBlankLines(t *testing.T) {
	input := "\n\n\ncontent\nmore\n\n\n"
	want := "content\nmore"
	if !HasEdgeBlankLines(input) {
		t.Fatal("HasEdgeBlankLines missed")
	}
	if got := StripEdgeBlankLines(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasEdgeBlankLines(want) {
		t.Fatal("clean text flagged")
	}
}

func TestNormalizeUnicodeLineSeparators(t *testing.T) {
	input := "a\u2028b\u2029c"
	want := "a\nb\nc"
	if !HasUnicodeLineSeparators(input) {
		t.Fatal("missed U+2028/2029")
	}
	if got := NormalizeUnicodeLineSeparators(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if HasUnicodeLineSeparators(want) {
		t.Fatal("clean text flagged")
	}
}

func TestStripVariationSelectors(t *testing.T) {
	input := "emoji\uFE0Fhere\uFE0Etext"
	if !HasVariationSelectors(input) {
		t.Fatal("missed variation selectors")
	}
	want := "emojiiheretext"
	got := StripVariationSelectors(input)
	if got != "emojiheretext" {
		t.Fatalf("got %q", got)
	}
	_ = want
	if HasVariationSelectors(got) {
		t.Fatal("clean text flagged")
	}
}

func TestStripHTMLCommentsText(t *testing.T) {
	input := "before<!-- hidden note -->after"
	got := StripHTMLCommentsText(input)
	if got != "beforeafter" {
		t.Fatalf("got %q", got)
	}
	if HasHTMLComments(got) {
		t.Fatal("clean text flagged")
	}
}

func TestSetextToATX(t *testing.T) {
	input := "Title\n=====\nBody text\nSubtitle\n-----\nBody"
	got := SetextToATX(input)
	if !strings.Contains(got, "# Title") || !strings.Contains(got, "## Subtitle") {
		t.Fatalf("setext conversion failed: %q", got)
	}
	if !strings.Contains(got, "Body text") && !strings.Contains(got, "Body") {
		t.Fatal("body lost")
	}
}

func TestStandardizeListMarkers(t *testing.T) {
	input := "* item one\n* item two\n- item three"
	got := StandardizeListMarkers(input)
	if strings.Contains(got, "* ") {
		t.Fatalf("asterisk bullet remains: %q", got)
	}
	if !strings.Contains(got, "- item one") {
		t.Fatalf("dash bullet missing: %q", got)
	}
	if !HasAsteriskBullets(input) {
		t.Fatal("HasAsteriskBullets missed")
	}
}

func TestStripHorizontalRules(t *testing.T) {
	input := "above\n---\nbelow\n***\nafter"
	got := StripHorizontalRules(input)
	if strings.Contains(got, "---") || strings.Contains(got, "***") {
		t.Fatalf("horizontal rules remain: %q", got)
	}
	if !strings.Contains(got, "above") || !strings.Contains(got, "below") {
		t.Fatalf("content lost: %q", got)
	}
}

func TestStripTOC(t *testing.T) {
	input := "# Doc\n\n## Table of Contents\n\n- [Intro](#intro)\n- [Setup](#setup)\n\n## Intro\n\nContent here."
	if !HasTOC(input) {
		t.Fatal("HasTOC missed")
	}
	got := StripTOC(input)
	if strings.Contains(got, "[Intro](#intro)") {
		t.Fatalf("TOC links remain: %q", got)
	}
	if !strings.Contains(got, "Content here") {
		t.Fatal("content lost")
	}
}

func TestStripBadges(t *testing.T) {
	input := "[![Build](https://img.shields.io/badge/build-passing-brightgreen)](https://ci.example.com)\n# Project"
	got := StripBadges(input)
	if strings.Contains(got, "shields.io") {
		t.Fatalf("badge remains: %q", got)
	}
	if !strings.Contains(got, "# Project") {
		t.Fatal("content lost")
	}
}

func TestStripFrontmatter(t *testing.T) {
	input := "---\ntitle: Test\n---\n\n# Content"
	got := StripFrontmatter(input)
	if strings.HasPrefix(got, "---") {
		t.Fatalf("frontmatter remains: %q", got)
	}
	if !strings.Contains(got, "# Content") {
		t.Fatal("content lost")
	}
}

func TestFoldEmptyHeadings(t *testing.T) {
	input := "## Parent\n## Child\ncontent"
	if !HasEmptyHeadings(input) {
		t.Fatal("HasEmptyHeadings missed")
	}
	got := FoldEmptyHeadings(input)
	if strings.Contains(got, "## Parent") {
		t.Fatalf("empty parent remains: %q", got)
	}
	if !strings.Contains(got, "## Child") || !strings.Contains(got, "content") {
		t.Fatalf("content lost: %q", got)
	}
}

func TestCollapseDoubledWords(t *testing.T) {
	input := "the the cat is is here"
	want := "the cat is here"
	if !HasDoubledWords(input) {
		t.Fatal("HasDoubledWords missed")
	}
	if got := CollapseDoubledWords(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	normal := "the cat and the dog"
	if CollapseDoubledWords(normal) != normal {
		t.Fatal("normal text changed")
	}
}
