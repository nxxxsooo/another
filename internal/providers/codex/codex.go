package codex

import (
	"context"
	"encoding/json"
	"errors"
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

const ProviderID = "codex"

// cap summarize scan so indexing thousands of rollout files stays practical.
const (
	codexSummarizeMaxLines = 500
	codexSummarizeMaxBytes = 512 * 1024
)

type Provider struct {
	sessionsRoot string
}

func New() *Provider {
	return &Provider{sessionsRoot: resolveSessionsRoot()}
}

func resolveSessionsRoot() string {
	if home := config.EnvOrDefault("CODEX_HOME", ""); home != "" {
		return filepath.Join(home, "sessions")
	}
	snap := filepath.Join(config.HomeDir(), "snap", "codex", "current", "sessions")
	if st, err := os.Stat(snap); err == nil && st.IsDir() {
		return snap
	}
	return filepath.Join(config.HomeDir(), ".codex", "sessions")
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "Codex" }
func (p *Provider) Installed() bool {
	st, err := os.Stat(p.sessionsRoot)
	return err == nil && st.IsDir()
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "sessions", Path: p.sessionsRoot, Env: "CODEX_HOME"}}
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	var out []model.Summary
	gui := p.loadGUITitles()
	err := filepath.WalkDir(p.sessionsRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !strings.HasPrefix(filepath.Base(path), "rollout-") || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			mtime, size := gui.fingerprint(info)
			if opts.SkipSource != nil && opts.SkipSource(path, mtime, size) {
				return nil
			}
			if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, time.Unix(0, mtime).Unix()) {
				return nil
			}
		}
		sm, err := p.summarizeFileWithGUI(path, gui)
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

// SummarizeFile returns a summary for a single rollout JSONL (used by tests and tooling).
func (p *Provider) SummarizeFile(path string) (model.Summary, error) {
	return p.summarizeFileWithGUI(path, p.loadGUITitles())
}

func sessionIDFromRollout(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	base = strings.TrimPrefix(base, "rollout-")
	// rollout-<timestamp>-<uuid>.jsonl: the UUID itself contains dashes, so
	// take the trailing 36 chars instead of the last dash-separated field.
	if len(base) >= 36 {
		if tail := base[len(base)-36:]; uuid.Validate(tail) == nil {
			return tail
		}
	}
	if i := strings.LastIndexByte(base, '-'); i >= 0 && i+1 < len(base) {
		return base[i+1:]
	}
	return base
}

func codexPayload(row map[string]any) map[string]any {
	if p, ok := row["payload"].(map[string]any); ok {
		return p
	}
	return row
}

func codexSkipUserText(text string) bool {
	return util.SkipUserMessage(text)
}

