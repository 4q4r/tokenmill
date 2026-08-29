//go:build linux

package corpus

import (
	"bufio"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/tokenmill/tokenmill/internal/replay"
)

// ErrAborted is returned for an explicitly aborted writer, including on later
// Close or repeated Abort calls. An unpublished writer must never look like a
// successful commit.
var ErrAborted = errors.New("corpus writer was aborted")

// maxQuarantineMessageBytes is a fixed per-entry metadata ceiling. It is not
// configurable, so callers cannot weaken the independent quarantine bound by
// supplying an unbounded diagnostic message.
const maxQuarantineMessageBytes = 4 << 10

// These seams keep cleanup and no-clobber commit failures observable in tests.
// The default link operation uses the owned temporary descriptor rather than
// resolving a mutable temporary pathname.
var linkTemporary = func(directory, temporary *os.File, outputPath string) error {
	if directory == nil || temporary == nil {
		return fmt.Errorf("temporary and output directory descriptors are required")
	}
	newPath := filepath.Base(outputPath)
	err := unix.Linkat(int(temporary.Fd()), "", int(directory.Fd()), newPath, unix.AT_EMPTY_PATH)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.EPERM) {
		return err
	}

	// AT_EMPTY_PATH can require CAP_DAC_READ_SEARCH. With procfs available,
	// the kernel's stable /proc/self/fd link is the documented descriptor-based
	// alternative and cannot be replaced through the output directory.
	procPath := fmt.Sprintf("/proc/self/fd/%d", temporary.Fd())
	procErr := unix.Linkat(unix.AT_FDCWD, procPath, int(directory.Fd()), newPath, unix.AT_SYMLINK_FOLLOW)
	if procErr != nil {
		return errors.Join(err, procErr)
	}
	return nil
}

var removeTemporary = func(_ *os.File, _ *os.File) error {
	// O_TMPFILE creates no directory entry; closing the final descriptor removes
	// the anonymous inode. Keep this seam so cleanup failures remain testable.
	return nil
}

func unlinkDirectoryEntry(directory *os.File, name string) error {
	if directory == nil {
		return fmt.Errorf("output directory descriptor is required")
	}
	err := unix.Unlinkat(int(directory.Fd()), name, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func unlinkOwnedPublishedOutput(directory *os.File, outputPath string, expected os.FileInfo) error {
	if directory == nil {
		return fmt.Errorf("output directory descriptor is required")
	}
	if expected == nil {
		return fmt.Errorf("published output identity is required")
	}
	tombstoneName, err := cleanupEntryName()
	if err != nil {
		return err
	}
	name := filepath.Base(outputPath)
	if err := moveOutputEntry(directory, name, tombstoneName, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	original, err := openCleanupEntry(directory, tombstoneName)
	if err != nil {
		restoreErr := restoreMovedOutput(directory, name, tombstoneName)
		return errors.Join(err, restoreErr)
	}
	originalInfo, statErr := original.Stat()
	originalCloseErr := closeCleanupEntryFile(original)
	if statErr != nil {
		restoreErr := restoreMovedOutput(directory, name, tombstoneName)
		return errors.Join(statErr, originalCloseErr, restoreErr)
	}
	if !originalInfo.Mode().IsRegular() || !os.SameFile(expected, originalInfo) {
		restoreErr := restoreMovedOutput(directory, name, tombstoneName)
		return errors.Join(originalCloseErr, restoreErr)
	}
	if err := unlinkDirectoryEntry(directory, tombstoneName); err != nil {
		return errors.Join(originalCloseErr, err, restoreMovedOutput(directory, name, tombstoneName))
	}
	return originalCloseErr
}

func cleanupEntryName() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := cryptorand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate cleanup entry name: %w", err)
	}
	return fmt.Sprintf(".tokenmill-cleanup-%d-%s", os.Getpid(), hex.EncodeToString(randomBytes)), nil
}

func openCleanupEntry(directory *os.File, name string) (*os.File, error) {
	if directory == nil {
		return nil, fmt.Errorf("output directory descriptor is required")
	}
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), directory.Name()+" (cleanup inspection)")
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open cleanup inspection handle")
	}
	return file, nil
}

