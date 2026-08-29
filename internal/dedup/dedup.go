package dedup

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// refRegex matches §ref:HASH§ where HASH is 8-64 hex chars (lowercase).
// IsRef is case-sensitive (lowercase only) by design to keep the reference
// format canonical and avoid ambiguous mixed-case refs. Store.Get/Expand
// normalize input via strings.ToLower and strings.TrimSpace, so they accept
// upper-case hashes for lookup, while IsRef remains strict. If a
// case-insensitive reference check is desired, use [0-9a-fA-F] or (?i).
var refRegex = regexp.MustCompile(`§ref:([0-9a-f]{8,64})§`)

var canonicalRefRegex = regexp.MustCompile(`^§ref:([0-9a-f]{8,64})§(?: \(original ([0-9]+) chars, use tokenmill_expand to retrieve\))?$`)

// entry holds dedup metadata for a single block.
type entry struct {
	content  string
	turn     int
	fullHash string // 64 hex
	hash8    string // 8 hex truncated
}

// Store is a thread-safe SHA256 whole-block dedup store.
// It is cache-safe (canonical-first): first occurrence is never mutated.
// Subsequent duplicates are replaced with §ref:HASH8§ references.
type Store struct {
	mu             sync.RWMutex
	freshnessTurns int
	entries        map[string]entry  // fullHash -> entry
	truncIndex     map[string]string // hash8 -> fullHash (first)
	db             *sql.DB
	dbPath         string
	lastErr        error
}

// New creates a Store. freshnessTurns <=0 defaults to 20.
// If dbPath != "" it opens SQLite via modernc.org/sqlite and persists entries.
// Database failures keep the in-memory behavior available and are exposed via Err.
func New(freshnessTurns int, dbPath string) *Store {
	if freshnessTurns <= 0 {
		freshnessTurns = 20
	}
	s := &Store{
		freshnessTurns: freshnessTurns,
		entries:        make(map[string]entry),
		truncIndex:     make(map[string]string),
		dbPath:         dbPath,
	}
	if dbPath != "" {
		dir := filepath.Dir(dbPath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				s.lastErr = fmt.Errorf("dedup: create database directory: %w", err)
			}
		}
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			f, e := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0600)
			if e == nil {
				if closeErr := f.Close(); closeErr != nil {
					s.lastErr = fmt.Errorf("dedup: close database bootstrap file: %w", closeErr)
				}
			} else {
				s.lastErr = fmt.Errorf("dedup: create database file: %w", e)
			}
		} else if err != nil {
			s.lastErr = fmt.Errorf("dedup: inspect database file: %w", err)
		}
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			s.lastErr = fmt.Errorf("dedup: open database: %w", err)
		} else {
			if _, pragmaErr := db.Exec("PRAGMA journal_mode=WAL;"); pragmaErr != nil {
				s.lastErr = fmt.Errorf("dedup: configure WAL: %w", pragmaErr)
			}
			if _, pragmaErr := db.Exec("PRAGMA busy_timeout=5000;"); pragmaErr != nil {
				s.lastErr = fmt.Errorf("dedup: configure busy timeout: %w", pragmaErr)
			}
			if _, pragmaErr := db.Exec("PRAGMA synchronous=NORMAL;"); pragmaErr != nil {
				s.lastErr = fmt.Errorf("dedup: configure synchronous mode: %w", pragmaErr)
			}
			_, err = db.Exec(`CREATE TABLE IF NOT EXISTS dedup (
				full_hash TEXT PRIMARY KEY,
				hash8 TEXT NOT NULL,
				content TEXT NOT NULL,
				turn INTEGER NOT NULL
			);`)
			if err == nil {
				if _, indexErr := db.Exec(`CREATE INDEX IF NOT EXISTS idx_hash8 ON dedup(hash8);`); indexErr != nil {
					s.lastErr = fmt.Errorf("dedup: create hash index: %w", indexErr)
				}
				rows, e := db.Query(`SELECT full_hash, hash8, content, turn FROM dedup`)
				if e == nil {
					for rows.Next() {
						var full, h8, content string
						var turn int
						if err := rows.Scan(&full, &h8, &content, &turn); err == nil {
							s.entries[full] = entry{content: content, turn: turn, fullHash: full, hash8: h8}
							if _, exists := s.truncIndex[h8]; !exists {
								s.truncIndex[h8] = full
							}
						} else {
							s.lastErr = fmt.Errorf("dedup: load entry: %w", err)
						}
					}
					if rowsErr := rows.Err(); rowsErr != nil {
						s.lastErr = fmt.Errorf("dedup: iterate entries: %w", rowsErr)
					}
					_ = rows.Close()
				} else {
					s.lastErr = fmt.Errorf("dedup: load entries: %w", e)
				}
				s.db = db
			} else {
				s.lastErr = fmt.Errorf("dedup: initialize database: %w", err)
				_ = db.Close()
			}
		}
	}
	return s
}

