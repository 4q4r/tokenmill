package textnorm

import "testing"

func TestFoldRanges(t *testing.T) {
	input := "lines 100, 101, 102, 103, 104 show the bug"
	want := "lines 100..104[, ] show the bug"
	if !HasFoldableRanges(input) {
		t.Fatal("HasFoldableRanges missed a consecutive run")
	}
	if got := FoldRanges(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := UnfoldRanges(want); got != input {
		t.Fatalf("unfold = %q, want %q", got, input)
	}
}

func TestFoldRangesSpaceSeparator(t *testing.T) {
	input := "grep hits: 12 13 14 15 16 17 in file"
	want := "grep hits: 12..17[ ] in file"
	folded := FoldRanges(input)
	if folded != want {
		t.Fatalf("got %q, want %q", folded, want)
	}
	if UnfoldRanges(folded) != input {
		t.Fatal("space-separator unfold mismatch")
	}
}

func TestFoldRangesRejectsNonConsecutive(t *testing.T) {
	input := "ports 80, 443, 8080, 9090 open"
	if FoldRanges(input) != input {
		t.Fatalf("non-consecutive run was folded: %q", FoldRanges(input))
	}
	if HasFoldableRanges(input) {
		t.Fatal("non-consecutive run flagged")
	}
}

func TestFoldRangesShortRunUntouched(t *testing.T) {
	input := "steps 1, 2, 3 done"
	if FoldRanges(input) != input {
		t.Fatalf("short run folded: %q", FoldRanges(input))
	}
}

func TestFoldRangesStep10(t *testing.T) {
	input := "ticks 10, 20, 30, 40, 50 done"
	want := "ticks 10..50 step 10[, ] done"
	folded := FoldRanges(input)
	if folded != want {
		t.Fatalf("got %q, want %q", folded, want)
	}
	if UnfoldRanges(folded) != input {
		t.Fatal("step-10 unfold mismatch")
	}
}

func TestUnfoldRangesMalformedEnvelopeUntouched(t *testing.T) {
	input := "range 500..1[, ] reversed"
	if got := UnfoldRanges(input); got != input {
		t.Fatalf("reversed envelope expanded: %q", got)
	}
	big := "range 1..999999[, ] huge"
	if got := UnfoldRanges(big); got != big {
		t.Fatal("oversized envelope expanded")
	}
}
