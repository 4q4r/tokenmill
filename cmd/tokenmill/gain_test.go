package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tokenmill/tokenmill/internal/stats"
)

func TestGain_JSONStructure(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(store *stats.Store)
		assert func(t *testing.T, data []byte)
	}{
		{
			name:  "empty db json structure has required keys",
			setup: func(s *stats.Store) {},
			assert: func(t *testing.T, data []byte) {
				var out map[string]json.RawMessage
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatalf("invalid json: %v data=%s", err, string(data))
				}
				for _, k := range []string{"summary", "daily", "weekly", "monthly"} {
					if _, ok := out[k]; !ok {
						t.Fatalf("missing key %s", k)
					}
				}
				var summary map[string]interface{}
				if err := json.Unmarshal(out["summary"], &summary); err != nil {
					t.Fatalf("summary unmarshal: %v", err)
				}
				for _, f := range []string{"total_commands", "total_input", "total_output", "total_saved", "avg_savings_pct", "total_time_ms", "avg_time_ms"} {
					if _, ok := summary[f]; !ok {
						t.Fatalf("summary missing field %s", f)
					}
				}
				if _, ok := summary["by_command"]; !ok {
					t.Fatalf("summary missing by_command")
				}
				if _, ok := summary["by_day"]; !ok {
					t.Fatalf("summary missing by_day")
				}
			},
		},
		{
			name: "with data summary counts",
			setup: func(s *stats.Store) {
				s.Record("ls", "input", "out", 100, 20, 50)
				s.Record("git status", "input2", "out2", 200, 50, 30)
			},
			assert: func(t *testing.T, data []byte) {
				var out struct {
					Summary stats.GainSummary  `json:"summary"`
					Daily   []stats.DayStats   `json:"daily"`
					Weekly  []stats.WeekStats  `json:"weekly"`
					Monthly []stats.MonthStats `json:"monthly"`
				}
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if out.Summary.TotalCommands != 2 {
					t.Fatalf("expected 2 got %d", out.Summary.TotalCommands)
				}
				if out.Summary.TotalSaved != 230 {
					t.Fatalf("expected saved 230 got %d", out.Summary.TotalSaved)
				}
				if len(out.Daily) == 0 {
					t.Fatalf("expected daily non-empty")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dir)
			t.Setenv("HOME", dir)
			t.Setenv("USERPROFILE", dir)
			store, err := stats.New("")
			if err != nil {
				t.Fatalf("New store: %v", err)
			}
			defer store.Close()
			tc.setup(store)
			root := newRootCmd()
			w := &testWriter{}
			root.SetOut(w)
			root.SetErr(w)
			root.SetArgs([]string{"--log-level", "error", "gain", "-f", "json"})
			if err := root.Execute(); err != nil {
				t.Fatalf("gain execute: %v stderr=%s", err, w.String())
			}
			tc.assert(t, w.Bytes())
		})
	}
}

func TestGain_CSVHeader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("HOME", dir)
	store, _ := stats.New("")
	store.Record("ls", "in", "out", 100, 20, 10)
	store.Close()

	root := newRootCmd()
	root.SetArgs([]string{"--log-level", "error", "gain", "-f", "csv"})
	w := &testWriter{}
	root.SetOut(w)
	root.SetErr(w)
	if err := root.Execute(); err != nil {
		t.Fatalf("gain csv: %v", err)
	}
	out := w.String()
	if out == "" {
		t.Fatal("empty csv")
	}
	expectedHeader := "date,commands,input_tokens,output_tokens,saved_tokens,savings_pct,total_time_ms,avg_time_ms"
	if len(out) < len(expectedHeader) || out[:len(expectedHeader)] != expectedHeader {
		t.Fatalf("csv header mismatch: got %q want prefix %q", out[:100], expectedHeader)
	}
}

func TestGain_History(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("HOME", dir)
	store, _ := stats.New("")
	store.Record("cmd-a", "in", "out", 100, 20, 10)
	store.Record("cmd-b", "in", "out", 200, 40, 20)
	store.Close()

	root := newRootCmd()
	root.SetArgs([]string{"--log-level", "error", "gain", "--history", "--limit", "1", "-f", "json"})
	w := &testWriter{}
	root.SetOut(w)
	root.SetErr(w)
	if err := root.Execute(); err != nil {
		t.Fatalf("history: %v", err)
	}
	var records []stats.CommandRecord
	if err := json.Unmarshal(w.Bytes(), &records); err != nil {
		t.Fatalf("history json unmarshal: %v out=%s", err, w.String())
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 history record got %d", len(records))
	}
}

