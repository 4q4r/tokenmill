package stats

import (
	"path/filepath"
	"testing"
)

// TestRecord_NegativeSaved verifies Record allows negative saved for audit when output > input.
// RED: if Record clamped saved to 0, this would fail. GREEN: saved = input - output (negative) is stored.
func TestRecord_NegativeSaved(t *testing.T) {
	s := newTempStore(t)
	// also test via helper ensure clean
	if err := s.Record("test-neg", "in", "out", 100, 150, 10); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	sum, err := s.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary failed: %v", err)
	}
	if sum.TotalSaved != -50 {
		t.Fatalf("expected TotalSaved -50 (100-150) got %d", sum.TotalSaved)
	}
	if sum.TotalSaved >= 0 {
		t.Fatalf("expected negative TotalSaved for output>input, got %d", sum.TotalSaved)
	}
	// also check pct negative
	if sum.AvgSavingsPct >= 0 {
		t.Fatalf("expected negative AvgSavingsPct got %.2f", sum.AvgSavingsPct)
	}
	recent, err := s.GetRecent(1)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 recent got %d", len(recent))
	}
	if recent[0].SavedTokens != -50 {
		t.Fatalf("expected saved -50 got %d", recent[0].SavedTokens)
	}
	if recent[0].SavingsPct >= 0 {
		t.Fatalf("expected negative pct got %.2f", recent[0].SavingsPct)
	}
	// second: multiple negatives aggregate
	s2 := newTempStore(t)
	// Use isolated store; verify aggregate negative with separate DB path to avoid cross-contamination
	_ = s2
	// reuse first store: add another negative and check aggregate
	if err := s.Record("test-neg2", "in", "out", 50, 100, 5); err != nil {
		t.Fatalf("Record2 failed: %v", err)
	}
	sum2, _ := s.GetSummary()
	if sum2.TotalSaved != -100 {
		t.Fatalf("expected TotalSaved -100 after two negatives ( -50 + -50 ) got %d TotalInput %d TotalOutput %d", sum2.TotalSaved, sum2.TotalInput, sum2.TotalOutput)
	}
	// ensure DB file isolation test for newTempStore helper
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "neg.db")
	s3, err := New(dbPath)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer s3.Close()
	if err := s3.Record("a", "in", "out", 10, 20, 1); err != nil {
		t.Fatalf("Record s3 failed: %v", err)
	}
	sum3, _ := s3.GetSummary()
	if sum3.TotalSaved != -10 {
		t.Fatalf("expected -10 got %d", sum3.TotalSaved)
	}
}

// TestRecord_ZeroAndPositive ensures positive and zero cases still work.
func TestRecord_ZeroAndPositive(t *testing.T) {
	s := newTempStore(t)
	if err := s.Record("pos", "in", "out", 200, 50, 10); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	sum, _ := s.GetSummary()
	if sum.TotalSaved != 150 {
		t.Fatalf("expected 150 got %d", sum.TotalSaved)
	}
	if err := s.Record("zero", "in", "out", 100, 100, 5); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	sum, _ = s.GetSummary()
	if sum.TotalSaved != 150 {
		t.Fatalf("expected 150 after zero (150+0) got %d", sum.TotalSaved)
	}
}
