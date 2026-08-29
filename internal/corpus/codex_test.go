//go:build linux

package corpus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenmill/tokenmill/internal/replay"
)

func TestCodexSourceImportsRolloutEventsWithParsedArgumentsAndCorrelation(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001.jsonl")
	fixture := mustFixture(t, "testdata/chat/codex/minimal.jsonl")
	malformedStart := bytes.Index(fixture, []byte(`{"timestamp":"2026-08-27T12:00:07Z","type":"response_item"`))
	if malformedStart < 0 {
		t.Fatal("Codex fixture has no malformed tail")
	}
	valid := fixture[:malformedStart]
	writeSourceFile(t, rollout, valid)
	before := fileHash(t, rollout)

	source := NewCodexSource(root)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].RelativePath != "sessions/2026/08/27/rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001.jsonl" {
		t.Fatalf("artifacts = %#v, want one approved rollout", artifacts)
	}

	output := filepath.Join(t.TempDir(), "codex.jsonl")
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
	if len(records) != 7 {
		t.Fatalf("record count = %d, want 7 rollout events", len(records))
	}
	for index, record := range records {
		if record.Source.System != "codex" || !strings.HasPrefix(record.RecordID, "codex:") || !strings.HasPrefix(record.SessionID, "codex:") {
			t.Fatalf("records[%d] has invalid source/IDs: %#v", index, record)
		}
		if record.Sequence != index || len(record.Messages) != 1 || !strings.HasPrefix(record.Messages[0].ID, "codex:") {
			t.Fatalf("records[%d] ordering/message ID invalid: %#v", index, record)
		}
	}
	if records[1].Model != "fixture-model" {
		t.Fatalf("turn context model = %q, want fixture-model", records[1].Model)
	}
	if !strings.Contains(string(mustMarshal(t, records)), "Привет") {
		t.Fatal("Unicode rollout content was not retained")
	}

	var callID, resultID string
	var parsedArguments bool
	for _, record := range records {
		message := record.Messages[0]
		if message.Role == replay.RoleAssistant && message.ToolCallID != "" {
			callID = message.ToolCallID
			for _, part := range message.Parts {
				if part.Type == "function_call" && bytes.Contains(part.Raw, []byte(`"arguments":{"command"`)) {
					parsedArguments = true
				}
			}
		}
		if message.Role == replay.RoleTool {
			resultID = message.ToolCallID
		}
	}
	if callID == "" || callID != resultID {
		t.Fatalf("tool correlation = call %q/result %q, want equal non-empty IDs", callID, resultID)
	}
	if !parsedArguments {
		t.Fatal("JSON-in-string function arguments were not parsed as opaque JSON")
	}
	if !hasOpaquePart(records, "future_block") || !hasOpaquePart(records, "future_event") {
		t.Fatalf("unknown Codex events/parts were not retained opaquely: %#v", records)
	}
	assertNoFixtureSecrets(t, output)

	secondOutput := filepath.Join(t.TempDir(), "codex-second.jsonl")
	secondWriter, err := NewWriter(Options{OutputPath: secondOutput})
	if err != nil {
		t.Fatalf("NewWriter second: %v", err)
	}
	if err := source.Read(context.Background(), artifacts[0], secondWriter); err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if err := secondWriter.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !bytes.Equal(mustReadFile(t, output), mustReadFile(t, secondOutput)) {
		t.Fatal("repeated Codex import output differs")
	}
	if after := fileHash(t, rollout); after != before {
		t.Fatalf("source hash changed: before=%s after=%s", before, after)
	}
}

func TestCodexSourceQuarantinesMalformedTailAndDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001.jsonl")
	fixture := mustFixture(t, "testdata/chat/codex/minimal.jsonl")
	writeSourceFile(t, rollout, fixture)

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "malformed.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewCodexSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 || string(quarantined[0].Raw) != "{\"timestamp\":\"2026-08-27T12:00:07Z\",\"type\":\"response_item\"\n" {
		t.Fatalf("quarantine = %#v, want malformed tail bytes", quarantined)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after malformed Codex tail")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("malformed output exists: %v", statErr)
	}
}

