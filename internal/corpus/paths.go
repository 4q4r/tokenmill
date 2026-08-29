//go:build linux && (amd64 || arm64)

package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	// CodeInputJSON identifies malformed, incomplete, oversized, or
	// schema-invalid corpus records.
	CodeInputJSON = "E_INPUT_JSON"
	// CodeSourceChanged identifies a source that changed during snapshot/read.
	CodeSourceChanged = "E_SOURCE_CHANGED"
	// CodePathEscape identifies a path outside the approved source root.
	CodePathEscape = "E_PATH_ESCAPE"
	// CodeSecretInCorpus identifies a forbidden secret-bearing input or unsafe
	// private-mode configuration.
	CodeSecretInCorpus = "E_SECRET_IN_CORPUS"
	// CodeDuplicateRecord identifies a repeated record ID in one corpus.
	CodeDuplicateRecord = "E_DUPLICATE_RECORD"
)

// Error is a stable corpus error. Code is safe to expose to callers and
// remains stable while Message can contain contextual details.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

var (
	ErrInputJSON       = &Error{Code: CodeInputJSON}
	ErrSourceChanged   = &Error{Code: CodeSourceChanged}
	ErrPathEscape      = &Error{Code: CodePathEscape}
	ErrSecretInCorpus  = &Error{Code: CodeSecretInCorpus}
	ErrDuplicateRecord = &Error{Code: CodeDuplicateRecord}
)

// CodeOf returns the first stable corpus code contained in err.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var corpusErr *Error
	if errors.As(err, &corpusErr) {
		return corpusErr.Code
	}
	return ""
}

func corpusError(code, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// PrivacyMode controls how corpus records are handled. Credential-bearing
// fields and credential-like values are removed in both modes; private mode
// only acknowledges that the remaining conversation may be sensitive.
type PrivacyMode string

const (
	PrivacyRedacted PrivacyMode = "redacted"
	PrivacyPrivate  PrivacyMode = "private"

	DefaultMaxLineBytes       = 1 << 20
	DefaultMaxQuarantineBytes = 16 << 20
	// DefaultMaxQuarantineEntries bounds retained rejected-line entries and
	// their per-entry metadata independently of the payload-byte budget.
	DefaultMaxQuarantineEntries = 4096

	// Linux does not expose these constants through the deprecated syscall
	// package. The package is intentionally Linux-specific because its source
	// and output safety contracts depend on descriptor-relative syscalls.
	linuxOPath           = 0x200000
	linuxOTmpfile        = 0x410000
	linuxATEmptyPath     = 0x1000
	linuxATSymlinkFollow = 0x400
	linuxSYSRenameat2    = 316
	linuxRenameNoReplace = 0x1
	linuxRenameExchange  = 0x2
)

// These seams keep raw descriptor cleanup failures deterministic in tests.
// The default operations are the Linux syscalls used by secure traversal.
var (
	openSecureAt  = syscall.Openat
	closeSecureFD = syscall.Close
)

// Options is the single corpus safety configuration shared by readers and
// writers.
type Options struct {
	Root       string
	OutputPath string
	// MaxLineBytes counts JSONL payload bytes and excludes an LF or CRLF
	// delimiter.
	MaxLineBytes int
	// MaxQuarantineBytes counts quarantined payload bytes and excludes an LF or
	// CRLF delimiter. Quarantine.Raw retains the delimiter bytes.
	MaxQuarantineBytes int
	// MaxQuarantineEntries bounds retained quarantine entries and their
	// metadata. Zero selects DefaultMaxQuarantineEntries. A caller may choose a
	// lower positive value, but cannot raise the secure package default.
	MaxQuarantineEntries int
	Privacy              PrivacyMode
	AllowPrivate         bool
}

// Validate checks options without changing them.
func (o Options) Validate() error {
	_, err := o.normalized()
	return err
}

func (o Options) normalized() (Options, error) {
	if strings.IndexByte(o.Root, 0) >= 0 || strings.IndexByte(o.OutputPath, 0) >= 0 {
		return Options{}, fmt.Errorf("path contains NUL byte")
	}
	if o.MaxLineBytes < 0 {
		return Options{}, fmt.Errorf("max line bytes must be positive")
	}
	if o.MaxLineBytes == 0 {
		o.MaxLineBytes = DefaultMaxLineBytes
	}
	if o.MaxQuarantineBytes < 0 {
		return Options{}, fmt.Errorf("max quarantine bytes must be positive")
	}
	if o.MaxQuarantineBytes == 0 {
		o.MaxQuarantineBytes = DefaultMaxQuarantineBytes
		if o.MaxQuarantineBytes < o.MaxLineBytes {
			o.MaxQuarantineBytes = o.MaxLineBytes
		}
	}
	if o.MaxQuarantineBytes < o.MaxLineBytes {
		return Options{}, fmt.Errorf("max quarantine bytes must not be smaller than max line bytes")
	}
	if o.MaxQuarantineEntries < 0 {
		return Options{}, fmt.Errorf("max quarantine entries must be positive")
	}
	if o.MaxQuarantineEntries == 0 {
		o.MaxQuarantineEntries = DefaultMaxQuarantineEntries
	}
	if o.MaxQuarantineEntries > DefaultMaxQuarantineEntries {
		return Options{}, fmt.Errorf("max quarantine entries must not exceed secure default %d", DefaultMaxQuarantineEntries)
	}
	if o.Privacy == "" {
		o.Privacy = PrivacyRedacted
	}
	switch o.Privacy {
	case PrivacyRedacted:
	case PrivacyPrivate:
		if !o.AllowPrivate {
			return Options{}, corpusError(CodeSecretInCorpus, "private mode requires AllowPrivate", nil)
		}
		if err := validateLocalOutputPath(o.OutputPath); err != nil {
			return Options{}, corpusError(CodeSecretInCorpus, "private mode requires a local output path", err)
		}
	default:
		return Options{}, fmt.Errorf("unsupported privacy mode %q", o.Privacy)
	}
	return o, nil
}

func validateLocalOutputPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	if strings.Contains(path, "://") {
		return fmt.Errorf("output path must be local")
	}
	return nil
}

