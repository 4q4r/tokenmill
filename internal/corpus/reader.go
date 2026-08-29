//go:build linux

package corpus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/tokenmill/tokenmill/internal/replay"
)

// Quarantine records source bytes that were not emitted as successful corpus
// records. Raw includes the newline when the source line had one.
type Quarantine struct {
	Line    int
	Offset  int64
	Code    string
	Message string
	Raw     []byte
}

// ReadResult reports accepted records and every quarantined input line.
type ReadResult struct {
	Accepted    int
	Quarantined []Quarantine
}

// Reader validates and imports generic JSONL artifacts without knowing a
// source-specific format.
type Reader struct {
	options Options
}

// closeReadFile is a narrow seam for source-close error tests. The normal
// implementation closes the operation-scoped descriptor directly.
var closeReadFile = func(file *os.File) error {
	return file.Close()
}

var errQuarantineBudgetExceeded = errors.New("quarantine byte budget exceeded")

// NewReader creates a reader. Option errors are returned by Read or ReadJSONL
// so construction remains side-effect free.
func NewReader(options Options) *Reader {
	return &Reader{options: options}
}

// Read opens an approved artifact through a secure descriptor, verifies its
// snapshot before and after the read, and writes only valid records to writer.
func (r *Reader) Read(ctx context.Context, artifact Artifact, writer *Writer) (result ReadResult, returnErr error) {
	if writer == nil {
		return ReadResult{}, fmt.Errorf("corpus reader requires a writer")
	}
	if ctx == nil {
		err := fmt.Errorf("corpus reader requires a context")
		writer.markFailure(err)
		return ReadResult{}, err
	}
	options, err := r.options.normalized()
	if err != nil {
		writer.markFailure(err)
		return ReadResult{}, err
	}
	expected, err := artifactSnapshotFor(artifact)
	if err != nil {
		writer.markFailure(err)
		return ReadResult{}, err
	}

	file, before, err := openArtifactForRead(options.Root, expected)
	if err != nil {
		writer.markFailure(err)
		return ReadResult{}, err
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

	info, err := file.Stat()
	if err != nil {
		err = corpusError(CodeSourceChanged, "stat source descriptor", err)
		writer.markFailure(err)
		return ReadResult{}, err
	}
	if err := writer.checkSourceCollision(options.Root, before.path, info); err != nil {
		writer.markFailure(err)
		return ReadResult{}, err
	}

	result, readErr := r.readJSONL(ctx, file, writer, options)
	after, snapshotErr := snapshotOpenedFile(file, before.root, before.path, before.relative)
	if snapshotErr == nil && !sameSnapshotValues(before, after) {
		snapshotErr = corpusError(CodeSourceChanged, "source changed during read", nil)
	}
	pathErr := verifyArtifactPath(options.Root, before)
	closeErr := closeSourceFile(file)
	closeOnReturn = false

	if readErr != nil {
		writer.markFailure(readErr)
		returnErr = errors.Join(returnErr, readErr)
	}
	if snapshotErr != nil {
		writer.markFailure(snapshotErr)
		returnErr = errors.Join(returnErr, snapshotErr)
	}
	if pathErr != nil {
		writer.markFailure(pathErr)
		returnErr = errors.Join(returnErr, pathErr)
	}
	if closeErr != nil {
		writer.markFailure(closeErr)
		returnErr = errors.Join(returnErr, closeErr)
	}
	if returnErr != nil {
		return result, returnErr
	}
	return result, nil
}

// ReadJSONL imports an already-open JSONL stream. It accepts only complete
// newline-terminated records and quarantines every rejected line.
func (r *Reader) ReadJSONL(ctx context.Context, input io.Reader, writer *Writer) (ReadResult, error) {
	if writer == nil {
		return ReadResult{}, fmt.Errorf("corpus reader requires a writer")
	}
	options, err := r.options.normalized()
	if err != nil {
		writer.markFailure(err)
		return ReadResult{}, err
	}
	return r.readJSONL(ctx, input, writer, options)
}

func (r *Reader) readJSONL(ctx context.Context, input io.Reader, writer *Writer, options Options) (ReadResult, error) {
	if input == nil {
		err := fmt.Errorf("corpus reader requires input")
		writer.markFailure(err)
		return ReadResult{}, err
	}
	if ctx == nil {
		err := fmt.Errorf("corpus reader requires a context")
		writer.markFailure(err)
		return ReadResult{}, err
	}
	reader := bufio.NewReader(input)
	var result ReadResult
	var firstErr error
	var offset int64
	lineNumber := 1
	remainingQuarantine := options.MaxQuarantineBytes - writer.quarantineBytes
	if remainingQuarantine < 0 {
		remainingQuarantine = 0
	}
	remainingQuarantineEntries := options.MaxQuarantineEntries - writer.quarantineEntries
	if remainingQuarantineEntries < 0 {
		remainingQuarantineEntries = 0
	}

	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			writer.markFailure(err)
			return result, errors.Join(firstErr, err)
		default:
		}

		raw, complete, inputErr := readBoundedLine(reader, options.MaxQuarantineBytes)
		if errors.Is(inputErr, errQuarantineBudgetExceeded) {
			err := corpusError(CodeInputJSON, "quarantine byte budget exceeded", inputErr)
			writer.markFailure(err)
			return result, errors.Join(firstErr, err)
		}
		if len(raw) == 0 && errors.Is(inputErr, io.EOF) {
			break
		}
		if len(raw) == 0 && inputErr != nil && !errors.Is(inputErr, io.EOF) {
			writer.markFailure(inputErr)
			return result, errors.Join(firstErr, inputErr)
		}
		lineOffset := offset
		offset += int64(len(raw))

		payload := raw
		if complete {
			payload = bytes.TrimSuffix(payload, []byte{'\n'})
			payload = bytes.TrimSuffix(payload, []byte{'\r'})
		}
		lineSize := len(payload)
		lineErr := error(nil)
		switch {
		case lineSize > options.MaxLineBytes:
			lineErr = corpusError(CodeInputJSON, "JSONL record exceeds maximum line size", nil)
		case !complete:
			lineErr = corpusError(CodeInputJSON, "final JSONL record is not newline terminated", nil)
		case !utf8.Valid(payload):
			lineErr = corpusError(CodeInputJSON, "JSONL record is not valid UTF-8", nil)
		default:
			var record replay.Record
			if decodeErr := json.Unmarshal(payload, &record); decodeErr != nil {
				lineErr = corpusError(CodeInputJSON, "malformed JSONL record", decodeErr)
			} else if validateErr := record.Validate(); validateErr != nil {
				lineErr = corpusError(CodeInputJSON, "record validation failed", validateErr)
			} else if writeErr := writer.Write(record); writeErr != nil {
				code := CodeOf(writeErr)
				if code == "" {
					code = CodeInputJSON
				}
				if !knownCorpusCode(code) {
					code = CodeInputJSON
				}
				lineErr = corpusError(code, writeErr.Error(), writeErr)
			}
		}
		if lineErr != nil {
			var quarantineErr error
			firstErr, quarantineErr = quarantineLine(&result, writer, lineNumber, lineOffset, CodeOf(lineErr), lineErr, raw, firstErr, &remainingQuarantine, &remainingQuarantineEntries)
			if quarantineErr != nil {
				return result, errors.Join(firstErr, quarantineErr)
			}
		} else {
			result.Accepted++
		}

		if inputErr != nil && !errors.Is(inputErr, io.EOF) {
			writer.markFailure(inputErr)
			if firstErr == nil {
				return result, inputErr
			}
			return result, errors.Join(firstErr, inputErr)
		}
		if !complete {
			break
		}
		lineNumber++
	}
	return result, firstErr
}

