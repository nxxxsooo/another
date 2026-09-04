// Package agy reads and writes native Antigravity CLI conversations.
package agy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
	_ "modernc.org/sqlite"
)

const ProviderID = "agy"

const summariesSchema = `
CREATE TABLE IF NOT EXISTS conversation_summaries (
	conversation_id text, title text NOT NULL DEFAULT '', preview text NOT NULL DEFAULT '',
	step_count integer NOT NULL DEFAULT 0, last_modified_time datetime NOT NULL,
	workspace_uris text NOT NULL, status text NOT NULL DEFAULT '', source text NOT NULL DEFAULT '',
	project_id text NOT NULL DEFAULT '', agent_name text NOT NULL DEFAULT '',
	parent_conversation_id text NOT NULL DEFAULT '', nesting_depth integer NOT NULL DEFAULT 0,
	battle_id text NOT NULL DEFAULT '', winning_conversation_id text NOT NULL DEFAULT '',
	not_fully_idle numeric NOT NULL DEFAULT false, killed numeric NOT NULL DEFAULT false,
	last_user_input_time datetime NOT NULL, last_user_input_step_index integer NOT NULL DEFAULT -1,
	app_data_dir text NOT NULL DEFAULT '', raw_summary BLOB, PRIMARY KEY (conversation_id)
);
CREATE INDEX IF NOT EXISTS idx_conversation_summaries_last_user_input_time ON conversation_summaries(last_user_input_time);
CREATE INDEX IF NOT EXISTS idx_conversation_summaries_last_modified_time ON conversation_summaries(last_modified_time);
`

var requiredSummaryColumns = []string{
	"conversation_id", "title", "preview", "step_count", "last_modified_time", "workspace_uris",
	"status", "source", "project_id", "agent_name", "parent_conversation_id", "nesting_depth",
	"battle_id", "winning_conversation_id", "not_fully_idle", "killed", "last_user_input_time",
	"last_user_input_step_index", "app_data_dir", "raw_summary",
}

type Provider struct {
	root string
}

func New() *Provider {
	root := config.EnvOrDefault("AGY_HOME", filepath.Join(config.HomeDir(), ".gemini", "antigravity-cli"))
	return &Provider{root: root}
}

func (p *Provider) ID() string           { return ProviderID }
func (p *Provider) DisplayName() string  { return "Antigravity" }
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{
		{Label: "conversations", Path: filepath.Join(p.root, "conversations"), Env: "AGY_HOME"},
		{Label: "brain", Path: filepath.Join(p.root, "brain"), Env: "AGY_HOME"},
		{Label: "summaries", Path: p.summariesDBPath(), Env: "AGY_HOME"},
	}
}

func (p *Provider) summariesDBPath() string {
	return filepath.Join(p.root, "conversation_summaries.db")
}

// Installed is also the target-readiness check used by the migration engine.
// A CLI-only installation can accept its first migrated conversation.
func (p *Provider) Installed() bool {
	if _, err := exec.LookPath("agy"); err == nil {
		return true
	}
	st, err := os.Stat(p.root)
	return err == nil && st.IsDir()
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	cmd := "agy --conversation " + util.ShellQuote(r.SessionID)
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && " + cmd
	}
	return cmd
}

type transcriptData struct {
	messages  []model.Message
	picker    *util.TitlePicker
	createdAt time.Time
	updatedAt time.Time
	migration *model.MigrationMeta
}

