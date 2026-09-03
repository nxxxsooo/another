package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/config"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const ProviderID = "opencode"

type Provider struct {
	dbPath string
}

func New() *Provider {
	root := config.EnvOrDefault("XDG_DATA_HOME", filepath.Join(config.HomeDir(), ".local", "share"))
	return &Provider{dbPath: filepath.Join(root, "opencode", "opencode.db")}
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "OpenCode" }
func (p *Provider) Installed() bool {
	_, err := os.Stat(p.dbPath)
	return err == nil
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "database", Path: p.dbPath}}
}

func (p *Provider) openRO() (*sql.DB, error) {
	return sql.Open("sqlite", p.dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	hasParent := sqliteColumnExists(db, "session", "parent_id")
	parentExpr := "NULL"
	if hasParent {
		parentExpr = "parent_id"
	}
	rows, err := db.Query(`SELECT id, directory, title, time_created, time_updated, ` + parentExpr + ` FROM session ORDER BY time_updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st, statErr := os.Stat(p.dbPath)
	if statErr != nil {
		return nil, statErr
	}
	stampTime, stampSize, err := provider.SQLiteSourceStamp(p.dbPath, st)
	if err != nil {
		return nil, err
	}
	mtime := stampTime.UnixNano()
	var out []model.Summary
	for rows.Next() {
		var id string
		var dir, title, parentID sql.NullString
		var created, updated int64
		if err := rows.Scan(&id, &dir, &title, &created, &updated, &parentID); err != nil {
			return nil, err
		}
		dirStr := ""
		if dir.Valid {
			dirStr = dir.String
		}
		if opts.ProjectFilter != "" && dirStr != "" && !strings.Contains(dirStr, opts.ProjectFilter) {
			continue
		}
		var msgCount int
		_ = db.QueryRow(`SELECT COUNT(*) FROM message WHERE session_id = ?`, id).Scan(&msgCount)
		titleStr := ""
		if title.Valid {
			titleStr = title.String
		}
		if t := util.PickStoredOrMessages(titleStr, p.opencodeUserLines(db, id)); t != "" {
			titleStr = t
		}
		if titleStr == "" {
			titleStr = "(opencode session)"
		}
		kind := model.SessionKindRoot
		if parentID.Valid && parentID.String != "" {
			kind = model.SessionKindSubagent
		}
		migration := openCodeMigration(db, id)
		out = append(out, model.Summary{
			ID: id, Provider: ProviderID, ProjectPath: dirStr, Title: titleStr,
			CreatedAt: time.UnixMilli(created), UpdatedAt: time.UnixMilli(updated),
			MessageCount: msgCount, StoragePath: p.dbPath + "#" + id,
			SourceMtime: mtime, SourceSize: stampSize, Kind: kind, ParentID: parentID.String,
			Migration: migration,
		})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, rows.Err()
}

func sqliteColumnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primaryKey int
		var defaultValue any
		if rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey) == nil && name == column {
			return true
		}
	}
	return false
}

func openCodeMigration(db *sql.DB, id string) *model.MigrationMeta {
	if !sqliteColumnExists(db, "session", "metadata") {
		return nil
	}
	var raw sql.NullString
	if db.QueryRow(`SELECT metadata FROM session WHERE id = ?`, id).Scan(&raw) != nil || !raw.Valid {
		return nil
	}
	var wrapped struct {
		Migration *model.MigrationMeta `json:"agenthop_migration"`
	}
	if json.Unmarshal([]byte(raw.String), &wrapped) != nil {
		return nil
	}
	return wrapped.Migration
}

type ocMessageData struct {
	Role string `json:"role"`
	Time struct {
		Created *int64 `json:"created"`
	} `json:"time"`
}

type ocPartData struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	id := ref.ID
	var dir, title sql.NullString
	var created, updated int64
	err = db.QueryRow(`SELECT directory, title, time_created, time_updated FROM session WHERE id = ?`, id).
		Scan(&dir, &title, &created, &updated)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	dirStr := ""
	if dir.Valid {
		dirStr = dir.String
	}
	titleStr := ""
	if title.Valid {
		titleStr = title.String
	}
	conv := &model.Conversation{
		ID: id, Provider: ProviderID, ProjectPath: dirStr, Title: titleStr,
		CreatedAt: time.UnixMilli(created), UpdatedAt: time.UnixMilli(updated),
		StoragePath: p.dbPath + "#" + id,
		Migration:   openCodeMigration(db, id),
	}
	rows, err := db.Query(`SELECT id, data, time_created FROM message WHERE session_id = ? ORDER BY time_created, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var msgID, data string
		var ts int64
		if err := rows.Scan(&msgID, &data, &ts); err != nil {
			return nil, err
		}
		var md ocMessageData
		if json.Unmarshal([]byte(data), &md) != nil || md.Role == "" {
			continue
		}
		content := p.messageText(db, msgID)
		if content == "" {
			continue
		}
		mrole := model.RoleUser
		if md.Role == "assistant" {
			mrole = model.RoleAssistant
		}
		msgTS := ts
		if md.Time.Created != nil {
			msgTS = *md.Time.Created
		}
		conv.Messages = append(conv.Messages, model.Message{
			Role: mrole, Content: content, Timestamp: time.UnixMilli(msgTS),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	return conv, nil
}

func (p *Provider) opencodeUserLines(db *sql.DB, sessionID string) []string {
	rows, err := db.Query(`SELECT id, data FROM message WHERE session_id = ? ORDER BY time_created, id LIMIT 40`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var msgID, data string
		if rows.Scan(&msgID, &data) != nil {
			continue
		}
		var md ocMessageData
		if json.Unmarshal([]byte(data), &md) != nil || md.Role != "user" {
			continue
		}
		if text := p.messageText(db, msgID); text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

func (p *Provider) messageText(db *sql.DB, messageID string) string {
	rows, err := db.Query(`SELECT data FROM part WHERE message_id = ? ORDER BY time_created, id`, messageID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var data string
		if rows.Scan(&data) != nil {
			continue
		}
		var pd ocPartData
		if json.Unmarshal([]byte(data), &pd) != nil || pd.Text == "" {
			continue
		}
		if pd.Type != "text" {
			continue
		}
		parts = append(parts, pd.Text)
	}
	return strings.Join(parts, "\n")
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	if len(conv.Messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	sessionID := ocID("ses_")
	project := opts.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	now := time.Now().UnixMilli()
	created := conv.CreatedAt.UnixMilli()
	if conv.CreatedAt.IsZero() {
		created = now
		for _, message := range conv.Messages {
			if !message.Timestamp.IsZero() && message.Timestamp.UnixMilli() < created {
				created = message.Timestamp.UnixMilli()
			}
		}
	}
	updated := conv.UpdatedAt.UnixMilli()
	if conv.UpdatedAt.IsZero() || updated < created {
		updated = created
	}
	for _, message := range conv.Messages {
		if !message.Timestamp.IsZero() && message.Timestamp.UnixMilli() > updated {
			updated = message.Timestamp.UnixMilli()
		}
	}
	title := conv.Title
	if title == "" {
		title = "Migrated session"
	}
	storagePath := p.dbPath + "#" + sessionID
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: storagePath, ProjectPath: project}, nil
	}
	db, err := sql.Open("sqlite", p.dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := p.ensureGlobalProject(db, now); err != nil {
		return nil, err
	}
	version := latestOpenCodeVersion(db)
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	meta := model.NewMigrationMeta(conv)
	metaJSON, _ := json.Marshal(map[string]any{"agenthop_migration": meta})
	slug := util.FirstUserSnippet(title, 20)
	if slug == "" {
		slug = "migrated"
	}
	_, err = tx.Exec(`INSERT INTO session (
  id, project_id, directory, title, version, slug, time_created, time_updated, metadata, agent, model, cost,
  tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write
) VALUES (?, 'global', ?, ?, ?, ?, ?, ?, ?, NULL, NULL, 0, 0, 0, 0, 0, 0)`,
		sessionID, project, title, version, slug, created, updated, string(metaJSON))
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	var lastTS int64
	haveLastTS := false
	for i, m := range conv.Messages {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		msgID := ocID("msg_")
		sourceTS := m.Timestamp.UnixMilli()
		if m.Timestamp.IsZero() {
			sourceTS = created + int64(i)
			if haveLastTS && sourceTS <= lastTS {
				sourceTS = lastTS + 1
			}
		}
		storageTS := sourceTS
		if haveLastTS && storageTS <= lastTS {
			storageTS = lastTS + 1
		}
		lastTS, haveLastTS = storageTS, true
		msgData, _ := json.Marshal(map[string]any{
			"role":    string(m.Role),
			"time":    map[string]any{"created": sourceTS},
			"summary": map[string]any{"diffs": []any{}},
		})
		_, err = tx.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
			msgID, sessionID, storageTS, storageTS, string(msgData))
		if err != nil {
			return nil, fmt.Errorf("insert message: %w", err)
		}
		partID := ocID("prt_")
		partData, _ := json.Marshal(ocPartData{Type: "text", Text: m.PlainText()})
		_, err = tx.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
			partID, msgID, sessionID, storageTS, storageTS, string(partData))
		if err != nil {
			return nil, fmt.Errorf("insert part: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session: %w", err)
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: storagePath, ProjectPath: project}, nil
}

func (p *Provider) ensureGlobalProject(db *sql.DB, now int64) error {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM project WHERE id = 'global'`).Scan(&n)
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES ('global', '/', ?, ?, '[]')`, now, now)
	return err
}

func latestOpenCodeVersion(db *sql.DB) string {
	var version sql.NullString
	if db.QueryRow(`SELECT version FROM session WHERE version <> '' ORDER BY time_updated DESC LIMIT 1`).Scan(&version) == nil && version.Valid {
		return version.String
	}
	// OpenCode requires this column even in an otherwise empty database.
	return "unknown"
}

func ocID(prefix string) string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	if len(raw) > 20 {
		raw = raw[:20]
	}
	return prefix + raw
}

// ResumeCommand includes the project directory so a pasted line lands in the
// right project regardless of where the user runs it.
func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	cmd := "opencode --session " + util.ShellQuote(r.SessionID)
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && " + cmd
	}
	return cmd
}

func (p *Provider) CleanupWrite(ctx context.Context, r provider.WriteResult) error {
	db, err := sql.Open("sqlite", p.dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM part WHERE session_id = ?`,
		`DELETE FROM message WHERE session_id = ?`,
		`DELETE FROM session WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, r.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
