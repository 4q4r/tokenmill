package table

import (
	"strings"
	"testing"
)

var dockerPsGolden = strings.Join([]string{
	"CONTAINER ID   IMAGE          COMMAND                  CREATED        STATUS        PORTS                    NAMES",
	"abc123         nginx          \"nginx -g 'daemon off;'\"   2 hours ago    Up 2 hours    0.0.0.0:80->80/tcp       web",
	"def456         redis          \"docker-entrypoint.sh\"   3 hours ago    Up 3 hours    6379/tcp                 redis",
	"ghi789         postgres       \"docker-entrypoint.sh\"   4 hours ago    Up 4 hours    5432/tcp                 db",
	"jkl012         mongo          \"docker-entrypoint.sh\"   5 hours ago    Up 5 hours    27017/tcp                mongo",
	"mno345         alpine         \"/bin/sh\"                6 hours ago    Up 6 hours                             alpine-test",
}, "\n")

var kubectlGolden = strings.Join([]string{
	"NAME                     READY   STATUS    RESTARTS   AGE",
	"pod-abc-1234             1/1     Running   0          2d",
	"pod-def-5678             1/1     Running   1          1d",
	"pod-ghi-9012             0/1     Pending   0          3h",
	"pod-jkl-3456             1/1     Running   2          5d",
	"pod-mno-7890             1/1     Running   0          10d",
}, "\n")

var boxGolden = "┌─────────┬─────────┐\n│ Col1    │ Col2    │\n├─────────┼─────────┤\n│ a       │ b       │\n│ c       │ d       │\n│ e       │ f       │\n│ g       │ h       │\n└─────────┴─────────┘"