func scanTranscript(ctx context.Context, path string, collect bool) (transcriptData, error) {
	out := transcriptData{picker: util.NewTitlePicker(80)}
	err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if meta, ok := model.ParseMigrationMeta(line); ok {
			out.migration = meta
			return nil
		}
		var row struct {
			Type      string          `json:"type"`
			CreatedAt string          `json:"created_at"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		text := rawText(row.Content)
		var role model.Role
		switch row.Type {
		case "USER_INPUT":
			text = extractUserText(text)
			if text == "" || util.SkipUserMessage(text) {
				return nil
			}
			role = model.RoleUser
			out.picker.Note(text)
		case "PLANNER_RESPONSE":
			if strings.TrimSpace(text) == "" {
				return nil
			}
			role = model.RoleAssistant
		default:
			return nil
		}
		ts := parseAGYTime(row.CreatedAt)
		if out.createdAt.IsZero() && !ts.IsZero() {
			out.createdAt = ts
		}
		if !ts.IsZero() {
			out.updatedAt = ts
		}
		if collect {
			out.messages = append(out.messages, model.Message{Role: role, Content: text, Timestamp: ts})
		} else {
			// A nil Content still records the portable-message count without
			// retaining entire transcripts during discovery.
			out.messages = append(out.messages, model.Message{Role: role})
		}
		return nil
	})
	return out, err
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func (p *Provider) transcriptPath(id, hint string) (string, error) {
	var dir string
	if strings.HasSuffix(hint, ".jsonl") {
		dir = filepath.Dir(hint)
	} else {
		dir = filepath.Join(p.root, "brain", id, ".system_generated", "logs")
	}
	for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}
	return "", provider.ErrNotFound
}

// summaryFingerprint combines the exact transcript selected for reading with
// the metadata row that supplies its native title, project, and lifecycle
// state. It catches WAL-visible title changes without forcing every session to
// refresh whenever some unrelated row in the shared summaries DB changes.
func summaryFingerprint(st os.FileInfo, fields ...string) (int64, int64) {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(st.ModTime().UnixNano()))
	_, _ = h.Write(buf[:])
	binary.LittleEndian.PutUint64(buf[:], uint64(st.Size()))
	_, _ = h.Write(buf[:])
	size := st.Size()
	for _, field := range fields {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(field))
		size += int64(len(field))
	}
	return int64(h.Sum64() & uint64(^uint64(0)>>1)), size
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	var out []model.Summary
	seen := make(map[string]bool)

	if _, err := os.Stat(p.summariesDBPath()); err == nil {
		db, err := sql.Open("sqlite", p.summariesDBPath()+"?mode=ro&_pragma=busy_timeout(5000)")
		if err != nil {
			return nil, err
		}
		rows, err := db.QueryContext(ctx, `
SELECT conversation_id, title, preview, step_count, last_modified_time,
       workspace_uris, parent_conversation_id
FROM conversation_summaries WHERE killed = 0 ORDER BY last_modified_time DESC`)
		if err != nil {
			db.Close()
			return nil, err
		}
		for rows.Next() {
			var id, title, preview, updatedRaw, urisRaw, parentID string
			var nativeSteps int
			if err := rows.Scan(&id, &title, &preview, &nativeSteps, &updatedRaw, &urisRaw, &parentID); err != nil {
				rows.Close()
				db.Close()
				return nil, err
			}
			if nativeSteps == 0 {
				// The shared summary can lag an active transcript. Leave the ID
				// unseen so the brain-directory pass can still discover it.
				continue
			}
			seen[id] = true
			project := parseWorkspaceURI(urisRaw)
			if opts.ProjectFilter != "" && !strings.Contains(project, opts.ProjectFilter) {
				continue
			}
			logPath, err := p.transcriptPath(id, "")
			if err != nil {
				continue
			}
			st, err := os.Stat(logPath)
			if err != nil {
				continue
			}
			mtime, size := summaryFingerprint(st, id, title, preview, updatedRaw, urisRaw, parentID, fmt.Sprint(nativeSteps))
			if opts.SkipSource != nil && opts.SkipSource(logPath, mtime, size) {
				continue
			}
			if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(logPath, time.Unix(0, mtime).Unix()) {
				continue
			}
			data, err := scanTranscript(ctx, logPath, false)
			if err != nil {
				rows.Close()
				db.Close()
				return nil, err
			}
			if len(data.messages) == 0 {
				continue
			}
			title = strings.TrimSpace(title)
			if title == "" {
				title = data.picker.Title()
			}
			if title == "" {
				title = util.FirstUserSnippet(preview, 80)
			}
			if title == "" {
				title = "(antigravity session)"
			}
			updatedAt := parseAGYTime(updatedRaw)
			if data.updatedAt.After(updatedAt) {
				updatedAt = data.updatedAt
			}
			if updatedAt.IsZero() {
				updatedAt = st.ModTime()
			}
			kind := model.SessionKindRoot
			if strings.TrimSpace(parentID) != "" {
				kind = model.SessionKindSubagent
			}
			out = append(out, model.Summary{
				ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
				CreatedAt: data.createdAt, UpdatedAt: updatedAt, MessageCount: len(data.messages),
				StoragePath: logPath, Kind: kind, ParentID: strings.TrimSpace(parentID),
				SourceMtime: mtime, SourceSize: size, Migration: data.migration,
			})
			if opts.Limit > 0 && len(out) >= opts.Limit {
				break
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			db.Close()
			return nil, err
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return out, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	brainDir := filepath.Join(p.root, "brain")
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || seen[entry.Name()] {
			continue
		}
		logPath, err := p.transcriptPath(entry.Name(), "")
		if err != nil {
			continue
		}
		st, err := os.Stat(logPath)
		if err != nil {
			continue
		}
		if opts.SkipSource != nil && opts.SkipSource(logPath, st.ModTime().UnixNano(), st.Size()) {
			continue
		}
		if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(logPath, st.ModTime().Unix()) {
			continue
		}
		sm, err := p.summarizeTranscript(ctx, entry.Name(), logPath, st)
		if err != nil {
			return nil, err
		}
		if sm.ID == "" || sm.MessageCount == 0 {
			continue
		}
		if opts.ProjectFilter != "" && !strings.Contains(sm.ProjectPath, opts.ProjectFilter) {
			continue
		}
		out = append(out, sm)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (p *Provider) summarizeTranscript(ctx context.Context, id, logPath string, st os.FileInfo) (model.Summary, error) {
	data, err := scanTranscript(ctx, logPath, false)
	if err != nil {
		return model.Summary{}, err
	}
	if len(data.messages) == 0 {
		return model.Summary{}, nil
	}
	created, updated := data.createdAt, data.updatedAt
	if updated.IsZero() {
		updated = st.ModTime()
	}
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: p.getProjectFromConvDB(id),
		Title: data.picker.TitleOr("(antigravity session)"), CreatedAt: created, UpdatedAt: updated,
		MessageCount: len(data.messages), StoragePath: logPath, Kind: model.SessionKindRoot,
		SourceMtime: st.ModTime().UnixNano(), SourceSize: st.Size(), Migration: data.migration,
	}, nil
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	logPath, err := p.transcriptPath(ref.ID, ref.StoragePath)
	if err != nil {
		return nil, err
	}
	data, err := scanTranscript(ctx, logPath, true)
	if err != nil {
		return nil, err
	}
	if len(data.messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, ProjectPath: ref.ProjectPath,
		Title: data.picker.TitleOr("(antigravity session)"), StoragePath: logPath,
		CreatedAt: data.createdAt, UpdatedAt: data.updatedAt, Messages: data.messages,
		MessageCount: len(data.messages), Migration: data.migration,
	}
	if title := p.getStoredTitle(conv.ID); title != "" {
		conv.Title = title
	}
	if conv.ProjectPath == "" {
		conv.ProjectPath = p.getStoredProject(conv.ID)
	}
	return conv, nil
}

func (p *Provider) LoadPreview(ctx context.Context, ref provider.SessionRef, limit int) (*model.Conversation, error) {
	conv, err := p.Load(ctx, ref)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(conv.Messages) > limit {
		conv.Messages = conv.Messages[len(conv.Messages)-limit:]
	}
	return conv, nil
}

func (p *Provider) openSummariesRW() (*sql.DB, error) {
	if err := os.MkdirAll(p.root, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p.summariesDBPath()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='conversation_summaries'`).Scan(&exists); err != nil {
		db.Close()
		return nil, err
	}
	if exists == 0 {
		if _, err := db.Exec(summariesSchema); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := validateSummariesSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_conversation_summaries_last_user_input_time ON conversation_summaries(last_user_input_time);
CREATE INDEX IF NOT EXISTS idx_conversation_summaries_last_modified_time ON conversation_summaries(last_modified_time);`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func validateSummariesSchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(conversation_summaries)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var missing []string
	for _, name := range requiredSummaryColumns {
		if !columns[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("agy: incompatible conversation_summaries schema; missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (_ *provider.WriteResult, retErr error) {
	var portable []model.Message
	for _, message := range conv.Messages {
		if (message.Role == model.RoleUser || message.Role == model.RoleAssistant) && message.PlainText() != "" {
			portable = append(portable, message)
		}
	}
	if len(portable) == 0 {
		return nil, provider.ErrEmptySession
	}
	project := opts.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	sessionID := uuid.New().String()
	brainDir := filepath.Join(p.root, "brain", sessionID, ".system_generated", "logs")
	compactPath := filepath.Join(brainDir, "transcript.jsonl")
	fullPath := filepath.Join(brainDir, "transcript_full.jsonl")
	convDBPath := filepath.Join(p.root, "conversations", sessionID+".db")
	result := &provider.WriteResult{SessionID: sessionID, StoragePath: fullPath, ProjectPath: project}
	if opts.DryRun {
		return result, nil
	}

	// Validate the shared native schema before creating any per-conversation
	// artifacts. This turns future incompatible AGY schema changes into a clear
	// refusal instead of a partial write.
	sdb, err := p.openSummariesRW()
	if err != nil {
		return nil, err
	}
	defer sdb.Close()
	succeeded := false
	defer func() {
		if !succeeded && retErr != nil {
			if cleanupErr := p.cleanupArtifacts(context.Background(), *result); cleanupErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("cleanup partial agy write: %w", cleanupErr))
			}
		}
	}()

	marker, err := json.Marshal(map[string]any{"type": model.MigrationType, "data": model.NewMigrationMeta(conv)})
	if err != nil {
		return nil, err
	}
	lines := []string{string(marker)}
	now := time.Now().UTC()
	lastUserIndex := -1
	for i, message := range portable {
		createdAt := ""
		if !message.Timestamp.IsZero() {
			createdAt = message.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		var row []byte
		switch message.Role {
		case model.RoleUser:
			lastUserIndex = i
			row, err = json.Marshal(map[string]any{
				"step_index": i, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
				"created_at": createdAt,
				"content":    "<USER_REQUEST>\n" + message.PlainText() + "\n</USER_REQUEST>",
			})
		case model.RoleAssistant:
			row, err = json.Marshal(map[string]any{
				"step_index": i, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
				"created_at": createdAt, "content": message.PlainText(),
			})
		}
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(row))
	}
	if err := os.MkdirAll(brainDir, 0o700); err != nil {
		return nil, err
	}
	data := []byte(strings.Join(lines, "\n") + "\n")
	if err := util.WriteFileAtomic(compactPath, data, 0o600); err != nil {
		return nil, err
	}
	if err := util.WriteFileAtomic(fullPath, data, 0o600); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(convDBPath), 0o700); err != nil {
		return nil, err
	}
	cdb, err := sql.Open("sqlite", convDBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	conversationSchema := `
CREATE TABLE trajectory_meta (trajectory_id text, cascade_id text, trajectory_type integer, source integer, PRIMARY KEY (trajectory_id));
CREATE TABLE steps (idx integer, step_type integer NOT NULL DEFAULT 0, status integer NOT NULL DEFAULT 0, has_subtrajectory numeric NOT NULL DEFAULT false, metadata blob, error_details blob, permissions blob, task_details blob, render_info blob, step_payload blob, step_format integer NOT NULL DEFAULT 0, PRIMARY KEY (idx));
CREATE INDEX idx_steps_status ON steps(status);
CREATE INDEX idx_steps_step_type ON steps(step_type);
CREATE TABLE gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx));
CREATE TABLE executor_metadata (idx integer, data blob, PRIMARY KEY (idx));
CREATE TABLE parent_references (idx integer, data blob, PRIMARY KEY (idx));
CREATE TABLE trajectory_metadata_blob (id text DEFAULT 'main', data blob, PRIMARY KEY (id));
CREATE TABLE battle_mode_infos (idx integer, data blob, PRIMARY KEY (idx));
INSERT INTO trajectory_meta (trajectory_id, cascade_id, trajectory_type, source) VALUES (?, ?, 4, 17);`
	if _, err := cdb.ExecContext(ctx, conversationSchema, sessionID, sessionID); err != nil {
		cdb.Close()
		return nil, err
	}
	if err := cdb.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(convDBPath, 0o600); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(conv.Title)
	preview := ""
	for _, message := range portable {
		if message.Role == model.RoleUser {
			preview = message.PlainText()
			if title == "" {
				title = util.FirstUserSnippet(preview, 80)
			}
			break
		}
	}
	urisJSON, err := json.Marshal([]string{workspaceURI(project)})
	if err != nil {
		return nil, err
	}
	timeStr := now.Format("2006-01-02 15:04:05.000000+00:00")
	tx, err := sdb.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO conversation_summaries (
	conversation_id, title, preview, step_count, last_modified_time, workspace_uris,
	status, source, project_id, agent_name, parent_conversation_id, nesting_depth,
	battle_id, winning_conversation_id, not_fully_idle, killed, last_user_input_time,
	last_user_input_step_index, app_data_dir, raw_summary
) VALUES (?, ?, ?, ?, ?, ?, '', '', 'default-cli-project', '', '', 0, '', '', 0, 0, ?, ?, ?, NULL)`,
		sessionID, title, preview, len(portable), timeStr, string(urisJSON), timeStr, lastUserIndex, p.root); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := os.Chmod(p.summariesDBPath(), 0o600); err != nil {
		return nil, err
	}
	succeeded = true
	return result, nil
}

