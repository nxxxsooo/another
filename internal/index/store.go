package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/config"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/CyrusSE/agenthop/internal/util"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = config.IndexPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	return s, nil
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
CREATE TABLE IF NOT EXISTS migration_dedup (
  provider TEXT NOT NULL,
  origin_digest TEXT NOT NULL,
  session_id TEXT NOT NULL,
  storage_path TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  origin_id TEXT,
  origin_source TEXT,
  PRIMARY KEY (provider, origin_digest)
);
CREATE INDEX IF NOT EXISTS idx_migration_dedup_provider ON migration_dedup(provider);
`)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`ALTER TABLE migration_dedup ADD COLUMN origin_id TEXT`)
	_, _ = s.db.Exec(`ALTER TABLE migration_dedup ADD COLUMN origin_source TEXT`)
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_migration_dedup_origin ON migration_dedup(provider, origin_id, origin_source)`)
	return err
}

// RecordMigration stores a successful migration for deduplication (SQLite and JSONL targets).
func (s *Store) RecordMigration(providerID, originDigest, sessionID, storagePath, originID, originSource string) error {
	if originDigest == "" || sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`
INSERT INTO migration_dedup (provider, origin_digest, session_id, storage_path, created_at, origin_id, origin_source)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, origin_digest) DO UPDATE SET
  session_id=excluded.session_id,
  storage_path=excluded.storage_path,
  created_at=excluded.created_at,
  origin_id=excluded.origin_id,
  origin_source=excluded.origin_source
`, providerID, originDigest, sessionID, storagePath, time.Now().Unix(), originID, originSource)
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

func (s *Store) Upsert(summary model.Summary) error {
	if summary.ProjectPath != "" {
		summary.ProjectPath = util.NormalizeProjectPath(summary.ProjectPath)
	}
	_, err := s.db.Exec(`
INSERT INTO sessions (id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, id) DO UPDATE SET
  project_path=excluded.project_path,
  title=excluded.title,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at,
  message_count=excluded.message_count,
  storage_path=excluded.storage_path,
  source_mtime=excluded.source_mtime
`, summary.ID, summary.Provider, summary.ProjectPath, summary.Title,
		summary.CreatedAt.Unix(), summary.UpdatedAt.Unix(), summary.MessageCount,
		summary.StoragePath, summary.SourceMtime)
	if err != nil {
		return err
	}
	// Several files can share one session id (resumed sessions); freshness is
	// tracked per file so incremental scans can skip every unchanged path.
	_, err = s.db.Exec(`INSERT INTO source_files (provider, storage_path, source_mtime) VALUES (?, ?, ?)
ON CONFLICT(provider, storage_path) DO UPDATE SET source_mtime=excluded.source_mtime`,
		summary.Provider, summary.StoragePath, summary.SourceMtime)
	return err
}

type ListOpts struct {
	Provider      string
	ProjectFilter string
	ProjectExact  string
	ProjectCWD    string // sessions whose project_path equals this directory exactly
	Limit         int
	Offset        int
	Query         string
}