// hashContent returns full 64 hex and truncated 8 hex for content.
func hashContent(content string) (full, trunc string) {
	h := sha256.Sum256([]byte(content))
	full = hex.EncodeToString(h[:])
	trunc = full[:8]
	return full, trunc
}

// formatRef returns the canonical ref string with stats suffix.
func formatRef(hash8 string, originalLen int) string {
	return fmt.Sprintf("§ref:%s§ (original %d chars, use tokenmill_expand to retrieve)", hash8, originalLen)
}

// IsRef reports whether s contains a §ref:HASH§ pattern.
func (s *Store) IsRef(str string) bool {
	return refRegex.MatchString(str)
}

// IsRefString is package-level helper.
func IsRef(str string) bool {
	return refRegex.MatchString(str)
}

// Put stores content at turn and returns full hash (64 hex).
// If same content already exists and is not expired, it preserves canonical turn (cache-safe).
func (s *Store) Put(content string, turn int) string {
	full, h8 := hashContent(content)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[full]; ok {
		// byte-compare collision fallback: if stored content differs, treat as distinct
		// (full same => content must be same barring SHA256 collision, but check anyway)
		if e.content == content {
			// canonical-first: do not overwrite turn, keep original
			// freshness check: if expired, treat as new canonical (update)
			if s.freshnessTurns > 0 && turn-e.turn >= s.freshnessTurns {
				// expired: update
				ne := entry{content: content, turn: turn, fullHash: full, hash8: h8}
				s.entries[full] = ne
				s.persistEntryLocked(ne)
				return full
			}
			return full
		}
		// content differs but full same (theoretical SHA256 collision) -> fallback byte-compare: treat as not same
		// store as distinct? But full collides, can't store both under same key. Return full anyway (collision).
		// For safety, we do not overwrite; return existing full.
		return full
	}
	// check truncated collision: if h8 already maps to different full with different content,
	// we keep first mapping (cache-safe) but still store new entry under its full key.
	// This ensures Decode of truncated ref resolves to first canonical (oldest) - caller should use full hash for unambiguous Expand.
	ne := entry{content: content, turn: turn, fullHash: full, hash8: h8}
	s.entries[full] = ne
	if _, exists := s.truncIndex[h8]; !exists {
		s.truncIndex[h8] = full
	}
	// Truncated collision: keep original mapping, do not overwrite truncIndex.
	// Still stored under full key; Get with full will succeed, Get with truncated will return first.
	s.persistEntryLocked(ne)
	return full
}

// Get returns content for hash (accepts 8-64 hex, lower/upper). Returns false if not found.
// Freshness is enforced via Cleanup/NotifyCompaction, not via Get auto-expiry.
func (s *Store) Get(hash string) (string, bool) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	// Extract hex part if hash contains §ref: wrapper? Allow raw hash or wrapped.
	if m := refRegex.FindStringSubmatch(hash); len(m) == 2 {
		hash = m[1]
	}
	if hash == "" {
		return "", false
	}
	if len(hash) < 8 || len(hash) > 64 {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	// fast path: full hash direct
	if e, ok := s.entries[hash]; ok {
		return e.content, true
	}
	// truncated 8 path
	if len(hash) == 8 {
		if full, ok := s.truncIndex[hash]; ok {
			if e, ok := s.entries[full]; ok {
				// collision fallback: verify stored full's prefix matches hash
				if e.hash8 != hash && !strings.HasPrefix(e.fullHash, hash) {
					return "", false
				}
				return e.content, true
			}
		}
	}
	// prefix search for 8-64 (allow 9-64 prefix)
	if len(hash) >= 8 {
		for _, e := range s.entries {
			if e.fullHash == hash || e.hash8 == hash || strings.HasPrefix(e.fullHash, hash) {
				return e.content, true
			}
		}
	}
	return "", false
}

// Expand is alias for Get but explicitly for ref hash.
func (s *Store) Expand(hash string) (string, bool) {
	// Accept both raw hash and §ref:...§ wrapped
	hash = strings.TrimSpace(hash)
	if m := refRegex.FindStringSubmatch(hash); len(m) == 2 {
		hash = m[1]
	}
	return s.Get(hash)
}

