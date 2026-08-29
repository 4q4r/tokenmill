//go:build linux

package corpus

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/replay"
)

// sourceLine is the bounded, byte-preserving unit shared by the JSONL
// importers. Raw retains a line delimiter when one was present; Payload does
// not.
type sourceLine struct {
	Line     int
	Offset   int64
	Raw      []byte
	Payload  []byte
	Complete bool
	InputErr error
}

// sourcePathPredicate is deliberately stricter than excludedSourceName. A
// source adapter must identify the exact transcript namespace it understands;
// the generic denylist is only a second line of defense.
type sourcePathPredicate func(string) bool

// scanTranscriptTree walks only descriptor-anchored directories below start.
// Every regular file in that tree must match allowFile. A source may provide
// an explicit ignore predicate for known, non-transcript companion files; all
// other unrecognized files are rejected instead of being silently skipped.
// Files outside start are not inspected because source roots also contain
// unrelated host state.
func scanTranscriptTree(ctx context.Context, root, start string, allowDir, allowFile sourcePathPredicate, ignoreFiles ...sourcePathPredicate) (artifacts []Artifact, returnErr error) {
	return scanTranscriptTreeWithPolicy(ctx, root, start, allowDir, allowFile, nil, ignoreFiles...)
}

func scanTranscriptTreeWithPolicy(ctx context.Context, root, start string, allowDir, allowFile, ignoreDirectory sourcePathPredicate, ignoreFiles ...sourcePathPredicate) (artifacts []Artifact, returnErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("source discovery requires a context")
	}
	rootDir, canonicalRoot, err := openSecureDirectory(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := closeSourceFile(rootDir); closeErr != nil {
			artifacts = nil
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()

	startDir, err := openSecureRelative(rootDir, start, true)
	if err != nil {
		if errors.Is(err, errSecurePathSymlink) || errors.Is(err, os.ErrNotExist) {
			return nil, corpusError(CodePathEscape, "approved transcript directory is unavailable", err)
		}
		return nil, fmt.Errorf("open approved transcript directory: %w", err)
	}
	defer func() {
		if closeErr := closeSourceFile(startDir); closeErr != nil {
			artifacts = nil
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()

	var relativePaths []string
	var walk func(*os.File, string) error
	walk = func(directory *os.File, relative string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		entries, err := directory.ReadDir(0)
		if err != nil {
			return fmt.Errorf("scan source directory %q: %w", relative, err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			child := filepath.Join(relative, entry.Name())
			entryType := entry.Type()
			if entryType&os.ModeSymlink != 0 {
				return corpusError(CodePathEscape, "approved transcript path contains a symlink", nil)
			}
			if entry.IsDir() {
				if ignoreDirectory != nil && ignoreDirectory(child) {
					continue
				}
				if !allowDir(child) {
					return corpusError(CodeSecretInCorpus, "directory is not an approved transcript namespace", nil)
				}
				nested, err := openSecureRelative(rootDir, child, true)
				if err != nil {
					if errors.Is(err, errSecurePathSymlink) {
						return corpusError(CodePathEscape, "approved transcript path contains a symlink", err)
					}
					return fmt.Errorf("open source directory %q: %w", child, err)
				}
				walkErr := walk(nested, child)
				closeErr := closeSourceFile(nested)
				if walkErr != nil || closeErr != nil {
					return errors.Join(walkErr, closeErr)
				}
				continue
			}
			if !allowFile(child) {
				if len(ignoreFiles) > 0 && ignoreFiles[0] != nil && ignoreFiles[0](child) {
					continue
				}
				return corpusError(CodeSecretInCorpus, "file is not an approved transcript path", nil)
			}
			relativePaths = append(relativePaths, child)
		}
		return nil
	}
	if err := walk(startDir, start); err != nil {
		return nil, err
	}

	artifacts = make([]Artifact, 0, len(relativePaths))
	sort.Strings(relativePaths)
	for _, relative := range relativePaths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		artifact, err := discoverSourceArtifact(canonicalRoot, filepath.Join(canonicalRoot, relative), allowFile)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func discoverSourceArtifact(root, candidate string, allow sourcePathPredicate) (artifact Artifact, returnErr error) {
	rootDir, canonicalRoot, err := openSecureDirectory(root)
	if err != nil {
		return Artifact{}, err
	}
	defer func() {
		if closeErr := closeSourceFile(rootDir); closeErr != nil {
			artifact = Artifact{}
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()

	path, relative, err := candidateWithinRoot(canonicalRoot, candidate)
	if err != nil {
		return Artifact{}, err
	}
	if allow == nil || !allow(relative) {
		return Artifact{}, corpusError(CodeSecretInCorpus, "source path is not an approved transcript", nil)
	}
	file, err := openSecureRelative(rootDir, relative, false)
	if err != nil {
		if errors.Is(err, errSecurePathSymlink) || errors.Is(err, os.ErrNotExist) {
			return Artifact{}, corpusError(CodePathEscape, "source path was replaced or contains a symlink", err)
		}
		return Artifact{}, fmt.Errorf("open source artifact: %w", err)
	}
	snapshot, snapshotErr := snapshotOpenedFile(file, canonicalRoot, path, relative)
	closeErr := closeSourceFile(file)
	if snapshotErr != nil || closeErr != nil {
		return Artifact{}, errors.Join(snapshotErr, closeErr)
	}
	return artifactFromSnapshot(snapshot), nil
}

func openSourceArtifactForRead(root string, expected artifactSnapshot, allow sourcePathPredicate) (file *os.File, snapshot artifactSnapshot, returnErr error) {
	rootDir, canonicalRoot, err := openSecureDirectory(root)
	if err != nil {
		return nil, artifactSnapshot{}, err
	}
	defer func() {
		if closeErr := closeSourceFile(rootDir); closeErr != nil {
			if file != nil {
				fileCloseErr := closeSourceFile(file)
				file = nil
				snapshot = artifactSnapshot{}
				returnErr = errors.Join(returnErr, fileCloseErr)
			}
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()
	if expected.root != "" && expected.root != canonicalRoot {
		return nil, artifactSnapshot{}, corpusError(CodeSourceChanged, "source root does not match artifact snapshot", nil)
	}
	path, relative, err := candidateWithinRoot(canonicalRoot, expected.path)
	if err != nil || relative != expected.relative || allow == nil || !allow(relative) {
		return nil, artifactSnapshot{}, corpusError(CodeSourceChanged, "source path no longer matches artifact snapshot", err)
	}
	file, err = openSecureRelative(rootDir, relative, false)
	if err != nil {
		if errors.Is(err, errSecurePathSymlink) {
			return nil, artifactSnapshot{}, corpusError(CodeSourceChanged, "source path was replaced", err)
		}
		return nil, artifactSnapshot{}, corpusError(CodeSourceChanged, "source artifact is unavailable", err)
	}
	snapshot, err = snapshotOpenedFile(file, canonicalRoot, path, relative)
	if err != nil {
		return nil, artifactSnapshot{}, errors.Join(err, closeSourceFile(file))
	}
	if !sameSnapshotValues(expected, snapshot) {
		return nil, artifactSnapshot{}, errors.Join(
			corpusError(CodeSourceChanged, "source does not match discovered artifact", nil),
			closeSourceFile(file),
		)
	}
	return file, snapshot, nil
}

func verifySourceArtifactPath(root string, expected artifactSnapshot, allow sourcePathPredicate) error {
	file, _, err := openSourceArtifactForRead(root, expected, allow)
	if err != nil {
		return err
	}
	return closeSourceFile(file)
}

func readSourceJSONL(ctx context.Context, artifact Artifact, writer *Writer, allow sourcePathPredicate, handle func(sourceLine) error) (returnErr error) {
	if writer == nil {
		return fmt.Errorf("nil corpus writer")
	}
	if ctx == nil {
		err := fmt.Errorf("source importer requires a context")
		writer.markFailure(err)
		return err
	}
	if handle == nil {
		err := fmt.Errorf("source importer requires a line handler")
		writer.markFailure(err)
		return err
	}
	expected, err := artifactSnapshotFor(artifact)
	if err != nil {
		writer.markFailure(err)
		return err
	}
	file, before, err := openSourceArtifactForRead(expected.root, expected, allow)
	if err != nil {
		writer.markFailure(err)
		return err
	}
	closeOnReturn := true
	defer func() {
		if closeOnReturn {
			if closeErr := closeSourceFile(file); closeErr != nil {
				writer.markFailure(closeErr)
				returnErr = errors.Join(returnErr, closeErr)
			}
		}
	}()
	if err := checkSourceOutputCollision(writer, before, file); err != nil {
		writer.markFailure(err)
		return err
	}

	reader := bufio.NewReader(file)
	options := writer.options
	var firstErr error
	var offset int64
	lineNumber := 1
	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			writer.markFailure(err)
			firstErr = errors.Join(firstErr, err)
			goto verify
		default:
		}

		raw, complete, inputErr := readBoundedLine(reader, options.MaxQuarantineBytes)
		if errors.Is(inputErr, errQuarantineBudgetExceeded) {
			err := corpusError(CodeInputJSON, "quarantine byte budget exceeded", inputErr)
			writer.markFailure(err)
			firstErr = errors.Join(firstErr, err)
			break
		}
		if len(raw) == 0 && errors.Is(inputErr, io.EOF) {
			break
		}
		if len(raw) == 0 && inputErr != nil && !errors.Is(inputErr, io.EOF) {
			writer.markFailure(inputErr)
			firstErr = errors.Join(firstErr, inputErr)
			break
		}
		lineOffset := offset
		offset += int64(len(raw))
		payload := raw
		if complete {
			payload = bytes.TrimSuffix(payload, []byte{'\n'})
			payload = bytes.TrimSuffix(payload, []byte{'\r'})
		}
		line := sourceLine{
			Line:     lineNumber,
			Offset:   lineOffset,
			Raw:      raw,
			Payload:  payload,
			Complete: complete,
			InputErr: inputErr,
		}
		var lineErr error
		switch {
		case len(payload) > options.MaxLineBytes:
			cause := corpusError(CodeInputJSON, "JSONL record exceeds maximum line size", nil)
			lineErr = quarantineSourceLine(writer, line, CodeInputJSON, cause.Error(), cause)
		case !utf8.Valid(payload):
			cause := corpusError(CodeInputJSON, "JSONL record is not valid UTF-8", nil)
			lineErr = quarantineSourceLine(writer, line, CodeInputJSON, cause.Error(), cause)
		default:
			lineErr = handle(line)
		}
		if lineErr != nil {
			if firstErr == nil {
				firstErr = lineErr
			} else {
				firstErr = errors.Join(firstErr, lineErr)
			}
			if errors.Is(lineErr, errQuarantineBudgetExceeded) {
				break
			}
		}
		if inputErr != nil && !errors.Is(inputErr, io.EOF) {
			writer.markFailure(inputErr)
			firstErr = errors.Join(firstErr, inputErr)
			break
		}
		if !complete {
			break
		}
		lineNumber++
	}

verify:
	after, snapshotErr := snapshotOpenedFile(file, before.root, before.path, before.relative)
	if snapshotErr == nil && !sameSnapshotValues(before, after) {
		snapshotErr = corpusError(CodeSourceChanged, "source changed during read", nil)
	}
	pathErr := verifySourceArtifactPath(before.root, before, allow)
	closeErr := closeSourceFile(file)
	closeOnReturn = false
	if snapshotErr != nil {
		writer.markFailure(snapshotErr)
	}
	if pathErr != nil {
		writer.markFailure(pathErr)
	}
	if closeErr != nil {
		writer.markFailure(closeErr)
	}
	return errors.Join(snapshotErr, pathErr, closeErr, firstErr)
}

func checkSourceOutputCollision(writer *Writer, snapshot artifactSnapshot, file *os.File) error {
	if file == nil {
		return corpusError(CodeSourceChanged, "source descriptor is unavailable", nil)
	}
	info, err := file.Stat()
	if err != nil {
		return corpusError(CodeSourceChanged, "stat source descriptor", err)
	}
	return writer.checkSourceCollision(snapshot.root, snapshot.path, info)
}

func quarantineSourceLine(writer *Writer, line sourceLine, code, message string, cause error) error {
	if code == "" || !knownCorpusCode(code) {
		code = CodeInputJSON
	}
	quarantineErr := writer.Quarantine(Quarantine{
		Line:    line.Line,
		Offset:  line.Offset,
		Code:    code,
		Message: message,
		Raw:     line.Raw,
	})
	if quarantineErr != nil {
		return errors.Join(cause, quarantineErr)
	}
	return cause
}

func writeImportedRecord(writer *Writer, line sourceLine, record replay.Record) error {
	redacted, err := redactImportedRecord(record)
	if err != nil {
		code := CodeOf(err)
		if code == "" {
			code = CodeInputJSON
		}
		if code == CodeSecretInCorpus {
			return tolerateSourceRecord(writer, line, record, err)
		}
		return quarantineSourceLine(writer, line, code, err.Error(), err)
	}
	if err := writer.Write(redacted); err != nil {
		code := CodeOf(err)
		if code == "" {
			code = CodeInputJSON
		}
		return quarantineSourceLine(writer, line, code, err.Error(), err)
	}
	return nil
}

// tolerateSourceRecord journals one record whose credential-like value
// survived redaction. The record is never published; raw journal bytes stay in
// memory only, matching the documented quarantine retention policy. For
// line-based sources the rejected source line is retained verbatim; for
// synthetic source lines the marshaled record is retained instead.
func tolerateSourceRecord(writer *Writer, line sourceLine, record replay.Record, cause error) error {
	raw := append([]byte(nil), line.Raw...)
	if len(raw) == 0 {
		encoded, err := json.Marshal(record)
		if err != nil {
			marshalErr := corpusError(CodeSecretInCorpus, "marshal credential-bearing record for quarantine", err)
			writer.markFailure(marshalErr)
			return marshalErr
		}
		raw = encoded
	}
	message := truncateQuarantineMessage(fmt.Sprintf("record turn %q skipped: %v", record.TurnID, cause))
	return writer.Tolerate(Quarantine{
		Line:    line.Line,
		Offset:  line.Offset,
		Code:    CodeSecretInCorpus,
		Message: message,
		Raw:     raw,
	})
}

func truncateQuarantineMessage(message string) string {
	if len(message) <= maxQuarantineMessageBytes {
		return message
	}
	truncated := message[:maxQuarantineMessageBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func redactImportedRecord(record replay.Record) (replay.Record, error) {
	if err := record.Validate(); err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "source record validation failed", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "marshal source record for redaction", err)
	}
	redactedJSON, err := redactImportedJSONValue(encoded, nil)
	if err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "redact source record", err)
	}
	var redacted replay.Record
	if err := json.Unmarshal(redactedJSON, &redacted); err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "decode redacted source record", err)
	}
	redacted, err = RedactRecord(redacted, Options{})
	if err != nil {
		return replay.Record{}, err
	}
	encoded, err = json.Marshal(redacted)
	if err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "marshal redacted source record", err)
	}
	if importedJSONContainsCredentialField(encoded, nil) {
		return replay.Record{}, corpusError(CodeSecretInCorpus, "source credential field remained after redaction", nil)
	}
	return redacted, nil
}

func redactImportedJSONValue(raw []byte, path []string) ([]byte, error) {
	return redactImportedJSONValueAtDepth(raw, path, 0)
}

func redactImportedJSONValueAtDepth(raw []byte, path []string, serializedDepth int) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty JSON value")
	}
	switch trimmed[0] {
	case '{':
		object, err := decodeJSONObjectFields(trimmed)
		if err != nil {
			return nil, err
		}
		redacted := make([]jsonObjectField, 0, len(object))
		for _, field := range object {
			childPath := appendJSONPath(path, field.key)
			if isStructuralHashPath(childPath) {
				if validStructuralHash(field.value) {
					redacted = append(redacted, jsonObjectField{
						key:   field.key,
						value: append(json.RawMessage(nil), field.value...),
					})
				}
				continue
			}
			if importedCredentialField(path, field.key) {
				continue
			}
			value, err := redactImportedJSONValueAtDepth(field.value, childPath, serializedDepth)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.key, err)
			}
			redacted = append(redacted, jsonObjectField{key: field.key, value: value})
		}
		return marshalJSONObjectFields(redacted)
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return nil, err
		}
		for index, value := range values {
			redacted, err := redactImportedJSONValueAtDepth(value, path, serializedDepth)
			if err != nil {
				return nil, fmt.Errorf("array item %d: %w", index, err)
			}
			values[index] = redacted
		}
		return json.Marshal(values)
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, err
		}
		if !isStructuralHashPath(path) {
			value = redactSecretPatterns(value)
			var err error
			value, err = redactSerializedJSON(value, path, serializedDepth, redactImportedJSONValueAtDepth, importedJSONContainsCredentialFieldAtDepth)
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(value)
	default:
		if err := validateSourceJSONDocument(trimmed); err != nil {
			return nil, err
		}
		return append([]byte(nil), trimmed...), nil
	}
}