func TestCodexSourceQuarantinesMalformedFunctionArguments(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000003.jsonl")
	input := strings.Join([]string{
		`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"session-codex-malformed"}}`,
		`{"timestamp":"2026-08-27T12:00:01Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"call-malformed","arguments":"{\"api_key\":\"malformed-api-secret\""}}`,
	}, "\n") + "\n"
	writeSourceFile(t, rollout, []byte(input))

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "malformed-arguments.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewCodexSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if len(writer.Quarantined()) != 1 {
		t.Fatalf("quarantine entries = %#v, want one malformed function call", writer.Quarantined())
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after malformed function arguments")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("malformed arguments output exists: %v", statErr)
	}
}

func TestCodexSourceIgnoresRootStateButRejectsUnapprovedTranscriptPaths(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, filepath.Join(root, "auth.json"), []byte("{}\n"))
	writeSourceFile(t, filepath.Join(root, "state_5.sqlite"), []byte("state\n"))
	writeSourceFile(t, filepath.Join(root, "sessions", "2026", "08", "27", "auth.jsonl"), []byte("{}\n"))
	writeSourceFile(t, filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27.json"), []byte("{}\n"))

	if _, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root}); err == nil {
		t.Fatal("Discover accepted unapproved Codex transcript paths")
	}
}

func TestCodexSourceRejectsRolloutSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeSourceFile(t, outside, []byte("{}\n"))
	link := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-escape.jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root}); CodeOf(err) != CodePathEscape {
		t.Fatalf("Discover symlink error = %v, want %s", err, CodePathEscape)
	}
}

func TestCodexSourceUsesTurnContextModelAndParsesAgentEvents(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-01-00000000-0000-0000-0000-000000000002.jsonl")
	input := strings.Join([]string{
		`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"session-codex-agent"}}`,
		`{"timestamp":"2026-08-27T12:00:01Z","type":"turn_context","payload":{"session_id":"session-codex-agent","turn_id":"turn-agent","model":"turn-model"}}`,
		`{"timestamp":"2026-08-27T12:00:02Z","type":"event_msg","payload":{"session_id":"session-codex-agent","type":"agent_message","message":"Ответ агента"}}`,
	}, "\n") + "\n"
	writeSourceFile(t, rollout, []byte(input))

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "agent.jsonl")
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
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3 rollout events", len(records))
	}
	if records[1].Model != "turn-model" {
		t.Fatalf("turn-context model = %q, want turn-model", records[1].Model)
	}
	if records[2].Messages[0].Role != replay.RoleAssistant || records[2].Messages[0].Parts[0].Text != "Ответ агента" {
		t.Fatalf("agent event = %#v, want assistant text", records[2].Messages[0])
	}
}

func TestCodexSourceAcceptsRevertedRolloutFilename(t *testing.T) {
	root := t.TempDir()
	relative := filepath.Join("sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000001_00000000-0000-0000-0000-000000000002.jsonl")
	path := filepath.Join(root, relative)
	writeSourceFile(t, path, []byte(`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"reverted-session"}}
`))

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].RelativePath != relative {
		t.Fatalf("artifacts = %#v, want reverted rollout", artifacts)
	}
}

