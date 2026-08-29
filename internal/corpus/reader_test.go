//go:build linux && (amd64 || arm64)

package corpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tokenmill/tokenmill/internal/replay"
)

func TestReaderAcceptsCompleteRecordsAndPreservesOpaqueParts(t *testing.T) {
	first := corpusRecord(0)
	second := corpusRecord(1)
	second.Messages[1].Parts = append(second.Messages[1].Parts, replay.Part{
		Type: "image",
		Raw:  json.RawMessage(`{"type":"image","uri":"file:///tmp/image.png","vendor":{"future":true}}`),
	})
	input := mustJSONL(t, first, second)

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	reader := NewReader(Options{MaxLineBytes: 4096})
	result, err := reader.ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if err != nil {
		t.Fatalf("ReadJSONL: %v", err)
	}
	if result.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2", result.Accepted)
	}
	if len(result.Quarantined) != 0 {
		t.Fatalf("quarantine = %#v, want empty", result.Quarantined)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var records []replay.Record
	for _, line := range bytes.Split(encoded, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record replay.Record
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode output record: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 2 || len(records[1].Messages[1].Parts) != 2 {
		t.Fatalf("output records lost ordering or opaque part: %#v", records)
	}
	if got := string(records[1].Messages[1].Parts[1].Raw); got != `{"type":"image","uri":"file:///tmp/image.png","vendor":{"future":true}}` {
		t.Fatalf("opaque raw = %q, want original semantic JSON", got)
	}
}

func TestReaderQuarantinesMalformedAndIncompleteJSONLWithoutPublishingOutput(t *testing.T) {
	valid := mustJSON(t, corpusRecord(0))
	input := append([]byte{}, valid...)
	input = append(input, '\n')
	input = append(input, []byte(`{"schema":`)...)
	input = append(input, '\n')
	input = append(input, []byte(`{"schema":"tokenmill.session/v1"}`)...)

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, err := NewReader(Options{}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if err == nil {
		t.Fatal("ReadJSONL accepted malformed and incomplete records")
	}
	if got := CodeOf(err); got != CodeInputJSON {
		t.Fatalf("error code = %q, want %q", got, CodeInputJSON)
	}
	if result.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", result.Accepted)
	}
	if len(result.Quarantined) != 2 {
		t.Fatalf("quarantine count = %d, want 2", len(result.Quarantined))
	}
	if got := string(result.Quarantined[0].Raw); got != `{"schema":`+"\n" {
		t.Fatalf("malformed quarantine raw = %q", got)
	}
	if got := string(result.Quarantined[1].Raw); got != `{"schema":"tokenmill.session/v1"}` {
		t.Fatalf("incomplete quarantine raw = %q", got)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after quarantine")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after failed import: stat error = %v", statErr)
	}
}

func TestReaderRejectsOversizedLinesAndQuarantinesTheirBytes(t *testing.T) {
	input := []byte(`{"schema":"tokenmill.session/v1","payload":"0123456789"}` + "\n")
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, err := NewReader(Options{MaxLineBytes: 16}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if err == nil {
		t.Fatal("ReadJSONL accepted an oversized line")
	}
	if got := CodeOf(err); got != CodeInputJSON {
		t.Fatalf("error code = %q, want %q", got, CodeInputJSON)
	}
	if len(result.Quarantined) != 1 || !bytes.Equal(result.Quarantined[0].Raw, input) {
		t.Fatalf("oversized line bytes were not quarantined: %#v", result.Quarantined)
	}
	_ = writer.Abort()
}

func TestReaderRejectsDuplicateRecordIDsAsObservableQuarantine(t *testing.T) {
	record := mustJSON(t, corpusRecord(0))
	input := append(append([]byte{}, record...), '\n')
	input = append(input, record...)
	input = append(input, '\n')

	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, err := NewReader(Options{}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if err == nil {
		t.Fatal("ReadJSONL accepted duplicate record ID")
	}
	if got := CodeOf(err); got != CodeDuplicateRecord {
		t.Fatalf("error code = %q, want %q", got, CodeDuplicateRecord)
	}
	if result.Accepted != 1 || len(result.Quarantined) != 1 {
		t.Fatalf("result = %#v, want one accepted and one quarantined", result)
	}
	if result.Quarantined[0].Code != CodeDuplicateRecord {
		t.Fatalf("quarantine code = %q, want %q", result.Quarantined[0].Code, CodeDuplicateRecord)
	}
	_ = writer.Abort()
}

func TestReaderRejectsUnknownSchemaVersion(t *testing.T) {
	encoded := strings.Replace(mustJSON(t, corpusRecord(0)), `"schema":"tokenmill.session/v1"`, `"schema":"tokenmill.session/v2"`, 1)
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, err := NewReader(Options{}).ReadJSONL(context.Background(), strings.NewReader(encoded+"\n"), writer)
	if err == nil {
		t.Fatal("ReadJSONL accepted an unknown schema version")
	}
	if got := CodeOf(err); got != CodeInputJSON {
		t.Fatalf("error code = %q, want %q", got, CodeInputJSON)
	}
	if len(result.Quarantined) != 1 {
		t.Fatalf("quarantine count = %d, want 1", len(result.Quarantined))
	}
	_ = writer.Abort()
}

func TestWriterProducesDeterministicOutputHashAndPrivateModeIsExplicit(t *testing.T) {
	records := []replay.Record{corpusRecord(0), corpusRecord(1)}
	paths := []string{
		filepath.Join(t.TempDir(), "one.jsonl"),
		filepath.Join(t.TempDir(), "two.jsonl"),
	}
	hashes := make([]string, 0, len(paths))
	for _, path := range paths {
		writer, err := NewWriter(Options{OutputPath: path})
		if err != nil {
			t.Fatalf("NewWriter(%q): %v", path, err)
		}
		for _, record := range records {
			if err := writer.Write(record); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		digest := sha256.Sum256(data)
		hashes = append(hashes, hex.EncodeToString(digest[:]))
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("deterministic output hashes differ: %v", hashes)
	}

	_, err := NewWriter(Options{
		OutputPath: filepath.Join(t.TempDir(), "private.jsonl"),
		Privacy:    PrivacyPrivate,
	})
	if err == nil {
		t.Fatal("private writer succeeded without AllowPrivate")
	}
	if got := CodeOf(err); got != CodeSecretInCorpus {
		t.Fatalf("private-mode error code = %q, want %q", got, CodeSecretInCorpus)
	}
}

func TestWriterUses0600ForCommittedCorpus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: path})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("output mode = %04o, want 0600", got)
	}
}

func TestArtifactRejectsSymlinkEscapeAndExcludesAuthFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "transcript.jsonl")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	link := filepath.Join(root, "link.jsonl")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := DiscoverArtifact(root, link); err == nil {
		t.Fatal("DiscoverArtifact accepted a symlink escaping the root")
	} else if got := CodeOf(err); got != CodePathEscape {
		t.Fatalf("symlink error code = %q, want %q", got, CodePathEscape)
	}

	auth := filepath.Join(root, "auth.json")
	if err := os.WriteFile(auth, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if _, err := DiscoverArtifact(root, auth); err == nil {
		t.Fatal("DiscoverArtifact accepted an excluded auth file")
	} else if got := CodeOf(err); got != CodeSecretInCorpus {
		t.Fatalf("auth-file error code = %q, want %q", got, CodeSecretInCorpus)
	}
}

