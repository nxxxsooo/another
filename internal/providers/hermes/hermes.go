package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
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

const ProviderID = "hermes"

type Provider struct {
	dbPath string
}

func New() *Provider {
	root := config.EnvOrDefault("HERMES_HOME", filepath.Join(config.HomeDir(), ".hermes"))
	return &Provider{dbPath: filepath.Join(root, "state.db")}
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "Hermes" }
func (p *Provider) Installed() bool {
	_, err := os.Stat(p.dbPath)
	return err == nil
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "state.db", Path: p.dbPath, Env: "HERMES_HOME"}}
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
	parentExpr := "NULL"
	if hermesColumnExists(db, "sessions", "parent_session_id") {
		parentExpr = "parent_session_id"
	}
	updatedExpr := "started_at"
	if hermesColumnExists(db, "messages", "timestamp") {
		updatedExpr = "COALESCE((SELECT MAX(timestamp) FROM messages WHERE session_id=sessions.id AND active=1), started_at)"
	}
	rows, err := db.Query(`SELECT id, title, started_at, message_count, cwd, ` + parentExpr + `, ` + updatedExpr + `
FROM sessions WHERE archived = 0 ORDER BY ` + updatedExpr + ` DESC`)
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
		var title, cwd, parentID sql.NullString
		var started, updated float64
		var msgCount int
		if err := rows.Scan(&id, &title, &started, &msgCount, &cwd, &parentID, &updated); err != nil {
			return nil, err
		}
		projectPath := ""
		if cwd.Valid {
			projectPath = cwd.String
		}
		if opts.ProjectFilter != "" && projectPath != "" && !strings.Contains(projectPath, opts.ProjectFilter) {
			continue
		}
		titleStr := ""
		if title.Valid {
			titleStr = title.String
		}
		if t := util.PickStoredOrMessages(titleStr, hermesUserLines(db, id)); t != "" {
			titleStr = t
		}
		if titleStr == "" {
			titleStr = "(hermes session)"
		}
		ts := time.Unix(int64(started), int64((started-float64(int64(started)))*1e9))
		updatedAt := time.Unix(int64(updated), int64((updated-float64(int64(updated)))*1e9))
		kind := model.SessionKindRoot
		if parentID.Valid && parentID.String != "" {
			kind = model.SessionKindSubagent
		}
		migration := hermesMigration(db, id)
		out = append(out, model.Summary{
			ID: id, Provider: ProviderID, ProjectPath: projectPath, Title: titleStr,
			CreatedAt: ts, UpdatedAt: updatedAt, MessageCount: msgCount,
			StoragePath: p.dbPath + "#" + id, SourceMtime: mtime, SourceSize: stampSize,
			Kind: kind, ParentID: parentID.String, Migration: migration,
		})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, rows.Err()
}