func codexTextFromContent(content any) string {
	arr, ok := content.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "input_text" && typ != "output_text" {
			continue
		}
		text, _ := m["text"].(string)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func codexApplyMeta(row map[string]any, id, project, parentID, kind *string, seen *bool) {
	if t, _ := row["type"].(string); t != "session_meta" {
		return
	}
	if *seen {
		return
	}
	*seen = true
	p := codexPayload(row)
	sid, _ := p["id"].(string)
	if sid == "" {
		sid, _ = p["session_id"].(string)
	}
	if sid == "" {
		sid, _ = row["session_id"].(string)
	}
	if sid != "" {
		*id = sid
	}
	if cwd, _ := row["cwd"].(string); cwd != "" {
		*project = cwd
	} else if cwd, _ := p["cwd"].(string); cwd != "" {
		*project = cwd
	}
	if source, ok := p["source"].(map[string]any); ok {
		if subagent, ok := source["subagent"].(map[string]any); ok {
			*kind = model.SessionKindSubagent
			if spawn, ok := subagent["thread_spawn"].(map[string]any); ok {
				*parentID = stringField(spawn, "parent_thread_id")
			}
		}
	}
}

func codexNoteUserText(text string, picker *util.TitlePicker, msgCount *int) {
	text = strings.TrimSpace(text)
	if text == "" || util.SkipUserMessage(text) {
		return
	}
	*msgCount++
	picker.Note(text)
}

func codexApplyRow(row map[string]any, id, project *string, picker *util.TitlePicker, msgCount *int) {
	switch t, _ := row["type"].(string); t {
	case "event_msg":
		if em, ok := row["event_msg"].(map[string]any); ok {
			role, _ := em["role"].(string)
			text, _ := em["message"].(string)
			if role == "user" {
				codexNoteUserText(text, picker, msgCount)
			} else if role == "assistant" {
				if text != "" {
					*msgCount++
				}
			}
			return
		}
		p := codexPayload(row)
		switch pt, _ := p["type"].(string); pt {
		case "user_message":
			codexNoteUserText(stringField(p, "message"), picker, msgCount)
		}
	case "response_item":
		p := codexPayload(row)
		if mt, _ := p["type"].(string); mt != "message" {
			return
		}
		role, _ := p["role"].(string)
		text := codexTextFromContent(p["content"])
		if role == "user" {
			codexNoteUserText(text, picker, msgCount)
		} else if role == "assistant" && text != "" {
			*msgCount++
		}
	}
}

type codexLoadedMessage struct {
	wireType  string
	role      string
	text      string
	timestamp time.Time
}

func codexAppendMessage(conv *model.Conversation, last *codexLoadedMessage, wireType, role, text string, ts time.Time) {
	if role != "user" && role != "assistant" || text == "" {
		return
	}
	if role == "user" && conv.Migration == nil && codexSkipUserText(text) {
		return
	}
	if role == "user" && codexIsRestoredUserPrompt(text) {
		return
	}
	current := codexLoadedMessage{wireType: wireType, role: role, text: text, timestamp: ts}
	delta := current.timestamp.Sub(last.timestamp)
	mirroredWireTypes := last.wireType == "event_msg" && current.wireType == "response_item" ||
		last.wireType == "response_item" && current.wireType == "event_msg"
	if last.role == current.role && last.text == current.text && mirroredWireTypes &&
		!last.timestamp.IsZero() && delta >= 0 && delta <= time.Second {
		return
	}
	*last = current
	mrole := model.RoleUser
	if role == "assistant" {
		mrole = model.RoleAssistant
	}
	conv.Messages = append(conv.Messages, model.Message{Role: mrole, Content: text, Timestamp: ts})
}

func (p *Provider) summarizeFileWithGUI(path string, gui guiTitles) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := sessionIDFromRollout(path)
	picker := util.NewTitlePicker(80)
	var msgCount int
	var first, last time.Time
	var project string
	kind := model.SessionKindRoot
	parentID := ""
	metaSeen := false
	var migration *model.MigrationMeta
	if err := util.ReadJSONLPrefix(path, codexSummarizeMaxBytes, codexSummarizeMaxLines, func(line []byte) error {
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		codexApplyMeta(row, &id, &project, &parentID, &kind, &metaSeen)
		if ts := util.ParseTime(stringField(row, "timestamp")); !ts.IsZero() {
			if first.IsZero() {
				first = ts
			}
			last = ts
		}
		codexApplyRow(row, &id, &project, picker, &msgCount)
		return nil
	}); err != nil {
		return model.Summary{}, err
	}
	tail, err := util.TailJSONLLines(path, 5)
	if err != nil {
		return model.Summary{}, err
	}
	for _, line := range tail {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		if ts, _ := row["timestamp"].(string); ts != "" {
			last = util.ParseTime(ts)
		}
	}
	if first.IsZero() {
		first = st.ModTime()
	}
	if last.IsZero() {
		last = st.ModTime()
	}
	title := picker.Title()
	if guiTitle := strings.TrimSpace(gui.names[id]); guiTitle != "" {
		title = guiTitle
		kind = model.SessionKindRoot
		parentID = ""
	}
	if title == "" {
		if project != "" {
			title = util.FirstUserSnippet(util.TildePath(project), 80)
		}
		if title == "" {
			title = "(no title)"
		}
	}
	mtime, size := gui.fingerprint(st)
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
		CreatedAt: first, UpdatedAt: last, MessageCount: msgCount,
		StoragePath: path, Kind: kind, ParentID: parentID,
		SourceMtime: mtime, SourceSize: size,
		Migration: migration,
	}, nil
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	path := ref.StoragePath
	if path == "" {
		return nil, provider.ErrNotFound
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	conv := &model.Conversation{ID: ref.ID, Provider: ProviderID, StoragePath: path}
	var lastMessage codexLoadedMessage
	metaSeen := false
	kind := model.SessionKindRoot
	parentID := ""
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			conv.Migration = meta
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		var id, project string
		codexApplyMeta(row, &id, &project, &parentID, &kind, &metaSeen)
		if id != "" {
			conv.ID = id
		}
		if project != "" {
			conv.ProjectPath = project
		}
		ts := util.ParseTime(stringField(row, "timestamp"))
		if em, ok := row["event_msg"].(map[string]any); ok {
			codexAppendMessage(conv, &lastMessage, "event_msg", stringField(em, "role"), stringField(em, "message"), ts)
			return nil
		}
		switch t, _ := row["type"].(string); t {
		case "event_msg":
			p := codexPayload(row)
			if pt, _ := p["type"].(string); pt == "user_message" {
				codexAppendMessage(conv, &lastMessage, "event_msg", "user", stringField(p, "message"), ts)
			}
		case "response_item":
			p := codexPayload(row)
			if mt, _ := p["type"].(string); mt != "message" {
				return nil
			}
			role, _ := p["role"].(string)
			codexAppendMessage(conv, &lastMessage, "response_item", role, codexTextFromContent(p["content"]), ts)
		}
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
	picker := util.NewTitlePicker(80)
	for _, m := range conv.Messages {
		if m.Role == model.RoleUser {
			picker.Note(m.PlainText())
		}
	}
	conv.Title = picker.Title()
	if guiTitle := strings.TrimSpace(p.loadGUITitles().names[conv.ID]); guiTitle != "" {
		conv.Title = guiTitle
		kind = model.SessionKindRoot
		parentID = ""
	}
	if kind == model.SessionKindSubagent && parentID != "" && conv.ID == "" {
		conv.ID = ref.ID
	}
	_ = st
	return conv, nil
}

