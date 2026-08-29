package table

import (
	"strings"
	"testing"
)

// P1 box short-circuit: containsBoxDrawing → return true without column check
// Should require parseBoxRow >=2 cols and 70% consistency
func TestP1_BoxShortCircuit_Failing(t *testing.T) {
	// 5 lines each containing a single box char but not a real table (only 1 col after parse)
	// Current DetectTable returns true (bug), after fix should be false
	input := strings.Join([]string{
		"│ single",
		"│ another",
		"│ third",
		"│ fourth",
		"│ fifth",
	}, "\n")
	got := DetectTable(input)
	if got != false {
		t.Fatalf("P1 box short-circuit: DetectTable for single-col box should be false, got %v", got)
	}
	// Also inconsistent box table: 4 rows with 2 cols, 1 row with 1 col inconsistency? Actually need 70% test
	// 5 lines: 3 with 2 cols, 2 with 1 col => consistent 3/5=60% <70 => false
	inconsistent := "│ a │ b │\n│ c │ d │\n│ e │ f │\n│ lonely\n│ single"
	// But lonely lines still contain box? "│ lonely" is 1 col
	got2 := DetectTable(inconsistent)
	if got2 != false {
		t.Fatalf("P1 box inconsistency: expected false for 60%% consistency, got %v", got2)
	}
	// Positive control: real box table should still be true
	if !DetectTable(boxGolden) {
		t.Fatalf("boxGolden should still be true")
	}
}

// P1 maxFreq>=5 breaks 70% for 5 rows (4/5=80% but fail)
func TestP1_MaxFreq70_Failing(t *testing.T) {
	// 5 rows where 4 have 3 cols consistent, 1 has 2 cols => 80% should be true
	input := strings.Join([]string{
		"COL1     COL2     COL3",
		"a        b        c",
		"d        e        f",
		"g        h", // 2 cols
		"j        k        l",
	}, "\n")
	got := DetectTable(input)
	if got != true {
		t.Fatalf("P1 maxFreq>=5: 4/5=80%% should be true, got %v", got)
	}
	// Also test ceil: 5*0.7=3.5 ceil=4 => need 4, exactly 4 should pass
	// Test 5 rows where only 3 consistent => 60% <70 => should be false
	input2 := strings.Join([]string{
		"COL1     COL2     COL3",
		"a        b        c",
		"d        e", // 2 cols
		"g        h", // 2 cols
		"j        k        l",
	}, "\n")
	got2 := DetectTable(input2)
	if got2 != false {
		t.Fatalf("P1 maxFreq 3/5 should be false, got %v", got2)
	}
}

// P1 ASCII | a | b | — parseRow uses fixed split instead of pipe
func TestP1_PipeRow_Failing(t *testing.T) {
	// At least parseRow should correctly parse pipe rows
	cells := parseRow("| a | b |")
	if len(cells) != 2 {
		t.Fatalf("P1 pipe parseRow: expected 2 cells for '| a | b |', got %v (%d)", cells, len(cells))
	}
	if cells[0] != "a" || cells[1] != "b" {
		t.Fatalf("P1 pipe parseRow: got %v want [a b]", cells)
	}
	cells3 := parseRow("| a | b | c |")
	if len(cells3) != 3 {
		t.Fatalf("P1 pipe parseRow: expected 3 cells, got %v", cells3)
	}
	// pipe without border should also parse
	cellsPipe := parseRow("| Col1 | Col2 | Col3 |")
	if len(cellsPipe) != 3 {
		t.Fatalf("P1 pipe parseRow header: expected 3, got %v", cellsPipe)
	}
	// border should be skipped
	if got := parseRow("|---|---|---|"); got != nil {
		t.Fatalf("pipe border |---|---| should be nil, got %v", got)
	}
	if got := parseRow("+---+---+"); got != nil {
		t.Fatalf("plus border should be nil, got %v", got)
	}
	// Full table conversion for ASCII box with +--- border (DetectTable true via ASCII fallback)
	asciiBox := strings.Join([]string{
		"+---+---+",
		"| a | b |",
		"| c | d |",
		"| e | f |",
		"| g | h |",
		"| i | j |",
		"+---+---+",
	}, "\n")
	tsv2, err2 := TableToTSV(asciiBox)
	if err2 != nil {
		t.Fatalf("P1 asciiBox TableToTSV error: %v", err2)
	}
	if !VerifyTable(asciiBox, tsv2) {
		t.Fatalf("P1 asciiBox Verify failed: asciiBox %q tsv %q", asciiBox, tsv2)
	}
	// Also pipe table with header and 5 data rows (needs DetectTable to succeed)
	// This one has no +---+ but has pipes; if DetectTable doesn't handle pure pipe, this will fail.
	// We test that parsePipe path still works when DetectTable is forced via pipe detection.
	// For now, we at least verify parseRow works for all rows.
	pipeInput := strings.Join([]string{
		"| Col1 | Col2 | Col3 |",
		"| a    | b    | c    |",
		"| d    | e    | f    |",
		"| g    | h    | i    |",
		"| j    | k    | l    |",
	}, "\n")
	// If DetectTable for pure pipe is false, we skip TableToTSV check; but parseRow already verified.
	// If it is true, we verify round-trip.
	if DetectTable(pipeInput) {
		tsv, err := TableToTSV(pipeInput)
		if err != nil {
			t.Fatalf("P1 pipe TableToTSV error: %v", err)
		}
		if !VerifyTable(pipeInput, tsv) {
			t.Fatalf("P1 pipe Verify failed")
		}
	}
}