func restoreMovedOutput(directory *os.File, outputName, tombstoneName string) error {
	return moveOutputEntry(directory, tombstoneName, outputName, unix.RENAME_NOREPLACE)
}

func moveOutput(directory *os.File, first, second string, flags uintptr) error {
	if directory == nil {
		return fmt.Errorf("output directory descriptor is required")
	}
	if err := unix.Renameat2(int(directory.Fd()), first, int(directory.Fd()), second, uint(flags)); err != nil {
		return err
	}
	return nil
}

var closeOutputDirectoryFile = func(file *os.File) error {
	return file.Close()
}

var closeRollbackDirectoryFile = func(file *os.File) error {
	return file.Close()
}

var closeCleanupEntryFile = func(file *os.File) error {
	return file.Close()
}

func closeOwnedRollbackDirectory(file *os.File) error {
	if file == nil {
		return nil
	}
	err := closeRollbackDirectoryFile(file)
	if file.Fd() != ^uintptr(0) {
		err = errors.Join(err, file.Close())
	}
	return err
}

var moveOutputEntry = moveOutput

// Writer atomically commits a validated JSONL corpus.
type Writer struct {
	options           Options
	outputPath        string
	outputDir         *os.File
	temporary         *os.File
	buffer            *bufio.Writer
	seen              map[string]struct{}
	quarantined       []Quarantine
	quarantineBytes   int
	quarantineEntries int
	failure           error
	writeFailure      error
	closed            bool
	aborted           bool
	closeErr          error
}

// NewWriter creates a private temporary file in the destination directory.
// The destination is published only by Close after all writes succeed, and a
// no-clobber link prevents a destination created later from being replaced.
func NewWriter(options Options) (*Writer, error) {
	normalized, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if err := validateLocalOutputPath(normalized.OutputPath); err != nil {
		return nil, fmt.Errorf("create corpus writer: %w", err)
	}
	outputPath, err := filepath.Abs(normalized.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}
	outputPath = filepath.Clean(outputPath)
	if excludedSourceName(filepath.Base(outputPath)) {
		return nil, corpusError(CodeSecretInCorpus, "output filename is excluded", nil)
	}
	if strings.TrimSpace(normalized.Root) != "" {
		rootPath, rootErr := filepath.Abs(normalized.Root)
		if rootErr != nil {
			return nil, corpusError(CodePathEscape, "resolve source root for output path", rootErr)
		}
		if pathContained(rootPath, outputPath) {
			return nil, corpusError(CodePathEscape, "corpus output must be outside the source root", nil)
		}
	}
	outputDirectoryPath := filepath.Dir(outputPath)
	outputDirectory, err := openSecurePath(outputDirectoryPath, true)
	if err != nil {
		return nil, fmt.Errorf("open output directory: %w", err)
	}
	temporary, err := openAnonymousTemporary(outputDirectory, outputPath)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create temporary corpus: %w", err), closeSourceFile(outputDirectory))
	}
	normalized.OutputPath = outputPath
	return &Writer{
		options:    normalized,
		outputPath: outputPath,
		outputDir:  outputDirectory,
		temporary:  temporary,
		buffer:     bufio.NewWriter(temporary),
		seen:       make(map[string]struct{}),
	}, nil
}

func openAnonymousTemporary(directory *os.File, displayPath string) (*os.File, error) {
	if directory == nil {
		return nil, fmt.Errorf("output directory descriptor is required")
	}
	fd, err := unix.Openat(
		int(directory.Fd()), ".",
		unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	temporary := os.NewFile(uintptr(fd), displayPath+" (anonymous temporary)")
	if temporary == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("create anonymous temporary file handle")
	}
	if err := temporary.Chmod(0o600); err != nil {
		return nil, errors.Join(err, temporary.Close())
	}
	return temporary, nil
}

