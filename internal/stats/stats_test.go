package stats

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// helper to create temp store
func newTempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return s
}

func TestRecordAndGetSummary(t *testing.T) {
	s := newTempStore(t)
	// empty summary
	sum, err := s.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary error: %v", err)
	}
	if sum.TotalCommands != 0 {
		t.Fatalf("expected 0 commands, got %d", sum.TotalCommands)
	}

	// record some
	if err := s.Record("ls", "input1", "out1", 100, 40, 100); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := s.Record("git status", "input2", "out2", 200, 50, 200); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	sum, err = s.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary error: %v", err)
	}
	if sum.TotalCommands != 2 {
		t.Fatalf("expected 2 got %d", sum.TotalCommands)
	}
	if sum.TotalInput != 300 {
		t.Fatalf("expected total_input 300 got %d", sum.TotalInput)
	}
	if sum.TotalOutput != 90 {
		t.Fatalf("expected total_output 90 got %d", sum.TotalOutput)
	}
	if sum.TotalSaved != 210 {
		t.Fatalf("expected saved 210 got %d", sum.TotalSaved)
	}
	expectedPct := 70.0
	if sum.AvgSavingsPct < expectedPct-0.1 || sum.AvgSavingsPct > expectedPct+0.1 {
		t.Fatalf("expected pct ~70 got %.2f", sum.AvgSavingsPct)
	}
	if sum.TotalTimeMs != 300 {
		t.Fatalf("expected total_time 300 got %d", sum.TotalTimeMs)
	}
	if sum.AvgTimeMs != 150 {
		t.Fatalf("expected avg 150 got %d", sum.AvgTimeMs)
	}
	if len(sum.ByCommand) == 0 {
		t.Fatalf("expected by_command non-empty")
	}
	// by_day should have entries
	if len(sum.ByDay) == 0 {
		t.Fatalf("expected by_day non-empty")
	}
}

func TestGetAllDays(t *testing.T) {
	s := newTempStore(t)
	if err := s.Record("cmd1", "in", "out", 100, 20, 50); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("cmd2", "in", "out", 200, 40, 60); err != nil {
		t.Fatal(err)
	}
	days, err := s.GetAllDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) == 0 {
		t.Fatal("expected days")
	}
	// Today should be present
	found := false
	for _, d := range days {
		if d.Commands >= 2 && d.InputTokens == 300 {
			found = true
			if d.SavedTokens != 240 {
				t.Fatalf("saved expected 240 got %d", d.SavedTokens)
			}
			if d.SavingsPct < 79 || d.SavingsPct > 81 {
				t.Fatalf("pct expected ~80 got %.2f", d.SavingsPct)
			}
		}
	}
	if !found {
		t.Fatalf("today not found in days: %+v", days)
	}
}

func TestGetByWeekAndMonth(t *testing.T) {
	s := newTempStore(t)
	for i := 0; i < 3; i++ {
		if err := s.Record(fmt.Sprintf("cmd%d", i), "in", "out", 100, 50, 10); err != nil {
			t.Fatal(err)
		}
	}
	weeks, err := s.GetByWeek()
	if err != nil {
		t.Fatal(err)
	}
	if len(weeks) == 0 {
		t.Fatal("expected weeks")
	}
	months, err := s.GetByMonth()
	if err != nil {
		t.Fatal(err)
	}
	if len(months) == 0 {
		t.Fatal("expected months")
	}
	if weeks[0].Commands != 3 {
		t.Fatalf("expected 3 commands in week got %d", weeks[0].Commands)
	}
	if months[0].Month == "" {
		t.Fatal("expected month string")
	}
}

func TestGetRecent(t *testing.T) {
	s := newTempStore(t)
	for i := 0; i < 5; i++ {
		if err := s.Record(fmt.Sprintf("cmd-%d", i), "in", "out", 100, 20, 10); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	recent, err := s.GetRecent(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 {
		t.Fatalf("expected 3 got %d", len(recent))
	}
	// most recent should be cmd-4 first
	if !strings.Contains(recent[0].Cmd, "cmd-4") {
		t.Fatalf("expected most recent cmd-4 got %q", recent[0].Cmd)
	}
	// limit larger than count
	all, err := s.GetRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 got %d", len(all))
	}
}

func TestExportJSON_Structure(t *testing.T) {
	s := newTempStore(t)
	if err := s.Record("ls", "in", "out", 100, 20, 50); err != nil {
		t.Fatal(err)
	}
	data, err := s.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid json: %v data=%s", err, string(data))
	}
	// must have summary,daily,weekly,monthly
	for _, key := range []string{"summary", "daily", "weekly", "monthly"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("missing key %s in json", key)
		}
	}
	var summary GainSummary
	if err := json.Unmarshal(out["summary"], &summary); err != nil {
		t.Fatalf("summary unmarshal: %v", err)
	}
	if summary.TotalCommands != 1 {
		t.Fatalf("expected 1 command in summary got %d", summary.TotalCommands)
	}
	var daily []DayStats
	if err := json.Unmarshal(out["daily"], &daily); err != nil {
		t.Fatalf("daily unmarshal: %v", err)
	}
	if len(daily) == 0 {
		t.Fatal("expected daily non-empty")
	}
	// check weekly/monthly unmarshal
	var weekly []WeekStats
	if err := json.Unmarshal(out["weekly"], &weekly); err != nil {
		t.Fatalf("weekly unmarshal: %v", err)
	}
	var monthly []MonthStats
	if err := json.Unmarshal(out["monthly"], &monthly); err != nil {
		t.Fatalf("monthly unmarshal: %v", err)
	}

	// Golden check: summary json fields snake_case
	var rawSum map[string]interface{}
	if err := json.Unmarshal(out["summary"], &rawSum); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"total_commands", "total_input", "total_output", "total_saved", "avg_savings_pct", "total_time_ms", "avg_time_ms"} {
		if _, ok := rawSum[field]; !ok {
			t.Fatalf("summary missing field %s", field)
		}
	}
}