func stringField(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func codexV2Timestamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func codexV2Line(ts string, typ string, payload map[string]any) (string, error) {
	b, err := json.Marshal(map[string]any{
		"timestamp": ts,
		"type":      typ,
		"payload":   payload,
	})
	return string(b), err
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	if len(conv.Messages) == 0 {
		return nil, provider.ErrEmptySession
	}
	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	sessionID := id.String()
	parts := now.Format("2006-01-02T15-04-05")
	dir := filepath.Join(p.sessionsRoot, now.Format("2006"), now.Format("01"), now.Format("02"))
	path := filepath.Join(dir, "rollout-"+parts+"-"+sessionID+".jsonl")
	project := opts.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
	}
	cliVersion, modelProvider := p.codexDefaults()
	lines, err := buildV2RolloutLines(conv, sessionID, project, now, cliVersion, modelProvider)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := util.WriteFileAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	result := &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}
	if err := p.EnsureResumable(conv, *result); err != nil {
		cleanupErr := p.CleanupWrite(ctx, *result)
		return nil, errors.Join(fmt.Errorf("register codex thread: %w", err), cleanupErr)
	}
	return result, nil
}

func buildV2RolloutLines(conv *model.Conversation, sessionID, project string, now time.Time, cliVersion, modelProvider string) ([]string, error) {
	meta := model.NewMigrationMeta(conv)
	var lines []string
	metaTS := codexV2Timestamp(now)
	if line, err := codexV2Line(metaTS, "session_meta", map[string]any{
		"id": sessionID, "session_id": sessionID,
		"timestamp": now.Format(time.RFC3339Nano),
		"cwd":       project, "originator": "another", "source": "cli",
		"thread_source": "user", "cli_version": cliVersion,
		"model_provider": modelProvider,
	}); err != nil {
		return nil, err
	} else {
		lines = append(lines, line)
	}
	metaLine := map[string]any{"type": model.MigrationType, "data": meta}
	if b, err := json.Marshal(metaLine); err != nil {
		return nil, err
	} else {
		lines = append(lines, string(b))
	}
	turnLines, err := codexBuildTurnLines(conv.Messages, project, now, conv.Provider)
	if err != nil {
		return nil, err
	}
	lines = append(lines, turnLines...)
	return lines, nil
}

// ResumeCommand includes the project directory: a Codex session is bound to the
// cwd it was written for, so resuming from elsewhere lands the agent in the
// wrong project.
func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	cmd := "codex resume " + util.ShellQuote(r.SessionID)
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && " + cmd
	}
	return cmd
}

func (p *Provider) DeleteSession(ctx context.Context, ref provider.SessionRef) error {
	return p.CleanupWrite(ctx, provider.WriteResult{
		SessionID: ref.ID, StoragePath: ref.StoragePath, ProjectPath: ref.ProjectPath,
	})
}

func (p *Provider) CleanupWrite(_ context.Context, r provider.WriteResult) error {
	if r.SessionID == "" || r.StoragePath == "" {
		return fmt.Errorf("codex: missing cleanup target")
	}
	rel, err := filepath.Rel(p.sessionsRoot, r.StoragePath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".jsonl") {
		return fmt.Errorf("codex: refusing cleanup outside sessions root: %s", r.StoragePath)
	}
	dbErr := p.deleteThread(r.SessionID)
	fileErr := os.Remove(r.StoragePath)
	if os.IsNotExist(fileErr) {
		fileErr = nil
	}
	return errors.Join(dbErr, fileErr)
}
