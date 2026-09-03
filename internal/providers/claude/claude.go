package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
)

const ProviderID = "claude-code"

type Provider struct {
	root string
}

func New() *Provider {
	root := config.EnvOrDefault("CLAUDE_CONFIG_DIR", filepath.Join(config.HomeDir(), ".claude"))
	return &Provider{root: root}
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) Installed() bool {
	st, err := os.Stat(filepath.Join(p.root, "projects"))
	return err == nil && st.IsDir()
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "projects", Path: filepath.Join(p.root, "projects"), Env: "CLAUDE_CONFIG_DIR"}}
}

func (p *Provider) projectsRoot() string {
	return filepath.Join(p.root, "projects")
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	root := p.projectsRoot()
	var out []model.Summary
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, "observer-sessions") && strings.Contains(path, "claude-mem") {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			if opts.SkipSource != nil && opts.SkipSource(path, info.ModTime().UnixNano(), info.Size()) {
				return nil
			}
			if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
				return nil
			}
		}
		sm, err := p.summarizeFile(path)
		if err != nil {
			return err
		}
		if sm.ID == "" {
			return fmt.Errorf("empty session id in %s", path)
		}
		if opts.ProjectFilter != "" && !strings.Contains(sm.ProjectPath, opts.ProjectFilter) {
			return nil
		}
		out = append(out, sm)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out, err
}