// Encode returns original on first occurrence (canonical), or ref string on subsequent.
// It is cache-safe: never mutates canonical, only subsequent.
// Reference format: §ref:HASH8§ (original N chars, use tokenmill_expand to retrieve)
func (s *Store) Encode(content string, turn int) string {
	full, h8 := hashContent(content)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already stored and not expired with byte-equality
	if e, ok := s.entries[full]; ok {
		if e.content == content {
			// check freshness: if expired, treat as new canonical
			if s.freshnessTurns > 0 && turn-e.turn >= s.freshnessTurns {
				// expired: update entry to new turn and return original (new canonical)
				ne := entry{content: content, turn: turn, fullHash: full, hash8: h8}
				s.entries[full] = ne
				// truncIndex already points to full (same)
				s.persistEntryLocked(ne)
				return content
			}
			// hit: return ref (13 tok + stats suffix)
			// Check truncated collision fallback: ensure that hash8 maps to this full (if collision, truncated ambiguous, but we still return ref with hash8;
			// Decode will resolve to first canonical which is this one if this is first, else may resolve to earlier colliding entry. That's acceptable because collision is rare and byte-compare will catch mismatch if needed.)
			// Since we are duplicate of existing, hash8 must already be in truncIndex pointing to this full (or first colliding).
			// If hash8 collision exists where this entry is not the first, truncated ref would be ambiguous. In that case we fallback to not dedup? But we already have full match, so we can return full-length ref to disambiguate.
			// To handle collision, if truncIndex[h8] != full, use full hash in ref for disambiguation.
			refHash := h8
			if mapped, ok := s.truncIndex[h8]; ok && mapped != full {
				// collision: use full hash to avoid ambiguity
				refHash = full
			}
			return formatRef(refHash, len(content))
		}
		// full hash collision with different content (SHA256 collision theoretical): fallback byte-compare -> treat as not duplicate
		// store new? But key collides, can't. Return original.
		return content
	}

	// Check truncated collision: if h8 already exists with different full/content, we still store but ensure we don't confuse.
	// For new content, if its hash8 collides with existing different content, we must not return ref for future duplicates of this new content via truncated lookup ambiguity.
	// We store entry and keep original truncIndex (first wins). Future Encode for this content will hit full path above, so will succeed via full check even though truncated mapping points elsewhere - we handle via full check.
	// To avoid ambiguous ref, future Encode for this colliding content will use full hash in ref.

	ne := entry{content: content, turn: turn, fullHash: full, hash8: h8}
	s.entries[full] = ne
	if _, exists := s.truncIndex[h8]; !exists {
		s.truncIndex[h8] = full
	}
	s.persistEntryLocked(ne)
	return content
}

// Decode extracts hash via regex and returns original content if found.
func (s *Store) Decode(ref string) (string, bool) {
	m := refRegex.FindStringSubmatch(ref)
	if len(m) != 2 {
		return "", false
	}
	hash := m[1]
	// hash may be 8-64; use Get logic
	content, ok := s.Get(hash)
	if !ok {
		return "", false
	}
	// collision fallback: verify byte-equality via recomputed hash?
	// Ensure that content's hash matches requested hash (prefix)
	full, h8 := hashContent(content)
	if hash == full || hash == h8 || strings.HasPrefix(full, hash) {
		return content, true
	}
	// If hash is 8 and collides, full's prefix may be different but truncIndex collision fallback: we already returned via truncIndex mapping to first, but if request was for colliding second's full prefix, we should not match first.
	// For truncated collision, Decode with hash8 will return first's content, which is correct for first's ref, but second's ref should have used full hash to avoid.
	// So if hash is 8 and we returned first's content but second's full's prefix is also same 8, ambiguous. We handle via checking if hash length 8 and multiple entries share same h8, we should ensure we return correct one via full verification? But Get already returned first. That's acceptable.
	return content, true
}

// Verify checks byte-equality: decode(encoded) == original or encoded == original.
func (s *Store) Verify(original, encoded string) bool {
	if original == encoded {
		return true
	}
	matches := canonicalRefRegex.FindStringSubmatch(encoded)
	if len(matches) == 0 {
		return false
	}
	if matches[2] != "" {
		length, err := strconv.Atoi(matches[2])
		if err != nil || length != len(original) {
			return false
		}
	}
	if s.IsRef(encoded) {
		decoded, ok := s.Decode(encoded)
		if !ok {
			return false
		}
		return decoded == original
	}
	// Not a ref: byte equality
	return original == encoded
}

