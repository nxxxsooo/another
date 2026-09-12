// Package qwen reads and writes native Qwen Code sessions.
package qwen

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

const ProviderID = "qwen"

const (
	// archiveDirName is where Qwen Code parks an archived session: a directory
	// inside the project's own chats directory, not a flag on the transcript.
	archiveDirName = "archive"
	// fileHistoryDirName holds the per-session backups Qwen Code takes before
	// it edits a file, keyed by session id under the global Qwen home.
	fileHistoryDirName = "file-history"
	// organizationStoreName is the per-project store that pins a session and
	// puts it in a group. Qwen Code clears a deleted session's entry from it.
	organizationStoreName = "session-organization.v1.json"
	// runtimeSuffix names the sidecar a live Qwen Code process writes beside
	// the transcript it is appending to.
	runtimeSuffix = ".runtime.json"
)

// sessionSidecars travel with the transcript. Qwen Code moves all three when it
// archives a session and removes all three when it deletes one: the git
// worktree the session runs in, the pull request it is reviewing, and the
// append-only ledger of its prompt terminal.
var sessionSidecars = []string{".worktree.json", ".pr.json", ".ledger.jsonl"}

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

	out := transcript{id: rows[0].SessionID, migration: migration}
	origin := util.NewOriginDirectory(rows[0].CWD)
	picker := util.NewTitlePicker(80)
	for _, row := range chain {
		if row.SessionID != "" {
			out.id = row.SessionID
		}
		origin.Note(row.CWD)
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
	out.project = origin.Path()
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
	cmd := "qwen --resume " + util.QuoteArg(result.SessionID)
	if result.ProjectPath != "" {
		return util.CdAnd(result.ProjectPath, cmd)
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
	defer func() { _ = f.Close() }()
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
	if !p.insideStore(path) || !isSessionPath(path) || filepath.Base(path) != result.SessionID+".jsonl" {
		return fmt.Errorf("qwen: refusing cleanup outside session store: %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// insideStore reports whether a path belongs to this provider's session store.
// Every lifecycle operation builds its paths from an id, so this is the fence
// that keeps a crafted ref from steering a move or a removal out of ~/.qwen.
func (p *Provider) insideStore(path string) bool {
	rel, err := filepath.Rel(p.projectsRoot(), path)
	return err == nil && rel != "." && rel != ".." &&
		!filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// validSessionID mirrors Qwen Code's own session file pattern. Qwen Code
// ignores a transcript whose name does not match it, and another refuses to
// build a path from one: an id is user-supplied, and it becomes a file name.
func validSessionID(id string) bool {
	if len(id) < 32 || len(id) > 36 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F', r == '-':
		default:
			return false
		}
	}
	return true
}

// location is one session's place in the store: the project's chats directory
// and which of Qwen Code's two states currently holds the transcript.
type location struct {
	chats    string
	archived bool
}

func (l location) dir(archived bool) string {
	if archived {
		return filepath.Join(l.chats, archiveDirName)
	}
	return l.chats
}

func (l location) transcript(id string, archived bool) string {
	return filepath.Join(l.dir(archived), id+".jsonl")
}

// chatsRoot maps a transcript in either state back to the project's active
// chats directory, and reports whether the path is shaped like a session at all.
func chatsRoot(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == archiveDirName {
		dir = filepath.Dir(dir)
	}
	if filepath.Base(dir) != "chats" {
		return "", false
	}
	return dir, true
}

// locate finds a session in either archive state. The ref carries the path the
// session had when it was indexed, so a session archived since then — by Qwen
// Code itself or by the archive below — is still found where it now lives.
func (p *Provider) locate(ref provider.SessionRef) (location, error) {
	if !validSessionID(ref.ID) {
		return location{}, fmt.Errorf("qwen: %q is not a session id", ref.ID)
	}
	var candidates []string
	if dir, ok := chatsRoot(ref.StoragePath); ok {
		candidates = append(candidates, dir)
	}
	if ref.ProjectPath != "" {
		candidates = append(candidates, filepath.Join(p.projectsRoot(), sanitizeProject(ref.ProjectPath), "chats"))
	}
	for _, chats := range candidates {
		if !p.insideStore(chats) {
			continue
		}
		if loc, ok := stateOf(chats, ref.ID); ok {
			return loc, nil
		}
	}
	var found location
	ok := false
	_ = filepath.WalkDir(p.projectsRoot(), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Base(path) != ref.ID+".jsonl" {
			return nil
		}
		chats, valid := chatsRoot(path)
		if !valid {
			return nil
		}
		found = location{chats: chats, archived: filepath.Base(filepath.Dir(path)) == archiveDirName}
		ok = true
		return filepath.SkipAll
	})
	if !ok {
		return location{}, provider.ErrNotFound
	}
	return found, nil
}

func stateOf(chats, id string) (location, bool) {
	loc := location{chats: chats}
	for _, archived := range []bool{false, true} {
		if _, err := os.Stat(loc.transcript(id, archived)); err == nil {
			loc.archived = archived
			return loc, true
		}
	}
	return location{}, false
}

// running reports whether a live Qwen Code process owns the session, read from
// the same evidence `qwen sessions ps` uses: the runtime sidecar beside the
// transcript, believed only when it names this session, and checked against the
// pid when this host wrote it. A sidecar from another machine is taken at its
// word, because a dead pid here says nothing about a process there.
//
// The pid it returns is only meaningful alongside a running or unknown answer,
// and exists so the refusal can name what to quit.
func running(chats, id string) (util.Liveness, int) {
	data, err := os.ReadFile(filepath.Join(chats, id+runtimeSuffix))
	if err != nil {
		return util.ProcessGone, 0
	}
	var status struct {
		PID       int    `json:"pid"`
		SessionID string `json:"session_id"`
		Hostname  string `json:"hostname"`
	}
	if json.Unmarshal(data, &status) != nil || status.SessionID != id || status.PID <= 0 {
		return util.ProcessGone, 0
	}
	if host, err := os.Hostname(); err != nil || status.Hostname != host {
		return util.ProcessRunning, status.PID
	}
	return util.ProcessLiveness(status.PID), status.PID
}

// refuseRunning guards every mutation of an existing transcript.
//
// An unknown answer is refused alongside a running one. The sidecar is a live
// Qwen Code's claim on this session; where another cannot check whether the
// process behind it survived, treating the claim as expired would move or
// delete a transcript out from under a running agent. Better to refuse and name
// the stale file, which the person can remove in a second, than to be wrong in
// the direction that loses their session.
func refuseRunning(loc location, id string) error {
	live, pid := running(loc.chats, id)
	return refusalFor(live, pid, id, filepath.Join(loc.chats, id+runtimeSuffix))
}

// refusalFor turns a liveness answer into the refusal the person reads.
func refusalFor(live util.Liveness, pid int, id, sidecar string) error {
	switch live {
	case util.ProcessRunning:
		return fmt.Errorf("qwen: session %s is open in a running Qwen Code", id)
	case util.ProcessUnknown:
		return fmt.Errorf(
			"qwen: session %s claims a running Qwen Code (pid %d) and this platform cannot check whether it still exists; quit Qwen Code, or delete %s if it is stale",
			id, pid, sidecar)
	}
	return nil
}

// ArchiveSession moves the session between Qwen Code's two chat directories.
// That move is the whole of Qwen Code's archive: there is no archived flag in
// the transcript, and its own session list reads `chats/` and `chats/archive/`
// as the two states. So an archived session leaves another's list exactly as it
// leaves Qwen Code's, and unarchiving brings back the same file, not a copy.
func (p *Provider) ArchiveSession(_ context.Context, ref provider.SessionRef, archived bool) error {
	loc, err := p.locate(ref)
	if err != nil {
		return err
	}
	if loc.archived == archived {
		return nil
	}
	if err := refuseRunning(loc, ref.ID); err != nil {
		return err
	}
	target := loc.dir(archived)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	if err := os.Rename(loc.transcript(ref.ID, loc.archived), loc.transcript(ref.ID, archived)); err != nil {
		return err
	}
	// The sidecars follow the transcript. Qwen Code warns rather than fails
	// when one cannot follow, and the same holds here: the archive already
	// happened, so reporting a failure would send the person to redo a move
	// that is done.
	for _, suffix := range sessionSidecars {
		moveIfAbsent(filepath.Join(loc.dir(loc.archived), ref.ID+suffix), filepath.Join(target, ref.ID+suffix))
	}
	return nil
}

// moveIfAbsent carries a sidecar across states without ever overwriting one.
// A file already waiting at the destination is the leftover of an interrupted
// move, and it is the newer state of the two; clobbering it would lose it.
func moveIfAbsent(source, target string) {
	if _, err := os.Stat(source); err != nil {
		return
	}
	if _, err := os.Stat(target); err == nil {
		return
	}
	_ = os.Rename(source, target)
}

// DeleteSession removes everything Qwen Code keys to one session: the
// transcript in whichever state holds it, the sidecars in both, the file
// backups the session's own edits produced, and its entry in the project's
// pin-and-group store. That is the same set Qwen Code's own delete removes, so
// nothing is left behind pointing at a session that no longer exists.
//
// There is no undo. A Qwen Code session is not one file, and another will not
// hold a session's file-history backups in memory waiting for second thoughts,
// so the delete is exactly as final as the confirmation says it is.
func (p *Provider) DeleteSession(_ context.Context, ref provider.SessionRef) error {
	loc, err := p.locate(ref)
	if err != nil {
		return err
	}
	if err := refuseRunning(loc, ref.ID); err != nil {
		return err
	}
	// The transcript goes first: while it is there the session is still listed,
	// and a failure that stopped halfway would otherwise leave a session in the
	// list whose sidecars had already been taken from under it.
	errs := []error{remove(loc.transcript(ref.ID, loc.archived))}
	// The runtime sidecar is stale by definition here — a live one refuses the
	// delete above — and left behind it would keep naming a session nothing
	// else can open.
	suffixes := append(append([]string{}, sessionSidecars...), runtimeSuffix)
	for _, archived := range []bool{false, true} {
		for _, suffix := range suffixes {
			errs = append(errs, remove(filepath.Join(loc.dir(archived), ref.ID+suffix)))
		}
	}
	errs = append(errs, os.RemoveAll(filepath.Join(p.root, fileHistoryDirName, ref.ID)))
	errs = append(errs, removeOrganizationEntry(filepath.Dir(loc.chats), ref.ID))
	return errors.Join(errs...)
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// removeOrganizationEntry drops the session from the project's pin-and-group
// store, which Qwen Code clears as part of its own delete. Only the one entry
// is touched: the rest of the file, including keys another does not know, is
// written back as it was found, and a store another cannot read is left alone
// rather than rebuilt into something Qwen Code would then have to recover from.
func removeOrganizationEntry(projectDir, id string) error {
	path := filepath.Join(projectDir, organizationStoreName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var store map[string]json.RawMessage
	if json.Unmarshal(data, &store) != nil {
		return nil
	}
	var sessions map[string]json.RawMessage
	if json.Unmarshal(store["sessions"], &sessions) != nil {
		return nil
	}
	if _, ok := sessions[id]; !ok {
		return nil
	}
	delete(sessions, id)
	encoded, err := json.Marshal(sessions)
	if err != nil {
		return err
	}
	store["sessions"] = encoded
	out, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return util.WriteFileAtomic(path, out, 0o600)
}
