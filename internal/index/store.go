package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	appOwnedDir := path == "" || filepath.Clean(path) == filepath.Clean(config.IndexPath())
	if path == "" {
		path = config.IndexPath()
	}
	dir := filepath.Dir(path)
	_, statErr := os.Lstat(dir)
	createdDir := os.IsNotExist(statErr)
	if statErr != nil && !createdDir {
		return nil, statErr
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if appOwnedDir || createdDir {
		info, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("secure index directory: refusing symlink %s", dir)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("secure index directory: %w", err)
		}
	}
	if err := validateIndexFiles(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := chmodIndexFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func validateIndexFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("secure index file %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("secure index file: refusing non-regular target %s", candidate)
		}
	}
	return nil
}

func chmodIndexFiles(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure index database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("secure index sidecar %s: %w", suffix, err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT NOT NULL,
  provider TEXT NOT NULL,
  project_path TEXT,
  title TEXT,
  created_at INTEGER,
  updated_at INTEGER,
  message_count INTEGER,
  storage_path TEXT NOT NULL,
  source_mtime INTEGER NOT NULL,
	  kind TEXT NOT NULL DEFAULT 'root',
	  parent_id TEXT,
	  source_size INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_provider ON sessions(provider);
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT
);
CREATE TABLE IF NOT EXISTS source_files (
  provider TEXT NOT NULL,
  storage_path TEXT NOT NULL,
  source_mtime INTEGER NOT NULL,
  PRIMARY KEY (provider, storage_path)
);
CREATE TABLE IF NOT EXISTS session_sources (
  provider TEXT NOT NULL,
  id TEXT NOT NULL,
  storage_path TEXT NOT NULL,
  project_path TEXT,
  title TEXT,
  created_at INTEGER,
  updated_at INTEGER,
  message_count INTEGER,
  source_mtime INTEGER NOT NULL,
  source_size INTEGER NOT NULL DEFAULT 0,
  source_priority INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL DEFAULT 'root',
  parent_id TEXT,
  PRIMARY KEY (provider, storage_path)
);
CREATE INDEX IF NOT EXISTS idx_session_sources_id ON session_sources(provider, id);
CREATE TABLE IF NOT EXISTS content_index (
  provider TEXT NOT NULL,
  session_id TEXT NOT NULL,
	  storage_path TEXT NOT NULL DEFAULT '',
  source_mtime INTEGER NOT NULL,
  source_size INTEGER NOT NULL DEFAULT 0,
	  session_updated INTEGER NOT NULL DEFAULT 0,
	  message_count INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  error TEXT,
  indexed_at INTEGER NOT NULL,
  PRIMARY KEY (provider, session_id)
);
CREATE VIRTUAL TABLE IF NOT EXISTS session_fts USING fts5(
  provider UNINDEXED,
  session_id UNINDEXED,
  title,
  body,
  tokenize = 'unicode61'
);
CREATE TABLE IF NOT EXISTS migration_dedup (
  provider TEXT NOT NULL,
  origin_digest TEXT NOT NULL,
  session_id TEXT NOT NULL,
  storage_path TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  origin_id TEXT,
  origin_source TEXT,
	  origin_message_count INTEGER NOT NULL DEFAULT 0,
	  legacy INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, origin_digest)
);
CREATE INDEX IF NOT EXISTS idx_migration_dedup_provider ON migration_dedup(provider);
`)
	if err != nil {
		return err
	}
	for _, statement := range []string{
		`ALTER TABLE migration_dedup ADD COLUMN origin_id TEXT`,
		`ALTER TABLE migration_dedup ADD COLUMN origin_source TEXT`,
		`ALTER TABLE migration_dedup ADD COLUMN origin_message_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE migration_dedup ADD COLUMN legacy INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'root'`,
		`ALTER TABLE sessions ADD COLUMN parent_id TEXT`,
		`ALTER TABLE sessions ADD COLUMN source_size INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE content_index ADD COLUMN session_updated INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE content_index ADD COLUMN message_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE content_index ADD COLUMN storage_path TEXT NOT NULL DEFAULT ''`,
	} {
		if err := addColumn(s.db, statement); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`
