package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/CyrusSE/agenthop/internal/util"
)

type SearchOpts struct {
	Query            string
	Provider         string
	ProjectFilter    string
	ProjectCWD       string
	IncludeSubagents bool
	Limit            int
	Offset           int
}

type SearchHit struct {
	Session   model.Summary `json:"session"`
	MatchType string        `json:"match_type"`
	Snippet   string        `json:"snippet,omitempty"`
}

type ContentIndexStatus struct {
	Indexed int `json:"indexed"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// IndexConversation replaces the searchable text for one canonical session.
// Only user and assistant text is retained; tool and system payloads are omitted.
func (s *Store) IndexConversation(summary model.Summary, conv *model.Conversation) error {
	var body strings.Builder
	for _, message := range conv.Messages {
		if message.Role != model.RoleUser && message.Role != model.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(message.PlainText())
		if message.Role == model.RoleUser {
			var ok bool
			text, ok = util.NormalizeUserText(text)
			if !ok {
				continue
			}
		} else if text == "" {
			continue
		}
		if body.Len() > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(string(message.Role))
		body.WriteString(": ")
		body.WriteString(text)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM session_fts WHERE provider = ? AND session_id = ?`, summary.Provider, summary.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO session_fts(provider, session_id, title, body) VALUES (?, ?, ?, ?)`,
		summary.Provider, summary.ID, summary.Title, body.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO content_index
  (provider, session_id, storage_path, source_mtime, source_size, session_updated, message_count, status, error, indexed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'ready', NULL, ?)
ON CONFLICT(provider, session_id) DO UPDATE SET
  storage_path=excluded.storage_path, source_mtime=excluded.source_mtime, source_size=excluded.source_size,
  session_updated=excluded.session_updated, message_count=excluded.message_count,
  status='ready', error=NULL, indexed_at=excluded.indexed_at`,
		summary.Provider, summary.ID, summary.StoragePath, summary.SourceMtime, summary.SourceSize,
		summary.UpdatedAt.Unix(), summary.MessageCount, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recordContentError(summary model.Summary, indexErr error) error {
	_, err := s.db.Exec(`INSERT INTO content_index
  (provider, session_id, storage_path, source_mtime, source_size, session_updated, message_count, status, error, indexed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'error', ?, ?)
ON CONFLICT(provider, session_id) DO UPDATE SET
  storage_path=excluded.storage_path, source_mtime=excluded.source_mtime, source_size=excluded.source_size,
  session_updated=excluded.session_updated, message_count=excluded.message_count,
  status='error', error=excluded.error, indexed_at=excluded.indexed_at`,
		summary.Provider, summary.ID, summary.StoragePath, summary.SourceMtime, summary.SourceSize,
		summary.UpdatedAt.Unix(), summary.MessageCount, indexErr.Error(), time.Now().Unix())
	return err
}

// IndexPendingContent indexes changed or missing canonical sessions sequentially.
// Failed unchanged sources are not retried unless retryErrors is true.
func (s *Store) IndexPendingContent(ctx context.Context, reg *registry.Registry, limit int, retryErrors bool) (indexed, failed int, err error) {
	query := `SELECT s.id, s.provider, s.project_path, s.title, s.created_at, s.updated_at,
       s.message_count, s.storage_path, s.source_mtime, s.kind, s.parent_id, s.source_size
FROM sessions s LEFT JOIN content_index c
  ON c.provider=s.provider AND c.session_id=s.id
WHERE c.session_id IS NULL OR c.session_updated<>s.updated_at OR c.message_count<>s.message_count`
	query += ` OR c.storage_path<>s.storage_path OR c.source_mtime<>s.source_mtime OR c.source_size<>s.source_size`
	if retryErrors {
		query += ` OR c.status='error'`
	}
	query += ` ORDER BY s.updated_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return 0, 0, err
	}
	var pending []model.Summary
	for rows.Next() {
		sm, scanErr := s.scanSummaryRow(rows)
		if scanErr != nil {
			rows.Close()
			return indexed, failed, scanErr
		}
		pending = append(pending, *sm)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return indexed, failed, err
	}
	if err := rows.Close(); err != nil {
		return indexed, failed, err
	}
	for _, sm := range pending {
		if err := ctx.Err(); err != nil {
			return indexed, failed, err
		}
		p, lookupErr := reg.Get(sm.Provider)
		if lookupErr != nil {
			failed++
			_ = s.recordContentError(sm, lookupErr)
			continue
		}
		conv, loadErr := p.Load(ctx, provider.SessionRef{
			ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
		})
		if loadErr != nil {
			failed++
			_ = s.recordContentError(sm, loadErr)
			continue
		}
		if indexErr := s.IndexConversation(sm, conv); indexErr != nil {
			failed++
			_ = s.recordContentError(sm, indexErr)
			continue
		}
		indexed++
	}
	return indexed, failed, nil
}

func ftsQuery(raw string) string {
	var tokens []string
	for _, token := range strings.FieldsFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	}) {
		if token != "" {
			tokens = append(tokens, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
		}
	}
	return strings.Join(tokens, " AND ")
}

func (s *Store) Search(opts SearchOpts) ([]SearchHit, error) {
	rawQuery := strings.TrimSpace(opts.Query)
	if rawQuery == "" {
		return nil, nil
	}
	want := opts.Limit
	if want <= 0 {
		want = 50
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	where, args := s.listWhere(ListOpts{
		Provider: opts.Provider, ProjectFilter: opts.ProjectFilter, ProjectCWD: opts.ProjectCWD,
		IncludeSubagents: opts.IncludeSubagents,
	})
	like := "%" + util.EscapeLike(rawQuery) + "%"
	args = append(args, like, like, like)
	match := ftsQuery(rawQuery)
	sqlQuery := `WITH eligible AS (
  SELECT *` + where + `
), matches AS (
  SELECT provider, id, 1 AS metadata, 0 AS content, title AS snippet
  FROM eligible
  WHERE id LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR project_path LIKE ? ESCAPE '\'`
	if match != "" {
		sqlQuery += `
	  UNION ALL
	  SELECT s.provider, s.id, 0, 1, snippet(session_fts, 3, '[', ']', ' … ', 24)
	  FROM session_fts
	  JOIN eligible s ON s.provider=session_fts.provider AND s.id=session_fts.session_id
	  JOIN content_index c ON c.provider=s.provider AND c.session_id=s.id
	    AND c.status='ready' AND c.storage_path=s.storage_path
	    AND c.source_mtime=s.source_mtime AND c.source_size=s.source_size
	    AND c.session_updated=s.updated_at AND c.message_count=s.message_count
	  WHERE session_fts MATCH ?`
		args = append(args, match)
	}
	sqlQuery += `
), combined AS (
  SELECT provider, id, MAX(metadata) AS metadata, MAX(content) AS content,
         MAX(CASE WHEN content=1 THEN snippet ELSE '' END) AS content_snippet
  FROM matches GROUP BY provider, id
)
SELECT s.id, s.provider, s.project_path, s.title, s.created_at, s.updated_at,
       s.message_count, s.storage_path, s.source_mtime, s.kind, s.parent_id, s.source_size,
       c.metadata, c.content, c.content_snippet
FROM combined c JOIN eligible s ON s.provider=c.provider AND s.id=c.id
ORDER BY s.updated_at DESC, s.provider, s.id
LIMIT ? OFFSET ?`
	args = append(args, want, offset)
	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var hit SearchHit
		var created, updated, mtime int64
		var metadata, content int
		if err := rows.Scan(
			&hit.Session.ID, &hit.Session.Provider, &hit.Session.ProjectPath, &hit.Session.Title,
			&created, &updated, &hit.Session.MessageCount, &hit.Session.StoragePath, &mtime,
			&hit.Session.Kind, &hit.Session.ParentID, &hit.Session.SourceSize,
			&metadata, &content, &hit.Snippet,
		); err != nil {
			return nil, err
		}
		hit.Session.CreatedAt = time.Unix(created, 0)
		hit.Session.UpdatedAt = time.Unix(updated, 0)
		hit.Session.SourceMtime = mtime
		switch {
		case metadata != 0 && content != 0:
			hit.MatchType = "metadata+content"
		case content != 0:
			hit.MatchType = "content"
		default:
			hit.MatchType = "metadata"
			hit.Snippet = hit.Session.Title
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (s *Store) ContentStatus() (ContentIndexStatus, error) {
	var status ContentIndexStatus
	err := s.db.QueryRow(`SELECT
	  COALESCE(SUM(CASE WHEN c.status='ready' AND c.storage_path=s.storage_path AND c.source_mtime=s.source_mtime AND c.source_size=s.source_size AND c.session_updated=s.updated_at AND c.message_count=s.message_count THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN c.status='error' AND c.storage_path=s.storage_path AND c.source_mtime=s.source_mtime AND c.source_size=s.source_size AND c.session_updated=s.updated_at AND c.message_count=s.message_count THEN 1 ELSE 0 END), 0),
	  COALESCE(SUM(CASE WHEN c.session_id IS NULL OR c.storage_path<>s.storage_path OR c.source_mtime<>s.source_mtime OR c.source_size<>s.source_size OR c.session_updated<>s.updated_at OR c.message_count<>s.message_count THEN 1 ELSE 0 END), 0)
FROM sessions s LEFT JOIN content_index c ON c.provider=s.provider AND c.session_id=s.id`).Scan(
		&status.Indexed, &status.Failed, &status.Pending)
	if err == sql.ErrNoRows {
		return ContentIndexStatus{}, nil
	}
	return status, err
}
