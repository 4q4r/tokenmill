//go:build linux && (amd64 || arm64)

package corpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/replay"
)

func TestClaudeSourceImportsApprovedTranscriptWithOpaqueAndCorrelatedParts(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	fixture := mustFixture(t, "testdata/chat/claude/minimal.jsonl")
	malformedStart := bytes.Index(fixture, []byte("{\"type\":\"assistant\"\n"))
	if malformedStart < 0 {
		t.Fatal("Claude fixture has no malformed tail")
	}
	valid := fixture[:malformedStart]
	writeSourceFile(t, transcript, valid)

	before := fileHash(t, transcript)
	source := NewClaudeSource(root)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].RelativePath != "projects/project-alpha/00000000-0000-0000-0000-000000000001.jsonl" {
		t.Fatalf("artifacts = %#v, want one approved project transcript", artifacts)
	}

	output := filepath.Join(t.TempDir(), "claude.jsonl")
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
	if len(records) != 5 {
		t.Fatalf("record count = %d, want 5 source events", len(records))
	}
	wantRoles := []replay.Role{replay.RoleSystem, replay.RoleUser, replay.RoleAssistant, replay.RoleTool, replay.RoleSystem}
	for index, record := range records {
		if record.Source.System != "claude" {
			t.Fatalf("records[%d].source.system = %q, want claude", index, record.Source.System)
		}
		if !strings.HasPrefix(record.RecordID, "claude:") || !strings.HasPrefix(record.SessionID, "claude:") {
			t.Fatalf("records[%d] IDs are not namespaced: %#v", index, record)
		}
		if len(record.Messages) != 1 {
			t.Fatalf("records[%d].messages = %#v, want one normalized message", index, record.Messages)
		}
		if record.Messages[0].Role != wantRoles[index] {
			t.Fatalf("records[%d].role = %q, want %q", index, record.Messages[0].Role, wantRoles[index])
		}
		if !strings.HasPrefix(record.Messages[0].ID, "claude:") {
			t.Fatalf("records[%d].message.id = %q is not namespaced", index, record.Messages[0].ID)
		}
		if record.Sequence != index {
			t.Fatalf("records[%d].sequence = %d, want %d", index, record.Sequence, index)
		}
	}
	if !strings.Contains(string(mustMarshal(t, records)), "Привет") {
		t.Fatal("Unicode message text was not retained")
	}

	var callID, resultID string
	for _, record := range records {
		message := record.Messages[0]
		if message.Role == replay.RoleAssistant {
			callID = message.ToolCallID
		}
		if message.Role == replay.RoleTool {
			resultID = message.ToolCallID
		}
	}
	if callID == "" || callID != resultID {
		t.Fatalf("tool correlation = call %q/result %q, want equal non-empty IDs", callID, resultID)
	}
	if !hasOpaquePart(records, "future_block") || !hasOpaquePart(records, "future_event") {
		t.Fatalf("unknown Claude events/parts were not retained opaquely: %#v", records)
	}
	assertNoFixtureSecrets(t, output)
	if after := fileHash(t, transcript); after != before {
		t.Fatalf("source hash changed: before=%s after=%s", before, after)
	}

	secondOutput := filepath.Join(t.TempDir(), "claude-second.jsonl")
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
	firstBytes := mustReadFile(t, output)
	secondBytes := mustReadFile(t, secondOutput)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("repeated Claude import output differs")
	}
}

func TestClaudeSourceQuarantinesMalformedUnterminatedTailAndDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	fixture := mustFixture(t, "testdata/chat/claude/minimal.jsonl")
	writeSourceFile(t, transcript, fixture)
	before := fileHash(t, transcript)

	source := NewClaudeSource(root)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "malformed.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := source.Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 1 || string(quarantined[0].Raw) != "{\"type\":\"assistant\"\n" {
		t.Fatalf("quarantine = %#v, want malformed tail bytes", quarantined)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after malformed Claude tail")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("malformed output exists: %v", statErr)
	}
	if after := fileHash(t, transcript); after != before {
		t.Fatalf("source hash changed after malformed import: before=%s after=%s", before, after)
	}
}

func TestClaudeSourceRejectsUnapprovedProjectTranscriptPaths(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, filepath.Join(root, "history.jsonl"), []byte("{}\n"))
	writeSourceFile(t, filepath.Join(root, "projects", "project-alpha", "auth.jsonl"), []byte("{}\n"))
	writeSourceFile(t, filepath.Join(root, "projects", "project-alpha", "session-0001.json"), []byte("{}\n"))

	if _, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root}); err == nil {
		t.Fatal("Discover accepted unapproved Claude project files")
	}
}

func TestClaudeSourceRejectsTranscriptSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeSourceFile(t, outside, []byte("{}\n"))
	link := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root}); CodeOf(err) != CodePathEscape {
		t.Fatalf("Discover symlink error = %v, want %s", err, CodePathEscape)
	}
}

func TestClaudeSourceQuarantinesOversizedSourceLines(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	line := mustFixture(t, "testdata/chat/claude/minimal.jsonl")
	line = append(line[:bytes.IndexByte(line, '\n')], '\n')
	writeSourceFile(t, transcript, line)

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	writer, err := NewWriter(Options{
		OutputPath:         filepath.Join(t.TempDir(), "oversized.jsonl"),
		MaxLineBytes:       len(line) - 2,
		MaxQuarantineBytes: len(line),
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewClaudeSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if len(writer.Quarantined()) != 1 || !bytes.Equal(writer.Quarantined()[0].Raw, line) {
		t.Fatalf("quarantine = %#v, want the oversized source line", writer.Quarantined())
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after oversized source line")
	}
}

func TestClaudeSourceQuarantinesInvalidUTF8SourceLines(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	line := []byte("{\"type\":\"user\",\"sessionId\":\"session-claude-1\",\"message\":{\"role\":\"user\",\"content\":\"")
	line = append(line, 0xff)
	line = append(line, []byte("\"}}\n")...)
	writeSourceFile(t, transcript, line)

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "invalid-utf8.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewClaudeSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if len(writer.Quarantined()) != 1 || !bytes.Equal(writer.Quarantined()[0].Raw, line) {
		t.Fatalf("quarantine = %#v, want invalid UTF-8 source bytes", writer.Quarantined())
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after invalid UTF-8 source line")
	}
}

func TestClaudeSourceStopsAfterQuarantineEntryBudgetExhaustion(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	writeSourceFile(t, transcript, []byte("{\n{\n{\n"))

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "quarantine-budget.jsonl")
	writer, err := NewWriter(Options{
		OutputPath:           output,
		MaxLineBytes:         64,
		MaxQuarantineBytes:   64,
		MaxQuarantineEntries: 1,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewClaudeSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if len(writer.Quarantined()) != 1 {
		t.Fatalf("quarantine entries = %d, want one before stopping", len(writer.Quarantined()))
	}
	if strings.Count(readErr.Error(), "quarantine entry limit exceeded") != 1 {
		t.Fatalf("Read error = %v, want one budget-exhaustion error", readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after quarantine entry exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after quarantine entry exhaustion: %v", statErr)
	}
}

func TestClaudeSourceStopsAfterQuarantineByteBudgetExhaustion(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	writeSourceFile(t, transcript, []byte("{\n{\n"))

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	output := filepath.Join(t.TempDir(), "quarantine-byte-budget.jsonl")
	writer, err := NewWriter(Options{
		OutputPath:           output,
		MaxLineBytes:         1,
		MaxQuarantineBytes:   1,
		MaxQuarantineEntries: 2,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	readErr := NewClaudeSource(root).Read(context.Background(), artifacts[0], writer)
	if readErr == nil || CodeOf(readErr) != CodeInputJSON {
		t.Fatalf("Read error = %v, want %s", readErr, CodeInputJSON)
	}
	if len(writer.Quarantined()) != 1 {
		t.Fatalf("quarantine entries = %d, want one before stopping", len(writer.Quarantined()))
	}
	if strings.Count(readErr.Error(), "quarantine byte budget exceeded") != 1 {
		t.Fatalf("Read error = %v, want one budget-exhaustion error", readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after quarantine byte exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after quarantine byte exhaustion: %v", statErr)
	}
}

func TestClaudeSourceImportsNestedSubagentTranscriptAndIgnoresKnownMetadata(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001.jsonl")
	mainLine := []byte(`{"type":"user","sessionId":"session-claude-1","uuid":"main-1","message":{"role":"user","content":"main"}}` + "\n")
	writeSourceFile(t, mainPath, mainLine)
	subagentPath := filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001", "subagents", "agent-c01d0000-0000-0000-0000-000000000002.jsonl")
	writeSourceFile(t, subagentPath, mustFixture(t, "testdata/chat/claude/subagent.jsonl"))
	writeSourceFile(t, filepath.Join(root, "projects", "project-alpha", "00000000-0000-0000-0000-000000000001", "subagents", "agent-c01d0000-0000-0000-0000-000000000002.meta.json"), []byte(`{"agentType":"worker"}`+"\n"))

	source := NewClaudeSource(root)
	artifacts, err := source.Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want main and nested subagent transcripts", artifacts)
	}
	output := filepath.Join(t.TempDir(), "subagents.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for _, artifact := range artifacts {
		if err := source.Read(context.Background(), artifact, writer); err != nil {
			t.Fatalf("Read %q: %v", artifact.RelativePath, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readCorpusRecords(t, output)
	if len(records) != 2 {
		t.Fatalf("records = %d, want main and subagent records", len(records))
	}
	var subagentFound bool
	for _, record := range records {
		if strings.Contains(record.SessionID, ":subagent:agent-child-1") {
			subagentFound = true
		}
	}
	if !subagentFound {
		t.Fatalf("nested subagent session was not namespaced: %#v", records)
	}
}

func TestClaudeSourceAcceptsDocumentedSidecarsAndShortAgentIDs(t *testing.T) {
	root := t.TempDir()
	project := "-home-user-git-tokenmill"
	session := "00000000-0000-0000-0000-000000000003"
	mainPath := filepath.Join(root, "projects", project, "00000000-0000-0000-0000-000000000004.jsonl")
	shortAgentPath := filepath.Join(root, "projects", project, session, "subagents", "agent-a54c53c73b09d1014.jsonl")
	writeSourceFile(t, mainPath, []byte(`{"type":"user","sessionId":"session-claude-docs","message":{"role":"user","content":"main"}}`+"\n"))
	writeSourceFile(t, shortAgentPath, []byte(`{"type":"assistant","sessionId":"session-claude-docs","message":{"role":"assistant","content":"agent"}}`+"\n"))
	writeSourceFile(t, filepath.Join(root, "projects", project, "sessions-index.json"), []byte(`{"credential":"must not be scanned"}`+"\n"))
	writeSourceFile(t, filepath.Join(root, "projects", project, "memory", "MEMORY.md"), []byte("credential: must not be scanned\n"))
	writeSourceFile(t, filepath.Join(root, "projects", project, session, "tool-results", "toolu_123.json"), []byte(`{"secret":"must not be scanned"}`+"\n"))

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want main and short-ID agent transcripts", artifacts)
	}
	paths := []string{artifacts[0].RelativePath, artifacts[1].RelativePath}
	if !containsString(paths, filepath.ToSlash(filepath.Join("projects", project, "00000000-0000-0000-0000-000000000004.jsonl"))) ||
		!containsString(paths, filepath.ToSlash(filepath.Join("projects", project, session, "subagents", "agent-a54c53c73b09d1014.jsonl"))) {
		t.Fatalf("artifacts = %#v, want documented transcript paths", paths)
	}
}

func TestClaudeSourceAcceptsDocumentedWorkflowTranscript(t *testing.T) {
	root := t.TempDir()
	session := "00000000-0000-0000-0000-000000000005"
	workflowDir := filepath.Join(root, "projects", "project-alpha", session, "subagents", "workflows", "wf_7c0e6255-566")
	writeSourceFile(t, filepath.Join(workflowDir, "agent-a54c53c73b09d1014.jsonl"), []byte(`{"type":"assistant","sessionId":"workflow-session","message":{"role":"assistant","content":"workflow"}}`+"\n"))
	writeSourceFile(t, filepath.Join(workflowDir, "agent-a54c53c73b09d1014.meta.json"), []byte(`{"agentType":"worker"}`+"\n"))

	artifacts, err := NewClaudeSource(root).Discover(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].RelativePath != filepath.ToSlash(filepath.Join("projects", "project-alpha", session, "subagents", "workflows", "wf_7c0e6255-566", "agent-a54c53c73b09d1014.jsonl")) {
		t.Fatalf("artifacts = %#v, want documented workflow transcript", artifacts)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mustFixture(t *testing.T, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectRootForTests(t), relative))
	if err != nil {
		t.Fatalf("Read fixture %q: %v", relative, err)
	}
	return data
}

func projectRootForTests(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Join(root, "..", "..")
}

func writeSourceFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll %q: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile %q: %v", path, err)
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	digest := sha256.Sum256(mustReadFile(t, path))
	return hex.EncodeToString(digest[:])
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %q: %v", path, err)
	}
	return data
}

func readCorpusRecords(t *testing.T, path string) []replay.Record {
	t.Helper()
	var records []replay.Record
	for _, line := range bytes.Split(mustReadFile(t, path), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record replay.Record
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("Unmarshal corpus record: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func hasOpaquePart(records []replay.Record, partType string) bool {
	for _, record := range records {
		for _, message := range record.Messages {
			for _, part := range message.Parts {
				if part.Type == partType && len(part.Raw) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func assertNoFixtureSecrets(t *testing.T, path string) {
	t.Helper()
	output := string(mustReadFile(t, path))
	for _, secret := range []string{
		"fixture-provider-secret",
		"fixture-cookie-secret",
		"fixture-openai-secret",
		"fixture-aws-secret",
		"fixture-gcp-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("serialized corpus contains credential sentinel %q", secret)
		}
	}
}