// Artifact is a stable read-only source snapshot. Path is canonicalized to a
// path inside the approved root; RelativePath is suitable for reports.
//
// The unexported snapshot is an integrity token created by DiscoverArtifact.
// Reader refuses public-field mutations instead of treating missing security
// fields as an instruction to skip validation.
type Artifact struct {
	Path          string
	RelativePath  string
	Size          int64
	ModTime       time.Time
	Inode         uint64
	ContentSHA256 string
	Captured      bool

	snapshot *artifactSnapshot
}

type artifactSnapshot struct {
	root               string
	path               string
	relative           string
	size               int64
	modTime            time.Time
	inode              uint64
	contentSHA256      string
	openCodeCompanions map[string]artifactSnapshot
}

// Close is retained as an explicit lifecycle boundary for source adapters.
// Secure descriptors are operation-scoped and are closed by the operation;
// discovered artifacts retain only immutable snapshot metadata.
func (a Artifact) Close() error {
	return nil
}

// Source is the boundary implemented by source-specific adapters in Task 3.
type Source interface {
	ID() string
	Discover(context.Context, Options) ([]Artifact, error)
	Read(context.Context, Artifact, *Writer) error
}

var errSecurePathSymlink = errors.New("source path contains a symlink")

// DiscoverArtifact validates root containment and captures a read-only file
// snapshot. It never follows a symlink and never accepts known
// authentication/state filenames.
func DiscoverArtifact(root, candidate string) (artifact Artifact, returnErr error) {
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
	if excludedSourceName(relative) {
		return Artifact{}, corpusError(CodeSecretInCorpus, "source filename is excluded", nil)
	}

	file, err := openSecureRelative(rootDir, relative, false)
	if err != nil {
		if errors.Is(err, errSecurePathSymlink) || errors.Is(err, syscall.ELOOP) {
			return Artifact{}, corpusError(CodePathEscape, "source path contains a symlink", err)
		}
		return Artifact{}, fmt.Errorf("open source artifact: %w", err)
	}
	defer func() {
		if closeErr := closeSourceFile(file); closeErr != nil {
			artifact = Artifact{}
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()

	snapshot, err := snapshotOpenedFile(file, canonicalRoot, path, relative)
	if err != nil {
		return Artifact{}, err
	}
	return artifactFromSnapshot(snapshot), nil
}

// SnapshotArtifact is an explicit alias for callers that want to emphasize
// that discovery captures a consistency snapshot.
func SnapshotArtifact(root, candidate string) (Artifact, error) {
	return DiscoverArtifact(root, candidate)
}

func artifactFromSnapshot(snapshot artifactSnapshot) Artifact {
	return Artifact{
		Path:          snapshot.path,
		RelativePath:  snapshot.relative,
		Size:          snapshot.size,
		ModTime:       snapshot.modTime,
		Inode:         snapshot.inode,
		ContentSHA256: snapshot.contentSHA256,
		Captured:      true,
		snapshot:      &snapshot,
	}
}

func openSecureDirectory(path string) (*os.File, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("source root is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve source root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	directory, err := openSecurePath(absolute, true)
	if err != nil {
		if errors.Is(err, errSecurePathSymlink) || errors.Is(err, syscall.ELOOP) {
			return nil, "", corpusError(CodePathEscape, "source root contains a symlink", err)
		}
		return nil, "", fmt.Errorf("open source root: %w", err)
	}
	info, err := directory.Stat()
	if err != nil {
		return nil, "", errors.Join(fmt.Errorf("stat source root: %w", err), closeSourceFile(directory))
	}
	if !info.IsDir() {
		return nil, "", errors.Join(fmt.Errorf("source root is not a directory"), closeSourceFile(directory))
	}
	return directory, absolute, nil
}

func openSecurePath(path string, directory bool) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute = filepath.Clean(absolute)
	if !filepath.IsAbs(absolute) || filepath.VolumeName(absolute) != "" {
		return nil, fmt.Errorf("secure path must be an absolute Unix path")
	}

	flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	fd, err := syscall.Open(string(filepath.Separator), flags, 0)
	if err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		componentFlags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
		if index < len(components)-1 || directory {
			componentFlags |= syscall.O_DIRECTORY
		}
		nextFD, openErr := openSecureComponent(fd, component, componentFlags)
		if openErr != nil {
			if errors.Is(openErr, syscall.ELOOP) {
				return nil, errors.Join(errSecurePathSymlink, openErr)
			}
			return nil, openErr
		}
		fd = nextFD
	}
	return os.NewFile(uintptr(fd), absolute), nil
}

func openSecureComponent(parentFD int, component string, flags int) (int, error) {
	nextFD, openErr := openSecureAt(parentFD, component, flags, 0)
	closeErr := closeSecureFD(parentFD)
	var nextCloseErr error
	if nextFD >= 0 && (openErr != nil || closeErr != nil) {
		nextCloseErr = closeSecureFD(nextFD)
	}
	if openErr != nil || closeErr != nil {
		return -1, errors.Join(openErr, closeErr, nextCloseErr)
	}
	return nextFD, nil
}

func candidateWithinRoot(root, candidate string) (string, string, error) {
	if strings.TrimSpace(candidate) == "" {
		return "", "", fmt.Errorf("source path is required")
	}
	absolute := candidate
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(root, absolute)
	}
	absolute, err := filepath.Abs(absolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve source path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if !pathContained(root, absolute) {
		return "", "", corpusError(CodePathEscape, "source path is outside root", nil)
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." || !pathContained(root, filepath.Join(root, relative)) {
		return "", "", corpusError(CodePathEscape, "cannot derive safe relative source path", err)
	}
	return absolute, relative, nil
}

func openSecureRelative(root *os.File, relative string, directory bool) (*os.File, error) {
	if root == nil {
		return nil, fmt.Errorf("source root descriptor is required")
	}
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) {
		return nil, corpusError(CodePathEscape, "source path is outside root", nil)
	}
	fd, err := syscall.Dup(int(root.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicate source root descriptor: %w", err)
	}
	components := strings.Split(clean, string(filepath.Separator))
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			closeErr := closeSecureFD(fd)
			return nil, errors.Join(corpusError(CodePathEscape, "source path is outside root", nil), closeErr)
		}
		flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
		if index < len(components)-1 || directory {
			flags |= syscall.O_DIRECTORY
		} else {
			// Opening a FIFO read-only without O_NONBLOCK can block before the
			// later regular-file check gets a chance to reject it.
			flags |= syscall.O_NONBLOCK
		}
		nextFD, openErr := openSecureComponent(fd, component, flags)
		if openErr != nil {
			if errors.Is(openErr, syscall.ELOOP) {
				return nil, errors.Join(errSecurePathSymlink, openErr)
			}
			return nil, openErr
		}
		fd = nextFD
	}
	file := os.NewFile(uintptr(fd), clean)
	info, statErr := file.Stat()
	if statErr != nil {
		return nil, errors.Join(fmt.Errorf("stat source path: %w", statErr), closeSourceFile(file))
	}
	if directory {
		if !info.IsDir() {
			return nil, errors.Join(fmt.Errorf("source path is not a directory"), closeSourceFile(file))
		}
	} else if !info.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("source artifact is not a regular file"), closeSourceFile(file))
	}
	return file, nil
}