func duplicateTemporary(file *os.File) (*os.File, error) {
	if file == nil {
		return nil, fmt.Errorf("temporary descriptor is required")
	}
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	duplicate := os.NewFile(uintptr(fd), file.Name()+" (publication descriptor)")
	if duplicate == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("duplicate temporary file handle")
	}
	return duplicate, nil
}

func duplicateOutputDirectory(file *os.File) (*os.File, error) {
	if file == nil {
		return nil, fmt.Errorf("output directory descriptor is required")
	}
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	duplicate := os.NewFile(uintptr(fd), file.Name()+" (rollback directory descriptor)")
	if duplicate == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("duplicate output directory handle")
	}
	return duplicate, nil
}

var duplicateOutputDirectoryFile = duplicateOutputDirectory

// Write validates, redacts, and appends one record in order.
func (w *Writer) Write(record replay.Record) error {
	if w == nil {
		return fmt.Errorf("nil corpus writer")
	}
	if w.closed {
		return fmt.Errorf("corpus writer is closed")
	}
	if w.writeFailure != nil {
		return w.writeFailure
	}
	if w.buffer == nil || w.temporary == nil || w.seen == nil {
		err := fmt.Errorf("corpus writer is not initialized")
		w.markWriteFailure(err)
		return err
	}
	if err := record.Validate(); err != nil {
		err = corpusError(CodeInputJSON, "record validation failed", err)
		w.markWriteFailure(err)
		return err
	}
	redacted, err := RedactRecord(record, w.options)
	if err != nil {
		w.markWriteFailure(err)
		return err
	}
	if _, exists := w.seen[redacted.RecordID]; exists {
		err := corpusError(CodeDuplicateRecord, "record ID already exists", nil)
		w.markFailure(err)
		return err
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		err = corpusError(CodeInputJSON, "marshal corpus record", err)
		w.markWriteFailure(err)
		return err
	}
	if _, err := w.buffer.Write(encoded); err != nil {
		wrapped := fmt.Errorf("write corpus record: %w", err)
		w.markWriteFailure(wrapped)
		return wrapped
	}
	if err := w.buffer.WriteByte('\n'); err != nil {
		wrapped := fmt.Errorf("write corpus delimiter: %w", err)
		w.markWriteFailure(wrapped)
		return wrapped
	}
	w.seen[redacted.RecordID] = struct{}{}
	return nil
}

// Quarantine records rejected source bytes and prevents a partial corpus from
// being committed. Quarantine does not stop later records from being
// classified; the writer remains non-committable until it is closed.
func (w *Writer) Quarantine(quarantine Quarantine) error {
	if err := w.admitQuarantine(quarantine); err != nil {
		return err
	}
	w.markFailure(corpusError(quarantine.Code, quarantine.Message, nil))
	return nil
}

// Tolerate records a rejected source entry in the quarantine journal without
// failing the corpus. The journal and its budgets are shared with Quarantine:
// exceeding the entry or byte budget, an unknown code, or oversized metadata
// latches the writer failure and prevents publication. Successful Tolerate
// calls keep the corpus publishable so source adapters can skip bounded,
// individually diagnosed malformed entries while publishing the rest. Tolerated
// entries stay observable through Quarantined.
func (w *Writer) Tolerate(quarantine Quarantine) error {
	return w.admitQuarantine(quarantine)
}

