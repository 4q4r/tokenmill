//go:build linux

package corpus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tokenmill/tokenmill/internal/replay"
	_ "modernc.org/sqlite"
)

// OpenCodePathResolver resolves the active OpenCode database path without
// involving a shell.
type OpenCodePathResolver func(context.Context) (string, error)

// OpenCodeSource imports the current OpenCode SQLite archive through a private
// consistency snapshot and read-only transaction. It never writes to the
// source database or its WAL sidecars.
type OpenCodeSource struct {
	// DatabasePath is an explicit path. When empty, Resolver is used, followed
	// by the exact `opencode db path` command.
	DatabasePath string
	Resolver     OpenCodePathResolver
}

// NewOpenCodeSource creates an importer with an optional explicit database
// path. An empty argument selects the documented CLI resolver.
func NewOpenCodeSource(path ...string) *OpenCodeSource {
	source := &OpenCodeSource{}
	if len(path) > 0 {
		source.DatabasePath = path[0]
	}
	return source
}

// NewOpenCodeSourceWithResolver creates an importer with an injected path
// resolver for deterministic tests and controlled deployments.
func NewOpenCodeSourceWithResolver(resolver OpenCodePathResolver) *OpenCodeSource {
	return &OpenCodeSource{Resolver: resolver}
}

// ID returns the stable source identifier.
func (s *OpenCodeSource) ID() string {
	return "opencode"
}

// Discover resolves and snapshots one approved OpenCode database artifact.
func (s *OpenCodeSource) Discover(ctx context.Context, options Options) ([]Artifact, error) {
	if ctx == nil {
		return nil, fmt.Errorf("OpenCode discovery requires a context")
	}
	path, err := s.resolveDatabasePath(ctx)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) && strings.TrimSpace(options.Root) != "" {
		path = filepath.Join(options.Root, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve OpenCode database path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	root := options.Root
	if strings.TrimSpace(root) == "" {
		root = filepath.Dir(absolute)
	}
	allow := s.allowedDatabasePath
	artifact, err := discoverSourceArtifact(root, absolute, allow)
	if err != nil {
		return nil, err
	}
	snapshot, err := artifactSnapshotFor(artifact)
	if err != nil {
		return nil, err
	}
	companionsBefore, err := snapshotOpenCodeCompanions(snapshot.root, snapshot.relative)
	if err != nil {
		return nil, err
	}
	file, current, err := openSourceArtifactForRead(snapshot.root, snapshot, allow)
	if err != nil {
		return nil, err
	}
	closeErr := closeSourceFile(file)
	if closeErr != nil {
		return nil, closeErr
	}
	if !sameSnapshotValues(snapshot, current) {
		return nil, corpusError(CodeSourceChanged, "OpenCode database changed during discovery", nil)
	}
	companionsAfter, err := snapshotOpenCodeCompanions(snapshot.root, snapshot.relative)
	if err != nil {
		return nil, err
	}
	if !sameOpenCodeCompanions(companionsBefore, companionsAfter) {
		return nil, corpusError(CodeSourceChanged, "OpenCode companion changed during discovery", nil)
	}
	snapshot.openCodeCompanions = companionsAfter
	artifact = artifactFromSnapshot(snapshot)
	return []Artifact{artifact}, nil
}

// Read imports all validated session/message/part rows from one consistent
// SQLite read transaction. The transaction is read-only and query_only is
// enabled before any application query runs.
func (s *OpenCodeSource) Read(ctx context.Context, artifact Artifact, writer *Writer) (returnErr error) {
	if writer == nil {
		return fmt.Errorf("nil corpus writer")
	}
	if ctx == nil {
		err := fmt.Errorf("OpenCode importer requires a context")
		writer.markFailure(err)
		return err
	}
	expected, err := artifactSnapshotFor(artifact)
	if err != nil {
		writer.markFailure(err)
		return err
	}
	if !s.allowedDatabasePath(expected.relative) {
		err := corpusError(CodeSourceChanged, "OpenCode artifact is not an approved database path", nil)
		writer.markFailure(err)
		return err
	}
	file, before, err := openSourceArtifactForRead(expected.root, expected, s.allowedDatabasePath)
	if err != nil {
		writer.markFailure(err)
		return err
	}
	defer func() {
		if closeErr := closeSourceFile(file); closeErr != nil {
			writer.markFailure(closeErr)
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()
	if err := checkSourceOutputCollision(writer, before, file); err != nil {
		writer.markFailure(err)
		return err
	}

	storeSnapshot, err := snapshotOpenCodeStore(file, before, expected.openCodeCompanions)
	if err != nil {
		return s.finishOpenCodeRead(writer, file, before, nil, err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(storeSnapshot.directory); cleanupErr != nil {
			err := fmt.Errorf("remove OpenCode snapshot directory: %w", cleanupErr)
			writer.markFailure(err)
			returnErr = errors.Join(returnErr, err)
		}
	}()

	db, err := sql.Open("sqlite", openCodeReadOnlyDSN(storeSnapshot.database))
	if err != nil {
		return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			writer.markFailure(closeErr)
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, corpusError(CodeInputJSON, "begin OpenCode read transaction", err))
	}
	committed := false
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err := corpusError(CodeInputJSON, "rollback OpenCode read transaction", rollbackErr)
				writer.markFailure(err)
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	if _, err := tx.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, corpusError(CodeInputJSON, "enable OpenCode query-only mode", err))
	}

	schema, err := probeOpenCodeSchema(ctx, tx)
	if err != nil {
		return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, err)
	}
	if err := s.streamOpenCodeRecords(ctx, tx, schema, artifact, writer); err != nil {
		return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, err)
	}
	if err := tx.Commit(); err != nil {
		return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, corpusError(CodeInputJSON, "commit OpenCode read transaction", err))
	}
	committed = true
	return s.finishOpenCodeRead(writer, file, before, storeSnapshot.companions, nil)
}

