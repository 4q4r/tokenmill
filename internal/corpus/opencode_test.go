//go:build linux

package corpus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenmill/tokenmill/internal/replay"
)

func TestOpenCodeSourceImportsReadOnlySQLiteRowsDeterministically(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	createOpenCodeFixtureDB(t, databasePath)
	before := fileHash(t, databasePath)

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].RelativePath != "opencode.db" {
		t.Fatalf("artifacts = %#v, want opencode.db", artifacts)
	}

	output := filepath.Join(t.TempDir(), "opencode.jsonl")
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
	if len(records) != 3 {
		t.Fatalf("record count = %d, want 3 messages", len(records))
	}
	wantRoles := []replay.Role{replay.RoleSystem, replay.RoleUser, replay.RoleAssistant}
	for index, record := range records {
		if record.Source.System != "opencode" || !strings.HasPrefix(record.RecordID, "opencode:") || !strings.HasPrefix(record.SessionID, "opencode:") {
			t.Fatalf("records[%d] source/IDs invalid: %#v", index, record)
		}
		if record.Sequence != index || len(record.Messages) != 1 {
			t.Fatalf("records[%d] ordering/messages invalid: %#v", index, record)
		}
		message := record.Messages[0]
		if message.Role != wantRoles[index] || !strings.HasPrefix(message.ID, "opencode:") {
			t.Fatalf("records[%d] role/message ID = %q/%q", index, message.Role, message.ID)
		}
	}
	if records[2].Model != "fixture-model" {
		t.Fatalf("assistant model = %q, want fixture-model", records[2].Model)
	}
	if !strings.Contains(string(mustMarshal(t, records)), "Привет") {
		t.Fatal("Unicode OpenCode text was not retained")
	}
	if records[2].Messages[0].ToolCallID != "call-opencode-1" {
		t.Fatalf("tool call ID = %q, want call-opencode-1", records[2].Messages[0].ToolCallID)
	}
	if !hasOpaquePart(records, "tool_result") {
		t.Fatalf("tool result was not retained opaquely: %#v", records)
	}
	if !hasOpaquePart(records, "future_part") || !hasOpaquePart(records, "future_event") {
		t.Fatalf("unknown OpenCode part was not retained opaquely: %#v", records)
	}
	assertNoFixtureSecrets(t, output)
	if after := fileHash(t, databasePath); after != before {
		t.Fatalf("SQLite source hash changed: before=%s after=%s", before, after)
	}
}

func TestOpenCodeSourceFailsClosedForMissingSchemaWithoutPublishing(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close database: %v", err)
	}

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "missing-schema.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := source.Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after missing OpenCode schema")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing-schema output exists: %v", statErr)
	}
}

func TestOpenCodeSourceUsesInjectedResolverWithoutShellInterpolation(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	createOpenCodeFixtureDB(t, databasePath)
	called := false
	source := NewOpenCodeSourceWithResolver(func(ctx context.Context) (string, error) {
		called = true
		if ctx == nil {
			t.Fatal("resolver received nil context")
		}
		return databasePath, nil
	})
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover with resolver: %v", err)
	}
	if !called || len(artifacts) != 1 {
		t.Fatalf("resolver called=%v artifacts=%#v", called, artifacts)
	}
}

func TestOpenCodeSourceRejectsUnapprovedDatabaseNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"opencode.db-wal", "opencode.db-shm", "openai-api-key.db", "state_5.sqlite",
		"credentialvault.db", "privatekeyvault.sqlite", "accesskeys.json", "openaiapikeys.db",
		"sessioncache.db", "sessionstore.db", "keyvault.db", "keybackup.db", "authheaders.db",
		"bearerheaders.db", "netrc", "providerkey.db", "awskey.db", "database.sqlite",
	} {
		path := filepath.Join(root, name)
		writeSourceFile(t, path, []byte("not a database\n"))
		if _, err := (&OpenCodeSource{DatabasePath: path}).Discover(context.Background(), Options{Root: root}); err == nil {
			t.Fatalf("Discover accepted unapproved OpenCode database name %q", name)
		}
	}
}