func TestCodexSourceNamespacesDistinctRolloutArtifacts(t *testing.T) {
	root := t.TempDir()
	rolloutDir := filepath.Join(root, "sessions", "2026", "08", "27")
	paths := []string{
		filepath.Join(rolloutDir, "rollout-2026-08-27T12-00-00-00000000-0000-0000-0000-000000000005.jsonl"),
		filepath.Join(rolloutDir, "rollout-2026-08-27T12-00-01-00000000-0000-0000-0000-000000000006.jsonl"),
	}
	for index, path := range paths {
		writeSourceFile(t, path, []byte(fmt.Sprintf(`{"timestamp":"2026-08-27T12:00:0%dZ","type":"session_meta","payload":{"id":"same-session"}}
`, index)))
	}

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != len(paths) {
		t.Fatalf("artifacts = %#v, want %d", artifacts, len(paths))
	}
	output := filepath.Join(t.TempDir(), "distinct-rollouts.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for _, artifact := range artifacts {
		if err := NewCodexSource(root).Read(context.Background(), artifact, writer); err != nil {
			t.Fatalf("Read %q: %v", artifact.RelativePath, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readCorpusRecords(t, output)
	if len(records) != len(paths) || records[0].RecordID == records[1].RecordID {
		t.Fatalf("records = %#v, want distinct archive IDs", records)
	}
}

func TestCodexSourceDoesNotUseUnknownPayloadIDAsSessionNamespace(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "sessions", "2026", "08", "27", "rollout-2026-08-27T12-00-02-00000000-0000-0000-0000-000000000007.jsonl")
	input := strings.Join([]string{
		`{"timestamp":"2026-08-27T12:00:02Z","type":"future_event","payload":{"id":"event-id","value":"before metadata"}}`,
		`{"timestamp":"2026-08-27T12:00:03Z","type":"session_meta","payload":{"id":"canonical-session"}}`,
		`{"timestamp":"2026-08-27T12:00:04Z","type":"event_msg","payload":{"type":"agent_message","message":"after metadata"}}`,
	}, "\n") + "\n"
	writeSourceFile(t, rollout, []byte(input))

	artifacts, err := NewCodexSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "canonical.jsonl")
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
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	for index, record := range records {
		if strings.Contains(record.SessionID, "event-id") {
			t.Fatalf("records[%d].session_id used unknown event ID: %q", index, record.SessionID)
		}
	}
	if !strings.Contains(records[1].SessionID, "canonical-session") || !strings.Contains(records[2].SessionID, "canonical-session") {
		t.Fatalf("metadata namespace was not retained: %#v", records)
	}
}

func TestNewImportedRecordUsesUnambiguousArtifactScopedIDs(t *testing.T) {
	message := replay.Message{Role: replay.RoleSystem, ID: "message:with:delimiters", Parts: []replay.Part{{Type: "opaque", Raw: []byte(`{"type":"opaque"}`)}}}
	leftArtifact := Artifact{RelativePath: "sessions/2026/08/27/rollout.jsonl", ContentSHA256: strings.Repeat("a", 64)}
	rightArtifact := leftArtifact
	rightArtifact.ContentSHA256 = strings.Repeat("b", 64)
	left := newImportedRecord("codex", leftArtifact, "session:with:delimiters", "turn:with:delimiters", 0, time.Time{}, "", message)
	right := newImportedRecord("codex", rightArtifact, "session:with:delimiters", "turn:with:delimiters", 0, time.Time{}, "", message)
	if left.RecordID == right.RecordID || left.SessionID == right.SessionID || left.Messages[0].ID == right.Messages[0].ID {
		t.Fatalf("same-path snapshots share IDs: left=%#v right=%#v", left, right)
	}

	artifactIdentity := strings.TrimPrefix(left.SessionID, "codex:session:with:delimiters:artifact:")
	ambiguousLeft := newImportedRecord("codex", leftArtifact, "a", "b:artifact:"+artifactIdentity+":turn:c", 0, time.Time{}, "", message)
	ambiguousRight := newImportedRecord("codex", leftArtifact, "a:artifact:"+artifactIdentity+":turn:b", "c", 0, time.Time{}, "", message)
	if ambiguousLeft.RecordID == ambiguousRight.RecordID {
		t.Fatalf("delimiter-containing IDs collide: %q", ambiguousLeft.RecordID)
	}
}
