package cursor

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/config"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const ProviderID = "cursor"

var (
	writeCursorNativeStore   = writeCursorStore
	writeCursorNativeSidecar = writeCursorSidecar
)

type Provider struct {
	chatsRoot    string
	projectsRoot string
}

type cursorSidecar struct {
	SchemaVersion   int    `json:"schemaVersion"`
	CreatedAtMillis int64  `json:"createdAtMs"`
	UpdatedAtMillis int64  `json:"updatedAtMs"`
	HasConversation bool   `json:"hasConversation"`
	Title           string `json:"title"`
	CWD             string `json:"cwd"`
	IsSubagent      bool   `json:"isSubagent"`
}

func New() *Provider {
	home := config.HomeDir()
	return &Provider{
		chatsRoot:    filepath.Join(home, ".cursor", "chats"),
		projectsRoot: filepath.Join(home, ".cursor", "projects"),
	}
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "Cursor" }
func (p *Provider) Installed() bool {
	_, err1 := os.Stat(p.chatsRoot)
	_, err2 := os.Stat(p.projectsRoot)
	return err1 == nil || err2 == nil
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{
		{Label: "chats", Path: p.chatsRoot},
		{Label: "projects", Path: p.projectsRoot},
	}
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	var out []model.Summary
	workspaceProjects := p.cursorWorkspaceProjects()
	walk := func(root string, visit func(string, os.DirEntry) (model.Summary, bool, error)) error {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			sm, eligible, err := visit(path, d)
			if err != nil {
				return err
			}
			if !eligible {
				return nil
			}
			out = append(out, sm)
			return nil
		})
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := walk(p.chatsRoot, func(path string, d os.DirEntry) (model.Summary, bool, error) {
		if filepath.Base(path) != "store.db" {
			return model.Summary{}, false, nil
		}
		info, err := d.Info()
		if err != nil {
			return model.Summary{}, false, err
		}
		mtime, size, err := cursorStoreSourceStamp(path, info)
		if err != nil {
			return model.Summary{}, false, err
		}
		if opts.SkipSource != nil && opts.SkipSource(path, mtime.UnixNano(), size) {
			return model.Summary{}, false, nil
		}
		if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, mtime.Unix()) {
			return model.Summary{}, false, nil
		}
		workspace := filepath.Base(filepath.Dir(filepath.Dir(path)))
		sm, err := p.summarizeStore(path, workspaceProjects[workspace])
		return sm, err == nil && sm.ID != "", err
	}); err != nil {
		return nil, err
	}
	if err := walk(p.projectsRoot, func(path string, d os.DirEntry) (model.Summary, bool, error) {
		if !strings.HasSuffix(path, ".jsonl") || !strings.Contains(path, "agent-transcripts") {
			return model.Summary{}, false, nil
		}
		info, err := d.Info()
		if err != nil {
			return model.Summary{}, false, err
		}
		if opts.SkipSource != nil && opts.SkipSource(path, info.ModTime().UnixNano(), info.Size()) {
			return model.Summary{}, false, nil
		}
		if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
			return model.Summary{}, false, nil
		}
		sm, err := p.summarizeTranscript(path)
		return sm, err == nil && sm.ID != "", err
	}); err != nil {
		return nil, err
	}
	transcripts := make(map[string]model.Summary)
	for _, sm := range out {
		if strings.HasSuffix(sm.StoragePath, ".jsonl") {
			transcripts[sm.ID] = sm
		}
	}
	filtered := out[:0]
	for i := range out {
		if strings.HasSuffix(out[i].StoragePath, "store.db") {
			if hint, ok := transcripts[out[i].ID]; ok {
				if out[i].ProjectPath == "" && hint.ProjectPath != "" {
					out[i].ProjectPath = hint.ProjectPath
				}
				if out[i].Title == "(cursor session)" && hint.Title != "" {
					out[i].Title = hint.Title
				}
			}
		}
		if opts.ProjectFilter != "" && !strings.Contains(out[i].ProjectPath, opts.ProjectFilter) {
			continue
		}
		filtered = append(filtered, out[i])
		if opts.Limit > 0 && len(filtered) >= opts.Limit {
			break
		}
	}
	return filtered, nil
}

func polishCursorStoredTitle(t string) string {
	if polished, ok := util.UserTitleFromText(t); ok {
		return polished
	}
	if util.IsWeakStoredTitle(t) {
		return ""
	}
	return util.FirstUserSnippet(t, 80)
}

func cursorSessionKind(id string) string {
	if strings.HasPrefix(id, "task-") || strings.HasPrefix(id, "task-tool_") {
		return model.SessionKindSubagent
	}
	return model.SessionKindRoot
}

func cursorSidecarPath(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "meta.json")
}

