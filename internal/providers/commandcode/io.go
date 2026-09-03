package commandcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
)

type ccLine struct {
	SessionID string `json:"sessionId"`
	ParentID  string `json:"parentId"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	Content   any    `json:"content"`
	Cwd       string `json:"cwd"`
}

type ccMeta struct {
	Title string `json:"title"`
	Model string `json:"model,omitempty"`
}

var writeCommandCodeFile = util.WriteFileAtomic

func discoverWithRoot(ctx context.Context, root, providerID string, opts provider.DiscoverOpts) ([]model.Summary, error) {
	var out []model.Summary
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") || strings.HasSuffix(path, ".checkpoints.jsonl") {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			mtime, size := ccSourceStamp(path, info)
			if opts.SkipSource != nil && opts.SkipSource(path, mtime.UnixNano(), size) {
				return nil
			}
			if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, mtime.Unix()) {
				return nil
			}
		}
		sm, err := summarizeCCFile(path, providerID)
		if err != nil {
			return err
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

func summarizeCCFile(path, providerID string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	encoded := filepath.Base(filepath.Dir(path))
	project := util.DecodeClaudeProjectPath(encoded)
	picker := util.NewTitlePicker(80)
	var migration *model.MigrationMeta
	var msgCount int
	var first, last time.Time
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		var row ccLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.SessionID != "" {
			id = row.SessionID
		}
		if row.Cwd != "" {
			project = row.Cwd
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
	}); err != nil {
		return model.Summary{}, err
	}
	if first.IsZero() {
		first = st.ModTime()
	}
	if last.IsZero() {
		last = st.ModTime()
	}
	title := picker.Title()
	if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		var meta ccMeta
		if json.Unmarshal(data, &meta) == nil && meta.Title != "" {
			title = meta.Title
		}
	}
	if title == "" {
		title = "(no title)"
	}
	mtime, size := ccSourceStamp(path, st)
	return model.Summary{
		ID: id, Provider: providerID, ProjectPath: project, Title: title,
		CreatedAt: first, UpdatedAt: last, MessageCount: msgCount,
		StoragePath: path, SourceMtime: mtime.UnixNano(), SourceSize: size,
		Kind: model.SessionKindRoot, Migration: migration,
	}, nil
}

func ccSourceStamp(path string, main os.FileInfo) (time.Time, int64) {
	mtime, size := main.ModTime(), main.Size()
	if meta, err := os.Stat(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		size += meta.Size()
		if meta.ModTime().After(mtime) {
			mtime = meta.ModTime()
		}
	}
	return mtime, size
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
	conv := &model.Conversation{ID: ref.ID, Provider: ProviderID, StoragePath: path, ProjectPath: ref.ProjectPath}
	if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		var meta ccMeta
		if json.Unmarshal(data, &meta) == nil {
			conv.Title = meta.Title
		}
	}
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			conv.Migration = meta
		}
		var row ccLine
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.SessionID != "" {
			conv.ID = row.SessionID
		}
		if row.Cwd != "" {
			conv.ProjectPath = row.Cwd
		}
		if row.Role != "user" && row.Role != "assistant" {
			return nil
		}
		role := model.RoleUser
		if row.Role == "assistant" {
			role = model.RoleAssistant
		}
		ts := util.ParseTime(row.Timestamp)
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: contentToString(row.Content), Timestamp: ts})
		if conv.CreatedAt.IsZero() && !ts.IsZero() {
			conv.CreatedAt = ts
		}
		if ts.After(conv.UpdatedAt) {
			conv.UpdatedAt = ts
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(conv.Messages) == 0 {
		return nil, provider.ErrNotFound
	}
	conv.MessageCount = len(conv.Messages)
	if conv.Title == "" {
		conv.Title = util.FirstUserSnippet(conv.Messages[0].PlainText(), 80)
	}
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
	lines := []string{}
	var parent string
	for i, m := range conv.Messages {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		id := uuid.New().String()
		ts := m.Timestamp
		if ts.IsZero() {
			ts = conv.CreatedAt
			if ts.IsZero() {
				ts = time.Now()
			}
			ts = ts.Add(time.Duration(i) * time.Millisecond)
		}
		row := map[string]any{
			"id": id, "sessionId": sessionID, "parentId": parent,
			"role": string(m.Role), "timestamp": ts.UTC().Format(time.RFC3339Nano),
			"content": []map[string]any{{"type": "text", "text": m.PlainText()}},
			"cwd":     project, "gitBranch": "-",
			"metadata": map[string]any{"timestamp": ts.UTC().Format(time.RFC3339Nano), "source": "cli", "version": 2},
		}
		if parent == "" {
			delete(row, "parentId")
		}
		b, _ := json.Marshal(row)
		lines = append(lines, string(b))
		parent = id
	}
	header, err := json.Marshal(map[string]any{"type": model.MigrationType, "data": meta})
	if err != nil {
		return nil, err
	}
	lines = append(lines, string(header))
	if err := writeCommandCodeFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
	metaData, err := json.MarshalIndent(ccMeta{Title: conv.Title}, "", "  ")
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := writeCommandCodeFile(metaPath, append(metaData, '\n'), 0o600); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
}