func (s *Store) listWhere(opts ListOpts) (string, []any) {
	q := ` FROM sessions WHERE 1=1`
	var args []any
	if opts.Provider != "" {
		q += ` AND provider = ?`
		args = append(args, opts.Provider)
	}
	if opts.ProjectCWD != "" {
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
	q := `SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime` + where
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
		if err := rows.Scan(&sm.ID, &sm.Provider, &sm.ProjectPath, &sm.Title, &created, &updated, &sm.MessageCount, &sm.StoragePath, &mtime); err != nil {
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
	if id == "" {
		return nil, provider.ErrNotFound
	}
	if sm, err := s.scanSummary(`SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime
FROM sessions WHERE provider = ? AND id = ? ORDER BY updated_at DESC LIMIT 1`, providerID, id); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	if sm, err := s.scanSummary(`SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime
FROM sessions WHERE provider = ? AND id LIKE ? ORDER BY updated_at DESC LIMIT 1`, providerID, id+"%"); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	return s.matchBySuffix(providerID, "%"+id, id, true)
}

func (s *Store) FindByID(id string) (*model.Summary, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, provider.ErrNotFound
	}
	if sm, err := s.scanSummary(`SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime
FROM sessions WHERE id = ? ORDER BY updated_at DESC LIMIT 1`, id); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	if sm, err := s.scanSummary(`SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime
FROM sessions WHERE id LIKE ? ORDER BY updated_at DESC LIMIT 1`, id+"%"); err == nil {
		return sm, nil
	} else if err != provider.ErrNotFound {
		return nil, err
	}
	return s.matchBySuffix("", "%"+id, id, false)
}

func (s *Store) matchBySuffix(providerID, likePattern, queryID string, withProvider bool) (*model.Summary, error) {
	var rows *sql.Rows
	var err error
	if withProvider {
		rows, err = s.db.Query(`
SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime
FROM sessions WHERE provider = ? AND id LIKE ? ORDER BY updated_at DESC`, providerID, likePattern)
	} else {
		rows, err = s.db.Query(`
SELECT id, provider, project_path, title, created_at, updated_at, message_count, storage_path, source_mtime
FROM sessions WHERE id LIKE ? ORDER BY updated_at DESC`, likePattern)
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
	if err := rows.Scan(&sm.ID, &sm.Provider, &sm.ProjectPath, &sm.Title, &created, &updated, &sm.MessageCount, &sm.StoragePath, &mtime); err != nil {
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
	if err := row.Scan(&sm.ID, &sm.Provider, &sm.ProjectPath, &sm.Title, &created, &updated, &sm.MessageCount, &sm.StoragePath, &mtime); err != nil {
		return nil, err
	}
	sm.CreatedAt = time.Unix(created, 0)
	sm.UpdatedAt = time.Unix(updated, 0)
	sm.SourceMtime = mtime
	return &sm, nil
}

func (s *Store) CountByProvider() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT provider, COUNT(*) FROM sessions GROUP BY provider`)
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
	if providerFilter != "" {
		if _, err := store.db.Exec(`DELETE FROM sessions WHERE provider = ?`, providerFilter); err != nil {
			return 0, err
		}
		_, _ = store.db.Exec(`DELETE FROM source_files WHERE provider = ?`, providerFilter)
	} else {
		if _, err := store.db.Exec(`DELETE FROM sessions`); err != nil {
			return 0, err
		}
		_, _ = store.db.Exec(`DELETE FROM source_files`)
	}
	total := 0
	for _, p := range reg.All() {
		if providerFilter != "" && p.ID() != providerFilter {
			continue
		}
		if !p.Installed() {
			continue
		}
		summaries, err := p.Discover(ctx, provider.DiscoverOpts{})
		if err != nil {
			return total, fmt.Errorf("%s: %w", p.ID(), err)
		}
		_ = recordDiscoverMeta(store, p.ID(), summaries)
		for _, sm := range summaries {
			if err := store.Upsert(sm); err != nil {
				return total, err
			}
			total++
		}
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
			continue
		}
		pid := p.ID()
		skip := func(storagePath string, mtime int64) bool {
			need, err := store.NeedsRefresh(pid, storagePath, mtime)
			return err == nil && !need
		}
		summaries, err := p.Discover(ctx, provider.DiscoverOpts{SkipUnchanged: skip})
		if err != nil {
			return total, fmt.Errorf("%s: %w", pid, err)
		}
		for _, sm := range summaries {
			need, err := store.NeedsRefresh(sm.Provider, sm.StoragePath, sm.SourceMtime)
			if err != nil || need {
				if err := store.Upsert(sm); err != nil {
					return total, err
				}
				total++
			}
		}
		// Skipped files never reach summaries, so record the indexed count
		// rather than this scan's (partial) discover count.
		if n, err := store.Count(ListOpts{Provider: pid}); err == nil {
			_ = store.SetMeta(discoverCountMetaKey(pid), strconv.Itoa(n))
		}
	}
	_ = store.SetMeta("last_update", time.Now().UTC().Format(time.RFC3339))
	return total, nil
}