var importedCredentialMarkers = []string{
	"accesskey", "apikey", "authorization", "authtoken", "bearer", "clientsecret",
	"cookie", "credential", "keymaterial", "oauth", "password", "passwd", "privatekey",
	"secret", "sshkey", "token",
}

var importedCredentialContexts = map[string]bool{
	"api": true, "apis": true, "auth": true, "authentication": true, "authorization": true,
	"credential": true, "credentials": true, "cookie": true, "cookies": true,
	"encryption": true, "header": true, "headers": true, "key": true, "keys": true,
	"master": true, "oauth": true, "provider": true, "providers": true, "secret": true,
	"secrets": true, "session": true, "signing": true, "token": true, "tokens": true,
}

func importedCredentialField(path []string, field string) bool {
	compact := compactFieldName(field)
	if compact == "" {
		return false
	}
	if compact == "contentsha256" && len(path) == 1 && compactFieldName(path[0]) == "source" {
		return false
	}
	if compact == "metadata" || compact == "vendor" || compact == "vendors" {
		return false
	}
	if strings.Contains(compact, "cookie") || strings.Contains(compact, "authorization") || strings.Contains(compact, "bearer") {
		return true
	}
	if hasImportedCredentialMarker(compact) {
		return true
	}
	if compact == "key" || compact == "keys" || compact == "value" {
		for _, component := range path {
			if importedCredentialContexts[compactFieldName(component)] {
				return true
			}
		}
		if compact == "key" || compact == "keys" {
			for _, component := range path {
				if compactFieldName(component) == "metadata" {
					return false
				}
			}
			return true
		}
	}
	return false
}