func TestOpenCodeSourceSupportsExplicitNestedDatabaseAndStableTieOrdering(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "nested", "fixture.sqlite")
	createOrderedOpenCodeFixtureDB(t, databasePath)

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].RelativePath != "nested/fixture.sqlite" {
		t.Fatalf("artifacts = %#v, want nested/fixture.sqlite", artifacts)
	}
	output := filepath.Join(t.TempDir(), "ordered.jsonl")
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
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].TurnID != "message-a" || records[1].TurnID != "message-b" {
		t.Fatalf("message order = %q, %q; want message-a, message-b", records[0].TurnID, records[1].TurnID)
	}
	parts := records[0].Messages[0].Parts
	if len(parts) != 2 || parts[0].Text != "part-a" || parts[1].Text != "part-z" {
		t.Fatalf("part order = %#v, want part-a, part-z", parts)
	}
}

func TestOpenCodeSourceUsesCurrentSessionModelAndTimestampColumns(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, version TEXT NOT NULL, model TEXT, time_created INTEGER NOT NULL)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, message_id TEXT NOT NULL, time_created INTEGER NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO session (id, version, model, time_created) VALUES ('session-current', '1.2.3', '{"id":"session-model","providerID":"fixture"}', 500)`,
		`INSERT INTO message (id, session_id, time_created, data) VALUES ('message-a-later', 'session-current', 2000, '{"role":"assistant"}')`,
		`INSERT INTO message (id, session_id, time_created, data) VALUES ('message-z-earlier', 'session-current', 1000, '{"role":"user"}')`,
		`INSERT INTO part (id, session_id, message_id, time_created, data) VALUES ('part-a-later', 'session-current', 'message-a-later', 2000, '{"type":"text","text":"later"}')`,
		`INSERT INTO part (id, session_id, message_id, time_created, data) VALUES ('part-z-earlier', 'session-current', 'message-z-earlier', 1000, '{"type":"text","text":"earlier"}')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("Exec %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close database: %v", err)
	}

	source := NewOpenCodeSource(databasePath)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "current-schema.jsonl")
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
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].TurnID != "message-z-earlier" || records[1].TurnID != "message-a-later" {
		t.Fatalf("message order = %q, %q; want database time_created order", records[0].TurnID, records[1].TurnID)
	}
	for index, record := range records {
		if record.Model != "session-model" {
			t.Fatalf("records[%d].model = %q, want session-model", index, record.Model)
		}
	}
	if records[0].Timestamp.UnixMilli() != 1000 || records[1].Timestamp.UnixMilli() != 2000 {
		t.Fatalf("timestamps = %s, %s; want database millisecond timestamps", records[0].Timestamp, records[1].Timestamp)
	}
}

func TestOpenCodeArtifactIdentityIncludesCompanionContent(t *testing.T) {
	mainHash := strings.Repeat("a", 64)
	firstArtifact := Artifact{
		RelativePath:  "opencode.db",
		ContentSHA256: mainHash,
		snapshot: &artifactSnapshot{
			openCodeCompanions: map[string]artifactSnapshot{
				"-wal": {contentSHA256: strings.Repeat("b", 64)},
			},
		},
	}
	secondArtifact := Artifact{
		RelativePath:  firstArtifact.RelativePath,
		ContentSHA256: mainHash,
		snapshot: &artifactSnapshot{
			openCodeCompanions: map[string]artifactSnapshot{
				"-wal": {contentSHA256: strings.Repeat("c", 64)},
			},
		},
	}
	first := newImportedRecord("opencode", firstArtifact, "session", "turn", 0, time.Time{}, "", replay.Message{Role: replay.RoleUser})
	second := newImportedRecord("opencode", secondArtifact, "session", "turn", 0, time.Time{}, "", replay.Message{Role: replay.RoleUser})
	if first.Source.ContentSHA256 == second.Source.ContentSHA256 {
		t.Fatalf("OpenCode source hashes are identical for different WAL content: %q", first.Source.ContentSHA256)
	}
	if first.RecordID == second.RecordID || first.SessionID == second.SessionID || first.Messages[0].ID == second.Messages[0].ID {
		t.Fatalf("OpenCode artifact IDs are identical for different WAL content: first=%#v second=%#v", first, second)
	}
}