func readBoundedLine(reader *bufio.Reader, maxCapture int) ([]byte, bool, error) {
	captureLimit := captureLimitForPayload(maxCapture)
	var raw []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(raw) > captureLimit || len(fragment) > captureLimit-len(raw) {
			return nil, false, errQuarantineBudgetExceeded
		}
		raw = append(raw, fragment...)
		if err == nil {
			return raw, true, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return raw, bytes.HasSuffix(raw, []byte{'\n'}), err
	}
}

// captureLimitForPayload allows the optional LF or CRLF delimiter in addition
// to the payload budget while keeping an unterminated line bounded.
func captureLimitForPayload(maxPayload int) int {
	const delimiterBytes = 2
	if maxPayload > int(^uint(0)>>1)-delimiterBytes {
		return maxPayload
	}
	return maxPayload + delimiterBytes
}

// quarantinePayloadBytes applies the same payload-only accounting as
// MaxLineBytes. Quarantine.Raw intentionally retains LF or CRLF bytes.
func quarantinePayloadBytes(raw []byte) int {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return len(raw)
	}
	payloadBytes := len(raw) - 1
	if payloadBytes > 0 && raw[payloadBytes-1] == '\r' {
		payloadBytes--
	}
	return payloadBytes
}

func quarantineLine(result *ReadResult, writer *Writer, line int, offset int64, code string, err error, raw []byte, firstErr error, remaining *int, remainingEntries *int) (error, error) {
	if code == "" || !knownCorpusCode(code) {
		code = CodeInputJSON
	}
	if *remainingEntries <= 0 {
		budgetErr := corpusError(CodeInputJSON, "quarantine entry limit exceeded", errQuarantineBudgetExceeded)
		writer.markFailure(budgetErr)
		return firstErr, budgetErr
	}
	if quarantinePayloadBytes(raw) > *remaining {
		budgetErr := corpusError(CodeInputJSON, "quarantine byte budget exceeded", errQuarantineBudgetExceeded)
		writer.markFailure(budgetErr)
		return firstErr, budgetErr
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	quarantine := Quarantine{
		Line:    line,
		Offset:  offset,
		Code:    code,
		Message: message,
		Raw:     raw,
	}
	if quarantineErr := writer.Quarantine(quarantine); quarantineErr != nil {
		return firstErr, quarantineErr
	}
	*remaining -= quarantinePayloadBytes(raw)
	*remainingEntries--
	quarantine.Raw = append([]byte(nil), raw...)
	result.Quarantined = append(result.Quarantined, quarantine)
	if firstErr == nil {
		return err, nil
	}
	return firstErr, nil
}

func openArtifactForRead(root string, expected artifactSnapshot) (file *os.File, snapshot artifactSnapshot, returnErr error) {
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
	if err != nil {
		if expected.root != "" {
			return nil, artifactSnapshot{}, corpusError(CodeSourceChanged, "source path no longer matches artifact snapshot", err)
		}
		return nil, artifactSnapshot{}, err
	}
	if relative != expected.relative || excludedSourceName(relative) {
		return nil, artifactSnapshot{}, corpusError(CodeSourceChanged, "source path no longer matches artifact snapshot", nil)
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
		closeErr := closeSourceFile(file)
		file = nil
		return nil, artifactSnapshot{}, errors.Join(err, closeErr)
	}
	if !sameSnapshotValues(expected, snapshot) {
		closeErr := closeSourceFile(file)
		file = nil
		return nil, artifactSnapshot{}, errors.Join(
			corpusError(CodeSourceChanged, "source does not match discovered artifact", nil),
			closeErr,
		)
	}
	return file, snapshot, nil
}

func verifyArtifactPath(root string, expected artifactSnapshot) error {
	file, _, err := openArtifactForRead(root, expected)
	if err != nil {
		return err
	}
	return closeSourceFile(file)
}

func closeSourceFile(file *os.File) error {
	if file == nil {
		return nil
	}
	err := closeReadFile(file)
	if err != nil {
		// The failed close already invalidated the descriptor; joining a
		// second Close would only append a spurious "file already closed"
		// error. This mirrors the writer-side rollback close guard.
		if file.Fd() != ^uintptr(0) {
			return errors.Join(err, file.Close())
		}
		return err
	}
	return nil
}

func sameSnapshotValues(left, right artifactSnapshot) bool {
	return left.root == right.root &&
		left.path == right.path &&
		left.relative == right.relative &&
		left.size == right.size &&
		left.modTime.Equal(right.modTime) &&
		left.inode == right.inode &&
		left.contentSHA256 != "" &&
		left.contentSHA256 == right.contentSHA256
}
