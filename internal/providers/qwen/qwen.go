// Package qwen reads and writes native Qwen Code sessions.
package qwen

import (
	"context"
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
)

const ProviderID = "qwen"

type Provider struct {
	root string
}

func New() *Provider {
	root := config.EnvOrDefault("QWEN_HOME", filepath.Join(config.HomeDir(), ".qwen"))
	return &Provider{root: root}
}

func (p *Provider) ID() string           { return ProviderID }
func (p *Provider) DisplayName() string  { return "Qwen Code" }
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) projectsRoot() string { return filepath.Join(p.root, "projects") }

func (p *Provider) Installed() bool {
	if _, err := exec.LookPath("qwen"); err == nil {
		return true
	}
	st, err := os.Stat(p.projectsRoot())
	return err == nil && st.IsDir()
}

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "projects", Path: p.projectsRoot(), Env: "QWEN_HOME"}}
}

type record struct {
	UUID          string          `json:"uuid"`
	ParentUUID    *string         `json:"parentUuid"`
	SessionID     string          `json:"sessionId"`
	Timestamp     string          `json:"timestamp"`
	Type          string          `json:"type"`
	Subtype       string          `json:"subtype"`
	CWD           string          `json:"cwd"`
	Message       *message        `json:"message"`
	SystemPayload json.RawMessage `json:"systemPayload"`
}

type message struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type transcript struct {
	id        string
	project   string
	title     string
	createdAt time.Time
	updatedAt time.Time
	messages  []model.Message
	migration *model.MigrationMeta
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	root := p.projectsRoot()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	var out []model.Summary
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !isSessionPath(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if opts.SkipSource != nil && opts.SkipSource(path, info.ModTime().UnixNano(), info.Size()) {
			return nil
		}
		if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
			return nil
		}
		data, err := scan(ctx, path)
		if err != nil {
			return err
		}
		if data.id == "" || len(data.messages) == 0 {
			return nil
		}
		if opts.ProjectFilter != "" && !strings.Contains(data.project, opts.ProjectFilter) {
			return nil
		}
		out = append(out, model.Summary{
			ID: data.id, Provider: ProviderID, ProjectPath: data.project, Title: data.title,
			CreatedAt: data.createdAt, UpdatedAt: data.updatedAt, MessageCount: len(data.messages),
			StoragePath: path, SourceMtime: info.ModTime().UnixNano(), SourceSize: info.Size(),
			Kind: model.SessionKindRoot, Migration: data.migration,
		})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out, err
}

func isSessionPath(path string) bool {
	return strings.HasSuffix(path, ".jsonl") &&
		!strings.HasSuffix(path, ".ledger.jsonl") && filepath.Base(filepath.Dir(path)) == "chats"
}

