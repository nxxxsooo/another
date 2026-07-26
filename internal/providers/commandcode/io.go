package commandcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
	"github.com/google/uuid"
)

type ccLine struct {
	SessionID string `json:"sessionId"`
	ParentID  string `json:"parentId"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	Content   any    `json:"content"`
}

func discoverWithRoot(ctx context.Context, root, providerID string, opts provider.DiscoverOpts) ([]model.Summary, error) {
	var out []model.Summary
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if opts.SkipUnchanged != nil {
			if info, err := d.Info(); err == nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
				return nil
			}
		}
		sm, err := summarizeCCFile(path, providerID)
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

func summarizeCCFile(path, providerID string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	encoded := filepath.Base(filepath.Dir(path))
	project := util.DecodeClaudeProjectPath(encoded)
	picker := util.NewTitlePicker(80)
	var msgCount int
	var first, last time.Time
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		var row ccLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.SessionID != "" {
			id = row.SessionID
		}
		if row.Role != "user" && row.Role != "assistant" {
			return nil
		}
		msgCount++
		ts := util.ParseTime(row.Timestamp)
		if first.IsZero() {
			first = ts
		}
		last = ts
		if row.Role == "user" {
			picker.Note(contentToString(row.Content))
		}
		return nil
	})
	title := picker.TitleOr("(no title)")
	return model.Summary{
		ID: id, Provider: providerID, ProjectPath: project, Title: title,
		CreatedAt: first, UpdatedAt: last, MessageCount: msgCount,
		StoragePath: path, SourceMtime: st.ModTime().Unix(),
	}, nil
}

func contentToString(c any) string {
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

func loadWithRoot(ref provider.SessionRef, root string) (*model.Conversation, error) {
	path := ref.StoragePath
	if path == "" {
		path = filepath.Join(root, util.EncodeClaudeProjectPath(ref.ProjectPath), ref.ID+".jsonl")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, provider.ErrNotFound
	}
	conv := &model.Conversation{ID: ref.ID, StoragePath: path}
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		var row ccLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.SessionID != "" {
			conv.ID = row.SessionID
		}
		if row.Role != "user" && row.Role != "assistant" {
			return nil
		}
		role := model.RoleUser
		if row.Role == "assistant" {
			role = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{
			Role: role, Content: contentToString(row.Content), Timestamp: util.ParseTime(row.Timestamp),
		})
		return nil
	})
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	return conv, nil
}

func writeWithRoot(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts, root string) (*provider.WriteResult, error) {
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
	dir := filepath.Join(root, util.EncodeClaudeProjectPath(project))
	sessionID := uuid.New().String()
	path := filepath.Join(dir, sessionID+".jsonl")
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	meta := model.NewMigrationMeta(conv)
	header := map[string]any{"type": model.MigrationType, "data": meta}
	lines := []string{}
	if b, err := json.Marshal(header); err == nil {
		lines = append(lines, string(b))
	}
	var parent string
	for _, m := range conv.Messages {
		id := uuid.New().String()
		row := map[string]any{
			"id": id, "sessionId": sessionID, "parentId": parent,
			"role": string(m.Role), "timestamp": m.Timestamp.UTC().Format(time.RFC3339Nano),
			"content": []map[string]any{{"type": "text", "text": m.PlainText()}},
		}
		if parent == "" {
			delete(row, "parentId")
		}
		b, _ := json.Marshal(row)
		lines = append(lines, string(b))
		parent = id
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
}
