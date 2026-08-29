//go:build linux

package corpus

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/replay"
)

func TestWriterTolerateKeepsCorpusPublishable(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tolerateErr := writer.Tolerate(Quarantine{
		Line:    2,
		Offset:  1,
		Code:    CodeInputJSON,
		Message: "tolerated entry",
		Raw:     []byte("raw-bytes"),
	})
	if tolerateErr != nil {
		t.Fatalf("Tolerate: %v", tolerateErr)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, statErr := os.Stat(output); statErr != nil {
		t.Fatalf("published output missing after tolerated entry: %v", statErr)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 {
		t.Fatalf("quarantine count = %d, want 1", len(quarantined))
	}
	if quarantined[0].Code != CodeInputJSON || quarantined[0].Message != "tolerated entry" {
		t.Fatalf("quarantine entry = %#v, want tolerated input entry", quarantined[0])
	}
	if !bytes.Equal(quarantined[0].Raw, []byte("raw-bytes")) {
		t.Fatalf("quarantine raw = %q, want raw-bytes", quarantined[0].Raw)
	}
}

func TestWriterTolerateFailsClosedOnEntryBudgetExhaustion(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxQuarantineEntries: 1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Tolerate(Quarantine{Code: CodeInputJSON, Raw: []byte("a")}); err != nil {
		t.Fatalf("first Tolerate: %v", err)
	}
	secondErr := writer.Tolerate(Quarantine{Code: CodeInputJSON, Raw: []byte("b")})
	if secondErr == nil || CodeOf(secondErr) != CodeInputJSON {
		t.Fatalf("second Tolerate error = %v, want %s", secondErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after budget exhaustion: %v", statErr)
	}
}

func TestWriterTolerateFailsClosedOnByteBudgetExhaustion(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxLineBytes: 1, MaxQuarantineBytes: 2})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tolerateErr := writer.Tolerate(Quarantine{Code: CodeInputJSON, Raw: []byte("abc")})
	if tolerateErr == nil || CodeOf(tolerateErr) != CodeInputJSON {
		t.Fatalf("Tolerate error = %v, want %s", tolerateErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine byte budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after byte budget exhaustion: %v", statErr)
	}
}

func TestWriterTolerateFailsClosedOnUnknownCode(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	tolerateErr := writer.Tolerate(Quarantine{Code: "E_UNKNOWN", Raw: []byte("a")})
	if tolerateErr == nil {
		t.Fatal("Tolerate accepted an unknown quarantine code")
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after unknown quarantine code")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after unknown code: %v", statErr)
	}
}

func createOpenCodeZeroPartFixtureDB(t *testing.T, path string, emptyMessages int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("Close database: %v", closeErr)
		}
	}()
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, version TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, message_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO session (id, version) VALUES ('session-zero', '1')`,
		`INSERT INTO message (id, session_id, data) VALUES ('message-ok', 'session-zero', '{"role":"assistant","time":{"created":2000},"model":"fixture-model"}')`,
		`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-ok', 'session-zero', 'message-ok', '{"type":"text","text":"hello"}')`,
	}
	for index := 0; index < emptyMessages; index++ {
		statements = append(statements, "INSERT INTO message (id, session_id, data) VALUES ('message-empty-"+string(rune('a'+index))+"', 'session-zero', '{\"role\":\"user\",\"time\":{\"created\":1000}}')")
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("Exec %q: %v", statement, err)
		}
	}
}

func TestOpenCodeSourceToleratesZeroPartMessagesAndImportsTheRest(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	createOpenCodeZeroPartFixtureDB(t, databasePath, 1)
	before := fileHash(t, databasePath)

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := source.Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readCorpusRecords(t, output)
	if len(records) != 1 {
		t.Fatalf("record count = %d, want the single valid message", len(records))
	}
	if records[0].TurnID != "message-ok" {
		t.Fatalf("published record turn = %q, want message-ok", records[0].TurnID)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 {
		t.Fatalf("quarantine count = %d, want 1", len(quarantined))
	}
	if quarantined[0].Code != CodeInputJSON {
		t.Fatalf("quarantine code = %q, want %s", quarantined[0].Code, CodeInputJSON)
	}
	if !strings.Contains(quarantined[0].Message, "message-empty") || !strings.Contains(quarantined[0].Message, "session-zero") {
		t.Fatalf("quarantine message = %q, want session and message identifiers", quarantined[0].Message)
	}
	if !bytes.Contains(quarantined[0].Raw, []byte(`"role":"user"`)) {
		t.Fatalf("quarantine raw = %q, want the raw message data", quarantined[0].Raw)
	}
	if after := fileHash(t, databasePath); after != before {
		t.Fatalf("SQLite source hash changed: before=%s after=%s", before, after)
	}
}

func createOpenCodeCredentialFixtureDB(t *testing.T, path string, credentialMessages int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("Close database: %v", closeErr)
		}
	}()
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, version TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, message_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO session (id, version) VALUES ('session-cred', '1')`,
		`INSERT INTO message (id, session_id, data) VALUES ('message-ok', 'session-cred', '{"role":"assistant","time":{"created":2000},"model":"fixture-model"}')`,
		`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-ok', 'session-cred', 'message-ok', '{"type":"text","text":"hello"}')`,
	}
	for index := 0; index < credentialMessages; index++ {
		id := "message-bad-" + string(rune('a'+index))
		statements = append(statements,
			"INSERT INTO message (id, session_id, data) VALUES ('"+id+"', 'session-cred', '{\"role\":\"user\",\"time\":{\"created\":1000}}')",
			"INSERT INTO part (id, session_id, message_id, data) VALUES ('part-bad-"+string(rune('a'+index))+"', 'session-cred', '"+id+"', '{\"type\":\"future_part\",\"data:image/png,AAAABBBBCCCC\":\"x\"}')",
		)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("Exec %q: %v", statement, err)
		}
	}
}