func pathContained(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func excludedSourceName(path string) bool {
	var allWords []string
	for _, component := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if excludedSourceComponent(component) {
			return true
		}
		allWords = append(allWords, pathWords(component)...)
	}
	return hasPathAPIKeyPair(allWords) || hasSessionStatePair(allWords) || hasProviderCredentialPair(allWords) ||
		hasJoinedProviderCredentialFamily(pathStem(allWords))
}

func excludedSourceComponent(component string) bool {
	lower := strings.ToLower(strings.TrimSpace(component))
	if lower == "" || lower == "." || lower == ".." {
		return false
	}
	if strings.HasPrefix(lower, ".env") || lower == ".netrc" || lower == "netrc" || lower == ".npmrc" || lower == ".pypirc" {
		return true
	}
	if strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
		return true
	}

	words := pathWords(component)
	compact := strings.Join(words, "")
	for _, word := range words {
		if sensitivePathWords[word] {
			return true
		}
		if strings.HasPrefix(word, "sqlite") {
			return true
		}
	}
	if strings.HasPrefix(compact, "sqlite") || strings.Contains(compact, "database") {
		return true
	}
	if hasPathAPIKeyPair(words) || hasSessionStatePair(words) || hasProviderCredentialPair(words) || hasJoinedProviderCredentialFamily(pathStem(words)) {
		return true
	}
	if hasJoinedSensitivePathFamily(pathStem(words)) || hasSSHKeyName(words) {
		return true
	}
	return false
}