type claudeLine struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Timestamp   string `json:"timestamp"`
	UUID        string `json:"uuid"`
	ParentUUID  string `json:"parentUuid"`
	CWD         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	CustomTitle string `json:"customTitle"`
	Message     *struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"message"`
}

func (p *Provider) summarizeFile(path string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	base := filepath.Base(path)
	id := strings.TrimSuffix(base, ".jsonl")
	encoded := p.encodedProjectForPath(path)
	project := util.DecodeClaudeProjectPath(encoded)
	kind := model.SessionKindRoot
	parentID := ""
	if filepath.Base(filepath.Dir(path)) == "subagents" || strings.Contains(filepath.ToSlash(path), "/subagents/") {
		kind = model.SessionKindSubagent
	}
	picker := util.NewTitlePicker(80)
	var migration *model.MigrationMeta
	var msgCount int
	var first, last time.Time
	var customTitle string
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		var row claudeLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.CWD != "" {
			project = row.CWD
		}
		if row.IsSidechain {
			kind = model.SessionKindSubagent
		}
		if kind == model.SessionKindSubagent && parentID == "" && row.SessionID != "" && row.SessionID != id {
			parentID = row.SessionID
		}
		if row.Type == "custom-title" && strings.TrimSpace(row.CustomTitle) != "" {
			customTitle = strings.TrimSpace(row.CustomTitle)
		}
		if row.Type != "user" && row.Type != "assistant" {
			if kind == model.SessionKindRoot && row.SessionID != "" {
				id = row.SessionID
			}
			return nil
		}
		if row.Message == nil {
			return nil
		}
		if row.IsMeta {
			return nil
		}
		msgCount++
		ts := util.ParseTime(row.Timestamp)
		if first.IsZero() {
			first = ts
		}
		last = ts
		if row.Message.Role == "user" {
			picker.Note(contentString(row.Message.Content))
		}
		return nil
	}); err != nil {
		return model.Summary{}, err
	}
	if first.IsZero() {
		first = st.ModTime()
	}
	if last.IsZero() {
		last = st.ModTime()
	}
	title := customTitle
	if title == "" {
		title = picker.TitleOr("(no title)")
	}
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
		CreatedAt: first, UpdatedAt: last, MessageCount: msgCount,
		StoragePath: path, Kind: kind, ParentID: parentID,
		SourceMtime: st.ModTime().UnixNano(), SourceSize: st.Size(),
		Migration: migration,
	}, nil
}

func (p *Provider) encodedProjectForPath(path string) string {
	rel, err := filepath.Rel(p.projectsRoot(), path)
	if err == nil {
		if first := strings.Split(filepath.ToSlash(rel), "/")[0]; first != "" && first != "." {
			return first
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func contentString(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["text"].(string); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	path := ref.StoragePath
	if path == "" {
		path = filepath.Join(p.projectsRoot(), util.EncodeClaudeProjectPath(ref.ProjectPath), ref.ID+".jsonl")
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	encoded := p.encodedProjectForPath(path)
	project := util.DecodeClaudeProjectPath(encoded)
	isSubagent := strings.Contains(filepath.ToSlash(path), "/subagents/")
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, ProjectPath: project, StoragePath: path,
	}
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			conv.Migration = meta
		}
		var row claudeLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if !isSubagent && row.SessionID != "" {
			conv.ID = row.SessionID
		}
		if row.CWD != "" {
			conv.ProjectPath = row.CWD
		}
		if row.Type == "custom-title" && strings.TrimSpace(row.CustomTitle) != "" {
			conv.Title = strings.TrimSpace(row.CustomTitle)
		}
		if row.Type != "user" && row.Type != "assistant" {
			return nil
		}
		if row.Message == nil || row.IsMeta {
			return nil
		}
		content := contentString(row.Message.Content)
		if content == "" {
			// tool_result-only or injected rows carry no text worth migrating
			return nil
		}
		ts := util.ParseTime(row.Timestamp)
		role := model.RoleUser
		if row.Message.Role == "assistant" || row.Type == "assistant" {
			role = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{
			Role: role, Content: content, Timestamp: ts,
		})
		return nil
	}); err != nil {
		return nil, err
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	conv.CreatedAt = conv.Messages[0].Timestamp
	conv.UpdatedAt = conv.Messages[len(conv.Messages)-1].Timestamp
	if conv.Title == "" {
		picker := util.NewTitlePicker(80)
		for _, m := range conv.Messages {
			if m.Role == model.RoleUser {
				picker.Note(m.PlainText())
			}
		}
		conv.Title = picker.Title()
	}
	_ = st
	return conv, nil
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
	dir := filepath.Join(p.projectsRoot(), util.EncodeClaudeProjectPath(project))
	sessionID := uuid.New().String()
	path := filepath.Join(dir, sessionID+".jsonl")
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var lines []string
	var parent string
	for _, m := range conv.Messages {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		content := m.PlainText()
		if content == "" {
			continue
		}
		u := uuid.New().String()
		entryType := "user"
		role := "user"
		message := map[string]any{"role": role, "content": content}
		if m.Role == model.RoleAssistant {
			entryType = "assistant"
			role = "assistant"
			message = map[string]any{
				"id": uuid.New().String(), "type": "message", "role": role,
				"model":   "unknown",
				"content": []any{map[string]any{"type": "text", "text": content}},
			}
		}
		ts := m.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		row := map[string]any{
			"type": entryType, "sessionId": sessionID,
			"timestamp": ts.UTC().Format(time.RFC3339Nano),
			"uuid":      u, "parentUuid": parent,
			"message": message,
			"cwd":     project, "version": "0.0.0", "gitBranch": "",
			"isSidechain": false, "userType": "external", "entrypoint": "cli",
		}
		if parent == "" {
			delete(row, "parentUuid")
		}
		b, _ := json.Marshal(row)
		lines = append(lines, string(b))
		parent = u
	}
	if len(lines) == 0 {
		return nil, provider.ErrEmptySession
	}
	progress := map[string]any{
		"type": "progress", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"uuid":      uuid.New().String(), "data": model.NewMigrationMeta(conv),
	}
	if b, err := json.Marshal(progress); err != nil {
		return nil, err
	} else {
		lines = append(lines, string(b))
	}
	if err := util.WriteFileAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && claude --resume " + util.ShellQuote(r.SessionID)
	}
	return "claude --resume " + util.ShellQuote(r.SessionID)
}

func (p *Provider) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("claude: title must not be empty")
	}
	path := ref.StoragePath
	if path == "" {
		path = filepath.Join(p.projectsRoot(), util.EncodeClaudeProjectPath(ref.ProjectPath), ref.ID+".jsonl")
	}
	rel, err := filepath.Rel(p.projectsRoot(), path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".jsonl") {
		return fmt.Errorf("claude: refusing rename outside projects root: %s", path)
	}
	row, err := json.Marshal(map[string]any{"type": "custom-title", "customTitle": title, "sessionId": ref.ID})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(row, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (p *Provider) DeleteSession(ctx context.Context, ref provider.SessionRef) error {
	return p.CleanupWrite(ctx, provider.WriteResult{
		SessionID: ref.ID, StoragePath: ref.StoragePath, ProjectPath: ref.ProjectPath,
	})
}

func (p *Provider) CleanupWrite(_ context.Context, r provider.WriteResult) error {
	rel, err := filepath.Rel(p.projectsRoot(), r.StoragePath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".jsonl") {
		return fmt.Errorf("claude: refusing cleanup outside projects root: %s", r.StoragePath)
	}
	if err := os.Remove(r.StoragePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