func (s *OpenCodeSource) finishOpenCodeRead(writer *Writer, file *os.File, before artifactSnapshot, companions map[string]artifactSnapshot, importErr error) error {
	after, snapshotErr := snapshotOpenedFile(file, before.root, before.path, before.relative)
	if snapshotErr == nil && !sameSnapshotValues(before, after) {
		snapshotErr = corpusError(CodeSourceChanged, "OpenCode database changed during read", nil)
	}
	pathErr := verifySourceArtifactPath(before.root, before, s.allowedDatabasePath)
	var companionErr error
	if companions != nil {
		afterCompanions, err := snapshotOpenCodeCompanions(before.root, before.relative)
		if err != nil {
			companionErr = err
		} else if !sameOpenCodeCompanions(companions, afterCompanions) {
			companionErr = corpusError(CodeSourceChanged, "OpenCode companion changed during read", nil)
		}
	}
	if importErr != nil {
		writer.markFailure(importErr)
	}
	if snapshotErr != nil {
		writer.markFailure(snapshotErr)
	}
	if pathErr != nil {
		writer.markFailure(pathErr)
	}
	if companionErr != nil {
		writer.markFailure(companionErr)
	}
	return errors.Join(snapshotErr, pathErr, companionErr, importErr)
}

func (s *OpenCodeSource) resolveDatabasePath(ctx context.Context) (string, error) {
	if s != nil && strings.TrimSpace(s.DatabasePath) != "" {
		return s.DatabasePath, nil
	}
	if s != nil && s.Resolver != nil {
		path, err := s.Resolver(ctx)
		if err != nil {
			return "", corpusError(CodeInputJSON, "resolve OpenCode database path", err)
		}
		return validateResolvedOpenCodePath(path)
	}
	command := exec.CommandContext(ctx, "opencode", "db", "path")
	output, err := command.Output()
	if err != nil {
		return "", corpusError(CodeInputJSON, "resolve OpenCode database path", err)
	}
	return validateResolvedOpenCodePath(string(output))
}

func validateResolvedOpenCodePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') || strings.ContainsAny(trimmed, "\r\n") {
		return "", corpusError(CodeInputJSON, "OpenCode path resolver returned an invalid path", nil)
	}
	return trimmed, nil
}

func (s *OpenCodeSource) allowedDatabasePath(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) == 0 {
		return false
	}
	for _, parent := range parts[:len(parts)-1] {
		if !safeTranscriptComponent(parent) {
			return false
		}
	}
	base := parts[0]
	if len(parts) > 1 {
		base = parts[len(parts)-1]
	}
	if base == "opencode.db" {
		return true
	}
	explicit := s != nil && (strings.TrimSpace(s.DatabasePath) != "" || s.Resolver != nil)
	if !explicit || !openCodeExplicitDatabaseNamePattern.MatchString(base) {
		return false
	}
	return !openCodeSensitiveDatabaseName(base)
}

var openCodeExplicitDatabaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]+\.(?:db|sqlite|sqlite3)$`)

func openCodeSensitiveDatabaseName(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{
		"-wal", "-shm", "-journal", ".wal", ".shm", ".journal", "auth", "cookie", "credential",
		"key", "secret", "token", "password", "private", "provider", "state", "logs", "goals",
		"database", "bearer", "sessioncache", "sessionstore", "sessionstate", "credentialvault",
		"keyvault", "keybackup", "authheaders", "bearerheaders",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return lower == "netrc" || strings.HasPrefix(lower, ".env")
}

func openCodeReadOnlyDSN(path string) string {
	value := url.URL{Scheme: "file", Path: path}
	value.RawQuery = "mode=ro&_query_only=1"
	return value.String()
}

type openCodeSchema struct {
	session []string
	message []string
	part    []string
}

func probeOpenCodeSchema(ctx context.Context, tx *sql.Tx) (openCodeSchema, error) {
	session, err := sqliteTableColumns(ctx, tx, "session")
	if err != nil {
		return openCodeSchema{}, err
	}
	message, err := sqliteTableColumns(ctx, tx, "message")
	if err != nil {
		return openCodeSchema{}, err
	}
	part, err := sqliteTableColumns(ctx, tx, "part")
	if err != nil {
		return openCodeSchema{}, err
	}
	if !hasColumns(session, "id") {
		return openCodeSchema{}, missingOpenCodeColumn("session", "id")
	}
	for _, column := range []string{"id", "session_id", "data"} {
		if !hasColumns(message, column) {
			return openCodeSchema{}, missingOpenCodeColumn("message", column)
		}
	}
	for _, column := range []string{"id", "session_id", "message_id", "data"} {
		if !hasColumns(part, column) {
			return openCodeSchema{}, missingOpenCodeColumn("part", column)
		}
	}
	return openCodeSchema{session: session, message: message, part: part}, nil
}

func sqliteTableColumns(ctx context.Context, tx *sql.Tx, table string) (columns []string, returnErr error) {
	query := `PRAGMA table_info("` + table + `")`
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, corpusError(CodeInputJSON, "probe OpenCode "+table+" table", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, corpusError(CodeInputJSON, "close OpenCode "+table+" schema rows", closeErr))
		}
	}()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, corpusError(CodeInputJSON, "decode OpenCode "+table+" schema", err)
		}
		_ = cid
		_ = dataType
		_ = notNull
		_ = primaryKey
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, corpusError(CodeInputJSON, "read OpenCode "+table+" schema", err)
	}
	if len(columns) == 0 {
		return nil, corpusError(CodeInputJSON, "OpenCode "+table+" table is missing", nil)
	}
	return columns, nil
}

func hasColumns(columns []string, required string) bool {
	for _, column := range columns {
		if strings.EqualFold(column, required) {
			return true
		}
	}
	return false
}

func missingOpenCodeColumn(table, column string) error {
	return corpusError(CodeInputJSON, fmt.Sprintf("OpenCode %s table is missing required column %s", table, column), nil)
}

type openCodeSessionRow struct {
	id      string
	version string
	model   string
}

type openCodeMessageRow struct {
	id             string
	sessionID      string
	data           map[string]json.RawMessage
	ordinal        int
	timeCreated    int64
	hasTimeCreated bool
	parts          []*openCodePartRow
}

type openCodePartRow struct {
	id        string
	sessionID string
	messageID string
	data      map[string]json.RawMessage
	ordinal   int
}

// streamOpenCodeRecords imports one session at a time: messages, parts, and
// records for a session are loaded, written, and released before the next
// session starts, so peak memory stays bounded by the largest session rather
// than the whole store. Global identity sets keep duplicate-ID detection
// store-wide, and every entry (record or tolerated skip) consumes one
// deterministic journal line.
func (s *OpenCodeSource) streamOpenCodeRecords(ctx context.Context, tx *sql.Tx, schema openCodeSchema, artifact Artifact, writer *Writer) error {
	sessions, err := loadOpenCodeSessions(ctx, tx, schema.session)
	if err != nil {
		return err
	}
	seenMessageIDs := make(map[string]struct{})
	seenPartIDs := make(map[string]struct{})
	line := 0
	for _, session := range sessions {
		messages, err := loadOpenCodeSessionMessages(ctx, tx, schema.message, session, seenMessageIDs)
		if err != nil {
			return err
		}
		if err := loadOpenCodeSessionParts(ctx, tx, schema.part, session, messages, seenPartIDs); err != nil {
			return err
		}
		sort.SliceStable(messages, func(i, j int) bool {
			left, right := openCodeMessageTime(messages[i]), openCodeMessageTime(messages[j])
			if left.IsZero() != right.IsZero() {
				return !left.IsZero()
			}
			if !left.Equal(right) {
				return left.Before(right)
			}
			if messages[i].id != messages[j].id {
				return messages[i].id < messages[j].id
			}
			return messages[i].ordinal < messages[j].ordinal
		})
		for sequence, messageRow := range messages {
			message, model, timestamp, err := openCodeMessage(messageRow)
			if err != nil {
				return err
			}
			if model == "" {
				model = session.model
			}
			parts := messageRow.parts
			sort.SliceStable(parts, func(i, j int) bool {
				if parts[i].id != parts[j].id {
					return parts[i].id < parts[j].id
				}
				return parts[i].ordinal < parts[j].ordinal
			})
			for _, partRow := range parts {
				part, callID, err := openCodePart(partRow)
				if err != nil {
					return err
				}
				message.Parts = append(message.Parts, part)
				if message.ToolCallID == "" && callID != "" {
					message.ToolCallID = callID
				}
			}
			line++
			if len(message.Parts) == 0 {
				raw, rawErr := json.Marshal(messageRow.data)
				if rawErr != nil {
					return corpusError(CodeInputJSON, "encode OpenCode zero-part message for quarantine", rawErr)
				}
				quarantine := Quarantine{
					Line:    line,
					Offset:  int64(line - 1),
					Code:    CodeInputJSON,
					Message: fmt.Sprintf("OpenCode session %s message %s has no parts", session.id, messageRow.id),
					Raw:     raw,
				}
				if err := writer.Tolerate(quarantine); err != nil {
					return err
				}
				continue
			}
			record := newImportedRecord("opencode", artifact, session.id, messageRow.id, sequence, timestamp, model, message)
			record.Source.Version = session.version
			record.Source.RelativeLocator = artifact.RelativePath + "#message/" + messageRow.id
			if err := writeImportedRecord(writer, sourceLine{Line: line, Offset: int64(line - 1)}, record); err != nil {
				return err
			}
		}
		for index := range messages {
			messages[index] = nil
		}
	}
	return nil
}

func loadOpenCodeSessions(ctx context.Context, tx *sql.Tx, columns []string) ([]openCodeSessionRow, error) {
	rows, err := querySQLiteRows(ctx, tx, "session", columns)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	sessions := make([]openCodeSessionRow, 0, len(rows))
	for _, row := range rows {
		id, err := requiredSQLiteString(row, columns, "id", "session")
		if err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			return nil, corpusError(CodeInputJSON, "duplicate OpenCode session ID", nil)
		}
		seen[id] = struct{}{}
		model, err := openCodeSessionModel(row, columns)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, openCodeSessionRow{id: id, version: optionalSQLiteString(row, columns, "version"), model: model})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].id < sessions[j].id })
	return sessions, nil
}

func loadOpenCodeSessionMessages(ctx context.Context, tx *sql.Tx, columns []string, session openCodeSessionRow, seenMessageIDs map[string]struct{}) ([]*openCodeMessageRow, error) {
	rows, err := querySQLiteRowsWhere(ctx, tx, "message", columns, "session_id", session.id)
	if err != nil {
		return nil, err
	}
	messages := make([]*openCodeMessageRow, 0, len(rows))
	for ordinal, row := range rows {
		id, err := requiredSQLiteString(row, columns, "id", "message")
		if err != nil {
			return nil, err
		}
		sessionID, err := requiredSQLiteString(row, columns, "session_id", "message")
		if err != nil {
			return nil, err
		}
		if sessionID != session.id {
			return nil, corpusError(CodeInputJSON, "OpenCode message session changed during scan", nil)
		}
		if _, exists := seenMessageIDs[id]; exists {
			return nil, corpusError(CodeInputJSON, "duplicate OpenCode message ID", nil)
		}
		seenMessageIDs[id] = struct{}{}
		data, err := requiredSQLiteJSON(row, columns, "data", "message")
		if err != nil {
			return nil, err
		}
		timeCreated, hasTimeCreated, err := optionalSQLiteInt64(row, "time_created")
		if err != nil {
			return nil, corpusError(CodeInputJSON, "decode OpenCode message.time_created", err)
		}
		messages = append(messages, &openCodeMessageRow{id: id, sessionID: sessionID, data: data, ordinal: ordinal, timeCreated: timeCreated, hasTimeCreated: hasTimeCreated})
	}
	return messages, nil
}

func loadOpenCodeSessionParts(ctx context.Context, tx *sql.Tx, columns []string, session openCodeSessionRow, messages []*openCodeMessageRow, seenPartIDs map[string]struct{}) error {
	messageSet := make(map[string]*openCodeMessageRow, len(messages))
	for _, message := range messages {
		messageSet[message.id] = message
	}
	rows, err := querySQLiteRowsWhere(ctx, tx, "part", columns, "session_id", session.id)
	if err != nil {
		return err
	}
	for ordinal, row := range rows {
		id, err := requiredSQLiteString(row, columns, "id", "part")
		if err != nil {
			return err
		}
		sessionID, err := requiredSQLiteString(row, columns, "session_id", "part")
		if err != nil {
			return err
		}
		messageID, err := requiredSQLiteString(row, columns, "message_id", "part")
		if err != nil {
			return err
		}
		if sessionID != session.id {
			return corpusError(CodeInputJSON, "OpenCode part session changed during scan", nil)
		}
		message, exists := messageSet[messageID]
		if !exists {
			return corpusError(CodeInputJSON, "OpenCode part refers to an unknown message", nil)
		}
		if _, exists := seenPartIDs[id]; exists {
			return corpusError(CodeInputJSON, "duplicate OpenCode part ID", nil)
		}
		seenPartIDs[id] = struct{}{}
		data, err := requiredSQLiteJSON(row, columns, "data", "part")
		if err != nil {
			return err
		}
		message.parts = append(message.parts, &openCodePartRow{id: id, sessionID: sessionID, messageID: messageID, data: data, ordinal: ordinal})
	}
	return nil
}

func querySQLiteRows(ctx context.Context, tx *sql.Tx, table string, columns []string) (result []map[string]any, returnErr error) {
	return querySQLiteRowsWhere(ctx, tx, table, columns, "", nil)
}

// querySQLiteRowsWhere reads one table, optionally restricted to rows where a
// column equals a value, so large stores can be imported per session instead
// of being buffered in full.
func querySQLiteRowsWhere(ctx context.Context, tx *sql.Tx, table string, columns []string, column string, value any) (result []map[string]any, returnErr error) {
	query := `SELECT * FROM "` + table + `"`
	args := []any(nil)
	if column != "" {
		query += ` WHERE "` + column + `" = ?`
		args = append(args, value)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, corpusError(CodeInputJSON, "read OpenCode "+table+" rows", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, corpusError(CodeInputJSON, "close OpenCode "+table+" rows", closeErr))
		}
	}()
	actualColumns, err := rows.Columns()
	if err != nil {
		return nil, corpusError(CodeInputJSON, "read OpenCode "+table+" columns", err)
	}
	if len(actualColumns) != len(columns) {
		return nil, corpusError(CodeInputJSON, "OpenCode "+table+" schema changed during transaction", nil)
	}
	for rows.Next() {
		values := make([]any, len(actualColumns))
		pointers := make([]any, len(actualColumns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, corpusError(CodeInputJSON, "decode OpenCode "+table+" row", err)
		}
		row := make(map[string]any, len(actualColumns))
		for index, column := range actualColumns {
			row[column] = values[index]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, corpusError(CodeInputJSON, "read OpenCode "+table+" rows", err)
	}
	return result, nil
}

func requiredSQLiteString(row map[string]any, columns []string, column, table string) (string, error) {
	value := optionalSQLiteString(row, columns, column)
	if strings.TrimSpace(value) == "" {
		return "", corpusError(CodeInputJSON, fmt.Sprintf("OpenCode %s.%s is empty", table, column), nil)
	}
	return value, nil
}

func optionalSQLiteString(row map[string]any, columns []string, column string) string {
	for actual, value := range row {
		if !strings.EqualFold(actual, column) || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case []byte:
			return string(typed)
		default:
			return fmt.Sprint(typed)
		}
	}
	_ = columns
	return ""
}

func optionalSQLiteInt64(row map[string]any, column string) (int64, bool, error) {
	for actual, value := range row {
		if !strings.EqualFold(actual, column) {
			continue
		}
		if value == nil {
			return 0, false, nil
		}
		switch typed := value.(type) {
		case int64:
			return typed, true, nil
		case int:
			return int64(typed), true, nil
		case int32:
			return int64(typed), true, nil
		case int16:
			return int64(typed), true, nil
		case int8:
			return int64(typed), true, nil
		case uint:
			if uint64(typed) > uint64(^uint64(0)>>1) {
				return 0, false, fmt.Errorf("integer overflows int64")
			}
			return int64(typed), true, nil
		case uint64:
			if typed > uint64(^uint64(0)>>1) {
				return 0, false, fmt.Errorf("integer overflows int64")
			}
			return int64(typed), true, nil
		case uint32:
			return int64(typed), true, nil
		case uint16:
			return int64(typed), true, nil
		case uint8:
			return int64(typed), true, nil
		case []byte:
			parsed, err := strconv.ParseInt(string(typed), 10, 64)
			return parsed, true, err
		case string:
			parsed, err := strconv.ParseInt(typed, 10, 64)
			return parsed, true, err
		default:
			return 0, false, fmt.Errorf("unsupported integer type %T", value)
		}
	}
	return 0, false, nil
}

func openCodeSessionModel(row map[string]any, columns []string) (string, error) {
	raw := optionalSQLiteString(row, columns, "model")
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	var modelID string
	if json.Unmarshal([]byte(raw), &modelID) == nil {
		return strings.TrimSpace(modelID), nil
	}
	object, err := decodeSourceObject([]byte(raw))
	if err != nil {
		return "", corpusError(CodeInputJSON, "malformed OpenCode session.model JSON", err)
	}
	if nested := sourceObject(object, "model"); nested != nil {
		object = nested
	}
	return sourceString(object, "modelID", "model_id", "modelId", "id", "name"), nil
}

func requiredSQLiteJSON(row map[string]any, columns []string, column, table string) (map[string]json.RawMessage, error) {
	var raw []byte
	for actual, value := range row {
		if !strings.EqualFold(actual, column) {
			continue
		}
		switch typed := value.(type) {
		case string:
			raw = []byte(typed)
		case []byte:
			raw = append([]byte(nil), typed...)
		case nil:
			return nil, corpusError(CodeInputJSON, fmt.Sprintf("OpenCode %s.%s is null", table, column), nil)
		default:
			return nil, corpusError(CodeInputJSON, fmt.Sprintf("OpenCode %s.%s is not text JSON", table, column), nil)
		}
	}
	if len(raw) == 0 {
		return nil, corpusError(CodeInputJSON, fmt.Sprintf("OpenCode %s.%s is missing", table, column), nil)
	}
	object, err := decodeSourceObject(raw)
	if err != nil {
		return nil, corpusError(CodeInputJSON, fmt.Sprintf("malformed OpenCode %s.%s JSON", table, column), err)
	}
	_ = columns
	return object, nil
}

func openCodeMessage(row *openCodeMessageRow) (replay.Message, string, time.Time, error) {
	role, ok := importedRole(sourceString(row.data, "role"))
	if !ok {
		return replay.Message{}, "", time.Time{}, corpusError(CodeInputJSON, "OpenCode message has an unsupported role", nil)
	}
	model := openCodeModel(row.data)
	timestamp := openCodeMessageTime(row)
	return replay.Message{Role: role, ID: row.id}, model, timestamp, nil
}

func openCodeMessageTime(row *openCodeMessageRow) time.Time {
	if row.hasTimeCreated {
		return time.UnixMilli(row.timeCreated).UTC()
	}
	if nested := sourceObject(row.data, "time"); nested != nil {
		if timestamp := sourceTimestamp(nested, "created", "created_at", "createdAt"); !timestamp.IsZero() {
			return timestamp
		}
	}
	return sourceTimestamp(row.data, "timestamp", "time_created", "created", "created_at", "createdAt")
}

func openCodeModel(data map[string]json.RawMessage) string {
	if model := sourceString(data, "model", "model_id", "modelId"); model != "" {
		return model
	}
	if model := sourceString(data, "modelID"); model != "" {
		return model
	}
	if object := sourceObject(data, "model"); object != nil {
		return sourceString(object, "modelID", "model_id", "modelId", "id", "name")
	}
	return ""
}

func openCodePart(row *openCodePartRow) (replay.Part, string, error) {
	partType := sourceString(row.data, "type")
	if partType == "" {
		return replay.Part{}, "", corpusError(CodeInputJSON, "OpenCode part has no type", nil)
	}
	part := replay.Part{Type: partType, Raw: rawObjectBytes(row.data)}
	callID := sourceString(row.data, "callID", "call_id", "callId", "toolCallID", "tool_call_id", "toolCallId")
	if state := sourceObject(row.data, "state"); state != nil && callID == "" {
		callID = sourceString(state, "callID", "call_id", "callId", "toolCallID", "tool_call_id", "toolCallId")
	}
	if partType == "text" {
		text, ok := sourceStringWithPresence(row.data, "text")
		if !ok {
			return replay.Part{}, "", corpusError(CodeInputJSON, "OpenCode text part has no text", nil)
		}
		part.Text = text
	}
	return part, callID, nil
}

var _ Source = (*OpenCodeSource)(nil)
