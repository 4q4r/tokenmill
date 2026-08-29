package stats

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tokenmill/tokenmill/internal/config"
	_ "modernc.org/sqlite"
)

// GainSummary aggregates overall statistics.
type GainSummary struct {
	TotalCommands int             `json:"total_commands"`
	TotalInput    int             `json:"total_input"`
	TotalOutput   int             `json:"total_output"`
	TotalSaved    int             `json:"total_saved"`
	AvgSavingsPct float64         `json:"avg_savings_pct"`
	TotalTimeMs   int64           `json:"total_time_ms"`
	AvgTimeMs     int64           `json:"avg_time_ms"`
	ByCommand     [][]interface{} `json:"by_command"`
	ByDay         [][]interface{} `json:"by_day"`
}

// DayStats aggregates per-day.
type DayStats struct {
	Date         string  `json:"date"`
	Commands     int     `json:"commands"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	SavedTokens  int     `json:"saved_tokens"`
	SavingsPct   float64 `json:"savings_pct"`
	TotalTimeMs  int64   `json:"total_time_ms"`
	AvgTimeMs    int64   `json:"avg_time_ms"`
}

// WeekStats aggregates per-week.
type WeekStats struct {
	WeekStart    string  `json:"week_start"`
	WeekEnd      string  `json:"week_end"`
	Commands     int     `json:"commands"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	SavedTokens  int     `json:"saved_tokens"`
	SavingsPct   float64 `json:"savings_pct"`
	TotalTimeMs  int64   `json:"total_time_ms"`
	AvgTimeMs    int64   `json:"avg_time_ms"`
}

// MonthStats aggregates per-month.
type MonthStats struct {
	Month        string  `json:"month"`
	Commands     int     `json:"commands"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	SavedTokens  int     `json:"saved_tokens"`
	SavingsPct   float64 `json:"savings_pct"`
	TotalTimeMs  int64   `json:"total_time_ms"`
	AvgTimeMs    int64   `json:"avg_time_ms"`
}

// CommandRecord represents a single command execution.
type CommandRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	Cmd          string    `json:"cmd"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	SavedTokens  int       `json:"saved_tokens"`
	SavingsPct   float64   `json:"savings_pct"`
	DurationMs   int64     `json:"duration_ms"`
	ProjectPath  string    `json:"project_path,omitempty"`
}

// Store manages SQLite tracking database.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	dbPath string
}

var configuredDatabasePath struct {
	sync.RWMutex
	path string
	set  bool
}

// SetConfiguredDatabasePath supplies the path selected by the CLI's --config
// resolution to existing callers of New("") without changing their API.
func SetConfiguredDatabasePath(path string) {
	configuredDatabasePath.Lock()
	configuredDatabasePath.path = path
	configuredDatabasePath.set = true
	configuredDatabasePath.Unlock()
}

// ResetConfiguredDatabasePath removes the one-command CLI override.
func ResetConfiguredDatabasePath() {
	configuredDatabasePath.Lock()
	configuredDatabasePath.path = ""
	configuredDatabasePath.set = false
	configuredDatabasePath.Unlock()
}

func cliDatabasePath() (string, bool) {
	configuredDatabasePath.RLock()
	defer configuredDatabasePath.RUnlock()
	return configuredDatabasePath.path, configuredDatabasePath.set
}

// defaultDBPath returns XDG fallback path ~/.local/share/tokenmill/tracking.db
func defaultDBPath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "tokenmill", "tracking.db")
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "tokenmill", "tracking.db")
	}
	// fallback to current dir
	return filepath.Join(".", "tracking.db")
}