func TestOpenCodeSourceToleratesUnredactableCredentialRecords(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	createOpenCodeCredentialFixtureDB(t, databasePath, 1)
	before := fileHash(t, databasePath)

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := source.Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	published := mustReadFile(t, output)
	if bytes.Contains(published, []byte("AAAABBBBCCCC")) {
		t.Fatal("credential-like value was published to the corpus output")
	}
	records := readCorpusRecords(t, output)
	if len(records) != 1 || records[0].TurnID != "message-ok" {
		t.Fatalf("record count/turn = %d/%q, want only message-ok", len(records), records[0].TurnID)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 {
		t.Fatalf("quarantine count = %d, want 1", len(quarantined))
	}
	if quarantined[0].Code != CodeSecretInCorpus {
		t.Fatalf("quarantine code = %q, want %s", quarantined[0].Code, CodeSecretInCorpus)
	}
	if !bytes.Contains(quarantined[0].Raw, []byte("message-bad-a")) {
		t.Fatalf("quarantine raw = %q, want the credential-bearing record", quarantined[0].Raw)
	}
	if after := fileHash(t, databasePath); after != before {
		t.Fatalf("SQLite source hash changed: before=%s after=%s", before, after)
	}
}

func TestOpenCodeSourceFailsClosedWhenCredentialRecordsExceedBudget(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	createOpenCodeCredentialFixtureDB(t, databasePath, 2)
	before := fileHash(t, databasePath)

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxQuarantineEntries: 1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := source.Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after budget exhaustion: %v", statErr)
	}
	if after := fileHash(t, databasePath); after != before {
		t.Fatalf("SQLite source hash changed: before=%s after=%s", before, after)
	}
}

const tolerateCredentialMarker = "AAAABBBBCCCC"

func TestTruncateQuarantineMessageKeepsValidUTF8(t *testing.T) {
	ascii := strings.Repeat("a", maxQuarantineMessageBytes)
	if got := truncateQuarantineMessage(ascii); got != ascii {
		t.Fatalf("ascii truncation changed exact-size input: %d vs %d", len(got), len(ascii))
	}
	if got := truncateQuarantineMessage("short"); got != "short" {
		t.Fatalf("under-limit message changed: %q", got)
	}

	multibyte := strings.Repeat("ö", maxQuarantineMessageBytes/2) + strings.Repeat("\U0001D11E", maxQuarantineMessageBytes)
	truncated := truncateQuarantineMessage(multibyte)
	if len(truncated) > maxQuarantineMessageBytes {
		t.Fatalf("truncated length = %d, want <= %d", len(truncated), maxQuarantineMessageBytes)
	}
	if !utf8.ValidString(truncated) {
		t.Fatal("truncated message is not valid UTF-8")
	}
	for _, r := range truncated {
		if r == utf8.RuneError {
			t.Fatal("truncated message contains replacement runes")
		}
	}
}

