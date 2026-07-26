package cursor

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CyrusSE/agenthop/internal/config"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const ProviderID = "cursor"

type Provider struct {
	chatsRoot    string
	projectsRoot string
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
	_ = filepath.WalkDir(p.chatsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "store.db" {
			return nil
		}
		if opts.SkipUnchanged != nil {
			if info, err := d.Info(); err == nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
				return nil
			}
		}
		sm, err := p.summarizeStore(path)
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
		return nil
	})
	_ = filepath.WalkDir(p.projectsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if !strings.Contains(path, "agent-transcripts") {
			return nil
		}
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return filepath.SkipAll
		}
		if opts.SkipUnchanged != nil {
			if info, err := d.Info(); err == nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
				return nil
			}
		}
		sm, err := p.summarizeTranscript(path)
		if err != nil || sm.ID == "" {
			return nil
		}
		out = append(out, sm)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out, nil
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

func (p *Provider) summarizeStore(path string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := filepath.Base(filepath.Dir(path))
	projectHash := filepath.Base(filepath.Dir(filepath.Dir(path)))
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return model.Summary{}, err
	}
	defer db.Close()
	title := "(cursor session)"
	var msgCount int
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k int
			var v []byte
			if rows.Scan(&k, &v) == nil && k == 0 {
				var meta map[string]any
				if json.Unmarshal(v, &meta) == nil {
					if t, _ := meta["title"].(string); t != "" {
						if polished := polishCursorStoredTitle(t); polished != "" {
							title = polished
						}
					}
				}
			}
		}
	}
	rows2, _ := db.Query(`SELECT COUNT(*) FROM blobs`)
	if rows2 != nil {
		defer rows2.Close()
		if rows2.Next() {
			_ = rows2.Scan(&msgCount)
		}
	}
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: projectHash, Title: title,
		UpdatedAt: st.ModTime(), CreatedAt: st.ModTime(), MessageCount: msgCount,
		StoragePath: path, SourceMtime: st.ModTime().Unix(),
	}, nil
}

func (p *Provider) summarizeTranscript(path string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	project := util.DecodeCursorProjectPath(transcriptProjectDir(path))
	picker := util.NewTitlePicker(80)
	var msgCount int
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		role, _ := row["role"].(string)
		if role == "" {
			if msg, ok := row["message"].(map[string]any); ok {
				role, _ = msg["role"].(string)
			}
		}
		text := extractCursorMessage(row)
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
	})
	title := picker.Title()
	if title == "" {
		_ = util.ReadJSONLLines(path, 40, func(line []byte) error {
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
		title = picker.Title()
	}
	if title == "" && project != "" {
		title = util.FirstUserSnippet(util.TildePath(project), 80)
	}
	if title == "" {
		title = "(transcript)"
	}
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
		UpdatedAt: st.ModTime(), CreatedAt: st.ModTime(), MessageCount: msgCount,
		StoragePath: path, SourceMtime: st.ModTime().Unix(),
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
	return p.loadStore(path, ref.ID)
}

func (p *Provider) loadTranscript(path, id string) (*model.Conversation, error) {
	conv := &model.Conversation{ID: id, Provider: ProviderID, StoragePath: path}
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		roleStr, _ := row["role"].(string)
		if roleStr != "user" && roleStr != "assistant" {
			return nil
		}
		text := extractCursorMessage(row)
		if text == "" {
			return nil
		}
		role := model.RoleUser
		if roleStr == "assistant" {
			role = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: text})
		return nil
	})
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	return conv, nil
}

func extractCursorMessage(row map[string]any) string {
	if msg, ok := row["message"].(map[string]any); ok {
		if c, ok := msg["content"].([]any); ok {
			var parts []string
			for _, item := range c {
				if part, ok := item.(map[string]any); ok {
					if t, _ := part["text"].(string); t != "" {
						parts = append(parts, t)
					}
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func (p *Provider) loadStore(path, id string) (*model.Conversation, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	conv := &model.Conversation{ID: id, Provider: ProviderID, StoragePath: path}
	rows, err := db.Query(`SELECT data FROM blobs ORDER BY rowid`)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if rows.Scan(&data) != nil {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			continue
		}
		roleStr, _ := payload["role"].(string)
		text, _ := payload["content"].(string)
		if text == "" {
			if blocks, ok := payload["content"].([]any); ok {
				for _, b := range blocks {
					if m, ok := b.(map[string]any); ok {
						if t, _ := m["text"].(string); t != "" {
							text += t
						}
					}
				}
			}
		}
		if roleStr == "" || text == "" {
			continue
		}
		role := model.RoleUser
		if roleStr == "assistant" {
			role = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: text})
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
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
	abs, _ := filepath.Abs(project)
	// "/home/x/proj" encodes to "home-x-proj", which DecodeCursorProjectPath
	// reverses; prefixing another "home-" made rediscovery decode /home/home/x.
	encoded := strings.TrimPrefix(util.EncodeClaudeProjectPath(abs), "-")
	sessionID := uuid.New().String()
	transcriptDir := filepath.Join(p.projectsRoot, encoded, "agent-transcripts", sessionID)
	path := filepath.Join(transcriptDir, sessionID+".jsonl")
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
	}
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		return nil, err
	}
	meta := model.NewMigrationMeta(conv)
	var lines []string
	header := map[string]any{"type": model.MigrationType, "data": meta}
	if b, err := json.Marshal(header); err == nil {
		lines = append(lines, string(b))
	}
	for _, m := range conv.Messages {
		row := map[string]any{
			"role": string(m.Role),
			"message": map[string]any{
				"content": []map[string]any{{"type": "text", "text": m.PlainText()}},
			},
		}
		if b, err := json.Marshal(row); err == nil {
			lines = append(lines, string(b))
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	return "cursor-agent --resume " + r.SessionID
}