// New creates a new Store. If dbPath is empty, uses default XDG path.
func New(dbPath string) (*Store, error) {
	if dbPath == "" {
		if configured, ok := cliDatabasePath(); ok {
			dbPath = configured
		} else {
			cfg, _ := config.Load()
			dbPath = cfg.DatabasePathOverride()
		}
		if dbPath == "" {
			dbPath = defaultDBPath()
		}
	}
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create dir: %w", err)
		}
	}
	// create file if not exists to ensure private perms
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("create db file: %w", err)
		}
		f.Close()
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// WAL + busy timeout
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA busy_timeout=5000;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")

	// create commands table if not exists (with project_path for --project filtering)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS commands (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			cmd TEXT NOT NULL,
			input_tokens INTEGER NOT NULL,
			output_tokens INTEGER NOT NULL,
			saved INTEGER NOT NULL,
			savings_pct REAL NOT NULL,
			duration_ms INTEGER NOT NULL,
			project_path TEXT NOT NULL DEFAULT ''
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}
	// Migration: add project_path column if missing (for existing DBs created before column existed)
	_, _ = db.Exec(`ALTER TABLE commands ADD COLUMN project_path TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_timestamp ON commands(timestamp);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_cmd ON commands(cmd);")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_project ON commands(project_path);")

	// also create daily view? For spec we create a view but not strictly needed
	_, _ = db.Exec(`
		CREATE VIEW IF NOT EXISTS daily AS
		SELECT DATE(timestamp) as date,
		       COUNT(*) as commands,
		       SUM(input_tokens) as input_tokens,
		       SUM(output_tokens) as output_tokens,
		       SUM(saved) as saved_tokens,
		       AVG(savings_pct) as savings_pct,
		       SUM(duration_ms) as total_time_ms
		FROM commands GROUP BY DATE(timestamp);
	`)

	return &Store{db: db, dbPath: dbPath}, nil
}

// DB exposes underlying sql.DB for testing (direct inserts).
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// currentProject returns cwd as project identifier; empty if cannot determine (global fallback).
func currentProject() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	// Normalize: clean path, keep absolute if possible
	return filepath.Clean(wd)
}

// Record inserts a command execution. Signature matches spec: cmd, input, output string, inputTokens, outputTokens, durationMs.
// input/output strings are stored only as token counts; original strings are ignored except for token calculations if needed.
// Project is auto-detected from cwd (for --project filtering); use RecordWithProject for explicit project.
func (s *Store) Record(cmd, input, output string, inputTokens, outputTokens int, durationMs int64) error {
	return s.RecordWithProject(cmd, input, output, inputTokens, outputTokens, durationMs, currentProject())
}

// RecordWithProject inserts with explicit project_path (for testing and programmatic use).
func (s *Store) RecordWithProject(cmd, input, output string, inputTokens, outputTokens int, durationMs int64, projectPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return fmt.Errorf("db not initialized")
	}
	saved := inputTokens - outputTokens // allow negative for audit (output > input) — no dead clamp, pct may be negative
	// pct: if inputTokens ==0 then 0 else saved/input*100 (allow negative)
	var pct float64
	if inputTokens > 0 {
		pct = float64(saved) / float64(inputTokens) * 100
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO commands (timestamp, cmd, input_tokens, output_tokens, saved, savings_pct, duration_ms, project_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ts, cmd, inputTokens, outputTokens, saved, pct, durationMs, projectPath)
	if err != nil {
		return err
	}
	// auto cleanup old? Not automatically; caller can call Cleanup
	return nil
}

// GetSummary returns aggregated summary.
func (s *Store) GetSummary() (*GainSummary, error) {
	return s.GetSummaryForProject("")
}

// GetSummaryForProject returns summary filtered by project_path (empty = global).
func (s *Store) GetSummaryForProject(project string) (*GainSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var totalCommands, totalInput, totalOutput, totalSaved int
	var totalTime int64
	var err error
	if project != "" {
		err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(saved),0), COALESCE(SUM(duration_ms),0) FROM commands WHERE project_path = ?`, project).Scan(&totalCommands, &totalInput, &totalOutput, &totalSaved, &totalTime)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(saved),0), COALESCE(SUM(duration_ms),0) FROM commands`).Scan(&totalCommands, &totalInput, &totalOutput, &totalSaved, &totalTime)
	}
	if err != nil {
		return nil, fmt.Errorf("query summary: %w", err)
	}
	var avgPct float64
	if totalInput > 0 {
		avgPct = float64(totalSaved) / float64(totalInput) * 100
	}
	var avgTime int64
	if totalCommands > 0 {
		avgTime = totalTime / int64(totalCommands)
	}

	// by_command
	byCommand := [][]interface{}{}
	var rows *sql.Rows
	if project != "" {
		rows, err = s.db.Query(`SELECT cmd, COUNT(*), COALESCE(SUM(saved),0), COALESCE(AVG(savings_pct),0), COALESCE(AVG(duration_ms),0) FROM commands WHERE project_path = ? GROUP BY cmd ORDER BY SUM(saved) DESC LIMIT 10`, project)
	} else {
		rows, err = s.db.Query(`SELECT cmd, COUNT(*), COALESCE(SUM(saved),0), COALESCE(AVG(savings_pct),0), COALESCE(AVG(duration_ms),0) FROM commands GROUP BY cmd ORDER BY SUM(saved) DESC LIMIT 10`)
	}
	if err != nil {
		return nil, fmt.Errorf("query by command: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cmd string
		var cnt, saved int
		var pct float64
		var avgMs float64
		if err := rows.Scan(&cmd, &cnt, &saved, &pct, &avgMs); err != nil {
			return nil, fmt.Errorf("scan by command: %w", err)
		}
		byCommand = append(byCommand, []interface{}{cmd, cnt, saved, pct, int64(avgMs)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate by command: %w", err)
	}

	// by_day last 30 days
	byDay := [][]interface{}{}
	var rows2 *sql.Rows
	if project != "" {
		rows2, err = s.db.Query(`SELECT DATE(timestamp), COALESCE(SUM(saved),0) FROM commands WHERE project_path = ? GROUP BY DATE(timestamp) ORDER BY DATE(timestamp) DESC LIMIT 30`, project)
	} else {
		rows2, err = s.db.Query(`SELECT DATE(timestamp), COALESCE(SUM(saved),0) FROM commands GROUP BY DATE(timestamp) ORDER BY DATE(timestamp) DESC LIMIT 30`)
	}
	if err != nil {
		return nil, fmt.Errorf("query by day: %w", err)
	}
	defer rows2.Close()
	var tmp [][]interface{}
	for rows2.Next() {
		var date string
		var saved int
		if err := rows2.Scan(&date, &saved); err != nil {
			return nil, fmt.Errorf("scan by day: %w", err)
		}
		tmp = append(tmp, []interface{}{date, saved})
	}
	if err := rows2.Err(); err != nil {
		return nil, fmt.Errorf("iterate by day: %w", err)
	}
	// reverse to chronological
	for i := len(tmp) - 1; i >= 0; i-- {
		byDay = append(byDay, tmp[i])
	}

	return &GainSummary{
		TotalCommands: totalCommands,
		TotalInput:    totalInput,
		TotalOutput:   totalOutput,
		TotalSaved:    totalSaved,
		AvgSavingsPct: avgPct,
		TotalTimeMs:   totalTime,
		AvgTimeMs:     avgTime,
		ByCommand:     byCommand,
		ByDay:         byDay,
	}, nil
}

// GetAllDays returns daily aggregates ordered chronologically.
func (s *Store) GetAllDays() ([]DayStats, error) {
	return s.GetAllDaysForProject("")
}

// GetAllDaysForProject returns daily aggregates filtered by project.
func (s *Store) GetAllDaysForProject(project string) ([]DayStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var rows *sql.Rows
	var err error
	if project != "" {
		rows, err = s.db.Query(`
		SELECT DATE(timestamp) as date,
		       COUNT(*) as commands,
		       COALESCE(SUM(input_tokens),0),
		       COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(saved),0),
		       COALESCE(SUM(duration_ms),0)
		FROM commands WHERE project_path = ?
		GROUP BY DATE(timestamp)
		ORDER BY DATE(timestamp) DESC`, project)
	} else {
		rows, err = s.db.Query(`
		SELECT DATE(timestamp) as date,
		       COUNT(*) as commands,
		       COALESCE(SUM(input_tokens),0),
		       COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(saved),0),
		       COALESCE(SUM(duration_ms),0)
		FROM commands
		GROUP BY DATE(timestamp)
		ORDER BY DATE(timestamp) DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("query daily stats: %w", err)
	}
	defer rows.Close()
	var result []DayStats
	for rows.Next() {
		var date string
		var commands, input, output, saved int
		var totalTime int64
		if err := rows.Scan(&date, &commands, &input, &output, &saved, &totalTime); err != nil {
			return nil, fmt.Errorf("scan daily stats: %w", err)
		}
		var pct float64
		if input > 0 {
			pct = float64(saved) / float64(input) * 100
		}
		var avg int64
		if commands > 0 {
			avg = totalTime / int64(commands)
		}
		result = append(result, DayStats{
			Date:         date,
			Commands:     commands,
			InputTokens:  input,
			OutputTokens: output,
			SavedTokens:  saved,
			SavingsPct:   pct,
			TotalTimeMs:  totalTime,
			AvgTimeMs:    avg,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily stats: %w", err)
	}
	// reverse to chronological oldest first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// GetByWeek returns weekly aggregates.
func (s *Store) GetByWeek() ([]WeekStats, error) {
	return s.GetByWeekForProject("")
}

// GetByWeekForProject returns weekly aggregates filtered by project.
func (s *Store) GetByWeekForProject(project string) ([]WeekStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var rows *sql.Rows
	var err error
	if project != "" {
		rows, err = s.db.Query(`
		SELECT
			DATE(timestamp, 'weekday 0', '-6 days') as week_start,
			DATE(timestamp, 'weekday 0') as week_end,
			COUNT(*) as commands,
			COALESCE(SUM(input_tokens),0),
			COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(saved),0),
			COALESCE(SUM(duration_ms),0)
		FROM commands WHERE project_path = ?
		GROUP BY week_start
		ORDER BY week_start DESC`, project)
	} else {
		rows, err = s.db.Query(`
		SELECT
			DATE(timestamp, 'weekday 0', '-6 days') as week_start,
			DATE(timestamp, 'weekday 0') as week_end,
			COUNT(*) as commands,
			COALESCE(SUM(input_tokens),0),
			COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(saved),0),
			COALESCE(SUM(duration_ms),0)
		FROM commands
		GROUP BY week_start
		ORDER BY week_start DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("query weekly stats: %w", err)
	}
	defer rows.Close()
	var result []WeekStats
	for rows.Next() {
		var weekStart, weekEnd string
		var commands, input, output, saved int
		var totalTime int64
		if err := rows.Scan(&weekStart, &weekEnd, &commands, &input, &output, &saved, &totalTime); err != nil {
			return nil, fmt.Errorf("scan weekly stats: %w", err)
		}
		var pct float64
		if input > 0 {
			pct = float64(saved) / float64(input) * 100
		}
		var avg int64
		if commands > 0 {
			avg = totalTime / int64(commands)
		}
		result = append(result, WeekStats{
			WeekStart:    weekStart,
			WeekEnd:      weekEnd,
			Commands:     commands,
			InputTokens:  input,
			OutputTokens: output,
			SavedTokens:  saved,
			SavingsPct:   pct,
			TotalTimeMs:  totalTime,
			AvgTimeMs:    avg,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate weekly stats: %w", err)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// GetByMonth returns monthly aggregates.
func (s *Store) GetByMonth() ([]MonthStats, error) {
	return s.GetByMonthForProject("")
}

// GetByMonthForProject returns monthly aggregates filtered by project.
func (s *Store) GetByMonthForProject(project string) ([]MonthStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var rows *sql.Rows
	var err error
	if project != "" {
		rows, err = s.db.Query(`
		SELECT
			strftime('%Y-%m', timestamp) as month,
			COUNT(*) as commands,
			COALESCE(SUM(input_tokens),0),
			COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(saved),0),
			COALESCE(SUM(duration_ms),0)
		FROM commands WHERE project_path = ?
		GROUP BY month
		ORDER BY month DESC`, project)
	} else {
		rows, err = s.db.Query(`
		SELECT
			strftime('%Y-%m', timestamp) as month,
			COUNT(*) as commands,
			COALESCE(SUM(input_tokens),0),
			COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(saved),0),
			COALESCE(SUM(duration_ms),0)
		FROM commands
		GROUP BY month
		ORDER BY month DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("query monthly stats: %w", err)
	}
	defer rows.Close()
	var result []MonthStats
	for rows.Next() {
		var month string
		var commands, input, output, saved int
		var totalTime int64
		if err := rows.Scan(&month, &commands, &input, &output, &saved, &totalTime); err != nil {
			return nil, fmt.Errorf("scan monthly stats: %w", err)
		}
		var pct float64
		if input > 0 {
			pct = float64(saved) / float64(input) * 100
		}
		var avg int64
		if commands > 0 {
			avg = totalTime / int64(commands)
		}
		result = append(result, MonthStats{
			Month:        month,
			Commands:     commands,
			InputTokens:  input,
			OutputTokens: output,
			SavedTokens:  saved,
			SavingsPct:   pct,
			TotalTimeMs:  totalTime,
			AvgTimeMs:    avg,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monthly stats: %w", err)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// GetRecent returns recent command records ordered newest first.
func (s *Store) GetRecent(limit int) ([]CommandRecord, error) {
	return s.GetRecentForProject(limit, "")
}

// GetRecentForProject returns recent records filtered by project_path (empty means all/global).
func (s *Store) GetRecentForProject(limit int, project string) ([]CommandRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 {
		limit = 10
	}
	var rows *sql.Rows
	var err error
	if project != "" {
		rows, err = s.db.Query(`SELECT timestamp, cmd, input_tokens, output_tokens, saved, savings_pct, duration_ms, project_path FROM commands WHERE project_path = ? ORDER BY timestamp DESC, id DESC LIMIT ?`, project, limit)
	} else {
		rows, err = s.db.Query(`SELECT timestamp, cmd, input_tokens, output_tokens, saved, savings_pct, duration_ms, project_path FROM commands ORDER BY timestamp DESC, id DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CommandRecord
	for rows.Next() {
		var tsStr, cmd string
		var input, output, saved int
		var pct float64
		var dur int64
		var proj string
		if err := rows.Scan(&tsStr, &cmd, &input, &output, &saved, &pct, &dur, &proj); err != nil {
			return nil, fmt.Errorf("scan recent commands: %w", err)
		}
		// parse timestamp
		ts, err := time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			ts, _ = time.Parse(time.RFC3339, tsStr)
			if ts.IsZero() {
				ts = time.Now().UTC()
			}
		}
		result = append(result, CommandRecord{
			Timestamp:    ts,
			Cmd:          cmd,
			InputTokens:  input,
			OutputTokens: output,
			SavedTokens:  saved,
			SavingsPct:   pct,
			DurationMs:   dur,
			ProjectPath:  proj,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent commands: %w", err)
	}
	return result, nil
}

// ExportJSON returns JSON like rtk: {"summary":{...}, "daily":[...], "weekly":[...], "monthly":[...]}
func (s *Store) ExportJSON() ([]byte, error) {
	return s.ExportJSONForProject("")
}

// ExportJSONForProject returns JSON filtered by project (empty = global).
func (s *Store) ExportJSONForProject(project string) ([]byte, error) {
	summary, err := s.GetSummaryForProject(project)
	if err != nil {
		return nil, err
	}
	daily, err := s.GetAllDaysForProject(project)
	if err != nil {
		return nil, err
	}
	weekly, err := s.GetByWeekForProject(project)
	if err != nil {
		return nil, err
	}
	monthly, err := s.GetByMonthForProject(project)
	if err != nil {
		return nil, err
	}
	// Ensure non-nil slices for JSON
	if daily == nil {
		daily = []DayStats{}
	}
	if weekly == nil {
		weekly = []WeekStats{}
	}
	if monthly == nil {
		monthly = []MonthStats{}
	}
	// Build export structure
	type exportData struct {
		Summary GainSummary  `json:"summary"`
		Daily   []DayStats   `json:"daily"`
		Weekly  []WeekStats  `json:"weekly"`
		Monthly []MonthStats `json:"monthly"`
	}
	data := exportData{
		Summary: *summary,
		Daily:   daily,
		Weekly:  weekly,
		Monthly: monthly,
	}
	return json.MarshalIndent(data, "", "  ")
}

// ExportCSV returns CSV string with header date,commands,input_tokens,output_tokens,saved_tokens,savings_pct,total_time_ms,avg_time_ms
func (s *Store) ExportCSV() (string, error) {
	return s.ExportCSVForProject("")
}

// ExportCSVForProject returns CSV filtered by project.
func (s *Store) ExportCSVForProject(project string) (string, error) {
	days, err := s.GetAllDaysForProject(project)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	// header
	header := []string{"date", "commands", "input_tokens", "output_tokens", "saved_tokens", "savings_pct", "total_time_ms", "avg_time_ms"}
	if err := w.Write(header); err != nil {
		return "", err
	}
	for _, d := range days {
		row := []string{
			d.Date,
			fmt.Sprintf("%d", d.Commands),
			fmt.Sprintf("%d", d.InputTokens),
			fmt.Sprintf("%d", d.OutputTokens),
			fmt.Sprintf("%d", d.SavedTokens),
			fmt.Sprintf("%.2f", d.SavingsPct),
			fmt.Sprintf("%d", d.TotalTimeMs),
			fmt.Sprintf("%d", d.AvgTimeMs),
		}
		if err := w.Write(row); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Cleanup removes records older than retention duration. Default 90 days if retention <=0.
func (s *Store) Cleanup(retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if retention <= 0 {
		retention = 90 * 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339Nano)
	_, err := s.db.Exec(`DELETE FROM commands WHERE timestamp < ?`, cutoff)
	return err
}
