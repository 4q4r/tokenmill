//go:build linux

package corpus

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTask3SourceReadsRejectOutputInsideSourceRoot(t *testing.T) {
	tests := []struct {
		name   string
		create func(*testing.T, string)
		source func(string) Source
		path   string
	}{
		{
			name: "claude",
			create: func(t *testing.T, root string) {
				writeSourceFile(t, filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl"), []byte(`{"type":"user","sessionId":"session-claude-1","message":{"role":"user","content":"hello"}}
`))
			},
			source: func(root string) Source { return NewClaudeSource(root) },
			path:   filepath.Join("projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl"),
		},
		{
			name: "codex",
			create: func(t *testing.T, root string) {
				writeSourceFile(t, filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001.jsonl"), []byte(`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"session-codex-1"}}
`))
			},
			source: func(root string) Source { return NewCodexSource(root) },
			path:   filepath.Join("sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001.jsonl"),
		},
		{
			name: "opencode",
			create: func(t *testing.T, root string) {
				createOpenCodeFixtureDB(t, filepath.Join(root, "opencode.db"))
			},
			source: func(root string) Source { return NewOpenCodeSource(filepath.Join(root, "opencode.db")) },
			path:   "opencode.db",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.create(t, root)
			source := test.source(root)
			artifacts, err := source.Discover(context.Background(), Options{Root: root})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(artifacts) != 1 || artifacts[0].RelativePath != test.path {
				t.Fatalf("artifacts = %#v, want %q", artifacts, test.path)
			}

			output := filepath.Join(root, "imported.jsonl")
			writer, err := NewWriter(Options{OutputPath: output})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			readErr := source.Read(context.Background(), artifacts[0], writer)
			if CodeOf(readErr) != CodePathEscape {
				t.Fatalf("Read error = %v, want %s", readErr, CodePathEscape)
			}
			if closeErr := writer.Close(); closeErr == nil {
				t.Fatal("Close published output inside source root")
			}
			if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("source-root output exists: %v", statErr)
			}
		})
	}
}

func TestTask3SourceAllowlistsRejectSensitiveLookalikePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "claude concatenated session credential vault",
			path: filepath.Join("projects", "project-alpha", "session-credentialvault.jsonl"),
		},
		{
			name: "claude provider session directory",
			path: filepath.Join("projects", "project-alpha", "provider", "subagents", "agent-worker.jsonl"),
		},
		{
			name: "claude provider workflow directory",
			path: filepath.Join("projects", "project-alpha", "00000000-0000-0000-0000-000000000001", "subagents", "workflows", "provider", "agent-a54c53c73b09d1014.jsonl"),
		},
		{
			name: "claude root agent transcript",
			path: filepath.Join("projects", "project-alpha", "agent-a54c53c73b09d1014.jsonl"),
		},
		{
			name: "claude concatenated agent credential vault",
			path: filepath.Join("projects", "project-alpha", "00000000-0000-0000-0000-000000000001", "subagents", "agent-credentialvault.jsonl"),
		},
		{
			name: "codex provider suffix",
			path: filepath.Join("sessions", "2026", "08", "27", "rollout-provider.jsonl"),
		},
		{
			name: "codex timestamp provider suffix",
			path: filepath.Join("sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-provider.jsonl"),
		},
		{
			name: "codex credential vault suffix",
			path: filepath.Join("sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-credentialvault.jsonl"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceFile(t, filepath.Join(root, test.path), []byte("{}\n"))
			var source Source
			if strings.HasPrefix(test.path, "projects") {
				source = NewClaudeSource()
			} else {
				source = NewCodexSource()
			}
			if _, err := source.Discover(context.Background(), Options{Root: root}); CodeOf(err) != CodeSecretInCorpus {
				t.Fatalf("Discover error = %v, want %s", err, CodeSecretInCorpus)
			}
		})
	}
}

func TestTask3CodexKeepsFirstSessionNamespaceAcrossCopiedMetadata(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001.jsonl")
	input := strings.Join([]string{
		`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"child-session"}}`,
		`{"timestamp":"2026-08-27T12:00:01Z","type":"session_meta","payload":{"id":"parent-session"}}`,
		`{"timestamp":"2026-08-27T12:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"child continuation"}}`,
	}, "\n") + "\n"
	writeSourceFile(t, rollout, []byte(input))

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "codex-copied.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := NewCodexSource(root).Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for index, record := range readCorpusRecords(t, output) {
		if !strings.HasPrefix(record.SessionID, "codex:child-session") {
			t.Fatalf("records[%d].session_id = %q, want child namespace", index, record.SessionID)
		}
	}
}

func TestTask3CodexKeepsIdenticalSessionMetadataAsUniqueArchiveRecords(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000002.jsonl")
	input := strings.Join([]string{
		`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"child-session"}}`,
		`{"timestamp":"2026-08-27T12:00:01Z","type":"session_meta","payload":{"id":"child-session"}}`,
	}, "\n") + "\n"
	writeSourceFile(t, rollout, []byte(input))

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "codex-duplicate-metadata.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := NewCodexSource(root).Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readCorpusRecords(t, output)
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if records[0].RecordID == records[1].RecordID {
		t.Fatalf("duplicate record IDs = %q", records[0].RecordID)
	}
	for index, record := range records {
		if !strings.HasPrefix(record.SessionID, "codex:child-session") {
			t.Fatalf("records[%d].session_id = %q, want child namespace", index, record.SessionID)
		}
	}
}