func hasImportedCredentialMarker(compact string) bool {
	for _, marker := range importedCredentialMarkers {
		if compact == marker || strings.Contains(compact, marker) {
			return true
		}
	}
	if compact == "key" || compact == "keys" || strings.HasPrefix(compact, "key") || strings.HasSuffix(compact, "key") || strings.HasSuffix(compact, "keys") {
		return true
	}
	for _, prefix := range []string{"aws", "gcp", "google", "azure", "openai", "anthropic", "claude", "codex", "opencode", "provider"} {
		if strings.HasPrefix(compact, prefix) && strings.Contains(compact, "key") {
			return true
		}
	}
	for _, prefix := range []string{"encryption", "signing", "master", "session", "header"} {
		if strings.HasPrefix(compact, prefix) && strings.Contains(compact, "key") {
			return true
		}
	}
	return false
}

func importedJSONContainsCredentialField(raw []byte, path []string) bool {
	return importedJSONContainsCredentialFieldAtDepth(raw, path, 0)
}

func importedJSONContainsCredentialFieldAtDepth(raw []byte, path []string, serializedDepth int) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] == '{' {
		object, err := decodeJSONObjectFields(trimmed)
		if err != nil {
			return true
		}
		for _, field := range object {
			childPath := appendJSONPath(path, field.key)
			if isStructuralHashPath(childPath) {
				continue
			}
			if importedCredentialField(path, field.key) {
				return true
			}
			if importedJSONContainsCredentialFieldAtDepth(field.value, childPath, serializedDepth) {
				return true
			}
		}
		return false
	}
	switch trimmed[0] {
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return true
		}
		for _, value := range values {
			if importedJSONContainsCredentialFieldAtDepth(value, path, serializedDepth) {
				return true
			}
		}
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return false
		}
		nested := strings.TrimSpace(value)
		if len(nested) == 0 || (nested[0] != '{' && nested[0] != '[') || !json.Valid([]byte(nested)) {
			return false
		}
		if serializedDepth >= maxSerializedJSONDepth {
			return true
		}
		return importedJSONContainsCredentialFieldAtDepth([]byte(nested), path, serializedDepth+1)
	}
	return false
}