var sensitivePathWords = map[string]bool{
	"accesskey":      true,
	"accesskeys":     true,
	"apikey":         true,
	"apikeys":        true,
	"auth":           true,
	"authentication": true,
	"authorization":  true,
	"aws":            true,
	"azure":          true,
	"cookie":         true,
	"cookies":        true,
	"bearer":         true,
	"credential":     true,
	"credentials":    true,
	"database":       true,
	"databases":      true,
	"db":             true,
	"git":            true,
	"goals":          true,
	"journal":        true,
	"key":            true,
	"keys":           true,
	"logs":           true,
	"oauth":          true,
	"oauth2":         true,
	"password":       true,
	"passwd":         true,
	"private":        true,
	"privatekey":     true,
	"privatekeys":    true,
	"secret":         true,
	"secrets":        true,
	"shm":            true,
	"sqlite":         true,
	"ssh":            true,
	"state":          true,
	"token":          true,
	"tokens":         true,
	"wal":            true,
}

func pathWords(value string) []string {
	var words []string
	var word []rune
	var previous rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		words = append(words, string(word))
		word = nil
	}
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			flush()
			previous = 0
			continue
		}
		if len(word) > 0 && unicode.IsUpper(character) && unicode.IsLower(previous) {
			flush()
		}
		word = append(word, unicode.ToLower(character))
		previous = character
	}
	flush()
	return words
}