func (w *Writer) admitQuarantine(quarantine Quarantine) error {
	if w == nil {
		return fmt.Errorf("nil corpus writer")
	}
	if w.closed {
		return fmt.Errorf("corpus writer is closed")
	}
	if w.options.MaxQuarantineBytes <= 0 || w.options.MaxQuarantineEntries <= 0 {
		return fmt.Errorf("corpus writer is not initialized")
	}
	if quarantine.Code == "" {
		quarantine.Code = CodeInputJSON
	}
	if !knownCorpusCode(quarantine.Code) {
		err := corpusError(CodeInputJSON, "unknown quarantine code", nil)
		w.markFailure(err)
		return err
	}
	if len(quarantine.Message) > maxQuarantineMessageBytes {
		err := corpusError(CodeInputJSON, "quarantine metadata exceeds per-entry limit", nil)
		w.markFailure(err)
		return err
	}
	if w.quarantineEntries >= w.options.MaxQuarantineEntries {
		err := corpusError(CodeInputJSON, "quarantine entry limit exceeded", errQuarantineBudgetExceeded)
		w.markFailure(err)
		return err
	}
	payloadBytes := quarantinePayloadBytes(quarantine.Raw)
	if payloadBytes > w.options.MaxQuarantineBytes-w.quarantineBytes {
		err := corpusError(CodeInputJSON, "quarantine byte budget exceeded", errQuarantineBudgetExceeded)
		w.markFailure(err)
		return err
	}
	quarantine.Raw = append([]byte(nil), quarantine.Raw...)
	w.quarantined = append(w.quarantined, quarantine)
	w.quarantineBytes += payloadBytes
	w.quarantineEntries++
	return nil
}

// Quarantined returns a defensive copy of all rejected source lines.
func (w *Writer) Quarantined() []Quarantine {
	if w == nil || len(w.quarantined) == 0 {
		return nil
	}
	result := make([]Quarantine, len(w.quarantined))
	for i, quarantine := range w.quarantined {
		result[i] = quarantine
		result[i].Raw = append([]byte(nil), quarantine.Raw...)
	}
	return result
}

func knownCorpusCode(code string) bool {
	switch code {
	case CodeInputJSON, CodeSourceChanged, CodePathEscape, CodeSecretInCorpus, CodeDuplicateRecord:
		return true
	default:
		return false
	}
}

func (w *Writer) markFailure(err error) {
	if w == nil || err == nil {
		return
	}
	if w.failure == nil || (CodeOf(err) == CodeSourceChanged && CodeOf(w.failure) != CodeSourceChanged) {
		w.failure = err
	}
}

func (w *Writer) markWriteFailure(err error) {
	if w == nil || err == nil {
		return
	}
	w.markFailure(err)
	if w.writeFailure == nil {
		w.writeFailure = err
	}
}

// checkSourceCollision rejects output paths inside the source root and exact
// inode/path collisions before any source bytes are read.
func (w *Writer) checkSourceCollision(root, sourcePath string, sourceInfo os.FileInfo) error {
	if w == nil {
		return fmt.Errorf("nil corpus writer")
	}
	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return corpusError(CodePathEscape, "resolve source root for output collision check", err)
	}
	canonicalSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return corpusError(CodePathEscape, "resolve source path for output collision check", err)
	}
	if pathContained(canonicalRoot, w.outputPath) || filepath.Clean(canonicalSource) == w.outputPath {
		return corpusError(CodePathEscape, "corpus output must not replace a source artifact", nil)
	}
	outputInfo, err := os.Stat(w.outputPath)
	if err == nil {
		if sourceInfo != nil && os.SameFile(sourceInfo, outputInfo) {
			return corpusError(CodePathEscape, "corpus output aliases a source artifact", nil)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return corpusError(CodePathEscape, "cannot inspect corpus output path", err)
	}
	return nil
}

func (w *Writer) verifyOutputDirectoryIdentity() error {
	if w == nil || w.outputDir == nil {
		return corpusError(CodePathEscape, "output directory descriptor is unavailable", nil)
	}
	heldInfo, err := w.outputDir.Stat()
	if err != nil {
		return corpusError(CodePathEscape, "stat held output directory", err)
	}
	if !heldInfo.IsDir() {
		return corpusError(CodePathEscape, "held output descriptor is not a directory", nil)
	}
	requestedDir, err := openSecurePath(filepath.Dir(w.outputPath), true)
	if err != nil {
		return corpusError(CodePathEscape, "requested output directory is unavailable", err)
	}
	requestedInfo, statErr := requestedDir.Stat()
	closeErr := closeSourceFile(requestedDir)
	if statErr != nil {
		return errors.Join(corpusError(CodePathEscape, "stat requested output directory", statErr), closeErr)
	}
	if closeErr != nil {
		return corpusError(CodePathEscape, "close requested output directory identity probe", closeErr)
	}
	if !os.SameFile(heldInfo, requestedInfo) {
		return corpusError(CodePathEscape, "output directory identity changed", nil)
	}
	return nil
}