func TestExportCSV(t *testing.T) {
	s := newTempStore(t)
	if err := s.Record("ls", "in", "out", 100, 20, 50); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("git", "in", "out", 200, 40, 60); err != nil {
		t.Fatal(err)
	}
	csvStr, err := s.ExportCSV()
	if err != nil {
		t.Fatal(err)
	}
	if csvStr == "" {
		t.Fatal("empty csv")
	}
	// Should contain header
	if !strings.Contains(csvStr, "date,commands,input_tokens,output_tokens,saved_tokens,savings_pct,total_time_ms,avg_time_ms") {
		t.Fatalf("header missing in csv: %q", csvStr[:200])
	}
	// Parse csv
	r := csv.NewReader(strings.NewReader(csvStr))
	// Need to handle comment lines starting with # if present
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse error: %v csv=%q", err, csvStr)
	}
	foundHeader := false
	for _, rec := range records {
		if len(rec) > 0 && rec[0] == "date" {
			foundHeader = true
			break
		}
	}
	if !foundHeader {
		t.Fatalf("header not found parsed: %v", records)
	}
	// Should have at least 1 data row
	if len(records) < 2 {
		t.Fatalf("expected at least 1 data row got %d", len(records))
	}
}

func TestCleanupRetention(t *testing.T) {
	s := newTempStore(t)
	// Insert old record directly via DB to simulate 100 days ago
	oldTime := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	_, err := s.DB().Exec(`INSERT INTO commands (timestamp, cmd, input_tokens, output_tokens, saved, savings_pct, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		oldTime, "old-cmd", 100, 20, 80, 80.0, 10)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}
	// Recent record
	if err := s.Record("new-cmd", "in", "out", 100, 20, 10); err != nil {
		t.Fatal(err)
	}
	// Before cleanup, total should be 2
	sum, _ := s.GetSummary()
	if sum.TotalCommands != 2 {
		t.Fatalf("expected 2 before cleanup got %d", sum.TotalCommands)
	}
	// Cleanup 90d
	if err := s.Cleanup(90 * 24 * time.Hour); err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}
	sum, _ = s.GetSummary()
	if sum.TotalCommands != 1 {
		t.Fatalf("expected 1 after cleanup got %d, should have removed old", sum.TotalCommands)
	}
	recent, _ := s.GetRecent(10)
	if len(recent) != 1 || recent[0].Cmd != "new-cmd" {
		t.Fatalf("expected only new-cmd after cleanup got %v", recent)
	}
}

func TestConcurrency_Record(t *testing.T) {
	s := newTempStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cmd := fmt.Sprintf("cmd-%d", n%5)
			if err := s.Record(cmd, "in", "out", 100, 20, 10); err != nil {
				t.Errorf("Record error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	sum, err := s.GetSummary()
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalCommands != 50 {
		t.Fatalf("expected 50 got %d", sum.TotalCommands)
	}
	if sum.TotalSaved != 50*80 {
		t.Fatalf("expected saved %d got %d", 50*80, sum.TotalSaved)
	}
}

func TestGracefulIfDBMissing(t *testing.T) {
	// New with non-existent path should create, but test graceful handling when DB file removed
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "missing.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Remove file and close? Test GetSummary after deletion should still handle gracefully
	// Instead test New with empty path (default) doesn't error and returns graceful empty
	// Also test corrupted path fallback
	s2, err := New("")
	if err != nil {
		t.Fatalf("New empty path should not error, got %v", err)
	}
	// GetSummary should be graceful (empty, no panic)
	sum, err := s2.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary graceful error: %v", err)
	}
	// may have existing data if default path has data, but should not panic
	_ = sum
	// Ensure original s still works after db file exists
	if err := s.Record("test", "in", "out", 10, 5, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRetention_CustomDuration(t *testing.T) {
	s := newTempStore(t)
	// insert record 5 days ago
	old := time.Now().AddDate(0, 0, -5).Format(time.RFC3339)
	_, err := s.DB().Exec(`INSERT INTO commands (timestamp, cmd, input_tokens, output_tokens, saved, savings_pct, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		old, "old", 100, 50, 50, 50.0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record("new", "in", "out", 100, 50, 5); err != nil {
		t.Fatal(err)
	}
	// cleanup with 90d should keep both
	if err := s.Cleanup(90 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	sum, _ := s.GetSummary()
	if sum.TotalCommands != 2 {
		t.Fatalf("expected 2 kept got %d", sum.TotalCommands)
	}
	// cleanup with 3d should remove old
	if err := s.Cleanup(3 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	sum, _ = s.GetSummary()
	if sum.TotalCommands != 1 {
		t.Fatalf("expected 1 after 3d cleanup got %d", sum.TotalCommands)
	}
}

func TestAggregation_ByCommandAndDay(t *testing.T) {
	s := newTempStore(t)
	// Record same cmd multiple times to test aggregation
	for i := 0; i < 3; i++ {
		if err := s.Record("ls", "in", "out", 100, 25, 10); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.Record("git", "in", "out", 200, 100, 20); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.GetSummary()
	if err != nil {
		t.Fatal(err)
	}
	// Check by_command aggregation contains both
	foundLs := false
	foundGit := false
	for _, bc := range sum.ByCommand {
		if bc[0] == "ls" {
			foundLs = true
			if bc[1] != 3 {
				t.Fatalf("ls count expected 3 got %v", bc[1])
			}
		}
		if bc[0] == "git" {
			foundGit = true
		}
	}
	if !foundLs || !foundGit {
		t.Fatalf("by_command missing ls/git: %v", sum.ByCommand)
	}
	days, _ := s.GetAllDays()
	if len(days) == 0 {
		t.Fatal("expected days")
	}
	// daily should aggregate all 5
	if days[0].Commands != 5 {
		t.Fatalf("expected 5 commands in day got %d", days[0].Commands)
	}
}

func TestNew_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "test.db")
	s, err := New(nested)
	if err != nil {
		t.Fatalf("New nested failed: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	if err := s.Record("cmd", "in", "out", 10, 5, 1); err != nil {
		t.Fatal(err)
	}
}

func TestExportJSONAndCSV_EmptyDB(t *testing.T) {
	s := newTempStore(t)
	data, err := s.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Summary GainSummary  `json:"summary"`
		Daily   []DayStats   `json:"daily"`
		Weekly  []WeekStats  `json:"weekly"`
		Monthly []MonthStats `json:"monthly"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal empty json: %v", err)
	}
	if out.Summary.TotalCommands != 0 {
		t.Fatalf("expected 0 got %d", out.Summary.TotalCommands)
	}
	csvStr, err := s.ExportCSV()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvStr, "date,commands") {
		t.Fatalf("csv header missing: %q", csvStr)
	}
}

func TestXDGDataPath(t *testing.T) {
	dataHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	want := filepath.Join(dataHome, "tokenmill", "tracking.db")
	if got := defaultDBPath(); got != want {
		t.Fatalf("defaultDBPath = %q, want %q", got, want)
	}
	store, err := New("")
	if err != nil {
		t.Fatalf("New with XDG_DATA_HOME: %v", err)
	}
	defer store.Close()
	if store.dbPath != want {
		t.Fatalf("store dbPath = %q, want %q", store.dbPath, want)
	}
}

func TestDatabasePathOverride(t *testing.T) {
	configHome := t.TempDir()
	dataHome := t.TempDir()
	home := t.TempDir()
	customPath := filepath.Join(t.TempDir(), "custom", "tracking.db")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TOKENMILL_DATABASE_PATH", "")
	t.Setenv("TOKENMILL_TRACKING_DATABASE_PATH", "")

	configPath := filepath.Join(configHome, "tokenmill", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	customPathJSON, err := json.Marshal(customPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"tracking":{"database_path":`+string(customPathJSON)+`}}`), 0644); err != nil {
		t.Fatal(err)
	}

	store, err := New("")
	if err != nil {
		t.Fatalf("New with configured database path: %v", err)
	}
	defer store.Close()
	if store.dbPath != customPath {
		t.Fatalf("configured dbPath = %q, want %q", store.dbPath, customPath)
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("configured database was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataHome, "tokenmill", "tracking.db")); err == nil {
		t.Fatal("default XDG database must not be created when an explicit config path is set")
	}
}

func TestGetSummaryReturnsDatabaseErrors(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "tracking.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	if _, err := store.DB().Exec("DROP TABLE commands"); err != nil {
		t.Fatalf("drop commands table: %v", err)
	}
	if _, err := store.GetSummary(); err == nil {
		t.Fatal("GetSummary must return database errors instead of an empty success")
	}
}
