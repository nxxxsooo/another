// Package pi reads and writes sessions for the pi coding agent.
//
// pi stores one directory per working directory under ~/.pi/agent/sessions,
// named by replacing every path separator with a dash and wrapping the result
// in double dashes. Each session is a JSONL file whose first row is a session
// header and whose remaining rows form a single parent chain of events.
package pi

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

const ProviderID = "pi"

// sessionFormatVersion is the schema version pi writes in its session header.
const sessionFormatVersion = 3

// migrationCustomType labels the custom event that carries the migration
// marker. pi keeps unknown custom events in the transcript without replaying
// them as conversation turns, so the marker survives a resume untouched.
const migrationCustomType = "another-migration"

// legacyBridgeText is the fixed bridge-turn literal earlier builds wrote
// verbatim. isBridgeTurn still recognizes it so already-migrated sessions
// don't retain a stray literal user turn when reloaded.
const legacyBridgeText = "上面是从另一个 agent 迁移过来的历史上下文，接着这里继续。"

// bridgeTextFor builds the synthetic trailing user turn appended when the
// migrated conversation ends on an assistant message. Anthropic-backed
// models reject a resumed session whose last turn is an assistant message,
// treating it as a prefill; naming the source agent doubles as provenance
// instead of a generic placeholder. Load drops this exact turn again so a
// round trip stays digest-stable.
func bridgeTextFor(sourceProvider string) string {
	if sourceProvider == "" {
		return legacyBridgeText
	}
	return "上面是从 " + sourceProvider + " 迁移过来的历史上下文，接着这里继续。"
}

// isBridgeTurn reports whether text is a synthetic bridge turn — the
// current per-source form or the earlier fixed literal.
func isBridgeTurn(text string) bool {
	if text == legacyBridgeText {
		return true
	}
	return strings.HasPrefix(text, "上面是从 ") && strings.HasSuffix(text, " 迁移过来的历史上下文，接着这里继续。")
}

type Provider struct {
	root string
}

func New() *Provider {
	root := config.EnvOrDefault("PI_AGENT_DIR", filepath.Join(config.HomeDir(), ".pi", "agent"))
	return &Provider{root: root}
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "pi" }

func (p *Provider) Installed() bool {
	st, err := os.Stat(p.sessionsRoot())
	return err == nil && st.IsDir()
}

func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "sessions", Path: p.sessionsRoot(), Env: "PI_AGENT_DIR"}}
}

func (p *Provider) sessionsRoot() string { return filepath.Join(p.root, "sessions") }

// encodeProjectDir mirrors pi's directory naming: "/a/b c" becomes "--a-b c--".
func encodeProjectDir(absPath string) string {
	trimmed := strings.Trim(filepath.ToSlash(absPath), "/")
	return "--" + strings.ReplaceAll(trimmed, "/", "-") + "--"
}

// decodeProjectDir is a best-effort inverse used only when a session file has
// no header cwd. The encoding is lossy for paths that already contain dashes,
// so the header value always wins.
func decodeProjectDir(name string) string {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, "--"), "--")
	if trimmed == "" {
		return ""
	}
	return "/" + strings.ReplaceAll(trimmed, "-", "/")
}

// piEvent is the envelope shared by every row after the session header.
type piEvent struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	ParentID   *string         `json:"parentId"`
	Timestamp  string          `json:"timestamp"`
	Name       string          `json:"name"`
	CustomType string          `json:"customType"`
	Message    *piMessage      `json:"message"`
	Data       json.RawMessage `json:"data"`
	// Session header fields.
	Version int    `json:"version"`
	CWD     string `json:"cwd"`
}

type piMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// blockText keeps only rendered text. Reasoning blocks and tool payloads never
// cross a migration boundary: they are provider-specific and often carry
// signatures that are invalid anywhere else.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	root := p.sessionsRoot()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
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
	return out, err
}