INSERT OR IGNORE INTO session_sources
  (provider, id, storage_path, project_path, title, created_at, updated_at, message_count,
   source_mtime, source_size, kind, parent_id)
SELECT provider, id, storage_path, project_path, title, created_at, updated_at, message_count,
       source_mtime, source_size, kind, parent_id
FROM sessions`); err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_migration_dedup_origin ON migration_dedup(provider, origin_id, origin_source)`)
	return err
}

func addColumn(db *sql.DB, statement string) error {
	_, err := db.Exec(statement)
	if err == nil || duplicateColumnError(err) {
		return nil
	}
	return err
}

func duplicateColumnError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_ERROR &&
		strings.Contains(sqliteErr.Error(), "duplicate column name:")
}

// RecordMigration stores a successful migration for deduplication (SQLite and JSONL targets).
func (s *Store) RecordMigration(providerID, originDigest, sessionID, storagePath, originID, originSource string) error {
	return s.RecordMigrationSnapshot(providerID, originDigest, sessionID, storagePath, originID, originSource, 0)
}

// RecordMigrationSnapshot stores a current-format, digest-addressed migration.
func (s *Store) RecordMigrationSnapshot(providerID, originDigest, sessionID, storagePath, originID, originSource string, originMessageCount int) error {
	if originDigest == "" || sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`
INSERT INTO migration_dedup (provider, origin_digest, session_id, storage_path, created_at, origin_id, origin_source, origin_message_count, legacy)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
ON CONFLICT(provider, origin_digest) DO UPDATE SET
  session_id=excluded.session_id,
  storage_path=excluded.storage_path,
  created_at=excluded.created_at,
  origin_id=excluded.origin_id,
  origin_source=excluded.origin_source,
  origin_message_count=excluded.origin_message_count,
  legacy=0
`, providerID, originDigest, sessionID, storagePath, time.Now().Unix(), originID, originSource, originMessageCount)
	return err
}

func recordIndexedMigration(tx *sql.Tx, summary model.Summary) error {
	meta := summary.Migration
	if meta == nil || meta.OriginID == "" || summary.ID == "" {
		return nil
	}
	digest, legacy := meta.OriginDigest, 0
	if digest == "" {
		// The sentinel is only a storage key. Legacy lookup is explicitly gated by
		// legacy=1 and matching originMessageCount.
		digest, legacy = "legacy:"+summary.ID, 1
	}
	_, err := tx.Exec(`
INSERT INTO migration_dedup
  (provider, origin_digest, session_id, storage_path, created_at, origin_id, origin_source, origin_message_count, legacy)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, origin_digest) DO UPDATE SET
  session_id=excluded.session_id, storage_path=excluded.storage_path,
  created_at=excluded.created_at, origin_id=excluded.origin_id,
  origin_source=excluded.origin_source,
  origin_message_count=excluded.origin_message_count, legacy=excluded.legacy`,
		summary.Provider, digest, summary.ID, summary.StoragePath, time.Now().Unix(),
		meta.OriginID, meta.OriginSource, meta.OriginMessageCount, legacy)
	return err
}

// FindMigration returns a prior migration target for the same origin digest.
func (s *Store) FindMigration(providerID, originDigest string) (sessionID, storagePath string, ok bool, err error) {
	if originDigest == "" {
		return "", "", false, nil
	}
	err = s.db.QueryRow(`
SELECT session_id, storage_path FROM migration_dedup
WHERE provider = ? AND origin_digest = ? LIMIT 1`, providerID, originDigest).Scan(&sessionID, &storagePath)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return sessionID, storagePath, true, nil
}

// FindMigrationByOrigin returns a prior migration for the same source session id.
func (s *Store) FindMigrationByOrigin(providerID, originID, originSource string) (sessionID, storagePath string, ok bool, err error) {
	if originID == "" {
		return "", "", false, nil
	}
	if originSource != "" {
		err = s.db.QueryRow(`
SELECT session_id, storage_path FROM migration_dedup
WHERE provider = ? AND origin_id = ? AND origin_source = ?
ORDER BY created_at DESC LIMIT 1`, providerID, originID, originSource).Scan(&sessionID, &storagePath)
	} else {
		err = s.db.QueryRow(`
SELECT session_id, storage_path FROM migration_dedup
WHERE provider = ? AND origin_id = ?
ORDER BY created_at DESC LIMIT 1`, providerID, originID).Scan(&sessionID, &storagePath)
	}
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return sessionID, storagePath, true, nil
}