func hermesColumnExists(db *sql.DB, table, column string) bool {
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

func hermesMigration(db *sql.DB, sessionID string) *model.MigrationMeta {
	rows, err := db.Query(`SELECT content FROM messages WHERE session_id = ? AND active = 0 AND role = 'system' ORDER BY id`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var content sql.NullString
		if rows.Scan(&content) == nil && content.Valid {
			if meta, ok := model.ParseMigrationMeta([]byte(content.String)); ok {
				return meta
			}
		}
	}
	return nil
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var title sql.NullString
	var started float64
	var cwd sql.NullString
	err = db.QueryRow(`SELECT title, started_at, cwd FROM sessions WHERE id = ?`, ref.ID).Scan(&title, &started, &cwd)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	titleStr := ""
	if title.Valid {
		titleStr = title.String
	}
	projectPath := ref.ProjectPath
	if projectPath == "" && cwd.Valid {
		projectPath = cwd.String
	}
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, Title: titleStr, ProjectPath: projectPath,
		CreatedAt: time.Unix(int64(started), int64((started-float64(int64(started)))*1e9)), StoragePath: p.dbPath + "#" + ref.ID,
		Migration: hermesMigration(db, ref.ID),
	}
	hasTimestamp := hermesColumnExists(db, "messages", "timestamp")
	timestampExpr := "NULL"
	if hasTimestamp {
		timestampExpr = "timestamp"
	}
	rows, err := db.Query(`SELECT role, content, `+timestampExpr+` FROM messages WHERE session_id = ? AND active = 1 ORDER BY id`, ref.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var content sql.NullString
		var timestamp sql.NullFloat64
		if err := rows.Scan(&role, &content, &timestamp); err != nil {
			return nil, err
		}
		text := ""
		if content.Valid {
			text = content.String
		}
		mrole, ok := hermesMapRole(role)
		if !ok || hermesSkipMessage(mrole, text) {
			continue
		}
		message := model.Message{Role: mrole, Content: text}
		if timestamp.Valid {
			message.Timestamp = time.Unix(int64(timestamp.Float64), int64((timestamp.Float64-float64(int64(timestamp.Float64)))*1e9))
			if message.Timestamp.After(conv.UpdatedAt) {
				conv.UpdatedAt = message.Timestamp
			}
		}
		conv.Messages = append(conv.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	if conv.Title == "" {
		conv.Title = util.FirstUserSnippet(conv.Messages[0].PlainText(), 60)
	}
	conv.MessageCount = len(conv.Messages)
	if conv.UpdatedAt.IsZero() {
		conv.UpdatedAt = conv.CreatedAt
	}
	return conv, nil
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	if len(conv.Messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	sessionID := uuid.New().String()
	nowTime := time.Now()
	if !conv.CreatedAt.IsZero() {
		nowTime = conv.CreatedAt
	}
	now := float64(nowTime.UnixNano()) / 1e9
	project := opts.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	title := conv.Title
	if title == "" {
		title = util.FirstUserSnippet(conv.Messages[0].PlainText(), 60)
	}
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: p.dbPath + "#" + sessionID, ProjectPath: project}, nil
	}
	db, err := sql.Open("sqlite", p.dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// sessions.title has a unique index; disambiguate collisions with the id.
	var titleCount int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE title = ?`, title).Scan(&titleCount)
	if titleCount > 0 {
		title = title + " · " + sessionID[:8]
	}
	messageCount := 0
	for _, message := range conv.Messages {
		if message.Role == model.RoleUser || message.Role == model.RoleAssistant {
			messageCount++
		}
	}
	if _, err = tx.Exec(`INSERT INTO sessions (id, source, started_at, message_count, title, cwd) VALUES (?, 'cli', ?, ?, ?, ?)`,
		sessionID, now, messageCount, title, project); err != nil {
		return nil, err
	}
	maxTimestamp := float64(0)
	haveTimestamp := false
	for i, m := range conv.Messages {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		// messages.timestamp is NOT NULL; keep ordering for zero timestamps.
		ts := float64(m.Timestamp.UnixNano()) / 1e9
		if m.Timestamp.IsZero() {
			ts = now + float64(i)/1000
			if haveTimestamp && ts <= maxTimestamp {
				ts = maxTimestamp + 0.001
			}
		}
		if !haveTimestamp || ts > maxTimestamp {
			maxTimestamp, haveTimestamp = ts, true
		}
		if _, err = tx.Exec(`INSERT INTO messages (session_id, role, content, timestamp) VALUES (?, ?, ?, ?)`,
			sessionID, string(m.Role), m.PlainText(), ts); err != nil {
			return nil, err
		}
	}
	marker, err := json.Marshal(model.NewMigrationMeta(conv))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO messages (session_id, role, content, timestamp, active) VALUES (?, 'system', ?, ?, 0)`,
		sessionID, string(marker), now+float64(len(conv.Messages))/1000); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: p.dbPath + "#" + sessionID, ProjectPath: project}, nil
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	return "hermes --resume " + util.ShellQuote(r.SessionID)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, r.SessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, r.SessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func hermesUserLines(db *sql.DB, sessionID string) []string {
	rows, err := db.Query(`SELECT content FROM messages WHERE session_id = ? AND role = 'user' AND active = 1 ORDER BY id LIMIT 40`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var content sql.NullString
		if rows.Scan(&content) == nil && content.Valid && content.String != "" {
			if hermesSkipMessage(model.RoleUser, content.String) {
				continue
			}
			lines = append(lines, content.String)
		}
	}
	return lines
}