func createOpenCodeFixtureDB(t *testing.T, path string) {
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
		`CREATE TABLE session (id TEXT PRIMARY KEY, version TEXT, project_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, message_id TEXT NOT NULL, data TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("Exec %q: %v", statement, err)
		}
	}

	var fixture struct {
		Session struct {
			ID        string `json:"id"`
			Version   string `json:"version"`
			ProjectID string `json:"project_id"`
		} `json:"session"`
		Messages []struct {
			ID        string                     `json:"id"`
			SessionID string                     `json:"session_id"`
			Data      map[string]json.RawMessage `json:"data"`
		} `json:"messages"`
		Parts []struct {
			ID        string                     `json:"id"`
			SessionID string                     `json:"session_id"`
			MessageID string                     `json:"message_id"`
			Data      map[string]json.RawMessage `json:"data"`
		} `json:"parts"`
		UnknownEvent map[string]json.RawMessage `json:"unknown_event"`
	}
	if err := json.Unmarshal(mustFixture(t, "testdata/chat/opencode/minimal.json"), &fixture); err != nil {
		t.Fatalf("Unmarshal OpenCode fixture: %v", err)
	}
	insertFixtureRow := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("insert OpenCode fixture row: %v", err)
		}
	}
	insertFixtureRow(`INSERT INTO session (id, version, project_id) VALUES (?, ?, ?)`, fixture.Session.ID, fixture.Session.Version, fixture.Session.ProjectID)
	for _, message := range fixture.Messages {
		data, err := json.Marshal(message.Data)
		if err != nil {
			t.Fatalf("Marshal OpenCode message fixture: %v", err)
		}
		insertFixtureRow(`INSERT INTO message (id, session_id, data) VALUES (?, ?, ?)`, message.ID, message.SessionID, data)
	}
	for _, part := range fixture.Parts {
		data, err := json.Marshal(part.Data)
		if err != nil {
			t.Fatalf("Marshal OpenCode part fixture: %v", err)
		}
		insertFixtureRow(`INSERT INTO part (id, session_id, message_id, data) VALUES (?, ?, ?, ?)`, part.ID, part.SessionID, part.MessageID, data)
	}
	if len(fixture.UnknownEvent) > 0 {
		data, err := json.Marshal(fixture.UnknownEvent)
		if err != nil {
			t.Fatalf("Marshal OpenCode unknown fixture: %v", err)
		}
		insertFixtureRow(`INSERT INTO part (id, session_id, message_id, data) VALUES (?, ?, ?, ?)`, "part-unknown-event", fixture.Session.ID, "message-assistant", data)
	}
}

func createOrderedOpenCodeFixtureDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
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
		`INSERT INTO session (id, version) VALUES ('session-order', '1')`,
		`INSERT INTO message (id, session_id, data) VALUES ('message-b', 'session-order', '{"role":"user","time":{"created":1000}}')`,
		`INSERT INTO message (id, session_id, data) VALUES ('message-a', 'session-order', '{"role":"user","time":{"created":1000}}')`,
		`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-z', 'session-order', 'message-a', '{"type":"text","text":"part-z"}')`,
		`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-a', 'session-order', 'message-a', '{"type":"text","text":"part-a"}')`,
		`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-b', 'session-order', 'message-b', '{"type":"text","text":"message-b"}')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("Exec %q: %v", statement, err)
		}
	}
}