// FindLegacyMigrationByOrigin matches only pre-digest markers and requires the
// same message count. Current digest records never fall back to origin id.
func (s *Store) FindLegacyMigrationByOrigin(providerID, originID, originSource string, originMessageCount int) (sessionID, storagePath string, ok bool, err error) {
	if originID == "" {
		return "", "", false, nil
	}
	err = s.db.QueryRow(`
SELECT session_id, storage_path FROM migration_dedup
WHERE provider = ? AND origin_id = ? AND origin_source = ?
  AND origin_message_count = ? AND legacy = 1
ORDER BY created_at DESC LIMIT 1`, providerID, originID, originSource, originMessageCount).Scan(&sessionID, &storagePath)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return sessionID, storagePath, true, nil
}

func (s *Store) Upsert(summary model.Summary) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertSource(tx, summary); err != nil {
		return err
	}
	if err := recordIndexedMigration(tx, summary); err != nil {
		return err
	}
	if err := rebuildProviderSessions(tx, summary.Provider); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeSummary(summary model.Summary) model.Summary {
	if summary.ProjectPath != "" {
		summary.ProjectPath = util.NormalizeProjectPath(summary.ProjectPath)
	}
	if summary.Kind == "" {
		summary.Kind = model.SessionKindRoot
	}
	return summary
}

func upsertSource(tx *sql.Tx, summary model.Summary) error {
	summary = normalizeSummary(summary)
	_, err := tx.Exec(`
INSERT INTO session_sources
  (provider, id, storage_path, project_path, title, created_at, updated_at, message_count,
   source_mtime, source_size, source_priority, kind, parent_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, storage_path) DO UPDATE SET
  id=excluded.id,
  project_path=excluded.project_path,
  title=excluded.title,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at,
  message_count=excluded.message_count,
  source_mtime=excluded.source_mtime,
  source_size=excluded.source_size,
  source_priority=excluded.source_priority,
  kind=excluded.kind,
  parent_id=excluded.parent_id`,
		summary.Provider, summary.ID, summary.StoragePath, summary.ProjectPath, summary.Title,
		summary.CreatedAt.Unix(), summary.UpdatedAt.Unix(), summary.MessageCount,
		summary.SourceMtime, summary.SourceSize, summary.SourcePriority, summary.Kind, summary.ParentID)
	if err != nil {
		return err
	}
	// Keep the legacy table populated so older Agenthop builds can still decide
	// whether a source changed after a downgrade.
	_, err = tx.Exec(`INSERT INTO source_files (provider, storage_path, source_mtime) VALUES (?, ?, ?)
ON CONFLICT(provider, storage_path) DO UPDATE SET source_mtime=excluded.source_mtime`,
		summary.Provider, summary.StoragePath, summary.SourceMtime)
	return err
}

func rebuildProviderSessions(tx *sql.Tx, providerID string) error {
	if _, err := tx.Exec(`DELETE FROM sessions WHERE provider = ?`, providerID); err != nil {
		return err
	}
	_, err := tx.Exec(`
INSERT INTO sessions
  (id, provider, project_path, title, created_at, updated_at, message_count, storage_path,
   source_mtime, kind, parent_id, source_size)
SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path,
       source_mtime, kind, parent_id, source_size
FROM (
	  SELECT ss.*,
	    ROW_NUMBER() OVER (
	      PARTITION BY provider, id
	      ORDER BY source_priority DESC, updated_at DESC, message_count DESC,
	               source_mtime DESC, storage_path ASC
    ) AS source_rank
  FROM session_sources ss WHERE provider = ?
)
WHERE source_rank = 1`, providerID)
	return err
}

type ListOpts struct {
	Provider         string
	ProjectFilter    string
	ProjectExact     string
	ProjectCWD       string   // sessions whose project_path equals this directory exactly
	ProjectRoots     []string // sessions at or below any root (used for one Git repository's worktrees)
	Limit            int
	Offset           int
	Query            string
	IncludeSubagents bool
}