func scan(ctx context.Context, path string) (transcript, error) {
	var rows []record
	var migration *model.MigrationMeta
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		var row record
		if json.Unmarshal(line, &row) != nil || row.UUID == "" {
			return nil
		}
		if meta := migrationFromPayload(row.SystemPayload); meta != nil {
			migration = meta
		}
		rows = append(rows, row)
		return nil
	}); err != nil {
		return transcript{}, err
	}
	if len(rows) == 0 {
		return transcript{}, nil
	}

	// Qwen transcripts are trees. Follow the newest record's parent chain so
	// rewound/dead branches never reappear in another's portable projection.
	byID := make(map[string]record, len(rows))
	for _, row := range rows {
		byID[row.UUID] = row
	}
	chain := make([]record, 0, len(rows))
	for current, seen := rows[len(rows)-1], map[string]bool{}; current.UUID != "" && !seen[current.UUID]; {
		seen[current.UUID] = true
		chain = append(chain, current)
		if current.ParentUUID == nil || *current.ParentUUID == "" {
			break
		}
		next, ok := byID[*current.ParentUUID]
		if !ok {
			break
		}
		current = next
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}

	out := transcript{id: rows[0].SessionID, project: rows[0].CWD, migration: migration}
	picker := util.NewTitlePicker(80)
	for _, row := range chain {
		if row.SessionID != "" {
			out.id = row.SessionID
		}
		if row.CWD != "" {
			out.project = row.CWD
		}
		if row.Type == "system" && row.Subtype == "custom_title" {
			var payload struct {
				CustomTitle string `json:"customTitle"`
			}
			if json.Unmarshal(row.SystemPayload, &payload) == nil {
				out.title = strings.TrimSpace(payload.CustomTitle)
			}
			continue
		}
		role, ok := qwenRole(row)
		if !ok {
			continue
		}
		text := messageText(row)
		if text == "" || role == model.RoleUser && util.SkipUserMessage(text) {
			continue
		}
		ts := util.ParseTime(row.Timestamp)
		out.messages = append(out.messages, model.Message{Role: role, Content: text, Timestamp: ts})
		if out.createdAt.IsZero() && !ts.IsZero() {
			out.createdAt = ts
		}
		if !ts.IsZero() {
			out.updatedAt = ts
		}
		if role == model.RoleUser {
			picker.Note(text)
		}
	}
	if out.title == "" {
		out.title = picker.TitleOr("(qwen session)")
	}
	return out, nil
}

func migrationFromPayload(raw json.RawMessage) *model.MigrationMeta {
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		AnotherMigration json.RawMessage `json:"anotherMigration"`
	}
	if json.Unmarshal(raw, &payload) != nil || len(payload.AnotherMigration) == 0 {
		return nil
	}
	var meta model.MigrationMeta
	if json.Unmarshal(payload.AnotherMigration, &meta) != nil || meta.Type != model.MigrationType {
		return nil
	}
	return &meta
}

func qwenRole(row record) (model.Role, bool) {
	if row.Message == nil || row.Subtype != "" {
		return "", false
	}
	switch row.Type {
	case "user":
		return model.RoleUser, true
	case "assistant":
		return model.RoleAssistant, true
	default:
		return "", false
	}
}