func decodeSourceObject(data []byte) (map[string]json.RawMessage, error) {
	if err := validateSourceJSONDocument(data); err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("JSON value must be an object")
	}
	return object, nil
}

func validateSourceJSONDocument(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateSourceJSONValue(decoder); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err == nil {
		return fmt.Errorf("JSON document contains trailing data")
	} else {
		return err
	}
}

func validateSourceJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object member name must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object member %q", key)
			}
			seen[key] = struct{}{}
			if err := validateSourceJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("JSON object is not terminated")
		}
	case '[':
		for decoder.More() {
			if err := validateSourceJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("JSON array is not terminated")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func sourceString(object map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := object[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sourceObject(object map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	for _, key := range keys {
		raw, ok := object[key]
		if !ok {
			continue
		}
		value, err := decodeSourceObject(raw)
		if err == nil {
			return value
		}
	}
	return nil
}

func sourceTimestamp(object map[string]json.RawMessage, keys ...string) time.Time {
	for _, key := range keys {
		raw, ok := object[key]
		if !ok {
			continue
		}
		if timestamp, ok := parseSourceTimestamp(raw); ok {
			return timestamp
		}
	}
	return time.Time{}
}

func parseSourceTimestamp(raw json.RawMessage) (time.Time, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if timestamp, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return timestamp.UTC(), true
		}
		if timestamp, err := time.Parse("2006-01-02 15:04:05.999999999Z07:00", text); err == nil {
			return timestamp.UTC(), true
		}
		return time.Time{}, false
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return time.Time{}, false
	}
	value, err := number.Int64()
	if err != nil {
		return time.Time{}, false
	}
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value).UTC(), true
	}
	return time.Unix(value, 0).UTC(), true
}

