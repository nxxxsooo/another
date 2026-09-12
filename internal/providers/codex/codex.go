package codex

import (
	"context"
	"encoding/json"
	"errors"
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

// codexMeta is the identity a rollout's session_meta line carries. Subagent
// and forked threads also name themselves there, which is the only title
// material they have: such a thread holds no user prompt of its own.
type codexMeta struct {
	id        string
	project   string
	parentID  string
	kind      string
	nickname  string
	agentPath string
	seen      bool
}

func newCodexMeta() codexMeta {
	return codexMeta{kind: model.SessionKindRoot}
}

func codexApplyMeta(row map[string]any, meta *codexMeta) {
	if t, _ := row["type"].(string); t != "session_meta" {
		return
	}
	if meta.seen {
		return
	}
	meta.seen = true
	p := codexPayload(row)
	sid, _ := p["id"].(string)
	if sid == "" {
		sid, _ = p["session_id"].(string)
	}
	if sid == "" {
		sid, _ = row["session_id"].(string)
	}
	if sid != "" {
		meta.id = sid
	}
	if cwd, _ := row["cwd"].(string); cwd != "" {
		meta.project = cwd
	} else if cwd, _ := p["cwd"].(string); cwd != "" {
		meta.project = cwd
	}
	meta.nickname = stringField(p, "agent_nickname")
	meta.agentPath = stringField(p, "agent_path")
	if source, ok := p["source"].(map[string]any); ok {
		if subagent, ok := source["subagent"].(map[string]any); ok {
			meta.kind = model.SessionKindSubagent
			if spawn, ok := subagent["thread_spawn"].(map[string]any); ok {
				meta.parentID = stringField(spawn, "parent_thread_id")
				if meta.nickname == "" {
					meta.nickname = stringField(spawn, "agent_nickname")
				}
				if meta.agentPath == "" {
					meta.agentPath = stringField(spawn, "agent_path")
				}
			}
		}
	}
}

// codexSubagentTitle names a thread that never received a user prompt. The
// agent_path leaf says what the thread was spawned to do and the nickname is
// what Codex Desktop calls it, so together they read like a real title.
func codexSubagentTitle(meta codexMeta) string {
	leaf := ""
	if trimmed := strings.Trim(strings.TrimSpace(meta.agentPath), "/"); trimmed != "" {
		parts := strings.Split(trimmed, "/")
		if candidate := strings.TrimSpace(parts[len(parts)-1]); candidate != "root" {
			leaf = candidate
		}
	}
	nickname := strings.TrimSpace(meta.nickname)
	switch {
	case leaf != "" && nickname != "":
		return leaf + " · " + nickname
	case leaf != "":
		return leaf
	default:
		return nickname
	}
}

// codexWireMessage reads the conversation turn out of one rollout record.
// Codex writes a turn up to twice: as an event_msg (user_message or
// agent_message) and again as a response_item. Subagent and forked threads
// only ever carry the event_msg half, so both spellings have to be understood
// here or their whole transcript reads as empty.
func codexWireMessage(row map[string]any) (wireType, role, text string, ok bool) {
	if em, isLegacy := row["event_msg"].(map[string]any); isLegacy {
		return "event_msg", stringField(em, "role"), stringField(em, "message"), true
	}
	switch t, _ := row["type"].(string); t {
	case "event_msg":
		p := codexPayload(row)
		switch pt, _ := p["type"].(string); pt {
		case "user_message":
			return "event_msg", "user", stringField(p, "message"), true
		case "agent_message":
			return "event_msg", "assistant", stringField(p, "message"), true
		}
	case "response_item":
		p := codexPayload(row)
		if mt, _ := p["type"].(string); mt == "message" {
			return "response_item", stringField(p, "role"), codexTextFromContent(p["content"]), true
		}
	}
	return "", "", "", false
}

func codexApplyRow(row map[string]any, last *codexLoadedMessage, picker *util.TitlePicker, msgCount *int, keepInjected bool, ts time.Time) {
	wireType, role, text, ok := codexWireMessage(row)
	if !ok || !codexAcceptMessage(last, wireType, role, text, ts, keepInjected) {
		return
	}
	*msgCount++
	if role == "user" {
		picker.Note(strings.TrimSpace(text))
	}
}

type codexLoadedMessage struct {
	wireType  string
	role      string
	text      string
	timestamp time.Time
}

// codexAcceptMessage reports whether a wire message is a real conversation
// turn, folding in the injected-transport filter, the synthetic bridge turn,
// and the mirrored event_msg/response_item pair Codex writes for the same
// text. Load and the summarize pass share it so an indexed message count means
// exactly what a loaded conversation holds.
func codexAcceptMessage(last *codexLoadedMessage, wireType, role, text string, ts time.Time, keepInjected bool) bool {
	if role != "user" && role != "assistant" || text == "" {
		return false
	}
	if role == "user" && !keepInjected && codexSkipUserText(text) {
		return false
	}
	if role == "user" && codexIsRestoredUserPrompt(text) {
		return false
	}
	current := codexLoadedMessage{wireType: wireType, role: role, text: text, timestamp: ts}
	delta := current.timestamp.Sub(last.timestamp)
	mirroredWireTypes := last.wireType == "event_msg" && current.wireType == "response_item" ||
		last.wireType == "response_item" && current.wireType == "event_msg"
	if last.role == current.role && last.text == current.text && mirroredWireTypes &&
		!last.timestamp.IsZero() && delta >= 0 && delta <= time.Second {
		return false
	}
	*last = current
	return true
}

func codexAppendMessage(conv *model.Conversation, last *codexLoadedMessage, wireType, role, text string, ts time.Time) {
	if !codexAcceptMessage(last, wireType, role, text, ts, conv.Migration != nil) {
		return
	}
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
	picker := util.NewTitlePicker(80)
	var msgCount int
	var first, last time.Time
	meta := newCodexMeta()
	meta.id = sessionIDFromRollout(path)
	var lastMessage codexLoadedMessage
	var migration *model.MigrationMeta
	if err := util.ReadJSONLPrefix(path, codexSummarizeMaxBytes, codexSummarizeMaxLines, func(line []byte) error {
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if m, ok := model.ParseMigrationMeta(line); ok {
			migration = m
		}
		codexApplyMeta(row, &meta)
		ts := util.ParseTime(stringField(row, "timestamp"))
		if !ts.IsZero() {
			if first.IsZero() {
				first = ts
			}
			last = ts
		}
		codexApplyRow(row, &lastMessage, picker, &msgCount, migration != nil, ts)
		return nil
	}); err != nil {
		return model.Summary{}, err
	}
	id, project := meta.id, meta.project
	kind, parentID := meta.kind, meta.parentID
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
	if title == "" {
		title = codexSubagentTitle(meta)
	}
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
	meta := newCodexMeta()
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if m, ok := model.ParseMigrationMeta(line); ok {
			conv.Migration = m
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		codexApplyMeta(row, &meta)
		ts := util.ParseTime(stringField(row, "timestamp"))
		if wireType, role, text, ok := codexWireMessage(row); ok {
			codexAppendMessage(conv, &lastMessage, wireType, role, text, ts)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if meta.id != "" {
		conv.ID = meta.id
	}
	if meta.project != "" {
		conv.ProjectPath = meta.project
	}
	kind, parentID := meta.kind, meta.parentID
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
	if conv.Title == "" {
		conv.Title = codexSubagentTitle(meta)
	}
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
	cmd := "codex resume " + util.QuoteArg(r.SessionID)
	if r.ProjectPath != "" {
		return util.CdAnd(r.ProjectPath, cmd)
	}
	return cmd
}

func (p *Provider) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("codex: title must not be empty")
	}
	// The CLI's own thread store first: that is the rename. The two Desktop
	// stores follow, and only the Electron one can refuse — see
	// writeDesktopTitle for why a running Desktop is told about, not fought.
	if err := errors.Join(p.renameThread(ref.ID, title), p.appendGUITitle(ref.ID, title)); err != nil {
		return err
	}
	return p.writeDesktopTitle(ref.ID, title)
}

func (p *Provider) ArchiveSession(ctx context.Context, ref provider.SessionRef, archived bool) error {
	verb := "archive"
	if !archived {
		verb = "unarchive"
	}
	cmd := exec.CommandContext(ctx, "codex", verb, ref.ID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("codex %s: %w: %s", verb, err, strings.TrimSpace(string(out)))
	}
	return nil
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
