// Package opencode2 integrates the OpenCode 2 preview as a separate provider
// from OpenCode V1. V2 has a different database schema and its own CLI shim;
// treating both as one provider would silently read or write the wrong store.
package opencode2

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

const ProviderID = "opencode2"

type Provider struct {
	dbPath  string
	command string
}

func New() *Provider {
	root := config.EnvOrDefault("XDG_DATA_HOME", filepath.Join(config.HomeDir(), ".local", "share"))
	dbPath := config.EnvOrDefault("OPENCODE2_DB_PATH", filepath.Join(root, "opencode", "opencode2.db"))
	command := config.EnvOrDefault("OPENCODE2_COMMAND", "opencode2")
	return &Provider{dbPath: dbPath, command: command}
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "OpenCode 2" }
func (p *Provider) Installed() bool {
	_, err := os.Stat(p.dbPath)
	return err == nil
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "database", Path: p.dbPath, Env: "OPENCODE2_DB_PATH"}}
}

func (p *Provider) openRO() (*sql.DB, error) {
	return sql.Open("sqlite", p.dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
}

type sessionRow struct {
	id, project, title, parent string
	created, updated           int64
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	st, err := os.Stat(p.dbPath)
	if err != nil {
		return nil, err
	}
	stamp, size, err := provider.SQLiteSourceStamp(p.dbPath, st)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, directory, COALESCE(title,''), COALESCE(parent_id,''), time_created, time_updated FROM session_v2 ORDER BY time_updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Summary
	for rows.Next() {
		var row sessionRow
		if err := rows.Scan(&row.id, &row.project, &row.title, &row.parent, &row.created, &row.updated); err != nil {
			return nil, err
		}
		if opts.ProjectFilter != "" && !strings.Contains(row.project, opts.ProjectFilter) {
			continue
		}
		var count int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_message WHERE session_id = ? AND type IN ('user','assistant')`, row.id).Scan(&count)
		title := strings.TrimSpace(row.title)
		if title == "" {
			title = p.firstUserTitle(db, row.id)
		}
		if title == "" {
			title = "(opencode2 session)"
		}
		kind := model.SessionKindRoot
		if row.parent != "" {
			kind = model.SessionKindSubagent
		}
		out = append(out, model.Summary{
			ID: row.id, Provider: ProviderID, ProjectPath: row.project, Title: title,
			CreatedAt: time.UnixMilli(row.created), UpdatedAt: time.UnixMilli(row.updated),
			MessageCount: count, StoragePath: p.dbPath + "#" + row.id,
			SourceMtime: stamp.UnixNano(), SourceSize: size, Kind: kind, ParentID: row.parent,
			Migration: p.migration(db, row.id),
		})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, rows.Err()
}

type messageData struct {
	Text    string `json:"text"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

func textFromMessage(data string) string {
	var message messageData
	if json.Unmarshal([]byte(data), &message) != nil {
		return ""
	}
	if message.Text != "" {
		return message.Text
	}
	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (p *Provider) firstUserTitle(db *sql.DB, sessionID string) string {
	rows, err := db.Query(`SELECT data FROM session_message WHERE session_id = ? AND type = 'user' ORDER BY seq LIMIT 20`, sessionID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	picker := util.NewTitlePicker(80)
	for rows.Next() {
		var data string
		if rows.Scan(&data) == nil {
			picker.Note(textFromMessage(data))
		}
	}
	return picker.Title()
}

func (p *Provider) migration(db *sql.DB, sessionID string) *model.MigrationMeta {
	var raw sql.NullString
	if db.QueryRow(`SELECT metadata FROM session_v2 WHERE id = ?`, sessionID).Scan(&raw) != nil || !raw.Valid {
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

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var row sessionRow
	row.id = ref.ID
	err = db.QueryRowContext(ctx, `SELECT directory, COALESCE(title,''), COALESCE(parent_id,''), time_created, time_updated FROM session_v2 WHERE id = ?`, ref.ID).
		Scan(&row.project, &row.title, &row.parent, &row.created, &row.updated)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, ProjectPath: row.project, Title: row.title,
		CreatedAt: time.UnixMilli(row.created), UpdatedAt: time.UnixMilli(row.updated),
		StoragePath: p.dbPath + "#" + ref.ID, Migration: p.migration(db, ref.ID),
	}
	rows, err := db.QueryContext(ctx, `SELECT type, time_created, data FROM session_message WHERE session_id = ? ORDER BY seq`, ref.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ, data string
		var created int64
		if err := rows.Scan(&typ, &created, &data); err != nil {
			return nil, err
		}
		role := model.RoleUser
		switch typ {
		case "user":
			role = model.RoleUser
		case "assistant":
			role = model.RoleAssistant
		default:
			continue
		}
		text := textFromMessage(data)
		if text == "" {
			continue
		}
		var parsed messageData
		_ = json.Unmarshal([]byte(data), &parsed)
		if parsed.Time.Created > 0 {
			created = parsed.Time.Created
		}
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: text, Timestamp: time.UnixMilli(created)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	if conv.Title == "" {
		conv.Title = p.firstUserTitle(db, ref.ID)
	}
	return conv, nil
}

func (p *Provider) defaults(db *sql.DB) (string, map[string]any, error) {
	var agent string
	var raw string
	err := db.QueryRow(`SELECT COALESCE(agent,''), model FROM session_v2 WHERE model IS NOT NULL AND model <> '' ORDER BY time_updated DESC LIMIT 1`).Scan(&agent, &raw)
	if err != nil {
		return "", nil, fmt.Errorf("OpenCode 2 has no model default yet; run opencode2 once first")
	}
	var modelRef map[string]any
	if json.Unmarshal([]byte(raw), &modelRef) != nil || modelRef["id"] == nil || modelRef["providerID"] == nil {
		return "", nil, fmt.Errorf("OpenCode 2 latest model reference is invalid")
	}
	if agent == "" {
		agent = "build"
	}
	return agent, modelRef, nil
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	if len(conv.Messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	project := opts.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	sessionID := oc2ID("ses_")
	result := &provider.WriteResult{SessionID: sessionID, StoragePath: p.dbPath + "#" + sessionID, ProjectPath: project}
	if opts.DryRun {
		return result, nil
	}
	db, err := p.openRO()
	if err != nil {
		return nil, err
	}
	agent, modelRef, err := p.defaults(db)
	_ = db.Close()
	if err != nil {
		return nil, err
	}
	payload, err := importPayload(conv, sessionID, project, agent, modelRef)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "another-opencode2-*.json")
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
	cmd := exec.CommandContext(ctx, p.command, "import", "--directory", project, path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("opencode2 import: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return result, nil
}

func importPayload(conv *model.Conversation, sessionID, project, agent string, modelRef map[string]any) ([]byte, error) {
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
	zeroTokens := map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}
	info := map[string]any{
		"id": sessionID, "projectID": "another-migration", "agent": agent, "model": modelRef,
		"cost": 0, "tokens": zeroTokens, "time": map[string]any{"created": created, "updated": updated},
		"title": title, "location": map[string]any{"directory": project},
		"metadata": map[string]any{"another_migration": model.NewMigrationMeta(conv)},
	}
	var messages []any
	seq := int64(0)
	for _, message := range conv.Messages {
		if message.Role != model.RoleUser && message.Role != model.RoleAssistant || message.PlainText() == "" {
			continue
		}
		ts := message.Timestamp.UnixMilli()
		if message.Timestamp.IsZero() {
			ts = created + seq
		}
		id := oc2ID("msg_")
		if message.Role == model.RoleUser {
			messages = append(messages, map[string]any{
				"id": id, "type": "user", "time": map[string]any{"created": ts},
				"text": message.PlainText(), "files": []any{}, "agents": []any{},
			})
		} else {
			messages = append(messages, map[string]any{
				"id": id, "type": "assistant", "time": map[string]any{"created": ts, "completed": ts},
				"agent": agent, "model": modelRef, "content": []any{map[string]any{"type": "text", "text": message.PlainText()}},
				"finish": "stop", "cost": 0, "tokens": zeroTokens,
			})
		}
		seq++
	}
	if len(messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	return json.Marshal(map[string]any{"info": info, "messages": messages})
}

func oc2ID(prefix string) string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	if len(raw) > 24 {
		raw = raw[:24]
	}
	return prefix + raw
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	cmd := "opencode2 --session " + util.ShellQuote(r.SessionID)
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && " + cmd
	}
	return cmd
}

func (p *Provider) RenameSession(ctx context.Context, ref provider.SessionRef, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("opencode2: title must not be empty")
	}
	data, _ := json.Marshal(map[string]string{"title": title})
	cmd := exec.CommandContext(ctx, p.command, "api", "POST", "/api/session/"+ref.ID+"/rename", "--data", string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("opencode2 rename: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (p *Provider) DeleteSession(ctx context.Context, ref provider.SessionRef) error {
	return p.delete(ctx, ref.ID)
}

func (p *Provider) CleanupWrite(ctx context.Context, r provider.WriteResult) error {
	return p.delete(ctx, r.SessionID)
}

func (p *Provider) delete(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("opencode2: missing session id")
	}
	cmd := exec.CommandContext(ctx, p.command, "api", "DELETE", "/api/session/"+sessionID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("opencode2 delete: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