func pathStem(words []string) string {
	if len(words) > 1 && pathFileExtensions[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	return strings.Join(words, "")
}

var pathFileExtensions = map[string]bool{
	"cfg": true, "conf": true, "ini": true, "json": true, "toml": true,
	"jsonl": true, "txt": true, "yaml": true, "yml": true,
}

func hasPathAPIKeyPair(words []string) bool {
	for index := 0; index+1 < len(words); index++ {
		if (words[index] == "api" || words[index] == "access") &&
			(words[index+1] == "key" || words[index+1] == "keys") {
			return true
		}
	}
	return false
}

func hasSessionStatePair(words []string) bool {
	for index, word := range words {
		if word != "session" {
			continue
		}
		for _, related := range words[index+1:] {
			switch related {
			case "cache", "db", "database", "journal", "shm", "state", "store", "wal":
				return true
			}
		}
	}
	return false
}

func hasProviderCredentialPair(words []string) bool {
	provider := false
	credential := false
	for _, word := range words {
		if word == "provider" || isCredentialProvider(word) {
			provider = true
		}
		switch word {
		case "access", "accesskey", "api", "apikey", "auth", "authorization", "credential", "credentials",
			"key", "keys", "password", "passwd", "private", "privatekey", "secret", "secrets", "token", "tokens":
			credential = true
		}
	}
	return provider && credential
}

func hasJoinedProviderCredentialFamily(compact string) bool {
	markers := []string{
		"accesskey", "apikey", "auth", "authorization", "credential", "credentials", "key", "keys",
		"password", "passwd", "private", "privatekey", "secret", "secrets", "token", "tokens",
	}
	joinedRemainders := map[string]bool{
		"backup": true, "backups": true, "cache": true, "caches": true, "data": true,
		"file": true, "files": true, "state": true, "store": true,
	}
	for _, provider := range credentialProviders {
		if !strings.HasPrefix(compact, provider) || len(compact) == len(provider) {
			continue
		}
		suffix := strings.TrimPrefix(compact, provider)
		for _, marker := range markers {
			if suffix == marker {
				return true
			}
			if strings.HasPrefix(suffix, marker) && (joinedRemainders[suffix[len(marker):]] || allDigits(suffix[len(marker):])) {
				return true
			}
		}
	}
	return false
}

func hasJoinedSensitivePathFamily(compact string) bool {
	markers := []string{
		"accesskey", "accesskeys", "apikey", "apikeys", "auth", "authentication", "authorization", "cookie", "cookies",
		"credential", "credentials", "database", "db", "goals", "headers", "journal", "key", "logs", "oauth",
		"password", "passwd", "privatekey", "provider", "secret", "secrets", "session", "shm", "state", "token", "tokens", "wal", "bearer",
	}
	joinedRemainders := map[string]bool{
		"backup": true, "backups": true, "cache": true, "caches": true, "data": true,
		"db": true, "file": true, "files": true, "key": true, "keys": true,
		"headers": true, "jar": true, "journal": true, "secret": true, "secrets": true, "state": true, "store": true,
		"token": true, "tokens": true, "vault": true,
	}
	for _, marker := range markers {
		if len(compact) > len(marker) && strings.HasSuffix(compact, marker) {
			return true
		}
		if !strings.HasPrefix(compact, marker) || len(compact) == len(marker) {
			continue
		}
		if joinedRemainders[compact[len(marker):]] {
			return true
		}
		if allDigits(compact[len(marker):]) {
			return true
		}
	}
	return false
}

func hasSSHKeyName(words []string) bool {
	if len(words) == 1 {
		for _, algorithm := range []string{"dsa", "ecdsa", "ed25519", "rsa"} {
			if strings.HasPrefix(words[0], "id"+algorithm) {
				return true
			}
		}
		return false
	}
	if len(words) < 2 || words[0] != "id" {
		return false
	}
	switch words[1] {
	case "dsa", "ecdsa", "ed25519", "rsa":
		return true
	default:
		return false
	}
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func snapshotOpenedFile(file *os.File, root, path, relative string) (artifactSnapshot, error) {
	if file == nil {
		return artifactSnapshot{}, fmt.Errorf("source descriptor is required")
	}
	before, err := file.Stat()
	if err != nil {
		return artifactSnapshot{}, fmt.Errorf("stat source artifact: %w", err)
	}
	if !before.Mode().IsRegular() {
		return artifactSnapshot{}, fmt.Errorf("source artifact is not a regular file")
	}
	hashBefore, err := hashOpenedFile(file)
	if err != nil {
		return artifactSnapshot{}, fmt.Errorf("hash source artifact: %w", err)
	}
	middle, err := file.Stat()
	if err != nil {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "source disappeared during snapshot", err)
	}
	hashAfter, err := hashOpenedFile(file)
	if err != nil {
		return artifactSnapshot{}, fmt.Errorf("hash source artifact: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "source disappeared during snapshot", err)
	}
	if !sameFileInfo(before, middle) || !sameFileInfo(middle, after) || hashBefore != hashAfter {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "source changed during snapshot", nil)
	}
	return artifactSnapshot{
		root:          root,
		path:          path,
		relative:      relative,
		size:          after.Size(),
		modTime:       after.ModTime(),
		inode:         fileInode(after),
		contentSHA256: hashAfter,
	}, nil
}

func hashOpenedFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	_, seekErr := file.Seek(0, io.SeekStart)
	if copyErr != nil {
		return "", copyErr
	}
	if seekErr != nil {
		return "", seekErr
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func fileInode(info os.FileInfo) uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(stat.Ino)
}

func sameFileInfo(left, right os.FileInfo) bool {
	return left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime()) &&
		fileInode(left) == fileInode(right)
}