func TestReaderDetectsSourceChangeAgainstArtifactSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "transcript.jsonl")
	original := mustJSON(t, corpusRecord(0)) + "\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	artifact, err := DiscoverArtifact(root, path)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	changed := mustJSON(t, corpusRecord(1)) + "\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatalf("WriteFile changed: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, err = NewReader(Options{Root: root}).Read(context.Background(), artifact, writer)
	if err == nil {
		t.Fatal("Read accepted a changed source artifact")
	}
	if got := CodeOf(err); got != CodeSourceChanged {
		t.Fatalf("source-change error code = %q, want %q", got, CodeSourceChanged)
	}
	_ = writer.Abort()
}

func TestReaderFailsClosedWhenSourcePathIsReplacedAfterDiscovery(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sourcePath := filepath.Join(root, "transcript.jsonl")
	outsidePath := filepath.Join(outside, "outside.jsonl")
	original := []byte(mustJSON(t, corpusRecord(0)) + "\n")
	outsideBytes := []byte(mustJSON(t, corpusRecord(1)) + "\n")
	if err := os.WriteFile(sourcePath, original, 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	if err := os.WriteFile(outsidePath, outsideBytes, 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	artifact, err := DiscoverArtifact(root, sourcePath)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("Remove source: %v", err)
	}
	if err := os.Symlink(outsidePath, sourcePath); err != nil {
		t.Fatalf("Symlink source: %v", err)
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, readErr := NewReader(Options{Root: root}).Read(context.Background(), artifact, writer)
	if got := CodeOf(readErr); got != CodeSourceChanged {
		t.Fatalf("read error code = %q, want %q (error = %v)", got, CodeSourceChanged, readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after source path replacement")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output exists after source path replacement: %v", err)
	}
}

func TestReaderRejectsForgedArtifactWithClearedHash(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "transcript.jsonl")
	original := mustJSON(t, corpusRecord(0)) + "\n"
	if err := os.WriteFile(sourcePath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	artifact, err := DiscoverArtifact(root, sourcePath)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	changed := strings.ReplaceAll(original, "turn-0", "turn-1")
	if err := os.WriteFile(sourcePath, []byte(changed), 0o600); err != nil {
		t.Fatalf("WriteFile changed source: %v", err)
	}
	if err := os.Chtimes(sourcePath, artifact.ModTime, artifact.ModTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	forged := artifact
	forged.ContentSHA256 = ""

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, readErr := NewReader(Options{Root: root}).Read(context.Background(), forged, writer)
	if got := CodeOf(readErr); got != CodeSourceChanged {
		t.Fatalf("read error code = %q, want %q (error = %v)", got, CodeSourceChanged, readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output for forged artifact")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output exists after forged artifact: %v", err)
	}
}

func TestWriterRejectsOutputCollisionWithInputArtifact(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(sourcePath, []byte(mustJSON(t, corpusRecord(0))+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	artifact, err := DiscoverArtifact(root, sourcePath)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	writer, err := NewWriter(Options{OutputPath: sourcePath})
	if err != nil {
		if got := CodeOf(err); got != CodePathEscape {
			t.Fatalf("collision error code = %q, want %q", got, CodePathEscape)
		}
		return
	}
	defer func() { _ = writer.Abort() }()
	_, readErr := NewReader(Options{Root: root}).Read(context.Background(), artifact, writer)
	if got := CodeOf(readErr); got != CodePathEscape {
		t.Fatalf("read collision error code = %q, want %q (error = %v)", got, CodePathEscape, readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close succeeded for input/output collision")
	}
}

func TestWriterDoesNotOverwriteDestinationCreatedAfterReservation(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	sentinel := []byte("pre-existing destination\n")
	if err := os.WriteFile(output, sentinel, 0o600); err != nil {
		t.Fatalf("WriteFile destination: %v", err)
	}
	if err := writer.Close(); err == nil {
		t.Fatal("Close overwrote a destination created after reservation")
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile destination: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("destination changed after collision: %q", got)
	}
}

func TestWriterDoesNotOverwriteSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	sentinel := []byte("do not replace\n")
	if err := os.WriteFile(target, sentinel, 0o600); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	output := filepath.Join(dir, "corpus.jsonl")
	if err := os.Symlink(target, output); err != nil {
		t.Fatalf("Symlink output: %v", err)
	}
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err == nil {
		t.Fatal("Close followed a symlink destination")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestReaderRejectsNonEOFReadErrorAfterValidPrefix(t *testing.T) {
	sentinel := errors.New("source read failed")
	input := []byte(mustJSON(t, corpusRecord(0)) + "\n")
	source := &scriptedReader{chunks: [][]byte{input}, errs: []error{nil, sentinel}}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, readErr := NewReader(Options{}).ReadJSONL(context.Background(), source, writer)
	if !errors.Is(readErr, sentinel) {
		t.Fatalf("read error = %v, want sentinel", readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published a prefix after source read failure")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output exists after source read failure: %v", err)
	}
}

func TestReaderMarksWriterOnCancellationAfterValidPrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &cancelAfterFirstRead{
		data:   []byte(mustJSON(t, corpusRecord(0)) + "\n"),
		cancel: cancel,
	}
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, readErr := NewReader(Options{}).ReadJSONL(ctx, source, writer)
	if !errors.Is(readErr, context.Canceled) {
		t.Fatalf("read error = %v, want context.Canceled", readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published a prefix after cancellation")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output exists after cancellation: %v", err)
	}
}

func TestReaderPreservesPartialBytesAndUnderlyingNonEOFError(t *testing.T) {
	sentinel := errors.New("unexpected source termination")
	source := &scriptedReader{
		chunks: [][]byte{[]byte(`{"schema":`)},
		errs:   []error{sentinel},
	}
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, readErr := NewReader(Options{}).ReadJSONL(context.Background(), source, writer)
	if !errors.Is(readErr, sentinel) {
		t.Fatalf("read error = %v, want sentinel", readErr)
	}
	if len(result.Quarantined) != 1 || !bytes.Equal(result.Quarantined[0].Raw, []byte(`{"schema":`)) {
		t.Fatalf("partial source bytes were not quarantined: %#v", result.Quarantined)
	}
	_ = writer.Abort()
}

func TestReaderRejectsInvalidUTF8BeforeJSONDecode(t *testing.T) {
	line := []byte(mustJSON(t, corpusRecord(0)) + "\n")
	marker := []byte(`"hello"`)
	invalid := []byte{'"', 'b', 'a', 0xff, 'd', '"'}
	line = bytes.Replace(line, marker, invalid, 1)
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, readErr := NewReader(Options{}).ReadJSONL(context.Background(), bytes.NewReader(line), writer)
	if got := CodeOf(readErr); got != CodeInputJSON {
		t.Fatalf("error code = %q, want %q", got, CodeInputJSON)
	}
	if len(result.Quarantined) != 1 || !bytes.Equal(result.Quarantined[0].Raw, line) {
		t.Fatalf("invalid UTF-8 source bytes were not quarantined: %#v", result.Quarantined)
	}
	_ = writer.Abort()
}

func TestReaderAcceptsLineAtConfiguredBoundaryAndRejectsUnterminatedOversize(t *testing.T) {
	line := []byte(mustJSON(t, corpusRecord(0)) + "\n")
	limit := len(line) - 1
	output := filepath.Join(t.TempDir(), "boundary.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, readErr := NewReader(Options{MaxLineBytes: limit}).ReadJSONL(context.Background(), bytes.NewReader(line), writer)
	if readErr != nil || result.Accepted != 1 {
		t.Fatalf("boundary read = %#v, %v; want one accepted record", result, readErr)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close boundary: %v", err)
	}

	oversized := bytes.Repeat([]byte("x"), limit+1)
	writer, err = NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "oversize.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter oversize: %v", err)
	}
	result, readErr = NewReader(Options{MaxLineBytes: limit}).ReadJSONL(context.Background(), bytes.NewReader(oversized), writer)
	if got := CodeOf(readErr); got != CodeInputJSON {
		t.Fatalf("unterminated oversize error code = %q, want %q", got, CodeInputJSON)
	}
	if len(result.Quarantined) != 1 || !bytes.Equal(result.Quarantined[0].Raw, oversized) {
		t.Fatalf("unterminated oversize bytes were not preserved: %#v", result.Quarantined)
	}
	if !strings.Contains(result.Quarantined[0].Message, "maximum") {
		t.Fatalf("unterminated oversize message = %q, want maximum-size reason", result.Quarantined[0].Message)
	}
	_ = writer.Abort()
}

func TestReaderContinuesClassifyingValidRecordsAfterQuarantine(t *testing.T) {
	first := []byte(mustJSON(t, corpusRecord(0)) + "\n")
	second := []byte(`{"schema":` + "\n")
	third := []byte(mustJSON(t, corpusRecord(1)) + "\n")
	input := append(append(append([]byte{}, first...), second...), third...)
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, readErr := NewReader(Options{}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if readErr == nil {
		t.Fatal("ReadJSONL accepted malformed record without error")
	}
	if result.Accepted != 2 || len(result.Quarantined) != 1 {
		t.Fatalf("result = %#v, want two accepted and one quarantined", result)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus containing a quarantined line")
	}
}

func TestReaderStopsExplicitlyWhenQuarantineBudgetIsExhausted(t *testing.T) {
	line := append(bytes.Repeat([]byte("x"), 40), '\n')
	input := append(append([]byte{}, line...), line...)
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, readErr := NewReader(Options{
		MaxLineBytes:       len(line),
		MaxQuarantineBytes: len(line),
	}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if got := CodeOf(readErr); got != CodeInputJSON {
		t.Fatalf("error code = %q, want %q", got, CodeInputJSON)
	}
	if !strings.Contains(readErr.Error(), "quarantine byte budget") {
		t.Fatalf("error = %v, want explicit quarantine-budget failure", readErr)
	}
	if len(result.Quarantined) != 1 || !bytes.Equal(result.Quarantined[0].Raw, line) {
		t.Fatalf("quarantine result = %#v, want exactly the first complete line", result.Quarantined)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after quarantine budget exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after quarantine budget exhaustion: %v", statErr)
	}
}

func TestWriterAbortMakesLaterCloseStableAndNonNil(t *testing.T) {
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	first := writer.Abort()
	if !errors.Is(first, ErrAborted) {
		t.Fatalf("Abort error = %v, want ErrAborted", first)
	}
	second := writer.Close()
	if !errors.Is(second, ErrAborted) {
		t.Fatalf("Close after Abort = %v, want ErrAborted", second)
	}
	third := writer.Abort()
	if !errors.Is(third, ErrAborted) {
		t.Fatalf("second Abort = %v, want ErrAborted", third)
	}
}

func TestWriterReportsRenameAndCleanupErrorsTogether(t *testing.T) {
	oldLink := linkTemporary
	oldRemove := removeTemporary
	linkErr := errors.New("rename failed")
	cleanupErr := errors.New("cleanup failed")
	linkTemporary = func(_ *os.File, _ *os.File, _ string) error { return linkErr }
	removeTemporary = func(_ *os.File, _ *os.File) error { return cleanupErr }
	defer func() {
		linkTemporary = oldLink
		removeTemporary = oldRemove
	}()

	dir := t.TempDir()
	writer, err := NewWriter(Options{OutputPath: filepath.Join(dir, "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeErr := writer.Close()
	if !errors.Is(closeErr, linkErr) || !errors.Is(closeErr, cleanupErr) {
		t.Fatalf("Close error = %v, want link and cleanup errors", closeErr)
	}
	_ = os.Remove(writer.temporary.Name())
}

func TestWriterRemovesPublishedOutputAfterPostLinkFailure(t *testing.T) {
	oldLink := linkTemporary
	linkTemporary = func(directory *os.File, publication *os.File, outputPath string) error {
		if err := oldLink(directory, publication, outputPath); err != nil {
			return err
		}
		return publication.Close()
	}
	defer func() { linkTemporary = oldLink }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err == nil {
		t.Fatal("Close succeeded after publication descriptor failure")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after post-link failure: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterLinkError(t *testing.T) {
	oldLink := linkTemporary
	linkErr := errors.New("link failed after publishing")
	linkTemporary = func(directory, publication *os.File, outputPath string) error {
		if err := oldLink(directory, publication, outputPath); err != nil {
			return err
		}
		return linkErr
	}
	defer func() { linkTemporary = oldLink }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeErr := writer.Close()
	if !errors.Is(closeErr, linkErr) {
		t.Fatalf("Close error = %v, want link error", closeErr)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after link error: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterOutputDirectoryCloseFailure(t *testing.T) {
	oldCloseDirectory := closeOutputDirectoryFile
	closeErr := errors.New("output directory close failed")
	closeOutputDirectoryFile = func(file *os.File) error {
		_ = file.Close()
		return closeErr
	}
	defer func() { closeOutputDirectoryFile = oldCloseDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want output directory close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after output directory close failure: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterRollbackDirectoryCloseFailure(t *testing.T) {
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("rollback directory close failed")
	closeRollbackDirectoryFile = func(file *os.File) error {
		_ = file.Close()
		return closeErr
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want rollback directory close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after rollback directory close failure: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterRollbackCleanupDuplicationFailure(t *testing.T) {
	oldDuplicateDirectory := duplicateOutputDirectoryFile
	duplicateErr := errors.New("rollback cleanup descriptor duplication failed")
	duplicateCalls := 0
	duplicateOutputDirectoryFile = func(file *os.File) (*os.File, error) {
		duplicateCalls++
		if duplicateCalls == 2 {
			return nil, duplicateErr
		}
		return duplicateOutputDirectory(file)
	}
	defer func() { duplicateOutputDirectoryFile = oldDuplicateDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, duplicateErr) {
		t.Fatalf("Close error = %v, want cleanup descriptor duplication error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after cleanup descriptor duplication failure: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterCleanupDirectoryCloseFailure(t *testing.T) {
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("cleanup directory close failed")
	closeCalls := 0
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 2 {
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want cleanup directory close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after cleanup directory close failure: %v", err)
	}
}

func TestWriterDoesNotRemoveReplacementAfterPublishedOutputIdentityRace(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	oldCloseDirectory := closeOutputDirectoryFile
	oldMove := moveOutputEntry
	closeErr := errors.New("output directory close failed after replacement")
	closeOutputDirectoryFile = func(file *os.File) error {
		_ = file.Close()
		return closeErr
	}
	moved := false
	moveOutputEntry = func(directory *os.File, first, second string, flags uintptr) error {
		err := moveOutput(directory, first, second, flags)
		if err == nil && !moved {
			moved = true
			replacement := filepath.Join(filepath.Dir(output), "replacement")
			if err := os.WriteFile(replacement, []byte("replacement\n"), 0o600); err != nil {
				return err
			}
			if err := os.Rename(replacement, output); err != nil {
				return err
			}
		}
		return err
	}
	defer func() {
		closeOutputDirectoryFile = oldCloseDirectory
		moveOutputEntry = oldMove
	}()

	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want output directory close error", err)
	}
	replacement, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
	if string(replacement) != "replacement\n" {
		t.Fatalf("replacement contents = %q, want replacement marker", replacement)
	}
}

func TestWriterCleansPublishedOutputUsingHeldDirectoryAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "output")
	movedDirectory := filepath.Join(parent, "moved-output")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir output: %v", err)
	}
	oldCloseDirectory := closeOutputDirectoryFile
	closeErr := errors.New("output directory moved during close")
	closeOutputDirectoryFile = func(file *os.File) error {
		if err := os.Rename(directory, movedDirectory); err != nil {
			return err
		}
		if err := os.Mkdir(directory, 0o700); err != nil {
			return err
		}
		_ = file.Close()
		return closeErr
	}
	defer func() { closeOutputDirectoryFile = oldCloseDirectory }()

	output := filepath.Join(directory, "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want moved-directory close error", err)
	}
	if _, err := os.Stat(filepath.Join(movedDirectory, "corpus.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains in renamed directory: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterFinalCleanupDirectoryCloseFailure(t *testing.T) {
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("final cleanup directory close failed")
	closeCalls := 0
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 3 {
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want final cleanup close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after final cleanup close failure: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterLastCleanupDirectoryCloseFailure(t *testing.T) {
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("last cleanup directory close failed")
	closeCalls := 0
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 4 {
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want last cleanup close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after last cleanup close failure: %v", err)
	}
}

func TestOpenCleanupEntryDoesNotBlockOnFIFO(t *testing.T) {
	directoryPath := t.TempDir()
	fifoPath := filepath.Join(directoryPath, "output")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatalf("Open directory: %v", err)
	}
	defer func() { _ = directory.Close() }()
	type result struct {
		file *os.File
		err  error
	}
	done := make(chan result, 1)
	go func() {
		file, err := openCleanupEntry(directory, "output")
		done <- result{file: file, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("openCleanupEntry: %v", got.err)
		}
		defer func() { _ = got.file.Close() }()
		info, err := got.file.Stat()
		if err != nil {
			t.Fatalf("Stat cleanup entry: %v", err)
		}
		if info.Mode().IsRegular() {
			t.Fatalf("FIFO cleanup entry was reported as regular: %v", info.Mode())
		}
	case <-time.After(250 * time.Millisecond):
		unblock, err := os.OpenFile(fifoPath, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = unblock.Close()
		}
		select {
		case got := <-done:
			if got.file != nil {
				_ = got.file.Close()
			}
		case <-time.After(time.Second):
			t.Fatal("openCleanupEntry remained blocked on FIFO")
		}
		t.Fatal("openCleanupEntry blocked on FIFO")
	}
}

func TestWriterRemovesPublishedOutputAfterFallbackCleanupDirectoryCloseFailure(t *testing.T) {
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("fallback cleanup directory close failed")
	closeCalls := 0
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 5 {
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want fallback cleanup close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after fallback cleanup close failure: %v", err)
	}
}

func TestWriterRemovesPublishedOutputAfterFinalFallbackDirectoryCloseFailure(t *testing.T) {
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("final fallback cleanup directory close failed")
	closeCalls := 0
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 6 {
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want final fallback cleanup close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after final fallback cleanup close failure: %v", err)
	}
}

func TestWriterCleansRenamedDirectoryAfterFinalFallbackCloseFailure(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "output")
	movedDirectory := filepath.Join(parent, "moved-output")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir output: %v", err)
	}
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	closeErr := errors.New("final fallback directory moved during close")
	closeCalls := 0
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 6 {
			if err := os.Rename(directory, movedDirectory); err != nil {
				return err
			}
			if err := os.Mkdir(directory, 0o700); err != nil {
				return err
			}
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	defer func() { closeRollbackDirectoryFile = oldCloseRollbackDirectory }()

	output := filepath.Join(directory, "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want moved-directory close error", closeResult)
	}
	if _, err := os.Stat(filepath.Join(movedDirectory, "corpus.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains in renamed directory: %v", err)
	}
}

func TestWriterDoesNotUsePathFallbackAfterEmergencyCloseFailure(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "output")
	movedDirectory := filepath.Join(parent, "moved-output")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir output: %v", err)
	}
	oldCloseRollbackDirectory := closeRollbackDirectoryFile
	oldMove := moveOutputEntry
	closeErr := errors.New("emergency directory close failed")
	closeCalls := 0
	renamed := false
	pathFallbackUsed := false
	closeRollbackDirectoryFile = func(file *os.File) error {
		closeCalls++
		if closeCalls == 7 {
			if err := os.Rename(directory, movedDirectory); err != nil {
				return err
			}
			if err := os.Mkdir(directory, 0o700); err != nil {
				return err
			}
			renamed = true
			_ = file.Close()
			return closeErr
		}
		return file.Close()
	}
	moveOutputEntry = func(file *os.File, first, second string, flags uintptr) error {
		if renamed {
			fileInfo, fileErr := file.Stat()
			pathInfo, pathErr := os.Stat(directory)
			if fileErr == nil && pathErr == nil && os.SameFile(fileInfo, pathInfo) {
				pathFallbackUsed = true
			}
		}
		return oldMove(file, first, second, flags)
	}
	defer func() {
		closeRollbackDirectoryFile = oldCloseRollbackDirectory
		moveOutputEntry = oldMove
	}()

	output := filepath.Join(directory, "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, closeErr) {
		t.Fatalf("Close error = %v, want emergency close error", closeResult)
	}
	if pathFallbackUsed {
		t.Fatal("Close reopened the mutable output pathname after emergency close failure")
	}
}

func TestWriterClosesAllCleanupDescriptorsAfterFailure(t *testing.T) {
	tests := []struct {
		name             string
		duplicateFailure int
		closeFailure     bool
	}{
		{name: "close failure", closeFailure: true},
		{name: "final duplication failure", duplicateFailure: 6},
		{name: "emergency duplication failure", duplicateFailure: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldDuplicateDirectory := duplicateOutputDirectoryFile
			oldCloseRollbackDirectory := closeRollbackDirectoryFile
			duplicateCalls := 0
			closeCalls := 0
			closeErr := errors.New("cleanup descriptor close failed")
			duplicateErr := errors.New("final cleanup descriptor duplication failed")
			var descriptors []int
			duplicateOutputDirectoryFile = func(file *os.File) (*os.File, error) {
				duplicateCalls++
				if duplicateCalls == test.duplicateFailure {
					return nil, duplicateErr
				}
				duplicate, err := duplicateOutputDirectory(file)
				if err == nil {
					descriptors = append(descriptors, int(duplicate.Fd()))
				}
				return duplicate, err
			}
			closeRollbackDirectoryFile = func(file *os.File) error {
				closeCalls++
				if test.closeFailure && closeCalls == 4 {
					_ = file.Close()
					return closeErr
				}
				return file.Close()
			}
			defer func() {
				duplicateOutputDirectoryFile = oldDuplicateDirectory
				closeRollbackDirectoryFile = oldCloseRollbackDirectory
			}()

			output := filepath.Join(t.TempDir(), "corpus.jsonl")
			writer, err := NewWriter(Options{OutputPath: output})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			if err := writer.Write(corpusRecord(0)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			closeResult := writer.Close()
			if closeResult == nil {
				t.Fatal("Close succeeded after injected cleanup failure")
			}
			for _, fd := range descriptors {
				_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0)
				if errno != syscall.EBADF {
					t.Fatalf("cleanup descriptor %d remained open: %v", fd, errno)
				}
			}
		})
	}
}

func TestWriterReportsPublishedOutputInspectionCloseError(t *testing.T) {
	oldLink := linkTemporary
	oldCloseInspection := closeCleanupEntryFile
	linkErr := errors.New("link failed after publishing")
	inspectionErr := errors.New("published output inspection close failed")
	linkTemporary = func(directory, publication *os.File, outputPath string) error {
		if err := oldLink(directory, publication, outputPath); err != nil {
			return err
		}
		return linkErr
	}
	closeCleanupEntryFile = func(file *os.File) error {
		_ = file.Close()
		return inspectionErr
	}
	defer func() {
		linkTemporary = oldLink
		closeCleanupEntryFile = oldCloseInspection
	}()

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeResult := writer.Close()
	if !errors.Is(closeResult, inspectionErr) {
		t.Fatalf("Close error = %v, want inspection close error", closeResult)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published output remains after inspection close error: %v", err)
	}
}

func TestReaderReturnsInjectedSourceCloseError(t *testing.T) {
	closeErr := errors.New("source close failed")

	root := t.TempDir()
	sourcePath := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(sourcePath, []byte(mustJSON(t, corpusRecord(0))+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	artifact, err := DiscoverArtifact(root, sourcePath)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	oldClose := closeReadFile
	closeReadFile = func(*os.File) error { return closeErr }
	defer func() { closeReadFile = oldClose }()
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, readErr := NewReader(Options{Root: root}).Read(context.Background(), artifact, writer)
	if !errors.Is(readErr, closeErr) {
		t.Fatalf("read error = %v, want close error", readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published corpus after source close failure")
	}
	_ = artifact.Close()
}

func TestDiscoverArtifactRejectsConservativeSensitiveFilenameVariants(t *testing.T) {
	root := t.TempDir()
	names := []string{
		"AUTH.SQLITE",
		"credentials.toml",
		"state_5.sqlite-wal",
		"state_5.sqlite-shm",
		"cookies.db",
		"private-key.pem",
		"session.sqlite",
		"secrets.json",
		"api_keys.json",
		"oauth_token.json",
		"id_ecdsa",
		".git-credentials",
		"opencode.db-journal",
		"my-auth.json",
		"user_credentials.json",
		"AUTH-STORE.json",
		"CREDENTIALS_BACKUP.json",
		"COOKIE_STORE.json",
		"SECRET-STORE.json",
		"SESSION-STATE.json",
		"AUTHENTICATION-CACHE.json",
		"DB-BACKUPS.json",
		"provider-credentials.json",
		"openai-api-key.json",
		"OpenAI-Key.json",
		"state-cache.json",
		"transcript.WAL",
		"transcript.SHM",
		"transcript.JOURNAL",
		"private-key.txt",
		"archive.sqlite3",
		"cookiejar.json",
		"idrsa",
		"credentialvault.json",
		"privatekeyvault.json",
		"key.json",
		"bearer.txt",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte("not a corpus\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := DiscoverArtifact(root, path); err == nil {
				t.Fatal("DiscoverArtifact accepted a sensitive filename variant")
			} else if got := CodeOf(err); got != CodeSecretInCorpus {
				t.Fatalf("error code = %q, want %q", got, CodeSecretInCorpus)
			}
		})
	}

	for _, relative := range []string{
		"auth/token.json",
		"auth-store/blob.json",
		"credentials_backup/blob.json",
		"cookie_store/blob.json",
		"secret-store/blob.json",
		"session-state/blob.json",
		"authentication-cache/blob.json",
		"db-backups/blob.json",
		"provider/CREDENTIALS/blob.json",
		"provider/key.json",
		"OPENAI/API-KEY/blob.json",
		"OPENAI/key.json",
		"database-backup/blob.json",
		"state-cache/blob.json",
		"session/cache/blob.json",
		"private-key-store/blob.json",
		"state_5.sqlite.WAL/blob.json",
		".ssh/config",
		".SSH/config",
		".GIT/config",
		"api_keys/blob.json",
		"oauth/blob.json",
		"private_keys/blob.pem.txt",
		"token_store/blob.json",
		"sqlite/blob.json",
	} {
		t.Run(relative, func(t *testing.T) {
			path := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte("not a corpus\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := DiscoverArtifact(root, path); err == nil {
				t.Fatal("DiscoverArtifact accepted a sensitive directory path")
			} else if got := CodeOf(err); got != CodeSecretInCorpus {
				t.Fatalf("error code = %q, want %q", got, CodeSecretInCorpus)
			}
		})
	}

	for _, relative := range []string{
		"sessions/2026-08-27/transcript.jsonl",
		"history/turn-1.jsonl",
		"conversation/transcript.jsonl",
		"my-authoring-notes.jsonl",
		"provider/openai-transcript.jsonl",
		"openai-keyword-transcript.jsonl",
		"project/tokenmill-transcript.jsonl",
		"vault-transcript.jsonl",
		"keynote-transcript.jsonl",
		"bearerless-transcript.jsonl",
	} {
		t.Run("harmless-"+relative, func(t *testing.T) {
			path := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte("transcript\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := DiscoverArtifact(root, path); err != nil {
				t.Fatalf("DiscoverArtifact rejected harmless transcript path: %v", err)
			}
		})
	}
}

func TestDiscoverArtifactRejectsConcatenatedSensitiveFilenameVariants(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"credentialvault.jsonl",
		"privatekeyvault.jsonl",
		"accesskeys.json",
		"openaiapikeys.json",
		"openaiapikeys.jsonl",
		"sessioncache.json",
		"sessionstore.json",
		"keyvault.json",
		"keybackup.json",
		"authheaders.json",
		"bearerheaders.json",
		"netrc",
		"providerkey.json",
		"awskey.json",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte("not a corpus\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := DiscoverArtifact(root, path); err == nil {
				t.Fatal("DiscoverArtifact accepted a concatenated sensitive filename")
			} else if got := CodeOf(err); got != CodeSecretInCorpus {
				t.Fatalf("error code = %q, want %q", got, CodeSecretInCorpus)
			}
		})
	}
}

func TestScanTranscriptTreeReportsDirectoryCloseError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sessions", "2026", "08", "27", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	closeErr := errors.New("directory close failed")
	oldClose := closeReadFile
	closeReadFile = func(file *os.File) error {
		_ = file.Close()
		return closeErr
	}
	defer func() { closeReadFile = oldClose }()

	_, err := scanTranscriptTree(
		context.Background(), root, "sessions",
		func(string) bool { return true },
		func(string) bool { return true },
	)
	if !errors.Is(err, closeErr) {
		t.Fatalf("scan error = %v, want directory close error", err)
	}
}

func TestOpenSourceArtifactForReadReportsRootCloseError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	artifact, err := DiscoverArtifact(root, path)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	closeErr := errors.New("root close failed")
	oldClose := closeReadFile
	closeReadFile = func(file *os.File) error {
		_ = file.Close()
		return closeErr
	}
	defer func() { closeReadFile = oldClose }()

	file, _, readErr := openSourceArtifactForRead(root, *artifact.snapshot, func(string) bool { return true })
	if !errors.Is(readErr, closeErr) {
		t.Fatalf("read error = %v, want root close error", readErr)
	}
	if file != nil {
		t.Fatal("returned source descriptor after root close failure")
	}
}

func TestWriterRejectsRenamedOutputDirectory(t *testing.T) {
	parent := t.TempDir()
	destinationDir := filepath.Join(parent, "destination")
	if err := os.Mkdir(destinationDir, 0o700); err != nil {
		t.Fatalf("Mkdir destination: %v", err)
	}
	output := filepath.Join(destinationDir, "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	renamedDir := filepath.Join(parent, "renamed-destination")
	if err := os.Rename(destinationDir, renamedDir); err != nil {
		t.Fatalf("Rename destination: %v", err)
	}
	if err := os.Mkdir(destinationDir, 0o700); err != nil {
		t.Fatalf("Recreate destination: %v", err)
	}

	closeErr := writer.Close()
	if closeErr == nil {
		t.Fatal("Close reported success after output directory identity changed")
	}
	if got := CodeOf(closeErr); got != CodePathEscape {
		t.Fatalf("directory identity error code = %q, want %q (error = %v)", got, CodePathEscape, closeErr)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("requested output exists after directory identity failure: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(renamedDir, "corpus.jsonl")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("renamed directory retained published output after identity failure: %v", statErr)
	}
}

func TestReaderAcceptsExactPayloadAtLineAndQuarantineBoundary(t *testing.T) {
	for _, delimiter := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("delimiter-%d", len(delimiter)), func(t *testing.T) {
			payload := []byte(mustJSON(t, corpusRecord(0)))
			limit := len(payload)
			output := filepath.Join(t.TempDir(), "corpus.jsonl")
			writer, err := NewWriter(Options{
				OutputPath:         output,
				MaxLineBytes:       limit,
				MaxQuarantineBytes: limit,
			})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			input := append(append([]byte{}, payload...), delimiter...)
			result, readErr := NewReader(Options{
				MaxLineBytes:       limit,
				MaxQuarantineBytes: limit,
			}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
			if readErr != nil {
				t.Fatalf("ReadJSONL: %v", readErr)
			}
			if result.Accepted != 1 || len(result.Quarantined) != 0 {
				t.Fatalf("result = %#v, want one accepted record and no quarantine", result)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestReaderChargesOnlyPayloadForMultipleRejectedLFAndCRLFRecords(t *testing.T) {
	for _, delimiter := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("delimiter-%d", len(delimiter)), func(t *testing.T) {
			payload := []byte("{}")
			budget := len(payload) * 2
			input := []byte(string(payload) + delimiter + string(payload) + delimiter + string(payload) + delimiter)
			output := filepath.Join(t.TempDir(), "corpus.jsonl")
			writer, err := NewWriter(Options{
				OutputPath:         output,
				MaxLineBytes:       len(payload),
				MaxQuarantineBytes: budget,
			})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			result, readErr := NewReader(Options{
				MaxLineBytes:       len(payload),
				MaxQuarantineBytes: budget,
			}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
			if got := CodeOf(readErr); got != CodeInputJSON {
				t.Fatalf("error code = %q, want %q (error = %v)", got, CodeInputJSON, readErr)
			}
			if !strings.Contains(readErr.Error(), "quarantine byte budget") {
				t.Fatalf("error = %v, want explicit payload-budget failure", readErr)
			}
			if len(result.Quarantined) != 2 {
				t.Fatalf("quarantine count = %d, want two exact-budget records", len(result.Quarantined))
			}
			for index, quarantine := range result.Quarantined {
				want := append(append([]byte(nil), payload...), []byte(delimiter)...)
				if !bytes.Equal(quarantine.Raw, want) {
					t.Fatalf("quarantine[%d] raw = %q, want %q", index, quarantine.Raw, want)
				}
			}
			if writer.quarantineBytes != budget {
				t.Fatalf("writer quarantine payload bytes = %d, want %d", writer.quarantineBytes, budget)
			}
			if closeErr := writer.Close(); closeErr == nil {
				t.Fatal("Close published output after explicit over-budget record")
			}
			if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("output exists after quarantine budget exhaustion: %v", statErr)
			}
		})
	}
}

func TestReaderBoundsZeroPayloadQuarantineEntries(t *testing.T) {
	input := []byte("\n\r\n\n")
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{
		OutputPath:           output,
		MaxLineBytes:         1,
		MaxQuarantineBytes:   1,
		MaxQuarantineEntries: 2,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, readErr := NewReader(Options{
		MaxLineBytes:         1,
		MaxQuarantineBytes:   1,
		MaxQuarantineEntries: 2,
	}).ReadJSONL(context.Background(), bytes.NewReader(input), writer)
	if got := CodeOf(readErr); got != CodeInputJSON {
		t.Fatalf("error code = %q, want %q (error = %v)", got, CodeInputJSON, readErr)
	}
	if !strings.Contains(readErr.Error(), "quarantine entry") {
		t.Fatalf("error = %v, want explicit quarantine-entry failure", readErr)
	}
	if len(result.Quarantined) != 2 {
		t.Fatalf("quarantine count = %d, want two entries", len(result.Quarantined))
	}
	if !bytes.Equal(result.Quarantined[0].Raw, []byte("\n")) || !bytes.Equal(result.Quarantined[1].Raw, []byte("\r\n")) {
		t.Fatalf("zero-payload quarantine raw bytes = %#v, want LF and CRLF", result.Quarantined)
	}
	if writer.quarantineBytes != 0 {
		t.Fatalf("zero-payload quarantine bytes = %d, want 0", writer.quarantineBytes)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output after quarantine-entry exhaustion")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after quarantine-entry exhaustion: %v", statErr)
	}
}

func TestWriterQuarantineBoundsZeroPayloadEntriesAndPreservesRawBytes(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{
		OutputPath:           output,
		MaxLineBytes:         64,
		MaxQuarantineBytes:   64,
		MaxQuarantineEntries: 2,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for line, raw := range [][]byte{[]byte("\n"), []byte("\r\n")} {
		if err := writer.Quarantine(Quarantine{Line: line + 1, Code: CodeInputJSON, Raw: raw}); err != nil {
			t.Fatalf("Quarantine line %d: %v", line+1, err)
		}
	}
	if writer.quarantineBytes != 0 {
		t.Fatalf("quarantine payload bytes = %d, want 0", writer.quarantineBytes)
	}
	thirdRaw := []byte("third-line\n")
	thirdErr := writer.Quarantine(Quarantine{Line: 3, Code: CodeInputJSON, Raw: thirdRaw})
	if got := CodeOf(thirdErr); got != CodeInputJSON {
		t.Fatalf("entry exhaustion code = %q, want %q (error = %v)", got, CodeInputJSON, thirdErr)
	}
	if !strings.Contains(thirdErr.Error(), "quarantine entry") {
		t.Fatalf("entry exhaustion error = %v, want entry-bound reason", thirdErr)
	}
	quarantined := writer.Quarantined()
	if len(quarantined) != 2 {
		t.Fatalf("quarantine count = %d, want two entries", len(quarantined))
	}
	if bytes.Equal(quarantined[len(quarantined)-1].Raw, thirdRaw) {
		t.Fatal("writer silently retained a rejected raw line after entry exhaustion")
	}
	if closeErr := writer.Close(); CodeOf(closeErr) != CodeInputJSON {
		t.Fatalf("Close error code = %q, want %q (error = %v)", CodeOf(closeErr), CodeInputJSON, closeErr)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after direct quarantine entry exhaustion: %v", statErr)
	}
}

func TestWriterQuarantineRejectsUnboundedMetadataWithoutTruncatingRaw(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxLineBytes: 64, MaxQuarantineBytes: 64})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	raw := []byte("raw\n")
	err = writer.Quarantine(Quarantine{
		Code:    CodeInputJSON,
		Message: strings.Repeat("m", maxQuarantineMessageBytes+1),
		Raw:     raw,
	})
	if got := CodeOf(err); got != CodeInputJSON {
		t.Fatalf("metadata exhaustion code = %q, want %q (error = %v)", got, CodeInputJSON, err)
	}
	if !strings.Contains(err.Error(), "quarantine metadata") {
		t.Fatalf("metadata exhaustion error = %v, want metadata-bound reason", err)
	}
	if len(writer.Quarantined()) != 0 {
		t.Fatalf("quarantine count = %d, want no truncated metadata entry", len(writer.Quarantined()))
	}
	if writer.quarantineBytes != 0 || writer.quarantineEntries != 0 {
		t.Fatalf("failed metadata quarantine changed accounting: payload=%d entries=%d", writer.quarantineBytes, writer.quarantineEntries)
	}
	if closeErr := writer.Close(); CodeOf(closeErr) != CodeInputJSON {
		t.Fatalf("Close error code = %q, want %q (error = %v)", CodeOf(closeErr), CodeInputJSON, closeErr)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after metadata exhaustion: %v", statErr)
	}
}

func TestWriterQuarantineRejectsUnknownCodeWithoutRetention(t *testing.T) {
	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output, MaxLineBytes: 64, MaxQuarantineBytes: 64})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	err = writer.Quarantine(Quarantine{
		Code: "E_NOT_A_CORPUS_CODE",
		Raw:  []byte("rejected\n"),
	})
	if got := CodeOf(err); got != CodeInputJSON {
		t.Fatalf("unknown-code error code = %q, want %q (error = %v)", got, CodeInputJSON, err)
	}
	if !strings.Contains(err.Error(), "unknown quarantine code") {
		t.Fatalf("unknown-code error = %v, want explicit validation reason", err)
	}
	if len(writer.Quarantined()) != 0 || writer.quarantineBytes != 0 || writer.quarantineEntries != 0 {
		t.Fatalf("unknown quarantine changed retention: entries=%d bytes=%d", writer.quarantineEntries, writer.quarantineBytes)
	}
	if closeErr := writer.Close(); CodeOf(closeErr) != CodeInputJSON {
		t.Fatalf("Close error code = %q, want %q (error = %v)", CodeOf(closeErr), CodeInputJSON, closeErr)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after unknown quarantine code: %v", statErr)
	}
}

func TestOptionsCannotRaiseSecureQuarantineEntryBound(t *testing.T) {
	if err := (Options{MaxQuarantineEntries: DefaultMaxQuarantineEntries + 1}).Validate(); err == nil {
		t.Fatal("Options accepted a quarantine entry limit above the secure default")
	}
}

func TestSecureTraversalJoinsOpenAndCloseErrors(t *testing.T) {
	openErr := errors.New("openat failed")
	closeErr := errors.New("descriptor close failed")
	oldOpenAt := openSecureAt
	oldCloseFD := closeSecureFD
	openSecureAt = func(int, string, int, uint32) (int, error) {
		return -1, openErr
	}
	closeSecureFD = func(fd int) error {
		_ = syscall.Close(fd)
		return closeErr
	}
	defer func() {
		openSecureAt = oldOpenAt
		closeSecureFD = oldCloseFD
	}()

	_, err := openSecurePath(filepath.Join(t.TempDir(), "child"), false)
	if !errors.Is(err, openErr) || !errors.Is(err, closeErr) {
		t.Fatalf("traversal error = %v, want open and close errors", err)
	}
}

func TestSecureTraversalClosesNextDescriptorWhenParentCloseFails(t *testing.T) {
	closeErr := errors.New("parent descriptor close failed")
	oldCloseFD := closeSecureFD
	closeCalls := 0
	closeSecureFD = func(fd int) error {
		closeCalls++
		err := syscall.Close(fd)
		if closeCalls == 1 {
			return errors.Join(closeErr, err)
		}
		return err
	}
	defer func() { closeSecureFD = oldCloseFD }()

	_, err := openSecurePath(filepath.Join(t.TempDir(), "child"), false)
	if !errors.Is(err, closeErr) {
		t.Fatalf("traversal error = %v, want close error", err)
	}
	if closeCalls < 2 {
		t.Fatalf("close calls = %d, want parent and next descriptor cleanup", closeCalls)
	}
}

func TestSecureTraversalJoinsCloseErrorWhenRejectingParentEscape(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open root: %v", err)
	}
	defer func() { _ = root.Close() }()

	closeErr := errors.New("escape cleanup failed")
	oldCloseFD := closeSecureFD
	closeSecureFD = func(fd int) error {
		_ = syscall.Close(fd)
		return closeErr
	}
	defer func() { closeSecureFD = oldCloseFD }()

	_, err = openSecureRelative(root, "../escape", false)
	if !errors.Is(err, closeErr) {
		t.Fatalf("escape error = %v, want close error", err)
	}
}

func TestSecureTraversalPreservesSymlinkAndRawCloseErrorsWithoutLeakingDescriptors(t *testing.T) {
	closeErr := errors.New("raw descriptor close failed")
	oldOpenAt := openSecureAt
	oldCloseFD := closeSecureFD
	defer func() {
		openSecureAt = oldOpenAt
		closeSecureFD = oldCloseFD
	}()

	var opened []int
	var closeCalls int
	openSecureAt = func(int, string, int, uint32) (int, error) {
		fd, err := syscall.Open("/dev/null", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			t.Fatalf("open test descriptor: %v", err)
		}
		opened = append(opened, fd)
		return fd, syscall.ELOOP
	}
	closeSecureFD = func(fd int) error {
		closeCalls++
		if err := syscall.Close(fd); err != nil {
			t.Errorf("close test descriptor %d: %v", fd, err)
		}
		if closeCalls%2 == 1 {
			return closeErr
		}
		return nil
	}

	assertClosed := func() {
		t.Helper()
		for _, fd := range opened {
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0)
			if errno != syscall.EBADF {
				t.Fatalf("test descriptor %d remained open: %v", fd, errno)
			}
		}
	}
	assertErrors := func(name string, err error) {
		t.Helper()
		for _, want := range []error{errSecurePathSymlink, syscall.ELOOP, closeErr} {
			if !errors.Is(err, want) {
				t.Errorf("%s error = %v, does not preserve %v", name, err, want)
			}
		}
	}

	_, err := openSecurePath(filepath.Join(t.TempDir(), "symlink"), false)
	assertErrors("openSecurePath", err)
	if closeCalls != 2 {
		t.Fatalf("openSecurePath close calls = %d, want parent and returned descriptor cleanup", closeCalls)
	}
	assertClosed()

	root, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open root: %v", err)
	}
	_, err = openSecureRelative(root, "symlink", false)
	assertErrors("openSecureRelative", err)
	if closeCalls != 4 {
		t.Fatalf("openSecureRelative cumulative close calls = %d, want four", closeCalls)
	}
	assertClosed()
	if err := root.Close(); err != nil {
		t.Fatalf("Close root: %v", err)
	}
}

func TestReaderRejectsTokenlessForgedArtifact(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "transcript.jsonl")
	data := []byte(mustJSON(t, corpusRecord(0)) + "\n")
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	digest := sha256.Sum256(data)
	forged := Artifact{
		Path:          sourcePath,
		RelativePath:  "transcript.jsonl",
		Size:          info.Size(),
		ModTime:       info.ModTime(),
		Inode:         fileInode(info),
		ContentSHA256: hex.EncodeToString(digest[:]),
		Captured:      true,
	}

	output := filepath.Join(t.TempDir(), "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, readErr := NewReader(Options{Root: root}).Read(context.Background(), forged, writer)
	if got := CodeOf(readErr); got != CodeSourceChanged {
		t.Fatalf("read error code = %q, want %q (error = %v)", got, CodeSourceChanged, readErr)
	}
	if closeErr := writer.Close(); closeErr == nil {
		t.Fatal("Close published output for tokenless forged artifact")
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output exists after tokenless forged artifact: %v", statErr)
	}
}

func TestWriterCreatesTemporaryWithoutAPathnameEntry(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "corpus.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary output is visible by pathname: %#v", entries)
	}
	if err := writer.Abort(); !errors.Is(err, ErrAborted) {
		t.Fatalf("Abort: %v", err)
	}
}

func TestWriterPublishesOwnedTemporaryDescriptorAfterTempNameReplacement(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "corpus.jsonl")
	target := filepath.Join(dir, "untrusted.txt")
	if err := os.WriteFile(target, []byte("untrusted target\n"), 0o600); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	temporaryPath := writer.temporary.Name()
	if _, statErr := os.Lstat(temporaryPath); statErr == nil {
		if err := os.Remove(temporaryPath); err != nil {
			t.Fatalf("Remove temporary entry: %v", err)
		}
		if err := os.Symlink(target, temporaryPath); err != nil {
			t.Fatalf("Symlink replacement: %v", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Lstat temporary entry: %v", statErr)
	}
	if err := writer.Write(corpusRecord(0)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	outputInfo, err := os.Lstat(output)
	if err != nil {
		t.Fatalf("Lstat output: %v", err)
	}
	if outputInfo.Mode()&os.ModeSymlink != 0 || !outputInfo.Mode().IsRegular() {
		t.Fatalf("output is not an owned regular file: %v", outputInfo.Mode())
	}
	outputData, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile output: %v", err)
	}
	if bytes.Equal(outputData, []byte("untrusted target\n")) {
		t.Fatal("output published attacker-controlled temporary entry")
	}
}

func TestDiscoverArtifactRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "transcript.jsonl")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := DiscoverArtifact(root, fifo)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("DiscoverArtifact accepted a FIFO")
		}
	case <-time.After(250 * time.Millisecond):
		writer, openErr := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if openErr == nil {
			_ = writer.Close()
		}
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("DiscoverArtifact remained blocked on a FIFO")
		}
		t.Fatal("DiscoverArtifact blocked before rejecting a FIFO")
	}
}

func TestReaderReportsCloseErrorOnEarlyReturn(t *testing.T) {
	closeErr := errors.New("source close failed before read")

	root := t.TempDir()
	sourcePath := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(sourcePath, []byte(mustJSON(t, corpusRecord(0))+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	artifact, err := DiscoverArtifact(root, sourcePath)
	if err != nil {
		t.Fatalf("DiscoverArtifact: %v", err)
	}
	oldClose := closeReadFile
	closeReadFile = func(*os.File) error { return closeErr }
	defer func() { closeReadFile = oldClose }()
	writer, err := NewWriter(Options{OutputPath: filepath.Join(t.TempDir(), "corpus.jsonl")})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	writer.outputPath = sourcePath
	_, readErr := NewReader(Options{Root: root}).Read(context.Background(), artifact, writer)
	if !errors.Is(readErr, closeErr) {
		t.Fatalf("read error = %v, want close error", readErr)
	}
	_ = writer.Abort()
}

func TestDiscoverArtifactReportsRootCloseErrorOnEarlyReturn(t *testing.T) {
	closeErr := errors.New("root close failed")
	oldClose := closeReadFile
	closeReadFile = func(*os.File) error { return closeErr }
	defer func() { closeReadFile = oldClose }()

	root := t.TempDir()
	_, err := DiscoverArtifact(root, filepath.Join(root, "..", "outside.jsonl"))
	if !errors.Is(err, closeErr) {
		t.Fatalf("discovery error = %v, want root close error", err)
	}
}

type scriptedReader struct {
	chunks [][]byte
	errs   []error
	index  int
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		if r.index < len(r.errs) {
			err := r.errs[r.index]
			r.index++
			return 0, err
		}
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	var err error
	if r.index < len(r.errs) {
		err = r.errs[r.index]
	}
	r.index++
	n := copy(p, chunk)
	if n != len(chunk) {
		r.chunks = append([][]byte{chunk[n:]}, r.chunks[r.index:]...)
		r.errs = append([]error{err}, r.errs[r.index:]...)
		r.index = 0
		return n, nil
	}
	return n, err
}

type cancelAfterFirstRead struct {
	data   []byte
	cancel context.CancelFunc
	sent   bool
}

func (r *cancelAfterFirstRead) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	n := copy(p, r.data)
	r.cancel()
	return n, nil
}

func corpusRecord(sequence int) replay.Record {
	return replay.Record{
		Schema:    replay.SchemaVersion,
		RecordID:  "fixture:session-1:turn-" + string(rune('0'+sequence)),
		Source:    replay.Source{System: "fixture", Version: "test"},
		SessionID: "fixture:session-1",
		TurnID:    "turn-" + string(rune('0'+sequence)),
		Sequence:  sequence,
		Timestamp: time.Date(2026, time.August, 27, 12, 0, sequence, 0, time.UTC),
		Messages: []replay.Message{
			{Role: replay.RoleUser, Parts: []replay.Part{{Type: "text", Text: "hello"}}},
			{Role: replay.RoleAssistant, Parts: []replay.Part{{Type: "text", Text: "world"}}},
		},
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(encoded)
}

func mustJSONL(t *testing.T, records ...replay.Record) []byte {
	t.Helper()
	var input bytes.Buffer
	for _, record := range records {
		input.WriteString(mustJSON(t, record))
		input.WriteByte('\n')
	}
	return input.Bytes()
}