func validateSessionID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("agy: invalid conversation id %q", id)
	}
	return nil
}

func (p *Provider) acquireLifecycleLock(id string) (func() error, error) {
	if os.Getenv("ANTIGRAVITY_CONVERSATION_ID") == id {
		return nil, fmt.Errorf("agy: conversation %s is currently active", id)
	}
	release, active, err := acquireConversationLock(filepath.Join(p.root, "presence", id+".lock"))
	if err != nil {
		return nil, fmt.Errorf("agy: acquire conversation lock: %w", err)
	}
	if active {
		return nil, fmt.Errorf("agy: conversation %s is currently active", id)
	}
	return release, nil
}

func (p *Provider) cleanupArtifacts(ctx context.Context, result provider.WriteResult) error {
	id := result.SessionID
	if err := validateSessionID(id); err != nil {
		return err
	}
	brainDir := filepath.Join(p.root, "brain", id)
	logsDir := filepath.Join(brainDir, ".system_generated", "logs")
	fullPath := filepath.Join(logsDir, "transcript_full.jsonl")
	if filepath.Clean(result.StoragePath) != filepath.Clean(fullPath) {
		return fmt.Errorf("agy: refusing cleanup for unexpected storage path %s", result.StoragePath)
	}
	for _, dir := range []string{brainDir, filepath.Join(brainDir, ".system_generated"), logsDir} {
		if st, err := os.Lstat(dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("agy: refusing symlinked artifact directory %s", dir)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	var errs []error
	if _, err := os.Stat(p.summariesDBPath()); err == nil {
		db, openErr := p.openSummariesRW()
		if openErr != nil {
			errs = append(errs, openErr)
		} else {
			if _, deleteErr := db.ExecContext(ctx, `DELETE FROM conversation_summaries WHERE conversation_id = ?`, id); deleteErr != nil {
				errs = append(errs, deleteErr)
			}
			if closeErr := db.Close(); closeErr != nil {
				errs = append(errs, closeErr)
			}
		}
	} else if !os.IsNotExist(err) {
		errs = append(errs, err)
	}

	for _, target := range []string{
		filepath.Join(logsDir, "transcript.jsonl"),
		fullPath,
		filepath.Join(p.root, "conversations", id+".db"),
		filepath.Join(p.root, "conversations", id+".db-shm"),
		filepath.Join(p.root, "conversations", id+".db-wal"),
	} {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	// Remove only directories that became empty. Anything AGY added after the
	// write remains untouched and makes os.Remove return ENOTEMPTY.
	for _, dir := range []string{logsDir, filepath.Join(brainDir, ".system_generated"), brainDir} {
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) && !errors.Is(err, os.ErrExist) {
			// ENOTEMPTY is expected when AGY created additional native state.
			if entries, readErr := os.ReadDir(dir); readErr != nil || len(entries) == 0 {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (p *Provider) CleanupWrite(ctx context.Context, result provider.WriteResult) error {
	return p.cleanupArtifacts(ctx, result)
}

func (p *Provider) RenameSession(ctx context.Context, ref provider.SessionRef, title string) (retErr error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("agy: title must not be empty")
	}
	if err := validateSessionID(ref.ID); err != nil {
		return err
	}
	release, err := p.acquireLifecycleLock(ref.ID)
	if err != nil {
		return err
	}
	defer func() {
		if err := release(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("agy: release conversation lock: %w", err))
		}
	}()
	db, err := p.openSummariesRW()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE conversation_summaries SET title = ? WHERE conversation_id = ?`, title, ref.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return provider.ErrNotFound
	}
	var stored string
	if err := tx.QueryRowContext(ctx, `SELECT title FROM conversation_summaries WHERE conversation_id = ?`, ref.ID).Scan(&stored); err != nil {
		return err
	}
	if stored != title {
		return fmt.Errorf("agy: title readback mismatch")
	}
	return tx.Commit()
}

func (p *Provider) getStoredTitle(id string) string {
	db, err := sql.Open("sqlite", p.summariesDBPath()+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var title, preview sql.NullString
	if err := db.QueryRow(`SELECT title, preview FROM conversation_summaries WHERE conversation_id = ?`, id).Scan(&title, &preview); err != nil {
		return ""
	}
	if title.Valid && strings.TrimSpace(title.String) != "" {
		return strings.TrimSpace(title.String)
	}
	if preview.Valid {
		return util.FirstUserSnippet(preview.String, 80)
	}
	return ""
}

func (p *Provider) getStoredProject(id string) string {
	db, err := sql.Open("sqlite", p.summariesDBPath()+"?mode=ro&_pragma=busy_timeout(1000)")
	if err == nil {
		defer db.Close()
		var raw sql.NullString
		if db.QueryRow(`SELECT workspace_uris FROM conversation_summaries WHERE conversation_id = ?`, id).Scan(&raw) == nil && raw.Valid {
			if project := parseWorkspaceURI(raw.String); project != "" {
				return project
			}
		}
	}
	return p.getProjectFromConvDB(id)
}

func (p *Provider) getProjectFromConvDB(id string) string {
	path := filepath.Join(p.root, "conversations", id+".db")
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var blob []byte
	if db.QueryRow(`SELECT data FROM trajectory_metadata_blob WHERE id = 'main'`).Scan(&blob) != nil {
		return ""
	}
	parts := bytes.Split(blob, []byte("file://"))
	for _, part := range parts[1:] {
		end := 0
		for end < len(part) && part[end] >= 32 && part[end] <= 126 {
			end++
		}
		path := string(part[:end])
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
	}
	return ""
}

func workspaceURI(project string) string {
	path := strings.ReplaceAll(project, "\\", "/")
	if strings.HasPrefix(path, "//") {
		parts := strings.SplitN(strings.TrimPrefix(path, "//"), "/", 2)
		host, share := parts[0], ""
		if len(parts) == 2 {
			share = "/" + parts[1]
		}
		return (&url.URL{Scheme: "file", Host: host, Path: share}).String()
	}
	if len(path) >= 2 && path[1] == ':' && path[0] != '/' {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func parseWorkspaceURI(raw string) string {
	var uris []string
	if json.Unmarshal([]byte(raw), &uris) != nil || len(uris) == 0 {
		return ""
	}
	parsed, err := url.Parse(uris[0])
	if err == nil && parsed.Scheme == "file" {
		if path, err := url.PathUnescape(parsed.Path); err == nil {
			if parsed.Host != "" {
				return filepath.Clean(filepath.FromSlash("//" + parsed.Host + path))
			}
			if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
			return filepath.Clean(filepath.FromSlash(path))
		}
	}
	return filepath.Clean(uris[0])
}

func parseAGYTime(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, format := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if ts, err := time.Parse(format, value); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func extractXMLTag(value, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	start := strings.Index(value, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(value[start:], close)
	if end < 0 {
		return ""
	}
	return value[start : start+end]
}

func extractUserText(content string) string {
	content = strings.TrimSpace(content)
	if request := extractXMLTag(content, "USER_REQUEST"); request != "" {
		return strings.TrimSpace(request)
	}
	if query := extractXMLTag(content, "user_query"); query != "" {
		return strings.TrimSpace(query)
	}
	return content
}