func artifactSnapshotFor(artifact Artifact) (artifactSnapshot, error) {
	if artifact.snapshot == nil {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "artifact snapshot token is missing", nil)
	}
	snapshot := *artifact.snapshot
	snapshot.openCodeCompanions = cloneArtifactSnapshots(snapshot.openCodeCompanions)
	if !artifact.Captured || artifact.Path != snapshot.path || artifact.RelativePath != snapshot.relative ||
		artifact.Size != snapshot.size || !artifact.ModTime.Equal(snapshot.modTime) ||
		artifact.Inode != snapshot.inode || artifact.ContentSHA256 != snapshot.contentSHA256 {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "artifact snapshot was modified", nil)
	}
	if snapshot.root == "" || snapshot.path == "" || snapshot.relative == "" || snapshot.size < 0 || snapshot.modTime.IsZero() ||
		snapshot.inode == 0 || len(snapshot.contentSHA256) != sha256.Size*2 {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "artifact snapshot is incomplete", nil)
	}
	if _, err := hex.DecodeString(snapshot.contentSHA256); err != nil {
		return artifactSnapshot{}, corpusError(CodeSourceChanged, "artifact snapshot hash is invalid", err)
	}
	return snapshot, nil
}

func cloneArtifactSnapshots(snapshots map[string]artifactSnapshot) map[string]artifactSnapshot {
	if snapshots == nil {
		return nil
	}
	clone := make(map[string]artifactSnapshot, len(snapshots))
	for key, snapshot := range snapshots {
		clone[key] = snapshot
	}
	return clone
}