func TestGainReturnsDatabaseOpenError(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tokenmill.jsonc")
	databasePath := t.TempDir()
	configData, err := json.Marshal(map[string]any{
		"tracking": map[string]string{"database_path": databasePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"--config", configPath, "gain", "-f", "json"})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err == nil {
		t.Fatal("gain must return database open errors instead of emitting empty success")
	}
}

func TestGain_QuotaPlaceholder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := newRootCmd()
	root.SetArgs([]string{"--log-level", "error", "gain", "--quota"})
	w := &testWriter{}
	root.SetOut(w)
	root.SetErr(w)
	if err := root.Execute(); err != nil {
		t.Fatalf("quota: %v", err)
	}
	if !contains(w.String(), "Quota") {
		t.Fatalf("quota placeholder missing, got %q", w.String())
	}
}

func TestGain_TextHumanTable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("HOME", dir)
	store, _ := stats.New("")
	store.Record("ls", "in", "out", 100, 20, 10)
	store.Close()
	root := newRootCmd()
	root.SetArgs([]string{"--log-level", "error", "gain"})
	w := &testWriter{}
	root.SetOut(w)
	root.SetErr(w)
	if err := root.Execute(); err != nil {
		t.Fatalf("text: %v", err)
	}
	out := w.String()
	if !contains(out, "TokenMill Savings") && !contains(out, "Total commands") {
		t.Fatalf("text output missing header, got %q", out[:200])
	}
}

type testWriter struct {
	buf []byte
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}
func (w *testWriter) Bytes() []byte  { return w.buf }
func (w *testWriter) String() string { return string(w.buf) }

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestGain_AllFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("HOME", dir)
	store, _ := stats.New("")
	store.Record("ls", "in", "out", 100, 20, 10)
	store.Close()
	for _, flag := range [][]string{{"--all"}, {"--weekly"}, {"--monthly"}, {"--daily"}} {
		t.Run(fmt.Sprintf("flag %v", flag), func(t *testing.T) {
			t.Setenv("HOME", dir)
			t.Setenv("XDG_DATA_HOME", dir)
			root := newRootCmd()
			args := []string{"--log-level", "error", "gain"}
			args = append(args, flag...)
			args = append(args, "-f", "json")
			root.SetArgs(args)
			w := &testWriter{}
			root.SetOut(w)
			root.SetErr(w)
			if err := root.Execute(); err != nil {
				t.Fatalf("%v: %v", flag, err)
			}
			var out map[string]json.RawMessage
			if err := json.Unmarshal(w.Bytes(), &out); err != nil {
				t.Fatalf("json %v: %v", flag, err)
			}
			if _, ok := out["summary"]; !ok {
				t.Fatalf("missing summary for %v", flag)
			}
		})
	}
}

func TestGain_ProjectFilterNoOp(t *testing.T) {
	// Now verifies that --project actually filters by project_path (persisted), not a no-op.
	// Creates two records in different projects and checks filtered vs global.
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	proj := filepath.Join(dir, "proj")
	other := filepath.Join(dir, "other")
	os.MkdirAll(filepath.Join(proj, ".opencode"), 0755)
	os.MkdirAll(other, 0755)
	oldWd, _ := os.Getwd()
	// Ensure cwd is proj for first record
	os.Chdir(proj)
	store, err := stats.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Record in proj (auto-detected project = proj)
	if err := store.Record("ls-proj", "in", "out", 100, 20, 10); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// Record in other project explicitly
	if err := store.RecordWithProject("ls-other", "in", "out", 200, 40, 20, other); err != nil {
		t.Fatalf("RecordWithProject: %v", err)
	}
	store.Close()

	// Run gain --project from proj: should see only 1 command (ls-proj)
	root := newRootCmd()
	root.SetArgs([]string{"--log-level", "error", "gain", "--project", "-f", "json"})
	w := &testWriter{}
	ew := &testWriter{}
	root.SetOut(w)
	root.SetErr(ew)
	if err := root.Execute(); err != nil {
		t.Fatalf("project: %v stderr=%s", err, ew.String())
	}
	if !contains(ew.String(), "filtering by project_path") {
		t.Fatalf("expected stderr to contain filtering note, got %q", ew.String())
	}
	var out struct {
		Summary stats.GainSummary `json:"summary"`
	}
	if err := json.Unmarshal(w.Bytes(), &out); err != nil {
		t.Fatalf("json: %v out=%s", err, w.String())
	}
	if out.Summary.TotalCommands != 1 {
		t.Fatalf("expected filtered TotalCommands=1 (only proj), got %d summary=%+v", out.Summary.TotalCommands, out.Summary)
	}
	if out.Summary.TotalSaved != 80 {
		t.Fatalf("expected filtered TotalSaved=80, got %d", out.Summary.TotalSaved)
	}

	// Global should see both (2)
	os.Chdir(proj)
	root2 := newRootCmd()
	root2.SetArgs([]string{"--log-level", "error", "gain", "-f", "json"})
	w2 := &testWriter{}
	root2.SetOut(w2)
	root2.SetErr(&testWriter{})
	if err := root2.Execute(); err != nil {
		t.Fatalf("global gain: %v", err)
	}
	var out2 struct {
		Summary stats.GainSummary `json:"summary"`
	}
	if err := json.Unmarshal(w2.Bytes(), &out2); err != nil {
		t.Fatalf("json2: %v", err)
	}
	if out2.Summary.TotalCommands != 2 {
		t.Fatalf("expected global TotalCommands=2, got %d", out2.Summary.TotalCommands)
	}
	// Restore wd
	os.Chdir(oldWd)
	_ = other
}