func readCursorSidecar(storePath string) (*cursorSidecar, error) {
	data, err := os.ReadFile(cursorSidecarPath(storePath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var meta cursorSidecar
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func cursorStoreSourceStamp(path string, main os.FileInfo) (time.Time, int64, error) {
	mtime, size, err := provider.SQLiteSourceStamp(path, main)
	if err != nil {
		return time.Time{}, 0, err
	}
	meta, err := os.Stat(cursorSidecarPath(path))
	if os.IsNotExist(err) {
		return mtime, size, nil
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	var fingerprint [32]byte
	binary.LittleEndian.PutUint64(fingerprint[0:8], uint64(mtime.UnixNano()))
	binary.LittleEndian.PutUint64(fingerprint[8:16], uint64(size))
	binary.LittleEndian.PutUint64(fingerprint[16:24], uint64(meta.ModTime().UnixNano()))
	binary.LittleEndian.PutUint64(fingerprint[24:32], uint64(meta.Size()))
	digest := sha256.Sum256(fingerprint[:])
	return time.Unix(0, int64(binary.LittleEndian.Uint64(digest[:8]))), size + meta.Size(), nil
}

func (p *Provider) cursorWorkspaceProjects() map[string]string {
	candidates := make(map[string]string)
	conflicts := make(map[string]bool)
	workspaces, _ := os.ReadDir(p.chatsRoot)
	for _, workspace := range workspaces {
		if !workspace.IsDir() {
			continue
		}
		workspacePath := filepath.Join(p.chatsRoot, workspace.Name())
		sessions, _ := os.ReadDir(workspacePath)
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			meta, err := readCursorSidecar(filepath.Join(workspacePath, session.Name(), "store.db"))
			if err != nil || meta == nil || meta.CWD == "" {
				continue
			}
			cwd := util.NormalizeProjectPath(meta.CWD)
			if previous := candidates[workspace.Name()]; previous != "" && previous != cwd {
				conflicts[workspace.Name()] = true
			} else {
				candidates[workspace.Name()] = cwd
			}
		}
		if candidates[workspace.Name()] == "" {
			candidates[workspace.Name()] = cursorWorkspaceStorageProject(config.HomeDir(), workspace.Name())
		}
	}
	for workspace := range conflicts {
		delete(candidates, workspace)
	}
	return candidates
}

func cursorWorkspaceStorageProject(home, workspace string) string {
	root := filepath.Join(home, ".config", "Cursor", "User", "workspaceStorage")
	switch runtime.GOOS {
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support", "Cursor", "User", "workspaceStorage")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			root = filepath.Join(appData, "Cursor", "User", "workspaceStorage")
		}
	}
	data, err := os.ReadFile(filepath.Join(root, workspace, "workspace.json"))
	if err != nil {
		return ""
	}
	var state struct {
		Folder string `json:"folder"`
	}
	if json.Unmarshal(data, &state) != nil || state.Folder == "" {
		return ""
	}
	u, err := url.Parse(state.Folder)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

func (p *Provider) summarizeStore(path, workspaceProject string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	mtime, size, err := cursorStoreSourceStamp(path, st)
	if err != nil {
		return model.Summary{}, err
	}
	id := filepath.Base(filepath.Dir(path))
	project := filepath.Base(filepath.Dir(filepath.Dir(path)))
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return model.Summary{}, err
	}
	defer db.Close()
	title := "(cursor session)"
	created := st.ModTime()
	updated, _, _ := provider.SQLiteSourceStamp(path, st)
	kind := cursorSessionKind(id)
	var migration *model.MigrationMeta
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k string
			var v []byte
			if rows.Scan(&k, &v) != nil {
				continue
			}
			if k == model.MigrationType {
				migration, _ = model.ParseMigrationMeta(v)
				continue
			}
			if k != "0" && k != "composerData" {
				continue
			}
			if meta := decodeCursorMeta(v); meta != nil {
				if value, _ := meta["agentId"].(string); value != "" {
					id = value
				}
				if value, _ := meta["name"].(string); value != "" {
					if polished := polishCursorStoredTitle(value); polished != "" {
						title = polished
					}
				}
				if value, _ := meta["title"].(string); value != "" && title == "(cursor session)" {
					if polished := polishCursorStoredTitle(value); polished != "" {
						title = polished
					}
				}
				if value, ok := meta["createdAt"].(float64); ok && value > 0 {
					created = time.UnixMilli(int64(value))
				}
				if value, _ := meta["projectPath"].(string); value != "" {
					project = value
				} else if value, _ := meta["workspaceUri"].(string); strings.HasPrefix(value, "file://") {
					project = strings.TrimPrefix(value, "file://")
				}
			}
		}
	}
	if sidecar, sidecarErr := readCursorSidecar(path); sidecarErr == nil && sidecar != nil {
		if sidecar.Title != "" {
			title = sidecar.Title
		}
		if sidecar.CWD != "" {
			project = sidecar.CWD
		} else if workspaceProject != "" {
			project = workspaceProject
		}
		if sidecar.CreatedAtMillis > 0 {
			created = time.UnixMilli(sidecar.CreatedAtMillis)
		}
		if sidecar.UpdatedAtMillis > 0 {
			updated = time.UnixMilli(sidecar.UpdatedAtMillis)
		}
		if sidecar.IsSubagent {
			kind = model.SessionKindSubagent
		}
	}
	if project == filepath.Base(filepath.Dir(filepath.Dir(path))) {
		project = workspaceProject
	}
	project = canonicalProject(project)
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
		UpdatedAt: updated, CreatedAt: created,
		StoragePath: path, SourceMtime: mtime.UnixNano(), SourceSize: size,
		Kind: kind, SourcePriority: 10, Migration: migration,
	}, nil
}

