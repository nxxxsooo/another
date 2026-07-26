package claude

import (
	"context"
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
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, "observer-sessions") && strings.Contains(path, "claude-mem") {
			return nil
		}
		if opts.SkipUnchanged != nil {
			if info, err := d.Info(); err == nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
				return nil
			}
		}
		sm, err := p.summarizeFile(path)
		if err != nil || sm.ID == "" {
			return nil
		}
		if opts.ProjectFilter != "" && !strings.Contains(sm.ProjectPath, opts.ProjectFilter) {
			return nil
		}
		out = append(out, sm)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return filepath.SkipAll
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return nil
	})
	return out, nil
}

type claudeLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	UUID      string `json:"uuid"`
	IsMeta    bool   `json:"isMeta"`
	Message   *struct {
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
	encoded := filepath.Base(filepath.Dir(path))
	project := util.DecodeClaudeProjectPath(encoded)
	picker := util.NewTitlePicker(80)
	var msgCount int
	var first, last time.Time
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		var row claudeLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.Type != "user" && row.Type != "assistant" {
			if row.SessionID != "" {
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
	})
	title := picker.TitleOr("(no title)")
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
		CreatedAt: first, UpdatedAt: last, MessageCount: msgCount,
		StoragePath: path, SourceMtime: st.ModTime().Unix(),
	}, nil
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
	encoded := filepath.Base(filepath.Dir(path))
	project := util.DecodeClaudeProjectPath(encoded)
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, ProjectPath: project, StoragePath: path,
	}
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		var row claudeLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.SessionID != "" {
			conv.ID = row.SessionID
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
	})
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
	meta := model.NewMigrationMeta(conv)
	var lines []string
	progress := map[string]any{
		"type": "progress", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"uuid": uuid.New().String(),
		"data": meta,
	}
	if b, err := json.Marshal(progress); err == nil {
		lines = append(lines, string(b))
	}
	var parent string
	for _, m := range conv.Messages {
		u := uuid.New().String()
		entryType := "user"
		role := "user"
		content := m.PlainText()
		if m.Role == model.RoleAssistant {
			entryType = "assistant"
			role = "assistant"
		}
		row := map[string]any{
			"type": entryType, "sessionId": sessionID,
			"timestamp": m.Timestamp.UTC().Format(time.RFC3339Nano),
			"uuid": u, "parentUuid": parent,
			"message": map[string]any{"role": role, "content": content},
		}
		if parent == "" {
			delete(row, "parentUuid")
		}
		b, _ := json.Marshal(row)
		lines = append(lines, string(b))
		parent = u
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	if r.ProjectPath != "" {
		return "cd " + r.ProjectPath + " && claude --resume " + r.SessionID
	}
	return "claude --resume " + r.SessionID
}
