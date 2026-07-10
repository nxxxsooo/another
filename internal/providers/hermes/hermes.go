package hermes

import (
	"context"
	"database/sql"
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
	rows, err := db.Query(`SELECT id, title, started_at, message_count, cwd FROM sessions WHERE archived = 0 ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st, _ := os.Stat(p.dbPath)
	mtime := st.ModTime().Unix()
	var out []model.Summary
	for rows.Next() {
		var id string
		var title, cwd sql.NullString
		var started float64
		var msgCount int
		if err := rows.Scan(&id, &title, &started, &msgCount, &cwd); err != nil {
			continue
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
		ts := time.Unix(int64(started), 0)
		out = append(out, model.Summary{
			ID: id, Provider: ProviderID, ProjectPath: projectPath, Title: titleStr,
			CreatedAt: ts, UpdatedAt: ts, MessageCount: msgCount,
			StoragePath: p.dbPath + "#" + id, SourceMtime: mtime,
		})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, rows.Err()
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
		CreatedAt: time.Unix(int64(started), 0), StoragePath: p.dbPath + "#" + ref.ID,
	}
	rows, err := db.Query(`SELECT role, content FROM messages WHERE session_id = ? AND active = 1 ORDER BY id`, ref.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var content sql.NullString
		if rows.Scan(&role, &content) != nil {
			continue
		}
		text := ""
		if content.Valid {
			text = content.String
		}
		mrole, ok := hermesMapRole(role)
		if !ok || hermesSkipMessage(mrole, text) {
			continue
		}
		conv.Messages = append(conv.Messages, model.Message{Role: mrole, Content: text})
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	if conv.Title == "" {
		conv.Title = util.FirstUserSnippet(conv.Messages[0].PlainText(), 60)
	}
	conv.MessageCount = len(conv.Messages)
	return conv, nil
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	if len(conv.Messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	sessionID := uuid.New().String()
	now := float64(time.Now().Unix())
	title := conv.Title
	if title == "" {
		title = util.FirstUserSnippet(conv.Messages[0].PlainText(), 60)
	}
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: p.dbPath + "#" + sessionID, ProjectPath: conv.ProjectPath}, nil
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
	if _, err = tx.Exec(`INSERT INTO sessions (id, source, started_at, message_count, title) VALUES (?, 'cli', ?, ?, ?)`,
		sessionID, now, len(conv.Messages), title); err != nil {
		return nil, err
	}
	for _, m := range conv.Messages {
		if _, err = tx.Exec(`INSERT INTO messages (session_id, role, content) VALUES (?, ?, ?)`,
			sessionID, string(m.Role), m.PlainText()); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: p.dbPath + "#" + sessionID, ProjectPath: conv.ProjectPath}, nil
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	return "hermes --session " + r.SessionID
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
