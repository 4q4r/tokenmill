//go:build linux && (amd64 || arm64)

package corpus

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var openCodeCompanionSuffixes = []string{"-wal", "-shm", "-journal"}

type openCodeStoreSnapshot struct {
	directory  string
	database   string
	companions map[string]artifactSnapshot
}

func snapshotOpenCodeStore(file *os.File, before artifactSnapshot, expectedCompanions map[string]artifactSnapshot) (snapshot openCodeStoreSnapshot, returnErr error) {
	companions, err := snapshotOpenCodeCompanions(before.root, before.relative)
	if err != nil {
		return openCodeStoreSnapshot{}, err
	}
	if expectedCompanions != nil && !sameOpenCodeCompanions(expectedCompanions, companions) {
		return openCodeStoreSnapshot{}, corpusError(CodeSourceChanged, "OpenCode companion changed since discovery", nil)
	}
	temporaryDirectory, err := os.MkdirTemp("", "tokenmill-opencode-snapshot-")
	if err != nil {
		return openCodeStoreSnapshot{}, fmt.Errorf("create OpenCode snapshot directory: %w", err)
	}
	cleanup := true
	defer func() {
		if !cleanup {
			return
		}
		if cleanupErr := os.RemoveAll(temporaryDirectory); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove OpenCode snapshot directory: %w", cleanupErr))
		}
	}()

	databasePath := filepath.Join(temporaryDirectory, filepath.Base(before.path))
	if err := copyOpenCodeDescriptor(file, databasePath); err != nil {
		return openCodeStoreSnapshot{}, err
	}
	for suffix := range companions {
		if err := copyOpenCodeCompanion(before.root, before.relative, suffix, filepath.Join(temporaryDirectory, filepath.Base(before.path)+suffix)); err != nil {
			return openCodeStoreSnapshot{}, err
		}
	}
	if err := verifyOpenCodeStoreSnapshot(file, before, companions); err != nil {
		return openCodeStoreSnapshot{}, err
	}
	cleanup = false
	return openCodeStoreSnapshot{
		directory:  temporaryDirectory,
		database:   databasePath,
		companions: companions,
	}, nil
}

func snapshotOpenCodeCompanions(root, relative string) (companions map[string]artifactSnapshot, returnErr error) {
	rootDirectory, canonicalRoot, err := openSecureDirectory(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := closeSourceFile(rootDirectory); closeErr != nil {
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()

	companions = make(map[string]artifactSnapshot)
	for _, suffix := range openCodeCompanionSuffixes {
		companionRelative := relative + suffix
		file, err := openSecureRelative(rootDirectory, companionRelative, false)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, corpusError(CodeSourceChanged, "OpenCode companion is unavailable", err)
		}
		snapshot, snapshotErr := snapshotOpenedFile(file, canonicalRoot, filepath.Join(canonicalRoot, companionRelative), companionRelative)
		closeErr := closeSourceFile(file)
		if snapshotErr != nil || closeErr != nil {
			return nil, errors.Join(snapshotErr, closeErr)
		}
		companions[suffix] = snapshot
	}
	return companions, nil
}

func copyOpenCodeCompanion(root, relative, suffix, destination string) (returnErr error) {
	rootDirectory, canonicalRoot, err := openSecureDirectory(root)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeSourceFile(rootDirectory); closeErr != nil {
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()
	companionRelative := relative + suffix
	file, err := openSecureRelative(rootDirectory, companionRelative, false)
	if err != nil {
		return corpusError(CodeSourceChanged, "OpenCode companion changed before snapshot", err)
	}
	defer func() {
		if closeErr := closeSourceFile(file); closeErr != nil {
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()
	if canonicalRoot == "" {
		return fmt.Errorf("OpenCode source root is empty")
	}
	return copyOpenCodeDescriptor(file, destination)
}

func copyOpenCodeDescriptor(source *os.File, destination string) (returnErr error) {
	if source == nil {
		return fmt.Errorf("OpenCode source descriptor is required")
	}
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create OpenCode snapshot file: %w", err)
	}
	defer func() {
		if closeErr := target.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind OpenCode source descriptor: %w", err)
	}
	if _, err := io.Copy(target, source); err != nil {
		return fmt.Errorf("copy OpenCode source into snapshot: %w", err)
	}
	if err := target.Sync(); err != nil {
		return fmt.Errorf("sync OpenCode snapshot file: %w", err)
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind OpenCode source descriptor after snapshot: %w", err)
	}
	return nil
}

func verifyOpenCodeStoreSnapshot(file *os.File, before artifactSnapshot, companions map[string]artifactSnapshot) error {
	after, err := snapshotOpenedFile(file, before.root, before.path, before.relative)
	if err != nil {
		return corpusError(CodeSourceChanged, "OpenCode database changed during snapshot", err)
	}
	if !sameSnapshotValues(before, after) {
		return corpusError(CodeSourceChanged, "OpenCode database changed during snapshot", nil)
	}
	afterCompanions, err := snapshotOpenCodeCompanions(before.root, before.relative)
	if err != nil {
		return err
	}
	if !sameOpenCodeCompanions(companions, afterCompanions) {
		return corpusError(CodeSourceChanged, "OpenCode companion changed during snapshot", nil)
	}
	return nil
}

func sameOpenCodeCompanions(left, right map[string]artifactSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for suffix, leftSnapshot := range left {
		rightSnapshot, ok := right[suffix]
		if !ok || !sameSnapshotValues(leftSnapshot, rightSnapshot) {
			return false
		}
	}
	return true
}