func TestClaudeSourceToleratesUnredactableCredentialRecords(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-cred", "00000000-0000-0000-0000-000000000002.jsonl")
	transcriptContent := `{"type":"future_event","sessionId":"session-claude-cred","uuid":"bad-1","timestamp":"2026-08-27T12:00:00Z","payload":{"data:image/png,AAAABBBBCCCC":"x"}}` + "\n" +
		`{"type":"user","sessionId":"session-claude-cred","uuid":"user-1","timestamp":"2026-08-27T12:00:01Z","message":{"role":"user","content":"hello"}}` + "\n"
	writeSourceFile(t, transcript, []byte(transcriptContent))
	before := fileHash(t, transcript)

	source := NewClaudeSource(root)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := source.Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	published := mustReadFile(t, output)
	if bytes.Contains(published, []byte(tolerateCredentialMarker)) {
		t.Fatal("credential-like value was published to the corpus output")
	}
	records := readCorpusRecords(t, output)
	if len(records) != 1 || records[0].Messages[0].Role != replay.RoleUser {
		t.Fatalf("record count/role = %d/%q, want only the clean user record", len(records), records[0].Messages[0].Role)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 {
		t.Fatalf("quarantine count = %d, want 1", len(quarantined))
	}
	if quarantined[0].Code != CodeSecretInCorpus {
		t.Fatalf("quarantine code = %q, want %s", quarantined[0].Code, CodeSecretInCorpus)
	}
	if !bytes.Contains(quarantined[0].Raw, []byte("bad-1")) {
		t.Fatalf("quarantine raw = %q, want the rejected source line", quarantined[0].Raw)
	}
	if after := fileHash(t, transcript); after != before {
		t.Fatalf("source hash changed: before=%s after=%s", before, after)
	}
}

func TestClaudeSourceFailsClosedWhenCredentialRecordsExceedBudget(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-cred", "00000000-0000-0000-0000-000000000002.jsonl")
	transcriptContent := `{"type":"future_event","sessionId":"session-claude-cred","uuid":"bad-1","timestamp":"2026-08-27T12:00:00Z","payload":{"data:image/png,AAAABBBBCCCC":"x"}}` + "\n" +
		`{"type":"future_event","sessionId":"session-claude-cred","uuid":"bad-2","timestamp":"2026-08-27T12:00:01Z","payload":{"data:image/png,AAAABBBBCCCC":"y"}}` + "\n"
	writeSourceFile(t, transcript, []byte(transcriptContent))
	before := fileHash(t, transcript)

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxQuarantineEntries: 1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewClaudeSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after budget exhaustion: %v", statErr)
	}
	if after := fileHash(t, transcript); after != before {
		t.Fatalf("source hash changed: before=%s after=%s", before, after)
	}
}

func TestCodexSourceToleratesUnredactableCredentialRecords(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000003.jsonl")
	rolloutContent := `{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"session-codex-cred","provider":"fixture","cli_version":"fixture-version"}}` + "\n" +
		`{"timestamp":"2026-08-27T12:00:01Z","type":"response_item","payload":{"session_id":"session-codex-cred","type":"future_item","data:image/png,AAAABBBBCCCC":"x"}}` + "\n" +
		`{"timestamp":"2026-08-27T12:00:02Z","type":"event_msg","payload":{"session_id":"session-codex-cred","type":"user_message","message":"hello"}}` + "\n"
	writeSourceFile(t, rollout, []byte(rolloutContent))
	before := fileHash(t, rollout)

	source := NewCodexSource(root)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := source.Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	published := mustReadFile(t, output)
	if bytes.Contains(published, []byte(tolerateCredentialMarker)) {
		t.Fatal("credential-like value was published to the corpus output")
	}
	if len(readCorpusRecords(t, output)) < 1 {
		t.Fatal("clean records were not published")
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 {
		t.Fatalf("quarantine count = %d, want 1", len(quarantined))
	}
	if quarantined[0].Code != CodeSecretInCorpus {
		t.Fatalf("quarantine code = %q, want %s", quarantined[0].Code, CodeSecretInCorpus)
	}
	if !bytes.Contains(quarantined[0].Raw, []byte("future_item")) {
		t.Fatalf("quarantine raw = %q, want the rejected source line", quarantined[0].Raw)
	}
	if after := fileHash(t, rollout); after != before {
		t.Fatalf("source hash changed: before=%s after=%s", before, after)
	}
}

func TestCodexSourceFailsClosedWhenCredentialRecordsExceedBudget(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000003.jsonl")
	rolloutContent := `{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"session-codex-cred","provider":"fixture","cli_version":"fixture-version"}}` + "\n" +
		`{"timestamp":"2026-08-27T12:00:01Z","type":"response_item","payload":{"session_id":"session-codex-cred","type":"future_item","data:image/png,AAAABBBBCCCC":"x"}}` + "\n" +
		`{"timestamp":"2026-08-27T12:00:02Z","type":"response_item","payload":{"session_id":"session-codex-cred","type":"future_item","data:image/png,AAAABBBBCCCC":"y"}}` + "\n"
	writeSourceFile(t, rollout, []byte(rolloutContent))
	before := fileHash(t, rollout)

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxQuarantineEntries: 1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewCodexSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after budget exhaustion: %v", statErr)
	}
	if after := fileHash(t, rollout); after != before {
		t.Fatalf("source hash changed: before=%s after=%s", before, after)
	}
}

func TestOpenCodeSourceFailsClosedWhenZeroPartMessagesExceedBudget(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	createOpenCodeZeroPartFixtureDB(t, databasePath, 2)
	before := fileHash(t, databasePath)

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxQuarantineEntries: 1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := source.Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after budget exhaustion: %v", statErr)
	}
	if after := fileHash(t, databasePath); after != before {
		t.Fatalf("SQLite source hash changed: before=%s after=%s", before, after)
	}
}