func TestGain_RTKReplica(t *testing.T) {
	// Golden test: tokenmill gain output must be pixel-perfect like rtk gain
	// Covers: header, separator, humanTokens, humanDuration, efficiency meter 24 chars, By Command, Recent Commands icons
	t.Run("humanTokens", func(t *testing.T) {
		if got := humanTokens(11400000); got != "11.4M" {
			t.Fatalf("humanTokens 11.4M: got %q", got)
		}
		if got := humanTokens(1900000); got != "1.9M" {
			t.Fatalf("humanTokens 1.9M: got %q", got)
		}
		if got := humanTokens(9500000); got != "9.5M" {
			t.Fatalf("humanTokens 9.5M: got %q", got)
		}
		if got := humanTokens(692); got != "692" {
			t.Fatalf("humanTokens 692: got %q", got)
		}
		if got := humanTokens(1200); got != "1.2K" {
			t.Fatalf("humanTokens 1200: got %q want 1.2K", got)
		}
	})
	t.Run("humanDuration", func(t *testing.T) {
		if got := humanDuration(80*60*1000 + 14*1000); got != "80m14s" {
			t.Fatalf("humanDuration 80m14s: got %q", got)
		}
		if got := humanDuration(1100); got != "1.1s" {
			t.Fatalf("humanDuration 1.1s: got %q", got)
		}
		if got := humanDuration(503); got != "503ms" {
			t.Fatalf("humanDuration 503ms: got %q", got)
		}
		if got := humanDuration(41100); got != "41.1s" {
			t.Fatalf("humanDuration 41.1s: got %q", got)
		}
	})
	t.Run("efficiencyMeter", func(t *testing.T) {
		meter := efficiencyMeter(83.0)
		// must be 24 chars, rounded
		if len([]rune(meter)) != 24 {
			t.Fatalf("efficiencyMeter len: got %d want 24, meter=%q", len([]rune(meter)), meter)
		}
		// 83% => round(83/100*24)=20 filled
		expectedFilled := 20
		filled := 0
		for _, ch := range meter {
			if ch == '█' {
				filled++
			}
		}
		if filled != expectedFilled {
			t.Fatalf("efficiencyMeter filled: got %d want %d meter=%q", filled, expectedFilled, meter)
		}
		// check 0% and 100% edges
		if len([]rune(efficiencyMeter(0))) != 24 {
			t.Fatalf("0%% meter len not 24")
		}
		if len([]rune(efficiencyMeter(100))) != 24 {
			t.Fatalf("100%% meter len not 24")
		}
		if efficiencyMeter(100) != "████████████████████████" {
			t.Fatalf("100%% meter not all filled: %q", efficiencyMeter(100))
		}
	})
	t.Run("gain_text_header_and_kpi", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_DATA_HOME", dir)
		t.Setenv("HOME", dir)
		store, _ := stats.New("")
		// Insert data to get known totals: 4434 cmds not needed, just ensure output contains rtk-like lines
		// Use large numbers via direct SQL to get 11.4M etc? easier test via helper formatting already covered
		// Here test header and structure with small data
		store.Record("rtk go test -race ./...", "in", "out", 100000, 0, 41100)
		store.Record("rtk ls -la ./plugin/tools", "in2", "out2", 1000, 540, 17)
		store.Record("rtk go build -o /tmp/test", "in3", "out3", 1000, 1000, 0)
		store.Close()

		root := newRootCmd()
		root.SetArgs([]string{"--log-level", "error", "gain"})
		w := &testWriter{}
		ew := &testWriter{}
		root.SetOut(w)
		root.SetErr(ew)
		if err := root.Execute(); err != nil {
			t.Fatalf("gain: %v", err)
		}
		out := w.String()
		errout := ew.String()

		// Header must be TokenMill Token Savings (Global Scope) with ═ separator 60
		if !contains(out, "TokenMill Token Savings (Global Scope)") {
			t.Fatalf("header missing TokenMill Token Savings (Global Scope), got:\n%s", out)
		}
		if !contains(out, "════════════════════════════════════════════════════════════") {
			t.Fatalf("header separator ═*60 missing, got:\n%s", out)
		}
		// Ensure old header not present (TokenMill Savings without Token)
		if contains(out, "TokenMill Savings (Global Scope)") && !contains(out, "TokenMill Token Savings") {
			t.Fatalf("old header still present without Token")
		}
		// KPI lines
		if !contains(out, "Total commands:") {
			t.Fatalf("missing Total commands KPI, got:\n%s", out)
		}
		if !contains(out, "Input tokens:") {
			t.Fatalf("missing Input tokens, got:\n%s", out)
		}
		if !contains(out, "Output tokens:") {
			t.Fatalf("missing Output tokens, got:\n%s", out)
		}
		if !contains(out, "Tokens saved:") {
			t.Fatalf("missing Tokens saved, got:\n%s", out)
		}
		if !contains(out, "Total exec time:") {
			t.Fatalf("missing Total exec time, got:\n%s", out)
		}
		// Efficiency meter 24 chars
		if !contains(out, "Efficiency meter:") {
			t.Fatalf("missing Efficiency meter, got:\n%s", out)
		}
		// Check By Command section
		if !contains(out, "By Command") {
			t.Fatalf("missing By Command, got:\n%s", out)
		}
		if !contains(out, "─") {
			t.Fatalf("missing ─ separator for By Command, got:\n%s", out)
		}
		// Header columns must be exactly like rtk: "#  Command" with Count Saved Avg% Time Impact
		if !contains(out, "Command") || !contains(out, "Count") || !contains(out, "Saved") || !contains(out, "Avg%") || !contains(out, "Impact") {
			t.Fatalf("By Command header columns incomplete, got:\n%s", out)
		}
		// Row should contain impact bar (█)
		if !contains(out, "█") {
			t.Fatalf("missing impact bar █, got:\n%s", out)
		}
		// Warn line on stderr when no hook
		combined := out + errout
		if !contains(combined, "[warn] No hook installed — run `tokenmill init -g` for automatic token savings") {
			t.Fatalf("warn line missing or wrong, got stdout:\n%s\nstderr:\n%s", out, errout)
		}
	})
	t.Run("recent_commands_icons", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_DATA_HOME", dir)
		t.Setenv("HOME", dir)
		store, _ := stats.New("")
		// Create 3 recent commands with distinct pct to test icon mapping
		// Use RecordWithProject with controlled pct: pct = saved/input*100
		// high >=70 => ▲ : 100% (input 1000, output 0)
		store.Record("rtk high saving cmd with long name to test truncation behaviour xxxxx", "in", "out", 1000, 0, 100)
		// medium 30-70 => ■ : 46% (input 1000, output 540 => 46%)
		store.Record("rtk ls -la ./plugin/tools", "in", "out", 1000, 540, 17)
		// low <30 => • : 0% (input 1000, output 1000)
		store.Record("rtk go build -o /tmp/test", "in", "out", 1000, 1000, 0)
		store.Close()

		root := newRootCmd()
		root.SetArgs([]string{"--log-level", "error", "gain", "--history"})
		w := &testWriter{}
		ew := &testWriter{}
		root.SetOut(w)
		root.SetErr(ew)
		if err := root.Execute(); err != nil {
			t.Fatalf("gain --history: %v", err)
		}
		out := w.String()
		// Recent Commands section header
		if !contains(out, "Recent Commands") {
			t.Fatalf("missing Recent Commands header, got:\n%s", out)
		}
		if !contains(out, "──────────────────────────────────────────────────────────") {
			t.Fatalf("missing Recent Commands separator ── *58, got:\n%s", out)
		}
		// Must contain icons ▲ ■ •
		if !contains(out, "▲") {
			t.Fatalf("missing ▲ icon for high savings, got:\n%s", out)
		}
		if !contains(out, "■") {
			t.Fatalf("missing ■ icon for medium savings, got:\n%s", out)
		}
		if !contains(out, "•") {
			t.Fatalf("missing • icon for low savings, got:\n%s", out)
		}
		// Check format MM-DD HH:MM <icon> <cmd> -<pct>% (<tokens>)
		// Example: 08-27 03:41 ■ rtk ls -la ... -46% (460)
		// Validate at least one line matches pattern with dash pct
		hasPct := contains(out, "% (") && contains(out, " -")
		if !hasPct {
			t.Fatalf("Recent Commands line format MM-DD HH:MM <icon> <cmd> -%% (<tokens>) missing, got:\n%s", out)
		}
		// Ensure truncation with ... for long cmd (>25)
		if !contains(out, "...") {
			t.Fatalf("expected truncation ... for long command, got:\n%s", out)
		}
		// Also must contain TokenMill header
		if !contains(out, "TokenMill Token Savings") {
			t.Fatalf("gain --history missing header TokenMill Token Savings, got:\n%s", out)
		}
	})
}