func TestTask3OpenCodeWALImportDoesNotMutateSidecars(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	db := createOpenCodeWALFixtureDB(t, databasePath)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close WAL fixture: %v", err)
		}
	}()

	sidecars := []string{databasePath + "-wal", databasePath + "-shm"}
	before := make(map[string]string, len(sidecars))
	for _, sidecar := range sidecars {
		if _, err := os.Stat(sidecar); err != nil {
			t.Fatalf("WAL sidecar %q is unavailable: %v", sidecar, err)
		}
		before[sidecar] = fileHash(t, sidecar)
	}

	artifacts, err := NewOpenCodeSource(databasePath).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := NewOpenCodeSource(databasePath).Read(context.Background(), artifacts[0], writer); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readCorpusRecords(t, output)
	var walTextFound bool
	for _, record := range records {
		for _, message := range record.Messages {
			for _, part := range message.Parts {
				if part.Type == "text" && part.Text == "wal" {
					walTextFound = true
				}
			}
		}
	}
	if !walTextFound {
		t.Fatalf("WAL-backed text was not imported: %#v", records)
	}
	for _, sidecar := range sidecars {
		if after := fileHash(t, sidecar); after != before[sidecar] {
			t.Fatalf("WAL sidecar hash changed for %s: before=%s after=%s", sidecar, before[sidecar], after)
		}
	}
}

func TestTask3OpenCodeRejectsSidecarChangesAfterDiscovery(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "opencode.db")
	db := createOpenCodeWALFixtureDB(t, databasePath)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close WAL fixture: %v", err)
		}
	}()

	artifacts, err := NewOpenCodeSource(databasePath).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	discovered, err := artifactSnapshotFor(artifacts[0])
	if err != nil {
		t.Fatalf("artifact snapshot: %v", err)
	}
	if len(discovered.openCodeCompanions) == 0 {
		t.Fatal("Discover did not capture OpenCode sidecars")
	}
	mainBefore := fileHash(t, databasePath)
	companionBefore := make(map[string]string, len(discovered.openCodeCompanions))
	for suffix := range discovered.openCodeCompanions {
		companionBefore[suffix] = fileHash(t, databasePath+suffix)
	}
	if _, err := db.Exec(`INSERT INTO message (id, session_id, data) VALUES ('message-after-discovery', 'session-wal', '{"role":"assistant"}')`); err != nil {
		t.Fatalf("insert message after discovery: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-after-discovery', 'session-wal', 'message-after-discovery', '{"type":"text","text":"after discovery"}')`); err != nil {
		t.Fatalf("insert part after discovery: %v", err)
	}
	changed := fileHash(t, databasePath) != mainBefore
	for suffix, before := range companionBefore {
		path := databasePath + suffix
		after, hashErr := os.Stat(path)
		if hashErr != nil || !after.Mode().IsRegular() || fileHash(t, path) != before {
			changed = true
		}
	}
	if !changed {
		t.Fatal("fixture write did not change the OpenCode database or sidecars")
	}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewOpenCodeSource(databasePath).Read(context.Background(), artifacts[0], writer)
	if CodeOf(readErr) != CodeSourceChanged {
		t.Fatalf("Read error = %v, want %s", readErr, CodeSourceChanged)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after post-discovery sidecar change")
	}
}

func TestTask3SourceChangeTakesPrecedenceOverEarlierQuarantine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "transcript.jsonl")
	writeSourceFile(t, path, []byte("first\n"))
	artifact, err := DiscoverArtifact(root, path)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	mutated := false
	readErr := readSourceJSONL(context.Background(), artifact, writer, func(string) bool { return true }, func(line sourceLine) error {
		if !mutated {
			mutated = true
			writeSourceFile(t, path, []byte("changed\n"))
		}
		cause := corpusError(CodeInputJSON, "test quarantine", nil)
		return quarantineSourceLine(writer, line, CodeInputJSON, cause.Error(), cause)
	})
	if CodeOf(readErr) != CodeSourceChanged {
		t.Fatalf("Read error = %v, want %s", readErr, CodeSourceChanged)
	}
	closeErr := writer.Close()
	if CodeOf(closeErr) != CodeSourceChanged {
		t.Fatalf("Close error = %v, want %s", closeErr, CodeSourceChanged)
	}
}

func createOpenCodeWALFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		t.Fatalf("enable WAL: %v", err)
	}
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, version TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, message_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO session (id, version) VALUES ('session-wal', '1')`,
		`INSERT INTO message (id, session_id, data) VALUES ('message-wal', 'session-wal', '{"role":"user"}')`,
		`INSERT INTO part (id, session_id, message_id, data) VALUES ('part-wal', 'session-wal', 'message-wal', '{"type":"text","text":"wal"}')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("Exec %q: %v", statement, err)
		}
	}
	return db
}