// Abort closes and removes the temporary output without publishing it.
func (w *Writer) Abort() error {
	if w == nil {
		return ErrAborted
	}
	if w.closed {
		return w.closeErr
	}
	w.closed = true
	w.aborted = true
	var abortErr error
	if w.temporary != nil {
		if err := w.temporary.Close(); err != nil {
			abortErr = errors.Join(abortErr, err)
		}
		if err := removeTemporary(w.outputDir, w.temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			abortErr = errors.Join(abortErr, err)
		}
	}
	if w.outputDir != nil {
		if err := w.outputDir.Close(); err != nil {
			abortErr = errors.Join(abortErr, err)
		}
	}
	w.closeErr = errors.Join(ErrAborted, abortErr)
	return w.closeErr
}

// Close flushes, syncs, closes, and atomically installs the temporary file.
// A no-clobber hard link is used instead of overwrite-prone rename semantics.
// Any record, quarantine, source, or I/O error removes the temporary file.
func (w *Writer) Close() error {
	if w == nil {
		return fmt.Errorf("nil corpus writer")
	}
	if w.closed {
		return w.closeErr
	}
	w.closed = true
	if w.aborted {
		w.closeErr = ErrAborted
		return w.closeErr
	}
	var closeErr error
	if w.buffer == nil || w.temporary == nil {
		closeErr = fmt.Errorf("corpus writer is not initialized")
	} else {
		if err := w.buffer.Flush(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
		if closeErr == nil {
			if err := w.temporary.Sync(); err != nil {
				closeErr = errors.Join(closeErr, err)
			}
		}
	}
	if closeErr != nil || w.failure != nil {
		if w.temporary != nil {
			closeErr = errors.Join(closeErr, w.temporary.Close())
		}
		cleanupErr := w.removeTemporary(w.temporary)
		closeErr = errors.Join(w.failure, closeErr, cleanupErr)
		closeErr = errors.Join(closeErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}
	if identityErr := w.verifyOutputDirectoryIdentity(); identityErr != nil {
		closeErr = errors.Join(identityErr, w.temporary.Close(), w.removeTemporary(w.temporary))
		closeErr = errors.Join(closeErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}

	publication, duplicateErr := duplicateTemporary(w.temporary)
	if duplicateErr != nil {
		closeErr = errors.Join(closeErr, duplicateErr, w.temporary.Close(), w.removeTemporary(w.temporary))
		closeErr = errors.Join(closeErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}
	publicationInfo, statErr := publication.Stat()
	if statErr != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("stat publication descriptor: %w", statErr), publication.Close(), w.removeTemporary(publication), w.temporary.Close(), w.removeTemporary(w.temporary))
		closeErr = errors.Join(closeErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}
	if err := w.temporary.Close(); err != nil {
		closeErr = errors.Join(err, publication.Close(), w.removeTemporary(publication))
		closeErr = errors.Join(closeErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}

	linkErr := linkTemporary(w.outputDir, publication, w.outputPath)
	if linkErr != nil {
		cleanupErr := errors.Join(unlinkOwnedPublishedOutput(w.outputDir, w.outputPath, publicationInfo), publication.Close(), w.removeTemporary(publication))
		closeErr = errors.Join(fmt.Errorf("commit corpus atomically: %w", linkErr), cleanupErr)
		closeErr = errors.Join(closeErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}
	cleanupErr := errors.Join(publication.Close(), w.removeTemporary(publication))
	if w.outputDir != nil {
		if err := w.outputDir.Sync(); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if cleanupErr != nil {
		cleanupErr = errors.Join(cleanupErr, unlinkOwnedPublishedOutput(w.outputDir, w.outputPath, publicationInfo))
		closeErr = errors.Join(cleanupErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}
	if identityErr := w.verifyOutputDirectoryIdentity(); identityErr != nil {
		cleanupErr := errors.Join(unlinkOwnedPublishedOutput(w.outputDir, w.outputPath, publicationInfo), w.outputDir.Sync())
		closeErr = errors.Join(identityErr, cleanupErr, w.closeOutputDirectory())
		w.closeErr = closeErr
		return w.closeErr
	}
	closeErr = errors.Join(cleanupErr, w.closePublishedOutputDirectory(publicationInfo))
	w.closeErr = closeErr
	return w.closeErr
}

func (w *Writer) removeTemporary(temporary *os.File) error {
	if w == nil || temporary == nil {
		return nil
	}
	err := removeTemporary(w.outputDir, temporary)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (w *Writer) closeOutputDirectory() error {
	if w == nil || w.outputDir == nil {
		return nil
	}
	err := closeOutputDirectoryFile(w.outputDir)
	w.outputDir = nil
	return err
}

func (w *Writer) closePublishedOutputDirectory(expected os.FileInfo) error {
	if w == nil || w.outputDir == nil {
		return nil
	}
	rollbackDirectory, duplicateErr := duplicateOutputDirectoryFile(w.outputDir)
	if duplicateErr != nil {
		cleanupErr := errors.Join(unlinkOwnedPublishedOutput(w.outputDir, w.outputPath, expected), w.closeOutputDirectory())
		return errors.Join(duplicateErr, cleanupErr)
	}
	closeErr := w.closeOutputDirectory()
	if closeErr == nil {
		cleanupDirectory, cleanupDuplicateErr := duplicateOutputDirectoryFile(rollbackDirectory)
		if cleanupDuplicateErr != nil {
			cleanupErr := errors.Join(
				unlinkOwnedPublishedOutput(rollbackDirectory, w.outputPath, expected),
				rollbackDirectory.Sync(),
				closeOwnedRollbackDirectory(rollbackDirectory),
			)
			return errors.Join(cleanupDuplicateErr, cleanupErr)
		}
		rollbackCloseErr := closeOwnedRollbackDirectory(rollbackDirectory)
		if rollbackCloseErr == nil {
			finalCleanupDirectory, finalDuplicateErr := duplicateOutputDirectoryFile(cleanupDirectory)
			if finalDuplicateErr != nil {
				cleanupErr := errors.Join(
					unlinkOwnedPublishedOutput(cleanupDirectory, w.outputPath, expected),
					cleanupDirectory.Sync(),
					closeOwnedRollbackDirectory(cleanupDirectory),
				)
				return errors.Join(finalDuplicateErr, cleanupErr)
			}
			cleanupCloseErr := closeOwnedRollbackDirectory(cleanupDirectory)
			if cleanupCloseErr == nil {
				lastCleanupDirectory, lastDuplicateErr := duplicateOutputDirectoryFile(finalCleanupDirectory)
				if lastDuplicateErr != nil {
					cleanupErr := errors.Join(
						unlinkOwnedPublishedOutput(finalCleanupDirectory, w.outputPath, expected),
						finalCleanupDirectory.Sync(),
						closeOwnedRollbackDirectory(finalCleanupDirectory),
					)
					return errors.Join(lastDuplicateErr, cleanupErr)
				}
				finalCloseErr := closeOwnedRollbackDirectory(finalCleanupDirectory)
				if finalCloseErr == nil {
					lastFallbackDirectory, fallbackDuplicateErr := duplicateOutputDirectoryFile(lastCleanupDirectory)
					if fallbackDuplicateErr != nil {
						cleanupErr := errors.Join(
							unlinkOwnedPublishedOutput(lastCleanupDirectory, w.outputPath, expected),
							lastCleanupDirectory.Sync(),
							closeOwnedRollbackDirectory(lastCleanupDirectory),
						)
						return errors.Join(fallbackDuplicateErr, cleanupErr)
					}
					finalFallbackDirectory, finalFallbackDuplicateErr := duplicateOutputDirectoryFile(lastFallbackDirectory)
					if finalFallbackDuplicateErr != nil {
						cleanupErr := errors.Join(
							unlinkOwnedPublishedOutput(lastFallbackDirectory, w.outputPath, expected),
							lastFallbackDirectory.Sync(),
							closeOwnedRollbackDirectory(lastFallbackDirectory),
							closeOwnedRollbackDirectory(lastCleanupDirectory),
						)
						return errors.Join(finalFallbackDuplicateErr, cleanupErr)
					}
					lastCloseErr := closeOwnedRollbackDirectory(lastCleanupDirectory)
					if lastCloseErr == nil {
						fallbackCloseErr := closeOwnedRollbackDirectory(lastFallbackDirectory)
						if fallbackCloseErr == nil {
							emergencyDirectory, emergencyDuplicateErr := duplicateOutputDirectoryFile(finalFallbackDirectory)
							if emergencyDuplicateErr != nil {
								cleanupErr := errors.Join(
									unlinkOwnedPublishedOutput(finalFallbackDirectory, w.outputPath, expected),
									finalFallbackDirectory.Sync(),
									closeOwnedRollbackDirectory(finalFallbackDirectory),
								)
								return errors.Join(emergencyDuplicateErr, cleanupErr)
							}
							finalFallbackCloseErr := closeOwnedRollbackDirectory(finalFallbackDirectory)
							if finalFallbackCloseErr == nil {
								emergencyCloseErr := closeOwnedRollbackDirectory(emergencyDirectory)
								if emergencyCloseErr == nil {
									return nil
								}
								// The emergency descriptor is the last safe namespace handle. Do
								// not reopen a mutable pathname after it has failed to close.
								return emergencyCloseErr
							}
							cleanupErr := errors.Join(
								unlinkOwnedPublishedOutput(emergencyDirectory, w.outputPath, expected),
								emergencyDirectory.Sync(),
								closeOwnedRollbackDirectory(emergencyDirectory),
							)
							return errors.Join(finalFallbackCloseErr, cleanupErr)
						}
						cleanupErr := errors.Join(
							unlinkOwnedPublishedOutput(finalFallbackDirectory, w.outputPath, expected),
							finalFallbackDirectory.Sync(),
							closeOwnedRollbackDirectory(finalFallbackDirectory),
						)
						return errors.Join(fallbackCloseErr, cleanupErr)
					}
					cleanupErr := errors.Join(
						unlinkOwnedPublishedOutput(finalFallbackDirectory, w.outputPath, expected),
						finalFallbackDirectory.Sync(),
						closeOwnedRollbackDirectory(finalFallbackDirectory),
						closeOwnedRollbackDirectory(lastFallbackDirectory),
					)
					return errors.Join(lastCloseErr, cleanupErr)
				}
				cleanupErr := errors.Join(
					unlinkOwnedPublishedOutput(lastCleanupDirectory, w.outputPath, expected),
					lastCleanupDirectory.Sync(),
					closeOwnedRollbackDirectory(lastCleanupDirectory),
				)
				return errors.Join(finalCloseErr, cleanupErr)
			}
			cleanupErr := errors.Join(
				unlinkOwnedPublishedOutput(finalCleanupDirectory, w.outputPath, expected),
				finalCleanupDirectory.Sync(),
				closeOwnedRollbackDirectory(finalCleanupDirectory),
			)
			return errors.Join(cleanupCloseErr, cleanupErr)
		}
		cleanupErr := errors.Join(
			unlinkOwnedPublishedOutput(cleanupDirectory, w.outputPath, expected),
			cleanupDirectory.Sync(),
			closeOwnedRollbackDirectory(cleanupDirectory),
		)
		return errors.Join(rollbackCloseErr, cleanupErr)
	}
	cleanupErr := errors.Join(
		unlinkOwnedPublishedOutput(rollbackDirectory, w.outputPath, expected),
		rollbackDirectory.Sync(),
		closeOwnedRollbackDirectory(rollbackDirectory),
	)
	return errors.Join(closeErr, cleanupErr)
}

// OutputPath returns the canonical destination path.
func (w *Writer) OutputPath() string {
	if w == nil {
		return ""
	}
	return w.outputPath
}
