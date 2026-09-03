package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
	_ "modernc.org/sqlite"
)

const ProviderID = "opencode"

type Provider struct {
	dbPath  string
	command string
}

func New() *Provider {
	root := config.EnvOrDefault("XDG_DATA_HOME", filepath.Join(config.HomeDir(), ".local", "share"))
	return &Provider{
		dbPath:  filepath.Join(root, "opencode", "opencode.db"),
		command: config.EnvOrDefault("OPENCODE_COMMAND", "opencode"),
	}
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
	activeWhere := ""
	if sqliteColumnExists(db, "session", "time_archived") {
		activeWhere = " WHERE time_archived IS NULL"
	}
	rows, err := db.Query(`SELECT id, directory, title, time_created, time_updated, ` + parentExpr + ` FROM session` + activeWhere + ` ORDER BY time_updated DESC`)
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
		Migration    *model.MigrationMeta `json:"another_migration"`
		MigrationOld *model.MigrationMeta `json:"agenthop_migration"`
	}
	if json.Unmarshal([]byte(raw.String), &wrapped) != nil {
		return nil
	}
	if wrapped.Migration == nil {
		wrapped.Migration = wrapped.MigrationOld
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
	result := &provider.WriteResult{SessionID: sessionID, StoragePath: p.dbPath + "#" + sessionID, ProjectPath: project}
	if opts.DryRun {
		return result, nil
	}
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	version, agent, modelRef, err := p.nativeDefaults(db)
	_ = db.Close()
	if err != nil {
		return nil, err
	}
	payload, err := openCodeImportPayload(conv, sessionID, project, version, agent, modelRef)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "another-opencode-*.json")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	command := p.command
	if command == "" {
		command = "opencode"
	}
	cmd := exec.CommandContext(ctx, command, "import", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opencode import: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return result, nil
}

func (p *Provider) nativeDefaults(db *sql.DB) (string, string, map[string]any, error) {
	var version, agent, raw string
	err := db.QueryRow(`SELECT version, COALESCE(agent,''), model FROM session WHERE model IS NOT NULL AND model <> '' ORDER BY time_updated DESC LIMIT 1`).
		Scan(&version, &agent, &raw)
	if err != nil {
		return "", "", nil, fmt.Errorf("OpenCode has no model default yet; run opencode once first")
	}
	var modelRef map[string]any
	if json.Unmarshal([]byte(raw), &modelRef) != nil || modelRef["id"] == nil || modelRef["providerID"] == nil {
		return "", "", nil, fmt.Errorf("OpenCode latest model reference is invalid")
	}
	if agent == "" {
		agent = "build"
	}
	return version, agent, modelRef, nil
}

func openCodeImportPayload(conv *model.Conversation, sessionID, project, version, agent string, modelRef map[string]any) ([]byte, error) {
	now := time.Now().UnixMilli()
	created, updated := now, now
	if !conv.CreatedAt.IsZero() {
		created = conv.CreatedAt.UnixMilli()
	}
	if !conv.UpdatedAt.IsZero() {
		updated = conv.UpdatedAt.UnixMilli()
	}
	if updated < created {
		updated = created
	}
	title := strings.TrimSpace(conv.Title)
	if title == "" {
		title = "Migrated session"
	}
	slug := util.FirstUserSnippet(title, 20)
	if slug == "" {
		slug = "migrated"
	}
	zeroTokens := map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}
	info := map[string]any{
		"id": sessionID, "slug": slug, "projectID": "global", "directory": project,
		"path": strings.TrimPrefix(filepath.ToSlash(project), "/"), "title": title,
		"agent": agent, "model": modelRef, "version": version,
		"summary": map[string]any{"additions": 0, "deletions": 0, "files": 0},
		"cost":    0, "tokens": zeroTokens,
		"metadata": map[string]any{"another_migration": model.NewMigrationMeta(conv)},
		"time":     map[string]any{"created": created, "updated": updated},
	}
	modelID, _ := modelRef["id"].(string)
	providerID, _ := modelRef["providerID"].(string)
	variant, _ := modelRef["variant"].(string)
	var messages []any
	var parent string
	for i, message := range conv.Messages {
		if message.Role != model.RoleUser && message.Role != model.RoleAssistant || message.PlainText() == "" {
			continue
		}
		ts := message.Timestamp.UnixMilli()
		if message.Timestamp.IsZero() {
			ts = created + int64(i)
		}
		messageID := ocID("msg_")
		messageInfo := map[string]any{
			"id": messageID, "sessionID": sessionID, "role": string(message.Role),
			"time": map[string]any{"created": ts},
		}
		if parent != "" {
			messageInfo["parentID"] = parent
		}
		if message.Role == model.RoleUser {
			messageInfo["agent"] = agent
			messageInfo["model"] = map[string]any{"providerID": providerID, "modelID": modelID}
		} else {
			messageInfo["mode"] = agent
			messageInfo["agent"] = agent
			messageInfo["path"] = map[string]any{"cwd": project, "root": "/"}
			messageInfo["cost"] = 0
			messageInfo["tokens"] = map[string]any{"total": 0, "input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}
			messageInfo["modelID"] = modelID
			messageInfo["providerID"] = providerID
			if variant != "" {
				messageInfo["variant"] = variant
			}
			messageInfo["time"] = map[string]any{"created": ts, "completed": ts}
			messageInfo["finish"] = "stop"
		}
		partID := ocID("prt_")
		messages = append(messages, map[string]any{
			"info": messageInfo,
			"parts": []any{map[string]any{
				"id": partID, "sessionID": sessionID, "messageID": messageID,
				"type": "text", "text": message.PlainText(),
			}},
		})
		parent = messageID
	}
	if len(messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	return json.Marshal(map[string]any{"info": info, "messages": messages})
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

func (p *Provider) RenameSession(ctx context.Context, ref provider.SessionRef, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("opencode: title must not be empty")
	}
	db, err := sql.Open("sqlite", p.dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, `UPDATE session SET title = ?, time_updated = ? WHERE id = ?`, title, time.Now().UnixMilli(), ref.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return provider.ErrNotFound
	}
	return nil
}

func (p *Provider) ArchiveSession(ctx context.Context, ref provider.SessionRef, archived bool) error {
	db, err := sql.Open("sqlite", p.dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	var value any
	if archived {
		value = time.Now().UnixMilli()
	}
	result, err := db.ExecContext(ctx, `UPDATE session SET time_archived = ?, time_updated = ? WHERE id = ?`, value, time.Now().UnixMilli(), ref.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return provider.ErrNotFound
	}
	return nil
}

func (p *Provider) DeleteSession(ctx context.Context, ref provider.SessionRef) error {
	return p.deleteWithCLI(ctx, ref.ID)
}

func (p *Provider) CleanupWrite(ctx context.Context, r provider.WriteResult) error {
	return p.deleteWithCLI(ctx, r.SessionID)
}

func (p *Provider) deleteWithCLI(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("opencode: missing session id")
	}
	command := p.command
	if command == "" {
		command = "opencode"
	}
	cmd := exec.CommandContext(ctx, command, "session", "delete", sessionID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("opencode delete: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