func decodeCursorMeta(value []byte) map[string]any {
	data := bytes.TrimSpace(value)
	if decoded, err := hex.DecodeString(string(data)); err == nil {
		data = decoded
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return nil
	}
	return meta
}

func (p *Provider) summarizeTranscript(path string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	project := util.DecodeCursorProjectPath(transcriptProjectDir(path))
	picker := util.NewTitlePicker(80)
	var migration *model.MigrationMeta
	var msgCount int
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if value, _ := row["cwd"].(string); value != "" {
			project = value
		} else if value, _ := row["projectPath"].(string); value != "" {
			project = value
		}
		role, _ := row["role"].(string)
		if role == "" {
			if msg, ok := row["message"].(map[string]any); ok {
				role, _ = msg["role"].(string)
			}
		}
		text := extractCursorMessage(row)
		if role == "user" {
			text = unwrapCursorUserQuery(text)
		}
		if role != "user" && role != "assistant" {
			return nil
		}
		if text == "" {
			return nil
		}
		msgCount++
		if role == "user" {
			picker.Note(text)
		}
		return nil
	}); err != nil {
		return model.Summary{}, err
	}
	title := picker.Title()
	if title == "" {
		err := util.ReadJSONLLines(path, 40, func(line []byte) error {
			var row map[string]any
			if json.Unmarshal(line, &row) != nil {
				return nil
			}
			text := extractCursorMessage(row)
			if text != "" {
				picker.Note(text)
				if picker.Title() != "" {
					return io.EOF
				}
			}
			return nil
		})
		if err != nil && err != io.EOF {
			return model.Summary{}, err
		}
		title = picker.Title()
	}
	if title == "" && project != "" {
		title = util.FirstUserSnippet(util.TildePath(project), 80)
	}
	if title == "" {
		title = "(transcript)"
	}
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: canonicalProject(project), Title: title,
		UpdatedAt: st.ModTime(), CreatedAt: st.ModTime(), MessageCount: msgCount,
		StoragePath: path, SourceMtime: st.ModTime().UnixNano(), SourceSize: st.Size(),
		Kind: cursorSessionKind(id), Migration: migration,
	}, nil
}