func messageText(row record) string {
	if row.Type == "user" && len(row.SystemPayload) > 0 {
		var payload struct {
			DisplayText *string `json:"displayText"`
		}
		if json.Unmarshal(row.SystemPayload, &payload) == nil && payload.DisplayText != nil {
			return strings.TrimSpace(*payload.DisplayText)
		}
	}
	if row.Message == nil {
		return ""
	}
	var parts []string
	for _, part := range row.Message.Parts {
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	path := ref.StoragePath
	if path == "" {
		var err error
		path, err = p.find(ref.ID, ref.ProjectPath)
		if err != nil {
			return nil, err
		}
	}
	data, err := scan(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(data.messages) == 0 {
		return nil, provider.ErrNotFound
	}
	return &model.Conversation{
		ID: data.id, Provider: ProviderID, ProjectPath: data.project, Title: data.title,
		CreatedAt: data.createdAt, UpdatedAt: data.updatedAt, Messages: data.messages,
		MessageCount: len(data.messages), StoragePath: path, Migration: data.migration,
	}, nil
}

func (p *Provider) find(id, project string) (string, error) {
	if id == "" {
		return "", provider.ErrNotFound
	}
	if project != "" {
		path := filepath.Join(p.projectsRoot(), sanitizeProject(project), "chats", id+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	var found string
	_ = filepath.WalkDir(p.projectsRoot(), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && filepath.Base(path) == id+".jsonl" && isSessionPath(path) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", provider.ErrNotFound
	}
	return found, nil
}

func sanitizeProject(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (p *Provider) Write(_ context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	var portable []model.Message
	for _, msg := range conv.Messages {
		if (msg.Role == model.RoleUser || msg.Role == model.RoleAssistant) && msg.PlainText() != "" {
			portable = append(portable, msg)
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
	id := uuid.New().String()
	path := filepath.Join(p.projectsRoot(), sanitizeProject(project), "chats", id+".jsonl")
	if opts.DryRun {
		return &provider.WriteResult{SessionID: id, StoragePath: path, ProjectPath: project}, nil
	}

	version := "another"
	meta := model.NewMigrationMeta(conv)
	rows := make([]string, 0, len(portable)+2)
	parent := ""
	appendRow := func(row map[string]any) error {
		recordID := uuid.New().String()
		row["uuid"] = recordID
		row["parentUuid"] = nil
		if parent != "" {
			row["parentUuid"] = parent
		}
		row["sessionId"] = id
		row["cwd"] = project
		row["version"] = version
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		rows = append(rows, string(encoded))
		parent = recordID
		return nil
	}
	now := time.Now().UTC()
	if err := appendRow(map[string]any{
		"timestamp": now.Format(time.RFC3339Nano), "type": "system", "subtype": "session_source",
		"provenance": "system", "systemPayload": map[string]any{
			"sourceType": "another", "sourceId": conv.Provider + ":" + conv.ID, "anotherMigration": meta,
		},
	}); err != nil {
		return nil, err
	}
	for i, msg := range portable {
		ts := msg.Timestamp
		if ts.IsZero() {
			ts = now.Add(time.Duration(i+1) * time.Millisecond)
		}
		row := map[string]any{
			"timestamp": ts.UTC().Format(time.RFC3339Nano), "type": string(msg.Role),
			"provenance": "real_user", "message": map[string]any{
				"role": string(msg.Role), "parts": []map[string]any{{"text": msg.PlainText()}},
			},
		}
		if msg.Role == model.RoleAssistant {
			row["provenance"] = "assistant_output"
			row["model"] = "migrated"
			row["message"].(map[string]any)["role"] = "model"
		}
		if err := appendRow(row); err != nil {
			return nil, err
		}
	}
	if title := strings.TrimSpace(conv.Title); title != "" {
		if err := appendRow(map[string]any{
			"timestamp": now.Format(time.RFC3339Nano), "type": "system", "subtype": "custom_title",
			"provenance": "system", "systemPayload": map[string]any{"customTitle": title, "titleSource": "manual"},
		}); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := util.WriteFileAtomic(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: id, StoragePath: path, ProjectPath: project}, nil
}

func (p *Provider) ResumeCommand(result provider.WriteResult) string {
	cmd := "qwen --resume " + util.ShellQuote(result.SessionID)
	if result.ProjectPath != "" {
		return "cd " + util.ShellQuote(result.ProjectPath) + " && " + cmd
	}
	return cmd
}

func (p *Provider) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	path := ref.StoragePath
	if path == "" {
		var err error
		path, err = p.find(ref.ID, ref.ProjectPath)
		if err != nil {
			return err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return provider.ErrNotFound
	}
	var last record
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row record
		if json.Unmarshal([]byte(line), &row) == nil && row.UUID != "" {
			last = row
		}
	}
	if last.UUID == "" {
		return provider.ErrNotFound
	}
	row := map[string]any{
		"uuid": uuid.New().String(), "parentUuid": last.UUID, "sessionId": last.SessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "type": "system", "subtype": "custom_title",
		"provenance": "system", "cwd": last.CWD, "version": "another",
		"systemPayload": map[string]any{"customTitle": strings.TrimSpace(title), "titleSource": "manual"},
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("qwen: rename: %w", err)
	}
	return f.Sync()
}

func (p *Provider) CleanupWrite(_ context.Context, result provider.WriteResult) error {
	path := result.StoragePath
	if path == "" {
		path = filepath.Join(p.projectsRoot(), sanitizeProject(result.ProjectPath), "chats", result.SessionID+".jsonl")
	}
	rel, err := filepath.Rel(p.projectsRoot(), path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !isSessionPath(path) || filepath.Base(path) != result.SessionID+".jsonl" {
		return fmt.Errorf("qwen: refusing cleanup outside session store: %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