func (s *Store) listWhere(opts ListOpts) (string, []any) {
	q := ` FROM sessions WHERE 1=1`
	var args []any
	if opts.Provider != "" {
		q += ` AND provider = ?`
		args = append(args, opts.Provider)
	}
	if !opts.IncludeSubagents {
		q += ` AND kind = 'root'`
	}
	if len(opts.ProjectRoots) > 0 {
		q += ` AND (`
		added := 0
		seen := make(map[string]bool)
		for _, root := range opts.ProjectRoots {
			root = util.NormalizeProjectPath(root)
			if root == "" || seen[root] {
				continue
			}
			if added > 0 {
				q += ` OR `
			}
			q += `(project_path = ? OR project_path LIKE ? ESCAPE '\')`
			args = append(args, root, util.EscapeLike(root)+`/%`)
			seen[root] = true
			added++
		}
		if added == 0 {
			q += `0`
		}
		q += `)`
	} else if opts.ProjectCWD != "" {
		norm := util.NormalizeProjectPath(opts.ProjectCWD)
		home := util.HomeDir()
		if home != "" && norm == home {
			q += ` AND project_path LIKE ? ESCAPE '\'`
			args = append(args, util.EscapeLike(norm)+`/%`)
		} else {
			q += ` AND project_path = ?`
			args = append(args, norm)
		}
	} else if opts.ProjectExact != "" {
		norm := util.NormalizeProjectPath(opts.ProjectExact)
		if norm == opts.ProjectExact {
			q += ` AND project_path = ?`
			args = append(args, norm)
		} else {
			q += ` AND (project_path = ? OR project_path = ?)`
			args = append(args, norm, opts.ProjectExact)
		}
	} else if opts.ProjectFilter != "" {
		q += ` AND project_path LIKE ? ESCAPE '\'`
		args = append(args, "%"+util.EscapeLike(opts.ProjectFilter)+"%")
	}
	if opts.Query != "" {
		q += ` AND (id LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR project_path LIKE ? ESCAPE '\')`
		like := "%" + util.EscapeLike(opts.Query) + "%"
		args = append(args, like, like, like)
	}
	return q, args
}

func (s *Store) Count(opts ListOpts) (int, error) {
	where, args := s.listWhere(opts)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*)`+where, args...).Scan(&n)
	return n, err
}

func (s *Store) List(opts ListOpts) ([]model.Summary, error) {
	where, args := s.listWhere(opts)
	q := `SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime, kind, parent_id, source_size` + where
	q += ` ORDER BY updated_at DESC`
	if opts.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, opts.Limit)
	}
	if opts.Offset > 0 {
		q += fmt.Sprintf(` OFFSET %d`, opts.Offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Summary
	for rows.Next() {
		var sm model.Summary
		var created, updated, mtime int64
		if err := rows.Scan(&sm.ID, &sm.Provider, &sm.ProjectPath, &sm.Title, &created, &updated, &sm.MessageCount, &sm.StoragePath, &mtime, &sm.Kind, &sm.ParentID, &sm.SourceSize); err != nil {
			return nil, err
		}
		sm.CreatedAt = time.Unix(created, 0)
		sm.UpdatedAt = time.Unix(updated, 0)
		sm.SourceMtime = mtime
		out = append(out, sm)
	}
	return out, rows.Err()
}

func (s *Store) Get(providerID, id string) (*model.Summary, error) {
	id = strings.TrimSpace(id)
	suffixOnly := strings.HasPrefix(id, "…") || strings.HasPrefix(id, "...")
	id = strings.TrimPrefix(strings.TrimPrefix(id, "…"), "...")
	if id == "" {
		return nil, provider.ErrNotFound
	}
	if suffixOnly {
		return s.matchIDs(providerID, `id LIKE ? ESCAPE '\'`, "%"+util.EscapeLike(id), id, true)
	}
	if sm, err := s.scanSummary(`SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime, kind, parent_id, source_size
FROM sessions WHERE provider = ? AND id = ? ORDER BY updated_at DESC LIMIT 1`, providerID, id); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	if sm, err := s.matchIDs(providerID, `id LIKE ? ESCAPE '\'`, util.EscapeLike(id)+"%", id, true); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	return s.matchIDs(providerID, `id LIKE ? ESCAPE '\'`, "%"+util.EscapeLike(id), id, true)
}