func (p *Provider) summarizeFile(path string) (model.Summary, error) {
	st, err := os.Stat(path)
	if err != nil {
		return model.Summary{}, err
	}
	id := sessionIDFromFilename(filepath.Base(path))
	project := decodeProjectDir(filepath.Base(filepath.Dir(path)))
	name := ""
	picker := util.NewTitlePicker(80)
	var migration *model.MigrationMeta
	var msgCount int
	var first, last time.Time

	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if meta, ok := model.ParseMigrationMeta(line); ok {
			migration = meta
		}
		var e piEvent
		if json.Unmarshal(line, &e) != nil {
			return nil
		}
		switch e.Type {
		case "session":
			if e.ID != "" {
				id = e.ID
			}
			if e.CWD != "" {
				project = e.CWD
			}
			if ts := util.ParseTime(e.Timestamp); !ts.IsZero() {
				first = ts
			}
		case "session_info":
			// Pi treats the latest session_info as authoritative, including an
			// empty name that explicitly clears an earlier title.
			name = strings.TrimSpace(e.Name)
		case "message":
			if e.Message == nil {
				return nil
			}
			if e.Message.Role != "user" && e.Message.Role != "assistant" {
				return nil
			}
			text := blockText(e.Message.Content)
			if text == "" {
				return nil
			}
			msgCount++
			ts := util.ParseTime(e.Timestamp)
			if first.IsZero() {
				first = ts
			}
			if !ts.IsZero() {
				last = ts
			}
			if e.Message.Role == "user" {
				picker.Note(text)
			}
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
	title := name
	if title == "" {
		title = picker.TitleOr("(no title)")
	}
	return model.Summary{
		ID: id, Provider: ProviderID, ProjectPath: project, Title: title,
		CreatedAt: first, UpdatedAt: last, MessageCount: msgCount,
		StoragePath: path, Kind: model.SessionKindRoot,
		SourceMtime: st.ModTime().UnixNano(), SourceSize: st.Size(),
		Migration: migration,
	}, nil
}

// sessionIDFromFilename recovers the uuid from "<stamp>_<uuid>.jsonl". The
// header id is authoritative; this only seeds the value.
func sessionIDFromFilename(base string) string {
	base = strings.TrimSuffix(base, ".jsonl")
	if idx := strings.LastIndex(base, "_"); idx >= 0 {
		return base[idx+1:]
	}
	return base
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	path := ref.StoragePath
	if path == "" {
		var err error
		path, err = p.findSessionFile(ref)
		if err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(path); err != nil {
		return nil, provider.ErrNotFound
	}
	conv := &model.Conversation{
		ID: ref.ID, Provider: ProviderID, StoragePath: path,
		ProjectPath: decodeProjectDir(filepath.Base(filepath.Dir(path))),
	}
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if meta, ok := model.ParseMigrationMeta(line); ok {
			conv.Migration = meta
		}
		var e piEvent
		if json.Unmarshal(line, &e) != nil {
			return nil
		}
		switch e.Type {
		case "session":
			if e.ID != "" {
				conv.ID = e.ID
			}
			if e.CWD != "" {
				conv.ProjectPath = e.CWD
			}
		case "session_info":
			conv.Title = strings.TrimSpace(e.Name)
		case "message":
			if e.Message == nil {
				return nil
			}
			role := model.RoleUser
			switch e.Message.Role {
			case "user":
				role = model.RoleUser
			case "assistant":
				role = model.RoleAssistant
			default:
				// toolResult and any future non-conversation role.
				return nil
			}
			text := blockText(e.Message.Content)
			if text == "" {
				return nil
			}
			conv.Messages = append(conv.Messages, model.Message{
				Role: role, Content: text, Timestamp: util.ParseTime(e.Timestamp),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}

	conv.Messages = dropBridgeTurn(conv.Messages)
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
	return conv, nil
}

// dropBridgeTurn removes the synthetic trailing user turn added by Write so a
// write/load round trip reproduces the source conversation exactly.
func dropBridgeTurn(msgs []model.Message) []model.Message {
	if n := len(msgs); n > 0 {
		last := msgs[n-1]
		if last.Role == model.RoleUser && isBridgeTurn(last.Content) {
			return msgs[:n-1]
		}
	}
	return msgs
}

func (p *Provider) findSessionFile(ref provider.SessionRef) (string, error) {
	if ref.ProjectPath == "" || ref.ID == "" {
		return "", provider.ErrNotFound
	}
	dir := filepath.Join(p.sessionsRoot(), encodeProjectDir(ref.ProjectPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", provider.ErrNotFound
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_"+ref.ID+".jsonl") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", provider.ErrNotFound
}

// eventChain serializes pi events, threading the parentId chain as it goes.
type eventChain struct {
	rows   []string
	parent *string
	err    error
}

func (c *eventChain) add(e map[string]any) {
	if c.err != nil {
		return
	}
	id := uuid.New().String()[:8]
	e["id"] = id
	e["parentId"] = c.parent
	if _, ok := e["timestamp"]; !ok {
		e["timestamp"] = nowISO()
	}
	b, err := json.Marshal(e)
	if err != nil {
		c.err = err
		return
	}
	c.rows = append(c.rows, string(b))
	c.parent = &id
}

// sessionStamp builds pi's filename prefix. The milliseconds must be rendered
// explicitly: Go only treats "000" as a fractional second when it follows a
// dot, so a dash-separated layout would silently emit a literal "000".
func sessionStamp(t time.Time) string {
	return fmt.Sprintf("%s%03dZ", t.UTC().Format("2006-01-02T15-04-05-"), t.UTC().Nanosecond()/int(time.Millisecond))
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func isoOrNow(ts time.Time) string {
	if ts.IsZero() {
		return nowISO()
	}
	return ts.UTC().Format("2006-01-02T15:04:05.000Z")
}

func unixMillis(ts time.Time) int64 {
	if ts.IsZero() {
		return time.Now().UTC().UnixMilli()
	}
	return ts.UTC().UnixMilli()
}

// zeroUsage is required on every persisted Pi assistant message. Migrated
// turns have no source billing data, so record a complete zero-value usage
// object rather than omitting it; Pi's session footer totals every assistant
// usage record when it opens a session.
func zeroUsage() map[string]any {
	return map[string]any{
		"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
		"cost": map[string]any{
			"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0,
		},
	}
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

	sessionID := uuid.New().String()
	now := time.Now().UTC()
	stamp := sessionStamp(now)
	dir := filepath.Join(p.sessionsRoot(), encodeProjectDir(project))
	path := filepath.Join(dir, fmt.Sprintf("%s_%s.jsonl", stamp, sessionID))
	if opts.DryRun {
		return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
	}

	header, err := json.Marshal(map[string]any{
		"type": "session", "version": sessionFormatVersion, "id": sessionID,
		"timestamp": isoOrNow(now), "cwd": project,
	})
	if err != nil {
		return nil, err
	}
	chain := &eventChain{rows: []string{string(header)}}

	title := strings.TrimSpace(conv.Title)
	if title == "" {
		title = "migrated session"
	}
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:99]) + "…"
	}
	chain.add(map[string]any{"type": "session_info", "name": title})
	chain.add(map[string]any{
		"type": "custom", "customType": migrationCustomType,
		"data": model.NewMigrationMeta(conv),
	})

	wrote := 0
	lastRole := model.RoleUser
	for _, m := range conv.Messages {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		text := m.PlainText()
		if text == "" {
			continue
		}
		role := "user"
		if m.Role == model.RoleAssistant {
			role = "assistant"
		}
		message := map[string]any{
			"role":      role,
			"content":   []any{map[string]any{"type": "text", "text": text}},
			"timestamp": unixMillis(m.Timestamp),
		}
		if role == "assistant" {
			// These turns are reconstructed history, not responses from the
			// currently selected model. Keep that provenance explicit while
			// satisfying Pi's complete AssistantMessage persistence contract.
			message["api"] = "pi-messages"
			message["provider"] = "another"
			message["model"] = "migration"
			message["stopReason"] = "stop"
			message["usage"] = zeroUsage()
		}
		chain.add(map[string]any{
			"type": "message", "timestamp": isoOrNow(m.Timestamp), "message": message,
		})
		wrote++
		lastRole = m.Role
	}
	if wrote == 0 {
		return nil, provider.ErrEmptySession
	}
	if lastRole == model.RoleAssistant {
		chain.add(map[string]any{
			"type": "message",
			"message": map[string]any{
				"role":      "user",
				"content":   []any{map[string]any{"type": "text", "text": bridgeTextFor(conv.Provider)}},
				"timestamp": time.Now().UTC().UnixMilli(),
			},
		})
	}
	if chain.err != nil {
		return nil, chain.err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := util.WriteFileAtomic(path, []byte(strings.Join(chain.rows, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: sessionID, StoragePath: path, ProjectPath: project}, nil
}

// ResumeCommand points at the exact session file. pi -r would also work but
// requires the user to pick the right entry from a list.
func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	cmd := "pi --session " + util.ShellQuote(r.StoragePath)
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && " + cmd
	}
	return cmd
}

func (p *Provider) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("pi: title must not be empty")
	}
	path := ref.StoragePath
	if path == "" {
		var err error
		path, err = p.findSessionFile(ref)
		if err != nil {
			return err
		}
	}
	rel, err := filepath.Rel(p.sessionsRoot(), path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".jsonl") {
		return fmt.Errorf("pi: refusing rename outside sessions root: %s", path)
	}
	var parent any
	if tail, err := util.TailJSONLLines(path, 1); err == nil && len(tail) == 1 {
		var last struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(tail[0], &last) == nil && last.ID != "" {
			parent = last.ID
		}
	}
	row, err := json.Marshal(map[string]any{
		"type": "session_info", "id": uuid.New().String()[:8], "parentId": parent,
		"timestamp": nowISO(), "name": title,
	})
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
	rel, err := filepath.Rel(p.sessionsRoot(), r.StoragePath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".jsonl") {
		return fmt.Errorf("pi: refusing cleanup outside sessions root: %s", r.StoragePath)
	}
	if err := os.Remove(r.StoragePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