func namespacedID(source, value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return source + ":" + value
}

func importedIdentityHash(parts ...string) string {
	hasher := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write([]byte(part))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func importedContentIdentity(artifact Artifact) string {
	if artifact.snapshot == nil || artifact.snapshot.openCodeCompanions == nil {
		return artifact.ContentSHA256
	}
	parts := []string{"main", artifact.ContentSHA256}
	for _, suffix := range openCodeCompanionSuffixes {
		parts = append(parts, suffix)
		companion, ok := artifact.snapshot.openCodeCompanions[suffix]
		if !ok {
			parts = append(parts, "absent")
			continue
		}
		parts = append(parts, companion.contentSHA256)
	}
	return importedIdentityHash(parts...)
}

func importedRole(value string) (replay.Role, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "system":
		return replay.RoleSystem, true
	case "developer":
		return replay.RoleDeveloper, true
	case "user":
		return replay.RoleUser, true
	case "assistant":
		return replay.RoleAssistant, true
	case "tool":
		return replay.RoleTool, true
	case "function":
		return replay.RoleFunction, true
	default:
		return "", false
	}
}

func newImportedRecord(source string, artifact Artifact, sessionID, turnID string, sequence int, timestamp time.Time, model string, message replay.Message) replay.Record {
	if sessionID == "" {
		sessionID = artifact.RelativePath
	}
	if turnID == "" {
		turnID = fmt.Sprintf("line-%d", sequence+1)
	}
	contentIdentity := importedContentIdentity(artifact)
	scopedSessionID := sessionID + ":artifact:" + importedIdentityHash(sessionID, artifact.RelativePath, contentIdentity)
	namespacedSession := namespacedID(source, scopedSessionID, artifact.RelativePath)
	if message.ID == "" {
		message.ID = turnID
	}
	message.ID = namespacedID(source, importedIdentityHash(scopedSessionID, turnID, message.ID, fmt.Sprintf("%d", sequence)), turnID)
	return replay.Record{
		Schema:    replay.SchemaVersion,
		RecordID:  namespacedID(source, importedIdentityHash(scopedSessionID, turnID, fmt.Sprintf("%d", sequence)), fmt.Sprintf("line-%d", sequence+1)),
		Source:    replay.Source{System: source, RelativeLocator: artifact.RelativePath, ContentSHA256: contentIdentity},
		SessionID: namespacedSession,
		TurnID:    turnID,
		Sequence:  sequence,
		Timestamp: timestamp,
		Model:     model,
		Messages:  []replay.Message{message},
		Replay:    "archive",
	}
}