func (s *Store) FindByID(id string) (*model.Summary, error) {
	id = strings.TrimSpace(id)
	suffixOnly := strings.HasPrefix(id, "…") || strings.HasPrefix(id, "...")
	id = strings.TrimPrefix(strings.TrimPrefix(id, "…"), "...")
	if id == "" {
		return nil, provider.ErrNotFound
	}
	if suffixOnly {
		return s.matchIDs("", `id LIKE ? ESCAPE '\'`, "%"+util.EscapeLike(id), id, false)
	}
	if sm, err := s.matchIDs("", `id = ?`, id, id, false); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	if sm, err := s.matchIDs("", `id LIKE ? ESCAPE '\'`, util.EscapeLike(id)+"%", id, false); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	return s.matchIDs("", `id LIKE ? ESCAPE '\'`, "%"+util.EscapeLike(id), id, false)
}

func (s *Store) matchIDs(providerID, predicate, value, queryID string, withProvider bool) (*model.Summary, error) {
	var rows *sql.Rows
	var err error
	if withProvider {
		rows, err = s.db.Query(`
SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime, kind, parent_id, source_size
FROM sessions WHERE provider = ? AND `+predicate+` ORDER BY updated_at DESC`, providerID, value)
	} else {
		rows, err = s.db.Query(`
SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime, kind, parent_id, source_size
FROM sessions WHERE `+predicate+` ORDER BY updated_at DESC`, value)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []model.Summary
	for rows.Next() {
		sm, err := s.scanSummaryRow(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, *sm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, provider.ErrNotFound
	case 1:
		return &matches[0], nil
	default:
		if withProvider {
			return nil, fmt.Errorf("ambiguous session id %q (%d matches) for provider %s; use a longer id", queryID, len(matches), providerID)
		}
		return nil, fmt.Errorf("ambiguous session id %q (%d matches); use a longer id", queryID, len(matches))
	}
}

func (s *Store) scanSummary(query string, args ...any) (*model.Summary, error) {
	row := s.db.QueryRow(query, args...)
	sm, err := s.scanSummaryFromRow(row)
	if err == sql.ErrNoRows {
		return nil, provider.ErrNotFound
	}
	return sm, err
}

func (s *Store) scanSummaryRow(rows *sql.Rows) (*model.Summary, error) {
	var sm model.Summary
	var created, updated, mtime int64
	if err := rows.Scan(&sm.ID, &sm.Provider, &sm.ProjectPath, &sm.Title, &created, &updated, &sm.MessageCount, &sm.StoragePath, &mtime, &sm.Kind, &sm.ParentID, &sm.SourceSize); err != nil {
		return nil, err
	}
	sm.CreatedAt = time.Unix(created, 0)
	sm.UpdatedAt = time.Unix(updated, 0)
	sm.SourceMtime = mtime
	return &sm, nil
}

func (s *Store) scanSummaryFromRow(row *sql.Row) (*model.Summary, error) {
	var sm model.Summary
	var created, updated, mtime int64
	if err := row.Scan(&sm.ID, &sm.Provider, &sm.ProjectPath, &sm.Title, &created, &updated, &sm.MessageCount, &sm.StoragePath, &mtime, &sm.Kind, &sm.ParentID, &sm.SourceSize); err != nil {
		return nil, err
	}
	sm.CreatedAt = time.Unix(created, 0)
	sm.UpdatedAt = time.Unix(updated, 0)
	sm.SourceMtime = mtime
	return &sm, nil
}

func (s *Store) CountByProvider() (map[string]int, error) {
	return s.CountByProviderFiltered(ListOpts{IncludeSubagents: true})
}

// CountByProviderFiltered counts sessions in the same scope used by List.
func (s *Store) CountByProviderFiltered(opts ListOpts) (map[string]int, error) {
	opts.Provider = ""
	where, args := s.listWhere(opts)
	rows, err := s.db.Query(`SELECT provider, COUNT(*)`+where+` GROUP BY provider`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

// KeepProviders removes disabled providers from every user-visible index table.
// Native session data is untouched; re-enabling a provider discovers it again.
func (s *Store) KeepProviders(enabled []string) error {
	keep := make(map[string]bool, len(enabled))
	for _, id := range enabled {
		keep[id] = true
	}
	rows, err := s.db.Query(`SELECT DISTINCT provider FROM session_sources UNION SELECT DISTINCT provider FROM sessions`)
	if err != nil {
		return err
	}
	var remove []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			remove = append(remove, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range remove {
		for _, query := range []string{
			`DELETE FROM session_fts WHERE provider = ?`,
			`DELETE FROM content_index WHERE provider = ?`,
			`DELETE FROM sessions WHERE provider = ?`,
			`DELETE FROM session_sources WHERE provider = ?`,
			`DELETE FROM source_files WHERE provider = ?`,
		} {
			if _, err := tx.Exec(query, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM meta WHERE key = ?`, discoverCountMetaKey(id)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) NeedsRefresh(providerID string, storagePath string, mtime int64) (bool, error) {
	var existing int64
	err := s.db.QueryRow(`SELECT source_mtime FROM source_files WHERE provider = ? AND storage_path = ? LIMIT 1`, providerID, storagePath).Scan(&existing)
	if err == sql.ErrNoRows {
		err = s.db.QueryRow(`SELECT source_mtime FROM sessions WHERE provider = ? AND storage_path = ? LIMIT 1`, providerID, storagePath).Scan(&existing)
	}
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return existing != mtime, nil
}

func (s *Store) NeedsSourceRefresh(providerID, storagePath string, mtime, size int64) (bool, error) {
	var existingMtime, existingSize int64
	err := s.db.QueryRow(`SELECT source_mtime, source_size FROM session_sources
WHERE provider = ? AND storage_path = ?`, providerID, storagePath).Scan(&existingMtime, &existingSize)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return existingMtime != mtime || existingSize != size, nil
}

// AnyInstalledUnindexed reports whether an installed provider has no sessions in the index.
func AnyInstalledUnindexed(reg *registry.Registry, store *Store) bool {
	counts, err := store.CountByProvider()
	if err != nil {
		return true
	}
	for _, p := range reg.Installed() {
		if counts[p.ID()] == 0 {
			return true
		}
	}
	return false
}

func discoverCountMetaKey(providerID string) string {
	return "discover_unique:" + providerID
}

func uniqueSummaryIDs(summaries []model.Summary) int {
	seen := make(map[string]struct{}, len(summaries))
	for _, sm := range summaries {
		seen[sm.ID] = struct{}{}
	}
	return len(seen)
}

// IndexMetadataMissing reports whether discover metadata has not been recorded yet.
func IndexMetadataMissing(reg *registry.Registry, store *Store) bool {
	for _, p := range reg.Installed() {
		meta, _ := store.GetMeta(discoverCountMetaKey(p.ID()))
		if meta == "" {
			return true
		}
	}
	return false
}

// IndexBehindDiscover reports whether indexed counts trail the last discover scan.
func IndexBehindDiscover(reg *registry.Registry, store *Store) bool {
	counts, err := store.CountByProvider()
	if err != nil {
		return true
	}
	for _, p := range reg.Installed() {
		meta, _ := store.GetMeta(discoverCountMetaKey(p.ID()))
		if meta == "" {
			continue
		}
		expected, err := strconv.Atoi(meta)
		if err != nil {
			continue
		}
		if counts[p.ID()] < expected {
			return true
		}
	}
	return false
}

// LastUpdateStale reports whether the index has not been updated recently.
func (s *Store) LastUpdateStale(maxAge time.Duration) bool {
	last, _ := s.GetMeta("last_update")
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return time.Since(t) > maxAge
}

// NeedsIncrementalIndex reports whether an incremental scan should run before listing.
func NeedsIncrementalIndex(reg *registry.Registry, store *Store, maxAge time.Duration) bool {
	if AnyInstalledUnindexed(reg, store) {
		return true
	}
	if IndexMetadataMissing(reg, store) {
		return true
	}
	if IndexBehindDiscover(reg, store) {
		return true
	}
	return store.LastUpdateStale(maxAge)
}

func recordDiscoverMeta(store *Store, providerID string, summaries []model.Summary) error {
	return store.SetMeta(discoverCountMetaKey(providerID), strconv.Itoa(uniqueSummaryIDs(summaries)))
}

func Rebuild(ctx context.Context, reg *registry.Registry, store *Store, providerFilter string) (int, error) {
	total := 0
	for _, p := range reg.All() {
		if providerFilter != "" && p.ID() != providerFilter {
			continue
		}
		if !p.Installed() {
			if err := store.reconcileProvider(p.ID(), nil, map[string]struct{}{}); err != nil {
				return total, err
			}
			_ = recordDiscoverMeta(store, p.ID(), nil)
			continue
		}
		summaries, err := p.Discover(ctx, provider.DiscoverOpts{})
		if err != nil {
			return total, fmt.Errorf("%s: %w", p.ID(), err)
		}
		summaries = dropTitlerSessions(summaries)
		seen := make(map[string]struct{}, len(summaries))
		for _, sm := range summaries {
			seen[sm.StoragePath] = struct{}{}
		}
		if err := store.reconcileProvider(p.ID(), summaries, seen); err != nil {
			return total, err
		}
		_ = recordDiscoverMeta(store, p.ID(), summaries)
		total += len(summaries)
	}
	if n, err := store.PruneTitlerSessions(); err == nil {
		total -= n
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_ = store.SetMeta("last_rebuild", now)
	_ = store.SetMeta("last_update", now)
	return total, nil
}

func UpdateIncremental(ctx context.Context, reg *registry.Registry, store *Store, providerFilter string) (int, error) {
	total := 0
	for _, p := range reg.All() {
		if providerFilter != "" && p.ID() != providerFilter {
			continue
		}
		if !p.Installed() {
			if err := store.reconcileProvider(p.ID(), nil, map[string]struct{}{}); err != nil {
				return total, err
			}
			_ = recordDiscoverMeta(store, p.ID(), nil)
			continue
		}
		pid := p.ID()
		seen := make(map[string]struct{})
		skip := func(storagePath string, mtime int64) bool {
			seen[storagePath] = struct{}{}
			need, err := store.NeedsRefresh(pid, storagePath, mtime)
			return err == nil && !need
		}
		skipSource := func(storagePath string, mtime, size int64) bool {
			seen[storagePath] = struct{}{}
			need, err := store.NeedsSourceRefresh(pid, storagePath, mtime, size)
			return err == nil && !need
		}
		summaries, err := p.Discover(ctx, provider.DiscoverOpts{SkipUnchanged: skip, SkipSource: skipSource})
		if err != nil {
			return total, fmt.Errorf("%s: %w", pid, err)
		}
		// Dropping them from seen as well is what removes rows indexed
		// before this filter existed: reconcile deletes any indexed path the
		// scan did not claim.
		summaries = dropTitlerSessions(summaries)
		for _, sm := range summaries {
			seen[sm.StoragePath] = struct{}{}
		}
		if err := store.reconcileProvider(pid, summaries, seen); err != nil {
			return total, err
		}
		total += len(summaries)
		// Skipped files never reach summaries, so record the indexed count
		// rather than this scan's (partial) discover count.
		if n, err := store.Count(ListOpts{Provider: pid, IncludeSubagents: true}); err == nil {
			_ = store.SetMeta(discoverCountMetaKey(pid), strconv.Itoa(n))
		}
	}
	// An unchanged file is skipped before it is ever examined, so leftovers
	// indexed by an older build would otherwise never be reconsidered.
	if n, err := store.PruneTitlerSessions(); err == nil {
		total -= n
	}
	_ = store.SetMeta("last_update", time.Now().UTC().Format(time.RFC3339))
	return total, nil
}

// dropTitlerSessions removes the sessions another created itself while asking
// an agent for a title. Several agents record a headless run with no way to
// opt out, so their leftovers are dropped on the way into the index instead of
// cluttering the list another exists to tidy.
func dropTitlerSessions(summaries []model.Summary) []model.Summary {
	out := summaries[:0]
	for _, sm := range summaries {
		if titler.IsGeneratedSession(sm.Title, sm.ProjectPath) {
			continue
		}
		out = append(out, sm)
	}
	return out
}

// PruneTitlerSessions evicts title-generation leftovers that were indexed
// before they were filtered, or that an incremental scan skipped as unchanged
// and therefore never re-examined. It only touches another's own index: the
// agent's session files stay where that agent put them.
func (s *Store) PruneTitlerSessions() (int, error) {
	// Every marker another has ever opened a prompt with, not just the
	// current one: leftovers keep the title the agent derived at the time.
	markers := titler.PromptMarkers()
	clauses := make([]string, 0, len(markers)+1)
	args := make([]any, 0, len(markers)+1)
	for _, marker := range markers {
		clauses = append(clauses, "title LIKE ?")
		args = append(args, marker+"%")
	}
	clauses = append(clauses, "project_path LIKE ?")
	args = append(args, "%/"+titler.TempDirPrefix+"%")
	rows, err := s.db.Query(
		`SELECT provider, storage_path FROM session_sources WHERE `+strings.Join(clauses, " OR "),
		args...)
	if err != nil {
		return 0, err
	}
	byProvider := map[string][]string{}
	total := 0
	for rows.Next() {
		var providerID, path string
		if err := rows.Scan(&providerID, &path); err != nil {
			rows.Close()
			return 0, err
		}
		byProvider[providerID] = append(byProvider[providerID], path)
		total++
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for providerID, paths := range byProvider {
		if err := s.dropProviderSources(providerID, paths); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// dropProviderSources deletes indexed sources and repairs everything derived
// from them, in one transaction.
func (s *Store) dropProviderSources(providerID string, paths []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := dropSources(tx, providerID, paths); err != nil {
		return err
	}
	if err := rebuildProviderSessions(tx, providerID); err != nil {
		return err
	}
	if err := pruneDerived(tx, providerID); err != nil {
		return err
	}
	return tx.Commit()
}

func dropSources(tx *sql.Tx, providerID string, paths []string) error {
	for _, path := range paths {
		if _, err := tx.Exec(`DELETE FROM session_sources WHERE provider = ? AND storage_path = ?`, providerID, path); err != nil {
			return err
		}
		_, _ = tx.Exec(`DELETE FROM source_files WHERE provider = ? AND storage_path = ?`, providerID, path)
	}
	return nil
}

func (s *Store) reconcileProvider(providerID string, changed []model.Summary, seen map[string]struct{}) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sm := range changed {
		if err := upsertSource(tx, sm); err != nil {
			return err
		}
		if err := recordIndexedMigration(tx, sm); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT storage_path FROM session_sources WHERE provider = ?`, providerID)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		if _, ok := seen[path]; !ok {
			stale = append(stale, path)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := dropSources(tx, providerID, stale); err != nil {
		return err
	}
	if err := rebuildProviderSessions(tx, providerID); err != nil {
		return err
	}
	if err := pruneDerived(tx, providerID); err != nil {
		return err
	}
	return tx.Commit()
}

// pruneDerived drops search and content rows that no longer match a live
// session row: they must not survive a deletion or a change of canonical
// source.
func pruneDerived(tx *sql.Tx, providerID string) error {
	if _, err := tx.Exec(`DELETE FROM session_fts
WHERE provider = ? AND NOT EXISTS (
  SELECT 1 FROM sessions s JOIN content_index c
    ON c.provider=s.provider AND c.session_id=s.id
	  WHERE s.provider=session_fts.provider AND s.id=session_fts.session_id
	    AND c.storage_path=s.storage_path AND c.source_mtime=s.source_mtime
	    AND c.source_size=s.source_size AND c.session_updated=s.updated_at
	    AND c.message_count=s.message_count AND c.status='ready'
)`, providerID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM content_index
WHERE provider = ? AND NOT EXISTS (
  SELECT 1 FROM sessions s
  WHERE s.provider=content_index.provider AND s.id=content_index.session_id
	    AND s.storage_path=content_index.storage_path
	    AND s.source_mtime=content_index.source_mtime
	    AND s.source_size=content_index.source_size
	    AND s.updated_at=content_index.session_updated
	    AND s.message_count=content_index.message_count
)`, providerID)
	return err
}