func TestDetectTable_Golden(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"docker ps", dockerPsGolden, true},
		{"kubectl", kubectlGolden, true},
		{"box-drawing", boxGolden, true},
		{"empty", "", false},
		{"non-table", "This is just a paragraph\nwith no table structure\njust sentences.\nAnother line\nYet another", false},
		{"4 rows should be false", "ID  Name  Value\n1   Alice 100\n2   Bob   200\n3   Carol 150", false},
		{"5 rows regular", "NAME     AGE  CITY       COUNTRY\nAlice    30   NYC        USA\nBob      25   LA         USA\nCarol    28   SF         USA\nDavid    32   Seattle    USA\nEve      29   Boston     USA", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectTable(tc.input)
			if got != tc.want {
				t.Fatalf("DetectTable %q = %v want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestTableToTSV_Golden(t *testing.T) {
	// docker ps
	tsv, err := TableToTSV(dockerPsGolden)
	if err != nil {
		t.Fatalf("TableToTSV docker ps error: %v", err)
	}
	if !IsTSV(tsv) {
		t.Fatalf("IsTSV should be true for docker ps TSV")
	}
	if !VerifyTable(dockerPsGolden, tsv) {
		t.Fatalf("VerifyTable failed for docker ps")
	}
	// Check delimiter is tab
	if !strings.Contains(tsv, "\t") {
		t.Fatalf("docker ps TSV should contain tab")
	}
	// kubectl
	tsv2, err2 := TableToTSV(kubectlGolden)
	if err2 != nil {
		t.Fatalf("TableToTSV kubectl error: %v", err2)
	}
	if !IsTSV(tsv2) {
		t.Fatalf("IsTSV should be true for kubectl TSV")
	}
	if !VerifyTable(kubectlGolden, tsv2) {
		t.Fatalf("VerifyTable failed for kubectl")
	}
	// box
	tsv3, err3 := TableToTSV(boxGolden)
	if err3 != nil {
		t.Fatalf("TableToTSV box error: %v", err3)
	}
	if !IsTSV(tsv3) {
		t.Fatalf("IsTSV should be true for box TSV")
	}
	if !VerifyTable(boxGolden, tsv3) {
		t.Fatalf("VerifyTable failed for box")
	}
}

func TestTableToTSV_Errors(t *testing.T) {
	_, err := TableToTSV("")
	if err == nil {
		t.Fatal("expected error for empty")
	}
	_, err2 := TableToTSV("just a paragraph\nno table\nplain text\nanother\none more")
	if err2 == nil {
		t.Fatal("expected error for non-table")
	}
}

func TestTableToTSV_PSVFallback(t *testing.T) {
	input := strings.Join([]string{
		"NAME          VALUE          DESCRIPTION",
		"foo           bar\tbaz        some desc",
		"alice         123            test",
		"bob           456            test2",
		"carol         789            test3",
		"dave          000            test4",
	}, "\n")
	tsv, err := TableToTSV(input)
	if err != nil {
		t.Fatalf("TableToTSV PSV fallback error: %v", err)
	}
	// Should use pipe delimiter
	if !strings.Contains(tsv, "|") {
		t.Fatalf("PSV fallback should contain |, got %q", tsv)
	}
	// Should not use tab as delimiter (but tabs remain inside cells)
	// Check that splitting by "|" yields consistent cols
	if !IsTSV(tsv) {
		t.Fatalf("IsTSV should be true for PSV fallback")
	}
	if !VerifyTable(input, tsv) {
		t.Fatalf("VerifyTable failed for PSV fallback")
	}
	// Verify delimiter is pipe, not tab for splitting
	lines := strings.Split(strings.TrimSpace(tsv), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			t.Fatalf("PSV line should have 3 cols via |, got %q -> %d", line, len(parts))
		}
	}
}

func TestIsTSV(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"tsv", "a\tb\tc\nd\te\tf\ng\th\ti", true},
		{"psv", "a|b|c\nd|e|f\ng|h|i", true},
		{"empty", "", false},
		{"single col", "a\nb\nc", false},
		{"non-tsv", "hello world\njust text", false},
		{"inconsistent", "a\tb\nc\td\te", false}, // inconsistent cols but 70%? 1 of 2 consistent -> 50% <70 false
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsTSV(tc.input)
			if got != tc.want {
				t.Fatalf("IsTSV %q = %v want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestVerifyTable(t *testing.T) {
	// Valid cases already tested via golden, test invalid
	if VerifyTable(dockerPsGolden, "a\tb\nd\te") {
		t.Fatal("Verify should fail for mismatched cells")
	}
	if VerifyTable("", "") != true {
		t.Fatal("empty both should be true")
	}
	if VerifyTable(dockerPsGolden, "") {
		t.Fatal("empty tsv should be false")
	}
	// Verify via cells equality: ensure that TSV with different delimiter fails
	tsv, _ := TableToTSV(kubectlGolden)
	// Modify tsv to break equality
	broken := strings.Replace(tsv, "pod-abc", "changed", 1)
	if VerifyTable(kubectlGolden, broken) {
		t.Fatal("Verify should fail for modified tsv")
	}
}

func TestDetectTable_AtLeast5Rows(t *testing.T) {
	// Exactly 5 rows including header -> should be true
	five := strings.Join([]string{
		"COL1     COL2     COL3",
		"a        b        c",
		"d        e        f",
		"g        h        i",
		"j        k        l",
	}, "\n")
	if !DetectTable(five) {
		t.Fatal("5 rows should be detected as table")
	}
	four := strings.Join([]string{
		"COL1     COL2     COL3",
		"a        b        c",
		"d        e        f",
		"g        h        i",
	}, "\n")
	if DetectTable(four) {
		t.Fatal("4 rows should not be detected as table per spec >=5")
	}
}

func TestTableToTSV_SplitByTwoSpaces(t *testing.T) {
	// Ensure split is by \s{2,} not single space
	input := strings.Join([]string{
		"FIRST      SECOND     THIRD",
		"a b        c d        e f",
		"g h        i j        k l",
		"m n        o p        q r",
		"s t        u v        w x",
		"y z        aa bb      cc dd",
	}, "\n")
	if !DetectTable(input) {
		t.Fatalf("should be detected as table")
	}
	tsv, err := TableToTSV(input)
	if err != nil {
		t.Fatalf("TableToTSV error: %v", err)
	}
	// Each cell should contain single space inside, not split
	lines := strings.Split(tsv, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 rows, got %d: %q", len(lines), tsv)
	}
	// Check first data row cell "a b" preserved
	if !strings.Contains(tsv, "a b") {
		t.Fatalf("cell with single space should be preserved, got %q", tsv)
	}
	if !VerifyTable(input, tsv) {
		t.Fatalf("Verify failed for single-space inside cells")
	}
}

func TestTableToTSV_PreservesEmptyBoxCell(t *testing.T) {
	input := strings.Join([]string{
		"┌────┬────┬────┐",
		"│ a  │    │ c  │",
		"│ d  │    │ f  │",
		"│ g  │    │ i  │",
		"│ j  │    │ l  │",
		"│ m  │    │ o  │",
		"└────┴────┴────┘",
	}, "\n")

	tsv, err := TableToTSV(input)
	if err != nil {
		t.Fatalf("TableToTSV error: %v", err)
	}
	if !strings.Contains(tsv, "a\t\tc") {
		t.Fatalf("middle empty cell was dropped: %q", tsv)
	}
	if !VerifyTable(input, tsv) {
		t.Fatalf("VerifyTable rejected positional empty cell: %q", tsv)
	}
}