// transcriptProjectDir returns the encoded project dir name for a transcript
// path, i.e. the segment before "agent-transcripts" — transcripts sit either
// directly in that dir or nested one level per session.
func transcriptProjectDir(path string) string {
	dir := filepath.Dir(path)
	for dir != "" {
		parent := filepath.Dir(dir)
		if filepath.Base(dir) == "agent-transcripts" {
			return filepath.Base(parent)
		}
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Base(filepath.Dir(filepath.Dir(path)))
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	path := ref.StoragePath
	if path == "" {
		return nil, provider.ErrNotFound
	}
	if strings.HasSuffix(path, ".jsonl") {
		return p.loadTranscript(path, ref.ID)
	}
	if transcript := p.transcriptForRef(ref); transcript != "" {
		conv, err := p.loadTranscript(transcript, ref.ID)
		if err != nil {
			return nil, err
		}
		p.enrichStoreConversationAt(conv, ref.ProjectPath, path)
		return conv, nil
	}
	conv, err := p.loadStore(path, ref.ID)
	if err != nil {
		return nil, err
	}
	p.enrichStoreConversation(conv, ref.ProjectPath)
	return conv, nil
}

func (p *Provider) transcriptForRef(ref provider.SessionRef) string {
	if ref.ID == "" || ref.ProjectPath == "" {
		return ""
	}
	encoded := strings.TrimPrefix(util.EncodeClaudeProjectPath(util.NormalizeProjectPath(ref.ProjectPath)), "-")
	root := filepath.Join(p.projectsRoot, encoded, "agent-transcripts")
	for _, candidate := range []string{
		filepath.Join(root, ref.ID, ref.ID+".jsonl"),
		filepath.Join(root, ref.ID+".jsonl"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (p *Provider) LoadPreview(ctx context.Context, ref provider.SessionRef, limit int) (*model.Conversation, error) {
	if strings.HasSuffix(ref.StoragePath, ".jsonl") {
		conv, err := p.loadTranscript(ref.StoragePath, ref.ID)
		if err == nil && limit > 0 && len(conv.Messages) > limit {
			conv.Messages = conv.Messages[len(conv.Messages)-limit:]
			conv.MessageCount = len(conv.Messages)
		}
		return conv, err
	}
	db, err := sql.Open("sqlite", ref.StoragePath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, StoragePath: ref.StoragePath, ProjectPath: ref.ProjectPath,
	}
	if rows, queryErr := db.Query(`SELECT key, value FROM meta`); queryErr == nil {
		for rows.Next() {
			var key string
			var value []byte
			if rows.Scan(&key, &value) != nil {
				continue
			}
			if key == model.MigrationType {
				conv.Migration, _ = model.ParseMigrationMeta(value)
				continue
			}
			if key != "0" && key != "composerData" {
				continue
			}
			if meta := decodeCursorMeta(value); meta != nil {
				if id, _ := meta["agentId"].(string); id != "" {
					conv.ID = id
				}
				if title, _ := meta["name"].(string); title != "" {
					conv.Title = title
				}
				if created, ok := meta["createdAt"].(float64); ok && created > 0 {
					conv.CreatedAt = time.UnixMilli(int64(created))
				}
			}
		}
		_ = rows.Close()
	}
	if limit <= 0 {
		limit = 40
	}
	oversample := max(limit*20, 800)
	rows, err := db.QueryContext(ctx, `SELECT data FROM blobs ORDER BY rowid DESC LIMIT ?`, oversample)
	if err != nil {
		return nil, err
	}
	var blobs [][]byte
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			_ = rows.Close()
			return nil, err
		}
		blobs = append(blobs, data)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	seen := make(map[string]bool)
	for i := len(blobs) - 1; i >= 0; i-- {
		role, text, timestamp := cursorStoredMessage(blobs[i])
		if (role != "user" && role != "assistant") || text == "" {
			continue
		}
		key := role + "\x00" + text
		if seen[key] {
			continue
		}
		seen[key] = true
		messageRole := model.RoleUser
		if role == "assistant" {
			messageRole = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{Role: messageRole, Content: text, Timestamp: timestamp})
		if timestamp.After(conv.UpdatedAt) {
			conv.UpdatedAt = timestamp
		}
	}
	if len(conv.Messages) > limit {
		conv.Messages = conv.Messages[len(conv.Messages)-limit:]
	}
	conv.MessageCount = len(conv.Messages)
	p.enrichStoreConversation(conv, ref.ProjectPath)
	return conv, nil
}

func (p *Provider) enrichStoreConversation(conv *model.Conversation, projectFallback string) {
	p.enrichStoreConversationAt(conv, projectFallback, conv.StoragePath)
}

func (p *Provider) enrichStoreConversationAt(conv *model.Conversation, projectFallback, storePath string) {
	if conv.ProjectPath == "" {
		conv.ProjectPath = projectFallback
	}
	if sidecar, err := readCursorSidecar(storePath); err == nil && sidecar != nil {
		if sidecar.Title != "" {
			conv.Title = sidecar.Title
		}
		if sidecar.CWD != "" {
			conv.ProjectPath = sidecar.CWD
		}
		if sidecar.CreatedAtMillis > 0 {
			conv.CreatedAt = time.UnixMilli(sidecar.CreatedAtMillis)
		}
		if sidecar.UpdatedAtMillis > 0 {
			conv.UpdatedAt = time.UnixMilli(sidecar.UpdatedAtMillis)
		}
	}
}

// canonicalProject gives every cursor summary one representation of the same
// directory. Workspace-derived paths already run through NormalizeProjectPath,
// while sidecar CWDs do not; on macOS that produced both /var/... and
// /private/var/... for one project, splitting it across --cwd filters and
// project grouping. Bare workspace names are left alone: they are not paths, so
// resolving them would anchor them to the process working directory.
func canonicalProject(project string) string {
	if project == "" || !filepath.IsAbs(project) {
		return project
	}
	return util.NormalizeProjectPath(project)
}

func (p *Provider) loadTranscript(path, id string) (*model.Conversation, error) {
	conv := &model.Conversation{
		ID: id, Provider: ProviderID, StoragePath: path,
		ProjectPath: canonicalProject(util.DecodeCursorProjectPath(transcriptProjectDir(path))),
	}
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			conv.Migration = meta
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if value, _ := row["cwd"].(string); value != "" {
			conv.ProjectPath = value
		} else if value, _ := row["projectPath"].(string); value != "" {
			conv.ProjectPath = value
		}
		roleStr, _ := row["role"].(string)
		if roleStr != "user" && roleStr != "assistant" {
			return nil
		}
		text := extractCursorMessage(row)
		if roleStr == "user" {
			text = unwrapCursorUserQuery(text)
		}
		if text == "" {
			return nil
		}
		role := model.RoleUser
		if roleStr == "assistant" {
			role = model.RoleAssistant
		}
		timestamp, _ := row["timestamp"].(string)
		ts := util.ParseTime(timestamp)
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: text, Timestamp: ts})
		if conv.CreatedAt.IsZero() && !ts.IsZero() {
			conv.CreatedAt = ts
		}
		if ts.After(conv.UpdatedAt) {
			conv.UpdatedAt = ts
		}
		if conv.Title == "" && role == model.RoleUser {
			conv.Title = util.FirstUserSnippet(text, 80)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	return conv, nil
}

func extractCursorMessage(row map[string]any) string {
	if msg, ok := row["message"].(map[string]any); ok {
		if text, _ := msg["content"].(string); text != "" {
			return cursorVisibleText(text)
		}
		if c, ok := msg["content"].([]any); ok {
			var parts []string
			for _, item := range c {
				if part, ok := item.(map[string]any); ok {
					if t, _ := part["text"].(string); t != "" {
						if visible := cursorVisibleText(t); visible != "" {
							parts = append(parts, visible)
						}
					}
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func unwrapCursorUserQuery(text string) string {
	const open, close = "<user_query>", "</user_query>"
	if strings.HasPrefix(text, open) && strings.HasSuffix(text, close) {
		return strings.TrimSuffix(strings.TrimPrefix(text, open), close)
	}
	return text
}

func (p *Provider) loadStore(path, id string) (*model.Conversation, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	conv := &model.Conversation{ID: id, Provider: ProviderID, StoragePath: path}
	rootID := ""
	if metaRows, metaErr := db.Query(`SELECT key, value FROM meta`); metaErr == nil {
		for metaRows.Next() {
			var key string
			var value []byte
			if metaRows.Scan(&key, &value) != nil {
				continue
			}
			if key == model.MigrationType {
				conv.Migration, _ = model.ParseMigrationMeta(value)
			}
			if key == "0" || key == "composerData" {
				if meta := decodeCursorMeta(value); meta != nil {
					if v, _ := meta["latestRootBlobId"].(string); v != "" {
						rootID = strings.ToLower(v)
					}
					if v, _ := meta["name"].(string); v != "" {
						conv.Title = v
					}
					if v, _ := meta["projectPath"].(string); v != "" {
						conv.ProjectPath = v
					} else if v, _ := meta["workspaceUri"].(string); strings.HasPrefix(v, "file://") {
						conv.ProjectPath = strings.TrimPrefix(v, "file://")
					}
					if v, ok := meta["createdAt"].(float64); ok && v > 0 {
						conv.CreatedAt = time.UnixMilli(int64(v))
					}
				}
			}
		}
		_ = metaRows.Close()
	}
	appendMessage := func(data []byte) {
		roleStr, text, timestamp := cursorStoredMessage(data)
		if roleStr == "" || text == "" {
			return
		}
		role := model.RoleUser
		if roleStr == "assistant" {
			role = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: text, Timestamp: timestamp})
		if timestamp.After(conv.UpdatedAt) {
			conv.UpdatedAt = timestamp
		}
	}
	if rootID != "" {
		root, err := cursorLoadBlobs(db, []string{rootID})
		if err != nil {
			return nil, err
		}
		visited := make(map[string]bool)
		var visit func(string, []byte) error
		visit = func(blobID string, data []byte) error {
			blobID = strings.ToLower(blobID)
			if visited[blobID] {
				return nil
			}
			visited[blobID] = true
			appendMessage(data)
			children := cursorBlobReferences(data)
			if blobID == rootID {
				children = cursorRootBlobReferences(data)
			}
			pending := children[:0]
			for _, child := range children {
				if !visited[strings.ToLower(child)] {
					pending = append(pending, child)
				}
			}
			loaded, err := cursorLoadBlobs(db, pending)
			if err != nil {
				return err
			}
			for _, child := range pending {
				child = strings.ToLower(child)
				if childData, ok := loaded[child]; ok {
					if err := visit(child, childData); err != nil {
						return err
					}
				}
			}
			return nil
		}
		rootID = strings.ToLower(rootID)
		if data, ok := root[rootID]; ok {
			if err := visit(rootID, data); err != nil {
				return nil, err
			}
		}
	} else {
		// Older stores without root metadata cannot identify stale branches.
		rows, err := db.Query(`SELECT data FROM blobs ORDER BY rowid`)
		if err != nil {
			return nil, provider.ErrNotFound
		}
		for rows.Next() {
			var data []byte
			if rows.Scan(&data) == nil {
				appendMessage(data)
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	return conv, nil
}

func cursorRootBlobReferences(data []byte) []string {
	data = cursorBlobData(data)
	var refs []string
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 {
			break
		}
		data = data[n:]
		switch tag & 7 {
		case 0:
			_, n = binary.Uvarint(data)
			if n <= 0 {
				return refs
			}
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return refs
			}
			data = data[8:]
		case 2:
			length, size := binary.Uvarint(data)
			if size <= 0 || length > uint64(len(data)-size) {
				return refs
			}
			value := data[size : size+int(length)]
			field := tag >> 3
			if (field == 1 || field == 8) && len(value) == sha256.Size {
				refs = append(refs, hex.EncodeToString(value))
			}
			data = data[size+int(length):]
		case 5:
			if len(data) < 4 {
				return refs
			}
			data = data[4:]
		default:
			return refs
		}
	}
	return refs
}

func cursorLoadBlobs(db *sql.DB, ids []string) (map[string][]byte, error) {
	const batchSize = 500
	out := make(map[string][]byte, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		args := make([]any, 0, end-start)
		placeholders := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, strings.ToLower(id))
			placeholders = append(placeholders, "?")
		}
		rows, err := db.Query(`SELECT id, data FROM blobs WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var data []byte
			if err := rows.Scan(&id, &data); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[strings.ToLower(id)] = data
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func cursorBlobReferences(data []byte) []string {
	data = cursorBlobData(data)
	var refs []string
	seen := make(map[string]bool)
	add := func(id string) {
		id = strings.ToLower(id)
		if !seen[id] {
			seen[id] = true
			refs = append(refs, id)
		}
	}
	for i := 0; i+64 <= len(data); i++ {
		candidate := string(data[i : i+64])
		if _, err := hex.DecodeString(candidate); err == nil {
			add(candidate)
			i += 63
		}
	}
	var walkProto func([]byte)
	walkProto = func(raw []byte) {
		for len(raw) > 0 {
			tag, n := binary.Uvarint(raw)
			if n <= 0 {
				return
			}
			raw = raw[n:]
			switch tag & 7 {
			case 0:
				_, n = binary.Uvarint(raw)
				if n <= 0 {
					return
				}
				raw = raw[n:]
			case 1:
				if len(raw) < 8 {
					return
				}
				raw = raw[8:]
			case 2:
				length, size := binary.Uvarint(raw)
				if size <= 0 || length > uint64(len(raw)-size) {
					return
				}
				value := raw[size : size+int(length)]
				if len(value) == sha256.Size {
					add(hex.EncodeToString(value))
				} else {
					walkProto(value)
				}
				raw = raw[size+int(length):]
			case 5:
				if len(raw) < 4 {
					return
				}
				raw = raw[4:]
			default:
				return
			}
		}
	}
	walkProto(data)
	return refs
}

func cursorBlobData(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if decoded, err := hex.DecodeString(string(trimmed)); err == nil {
		return decoded
	}
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return trimmed
	}
	return data
}

func cursorStoredMessage(data []byte) (string, string, time.Time) {
	data = cursorBlobData(data)
	if len(data) == 0 {
		return "", "", time.Time{}
	}
	tryJSON := func(raw []byte) (string, string, time.Time) {
		var payload map[string]any
		if json.Unmarshal(raw, &payload) != nil {
			return "", "", time.Time{}
		}
		role, _ := payload["role"].(string)
		if role != "user" && role != "assistant" {
			return "", "", time.Time{}
		}
		timestamp, _ := payload["timestamp"].(string)
		return role, cursorContent(payload["content"]), util.ParseTime(timestamp)
	}
	if role, text, timestamp := tryJSON(data); role != "" && text != "" {
		return role, text, timestamp
	}
	// Cursor also wraps JSON messages in protobuf blobs. Find a complete JSON
	// object instead of treating arbitrary protobuf bytes as UTF-8 text.
	for start := bytes.IndexByte(data, '{'); start >= 0; {
		if raw := embeddedJSONObject(data[start:]); raw != nil {
			if role, text, timestamp := tryJSON(raw); role != "" && text != "" {
				return role, text, timestamp
			}
		}
		next := bytes.IndexByte(data[start+1:], '{')
		if next < 0 {
			break
		}
		start += next + 1
	}
	return "", "", time.Time{}
}

func cursorContent(value any) string {
	if text, _ := value.(string); text != "" {
		return cursorVisibleText(text)
	}
	blocks, _ := value.([]any)
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if item, ok := block.(map[string]any); ok {
			if text, _ := item["text"].(string); text != "" {
				if visible := cursorVisibleText(text); visible != "" {
					parts = append(parts, visible)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func cursorVisibleText(text string) string {
	if !strings.Contains(text, "[REDACTED]") {
		return text
	}
	lines := strings.Split(text, "\n")
	visible := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) != "[REDACTED]" {
			visible = append(visible, line)
		}
	}
	return strings.TrimSpace(strings.Join(visible, "\n"))
}

func embeddedJSONObject(data []byte) []byte {
	depth, inString, escaped := 0, false, false
	for i, b := range data {
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return data[:i+1]
			}
		}
	}
	return nil
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
	abs, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	// "/home/x/proj" encodes to "home-x-proj", which DecodeCursorProjectPath
	// reverses; prefixing another "home-" made rediscovery decode /home/home/x.
	encoded := strings.TrimPrefix(util.EncodeClaudeProjectPath(abs), "-")
	sessionID := uuid.New().String()
	transcriptDir := filepath.Join(p.projectsRoot, encoded, "agent-transcripts", sessionID)
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")
	projectHash := fmt.Sprintf("%x", md5.Sum([]byte(abs)))
	storeDir := filepath.Join(p.chatsRoot, projectHash, sessionID)
	storePath := filepath.Join(storeDir, "store.db")
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: storePath, ProjectPath: abs}, nil
	}
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		_ = os.Remove(transcriptDir)
		return nil, err
	}
	meta := model.NewMigrationMeta(conv)
	var lines []string
	for i, m := range conv.Messages {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		text := m.PlainText()
		if m.Role == model.RoleUser {
			text = "<user_query>" + text + "</user_query>"
		}
		row := map[string]any{
			"role":      string(m.Role),
			"timestamp": cursorTimestamp(m.Timestamp, conv.CreatedAt, i).Format(time.RFC3339Nano),
			"cwd":       abs,
			"message": map[string]any{
				"role":    string(m.Role),
				"content": []map[string]any{{"type": "text", "text": text}},
			},
		}
		b, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			return nil, marshalErr
		}
		lines = append(lines, string(b))
	}
	header, err := json.Marshal(map[string]any{"type": model.MigrationType, "data": meta})
	if err != nil {
		return nil, err
	}
	lines = append(lines, string(header))
	if err := util.WriteFileAtomic(transcriptPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return nil, errors.Join(err, p.cleanupCursorPaths(storePath, transcriptPath))
	}
	if err := writeCursorNativeStore(storePath, sessionID, abs, conv); err != nil {
		return nil, errors.Join(err, p.cleanupCursorPaths(storePath, transcriptPath))
	}
	if err := writeCursorNativeSidecar(cursorSidecarPath(storePath), abs, conv); err != nil {
		return nil, errors.Join(err, p.cleanupCursorPaths(storePath, transcriptPath))
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: storePath, ProjectPath: abs}, nil
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	return "cursor-agent --resume " + util.ShellQuote(r.SessionID)
}

func (p *Provider) DeleteSession(ctx context.Context, ref provider.SessionRef) error {
	return p.CleanupWrite(ctx, provider.WriteResult{
		SessionID: ref.ID, StoragePath: ref.StoragePath, ProjectPath: ref.ProjectPath,
	})
}

func (p *Provider) CleanupWrite(_ context.Context, r provider.WriteResult) error {
	storePath := r.StoragePath
	if storePath == "" {
		projectHash := fmt.Sprintf("%x", md5.Sum([]byte(r.ProjectPath)))
		storePath = filepath.Join(p.chatsRoot, projectHash, r.SessionID, "store.db")
	}
	abs, err := filepath.Abs(r.ProjectPath)
	if err != nil {
		return err
	}
	encoded := strings.TrimPrefix(util.EncodeClaudeProjectPath(abs), "-")
	transcript := filepath.Join(p.projectsRoot, encoded, "agent-transcripts", r.SessionID, r.SessionID+".jsonl")
	return p.cleanupCursorPaths(storePath, transcript)
}

func (p *Provider) cleanupCursorPaths(storePath, transcriptPath string) error {
	storePaths := []string{storePath, storePath + "-wal", storePath + "-shm", cursorSidecarPath(storePath), filepath.Dir(storePath)}
	transcriptPaths := []string{transcriptPath, filepath.Dir(transcriptPath)}
	for _, path := range storePaths {
		if !cursorCleanupPath(p.chatsRoot, path) {
			return fmt.Errorf("cursor: refusing cleanup outside chats root: %s", path)
		}
	}
	for _, path := range transcriptPaths {
		if !cursorCleanupPath(p.projectsRoot, path) {
			return fmt.Errorf("cursor: refusing cleanup outside projects root: %s", path)
		}
	}
	var cleanupErr error
	for _, path := range append(storePaths[:4], transcriptPaths[0]) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	for _, dir := range []string{storePaths[4], transcriptPaths[1]} {
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func cursorCleanupPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cursorTimestamp(ts, created time.Time, index int) time.Time {
	if !ts.IsZero() {
		return ts.UTC()
	}
	if created.IsZero() {
		created = time.Now()
	}
	return created.UTC().Add(time.Duration(index) * time.Millisecond)
}

func writeCursorSidecar(path, project string, conv *model.Conversation) error {
	created := conv.CreatedAt
	updated := conv.UpdatedAt
	for _, message := range conv.Messages {
		if created.IsZero() && !message.Timestamp.IsZero() {
			created = message.Timestamp
		}
		if message.Timestamp.After(updated) {
			updated = message.Timestamp
		}
	}
	if created.IsZero() {
		created = time.Now()
	}
	if updated.IsZero() {
		updated = created
	}
	title := conv.Title
	if title == "" {
		title = "Migrated Chat"
	}
	data, err := json.Marshal(cursorSidecar{
		SchemaVersion: 1, CreatedAtMillis: created.UnixMilli(), UpdatedAtMillis: updated.UnixMilli(),
		HasConversation: true, Title: title, CWD: project,
	})
	if err != nil {
		return err
	}
	return util.WriteFileAtomic(path, data, 0o600)
}

type cursorStoreBlob struct {
	id   string
	data []byte
}

func writeCursorStore(path, sessionID, project string, conv *model.Conversation) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agenthop-store-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	db, err := sql.Open("sqlite", tmpPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	closeDB := func() error { return db.Close() }
	if _, err = db.Exec(`PRAGMA user_version=1; CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB); CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);`); err != nil {
		_ = closeDB()
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		_ = closeDB()
		return err
	}
	blobs, rootID, err := buildCursorBlobs(conv, project)
	if err != nil {
		_ = tx.Rollback()
		_ = closeDB()
		return err
	}
	for _, blob := range blobs {
		if _, err = tx.Exec(`INSERT INTO blobs (id, data) VALUES (?, ?)`, blob.id, blob.data); err != nil {
			_ = tx.Rollback()
			_ = closeDB()
			return err
		}
	}
	created := conv.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	name := conv.Title
	if name == "" {
		name = "Migrated Chat"
	}
	metaJSON, err := json.Marshal(map[string]any{
		"agentId": sessionID, "latestRootBlobId": rootID, "name": name,
		"mode": "default", "createdAt": created.UnixMilli(),
		"projectPath": project, "workspaceUri": "file://" + project,
	})
	if err != nil {
		_ = tx.Rollback()
		_ = closeDB()
		return err
	}
	migrationJSON, err := json.Marshal(model.NewMigrationMeta(conv))
	if err != nil {
		_ = tx.Rollback()
		_ = closeDB()
		return err
	}
	if _, err = tx.Exec(`INSERT INTO meta (key, value) VALUES ('0', ?), (?, ?)`, hex.EncodeToString(metaJSON), model.MigrationType, string(migrationJSON)); err != nil {
		_ = tx.Rollback()
		_ = closeDB()
		return err
	}
	if err = tx.Commit(); err != nil {
		_ = closeDB()
		return err
	}
	if err = closeDB(); err != nil {
		return err
	}
	if err = os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func buildCursorBlobs(conv *model.Conversation, project string) ([]cursorStoreBlob, string, error) {
	var blobs []cursorStoreBlob
	var rootHashes [][]byte
	seen := map[string]bool{}
	var messages []model.Message
	store := func(data []byte) []byte {
		hash := sha256.Sum256(data)
		id := hex.EncodeToString(hash[:])
		if !seen[id] {
			blobs = append(blobs, cursorStoreBlob{id: id, data: data})
			seen[id] = true
		}
		return hash[:]
	}
	for i, message := range conv.Messages {
		if message.Role != model.RoleUser && message.Role != model.RoleAssistant {
			continue
		}
		messages = append(messages, message)
		payload, err := json.Marshal(map[string]any{
			"role":           message.Role,
			"content":        []map[string]any{{"type": "text", "text": message.PlainText()}},
			"_agenthopOrder": i,
			"timestamp":      cursorTimestamp(message.Timestamp, conv.CreatedAt, i).Format(time.RFC3339Nano),
		})
		if err != nil {
			return nil, "", err
		}
		rootHashes = append(rootHashes, store(payload))
	}
	var turnHashes [][]byte
	var pendingUser *model.Message
	var pendingAssistants []model.Message
	flushTurn := func() {
		if pendingUser == nil {
			return
		}
		user := appendProtoBytes(nil, 1, []byte(pendingUser.PlainText()))
		user = appendProtoBytes(user, 2, []byte(uuid.New().String()))
		userHash := store(user)
		agentTurn := appendProtoBytes(nil, 1, userHash)
		for _, assistant := range pendingAssistants {
			step := appendProtoBytes(nil, 1, []byte(assistant.PlainText()))
			agentTurn = appendProtoBytes(agentTurn, 2, store(step))
		}
		agentTurn = appendProtoBytes(agentTurn, 3, []byte(uuid.New().String()))
		turnHashes = append(turnHashes, store(appendProtoBytes(nil, 1, agentTurn)))
		pendingUser = nil
		pendingAssistants = nil
	}
	for i := range messages {
		message := messages[i]
		if message.Role == model.RoleUser {
			flushTurn()
			pendingUser = &messages[i]
		} else if pendingUser != nil {
			pendingAssistants = append(pendingAssistants, message)
		}
	}
	flushTurn()
	root := []byte{}
	for _, hash := range rootHashes {
		root = appendProtoBytes(root, 1, hash)
	}
	for _, hash := range turnHashes {
		root = appendProtoBytes(root, 8, hash)
	}
	root = appendProtoBytes(root, 9, []byte("file://"+project))
	root = append(root, byte(10<<3), 0)
	rootHash := store(root)
	// Keep conversation rows first for deterministic round trips; the root blob
	// follows them and Cursor resolves it through latestRootBlobId.
	return blobs, hex.EncodeToString(rootHash), nil
}

func appendProtoBytes(dst []byte, field uint64, value []byte) []byte {
	dst = appendUvarint(dst, field<<3|2)
	dst = appendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendUvarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}