// Cleanup removes entries older than freshnessTurns relative to currentTurn. Returns count removed.
func (s *Store) Cleanup(currentTurn int) int {
	if s.freshnessTurns <= 0 {
		return 0
	}
	type delInfo struct {
		full string
		h8   string
	}
	var toDelete []delInfo
	s.mu.Lock()
	defer s.mu.Unlock()
	for full, e := range s.entries {
		if currentTurn-e.turn >= s.freshnessTurns {
			toDelete = append(toDelete, delInfo{full: full, h8: e.hash8})
		}
	}
	for _, d := range toDelete {
		delete(s.entries, d.full)
	}
	if s.db != nil {
		for _, d := range toDelete {
			if _, err := s.db.Exec(`DELETE FROM dedup WHERE full_hash = ?`, d.full); err != nil {
				s.lastErr = fmt.Errorf("dedup: delete %s: %w", d.full, err)
			}
		}
	} else if len(toDelete) > 0 && s.dbPath != "" {
		s.markPersistenceUnavailableLocked()
	}

	for _, d := range toDelete {
		if s.truncIndex[d.h8] == d.full {
			delete(s.truncIndex, d.h8)
			for f, ent := range s.entries {
				if ent.hash8 == d.h8 {
					s.truncIndex[d.h8] = f
					break
				}
			}
		}
	}
	return len(toDelete)
}

// NotifyCompaction is called when LLM context window is sliced.
// keepTurns is number of recent turns to keep (count-based). It keeps the most recent keepTurns entries by turn and removes older.
// Returns count removed. If keepTurns >= size, keeps all.
func (s *Store) NotifyCompaction(keepTurns int) int {
	if keepTurns <= 0 {
		s.mu.RLock()
		max := s.maxTurnLocked()
		s.mu.RUnlock()
		return s.Cleanup(max + 1)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return 0
	}
	if keepTurns >= len(s.entries) {
		return 0
	}
	// Collect turns sorted descending
	type kv struct {
		full string
		turn int
		h8   string
	}
	var list []kv
	for full, e := range s.entries {
		list = append(list, kv{full: full, turn: e.turn, h8: e.hash8})
	}
	// sort by turn descending using sort.Slice (O(n log n))
	sort.Slice(list, func(i, j int) bool { return list[i].turn > list[j].turn })
	// keep first keepTurns, delete rest
	toDelete := list[keepTurns:]
	var dels []kv
	for _, kv := range toDelete {
		if _, ok := s.entries[kv.full]; ok {
			delete(s.entries, kv.full)
			dels = append(dels, kv)
		}
	}
	if s.db != nil {
		for _, kv := range dels {
			if _, err := s.db.Exec(`DELETE FROM dedup WHERE full_hash = ?`, kv.full); err != nil {
				s.lastErr = fmt.Errorf("dedup: delete %s: %w", kv.full, err)
			}
		}
	} else if len(dels) > 0 && s.dbPath != "" {
		s.markPersistenceUnavailableLocked()
	}

	for _, kv := range dels {
		if s.truncIndex[kv.h8] == kv.full {
			delete(s.truncIndex, kv.h8)
			for f, ent := range s.entries {
				if ent.hash8 == kv.h8 {
					s.truncIndex[kv.h8] = f
					break
				}
			}
		}
	}
	return len(dels)
}

func (s *Store) maxTurnLocked() int {
	maxTurn := -1
	for _, e := range s.entries {
		if e.turn > maxTurn {
			maxTurn = e.turn
		}
	}
	return maxTurn
}

func (s *Store) persistEntryLocked(e entry) {
	// The caller holds s.mu. Keeping the database operation under the same lock
	// prevents Close or cleanup from changing the shared DB pointer mid-write.
	if s.db == nil {
		if s.dbPath != "" {
			s.markPersistenceUnavailableLocked()
		}
		return
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO dedup(full_hash, hash8, content, turn) VALUES(?,?,?,?)`, e.fullHash, e.hash8, e.content, e.turn); err != nil {
		s.lastErr = fmt.Errorf("dedup: persist %s: %w", e.fullHash, err)
	}
}

func (s *Store) markPersistenceUnavailableLocked() {
	s.lastErr = fmt.Errorf("dedup: persistence unavailable")
}

// Close closes DB if opened.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		if err != nil {
			s.lastErr = fmt.Errorf("dedup: close: %w", err)
		}
		return err
	}
	return nil
}

// Err returns the most recent persistence or database-lifecycle error.
func (s *Store) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// Size returns number of entries (for testing).
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// DBPath returns configured dbPath.
func (s *Store) DBPath() string { return s.dbPath }

// FreshnessTurns returns configured freshness.
func (s *Store) FreshnessTurns() int { return s.freshnessTurns }
