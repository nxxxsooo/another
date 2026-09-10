// Package codem reads and writes native CodeM sessions. CodeM is Feishu's
// coding agent; it keeps one JSONL transcript per session, in a directory named
// after the hash of the directory the session was started in.
package codem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const ProviderID = "codem"

// projectHashLen is how much of the directory hash CodeM keeps: sha256 of the
// working directory, truncated to its first 16 hex characters. The hash is
// one-way, so a session's directory can only be read back out of the `cwd` its
// own header records — never out of the path the transcript sits at.
const projectHashLen = 16

// schemaVersion is the transcript version CodeM 0.1.x writes and reads. It sits
// in the header, which is the record that makes a file a session at all.
const schemaVersion = 10

// defaultModel is CodeM's own router entry, written into a header another
// creates. It is not a claim about which model produced the migrated messages:
// the header names the runtime that will continue the session, and a resumed
// run resolves its real model from the active profile.
const defaultModel = "codem-router/auto"

type Provider struct {
	root string
}

func New() *Provider {
	root := config.EnvOrDefault("CODEM_HOME", filepath.Join(config.HomeDir(), ".codem"))
	return &Provider{root: root}
}

func (p *Provider) ID() string           { return ProviderID }
func (p *Provider) DisplayName() string  { return "CodeM" }
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) sessionsRoot() string { return filepath.Join(p.root, "sessions") }

func (p *Provider) Installed() bool {
	if _, err := exec.LookPath("codem"); err == nil {
		return true
	}
	st, err := os.Stat(p.sessionsRoot())
	return err == nil && st.IsDir()
}

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "sessions", Path: p.sessionsRoot(), Env: "CODEM_HOME"}}
}

// projectHash mirrors CodeM's own key for a working directory.
func projectHash(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:])[:projectHashLen]
}

func (p *Provider) transcriptPath(project, id string) string {
	return filepath.Join(p.sessionsRoot(), projectHash(project), id+".jsonl")
}

// sessionKey is the id another carries for a CodeM session: the directory hash
// and CodeM's own id, joined. A CodeM id is unique inside one directory and
// nowhere else — the Feishu client reuses a single sess_feishu_p2p_<peer> id in
// every directory it is opened from, and `--session` will happily take one id
// into a second project — so an unqualified id would collapse conversations
// that have nothing to do with each other into one row.
func sessionKey(hash, id string) string { return hash + "_" + id }

// splitSessionKey takes a qualified id apart. An id that carries no hash is
// returned as it is, so a ref typed by hand still resolves.
func splitSessionKey(key string) (hash, id string) {
	if len(key) > projectHashLen && key[projectHashLen] == '_' && isHex(key[:projectHashLen]) {
		return key[:projectHashLen], key[projectHashLen+1:]
	}
	return "", key
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return s != ""
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	root := p.sessionsRoot()
	dirs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []model.Summary
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Transcripts live one level down, inside a project-hash directory.
		// The loose sess_*.json files beside those directories are the Feishu
		// client's session records, not transcripts; each one's conversation
		// is already indexed from the .jsonl of the same name below.
		if !dir.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, dir.Name()))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// A session's scratchpad and tool results sit in a directory named
			// after it, one level further down. Stopping here keeps the walk
			// off them without having to name them.
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(root, dir.Name(), entry.Name())
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			if opts.SkipSource != nil && opts.SkipSource(path, info.ModTime().UnixNano(), info.Size()) {
				continue
			}
			if opts.SkipSource == nil && opts.SkipUnchanged != nil && opts.SkipUnchanged(path, info.ModTime().Unix()) {
				continue
			}
			data, err := scan(ctx, path, 0)
			if err != nil {
				return nil, err
			}
			if data.id == "" || len(data.messages) == 0 {
				continue
			}
			if opts.ProjectFilter != "" && !strings.Contains(data.project, opts.ProjectFilter) {
				continue
			}
			out = append(out, model.Summary{
				ID: sessionKey(dir.Name(), data.id), Provider: ProviderID, ProjectPath: data.project,
				Title: data.title, CreatedAt: data.created(info), UpdatedAt: data.updated(info),
				MessageCount: len(data.messages), StoragePath: path,
				SourceMtime: info.ModTime().UnixNano(), SourceSize: info.Size(),
				Kind: model.SessionKindRoot, Migration: data.migration,
			})
			if opts.Limit > 0 && len(out) >= opts.Limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	return p.load(ctx, ref, 0)
}

// LoadPreview keeps only the most recent messages, and keeps them without
// holding the rest: the scan drops what falls out of the window as it reads, so
// a preview of a long session costs the window rather than the transcript.
func (p *Provider) LoadPreview(ctx context.Context, ref provider.SessionRef, limit int) (*model.Conversation, error) {
	return p.load(ctx, ref, limit)
}

func (p *Provider) load(ctx context.Context, ref provider.SessionRef, keepLast int) (*model.Conversation, error) {
	path, err := p.locate(ref)
	if err != nil {
		return nil, err
	}
	data, err := scan(ctx, path, keepLast)
	if err != nil {
		return nil, err
	}
	if len(data.messages) == 0 {
		return nil, provider.ErrNotFound
	}
	info, _ := os.Stat(path)
	return &model.Conversation{
		ID: sessionKey(filepath.Base(filepath.Dir(path)), data.id), Provider: ProviderID, ProjectPath: data.project, Title: data.title,
		CreatedAt: data.created(info), UpdatedAt: data.updated(info), Messages: data.messages,
		MessageCount: len(data.messages), StoragePath: path, Migration: data.migration,
	}, nil
}

// locate resolves a ref to a transcript for reading. A recorded storage path is
// used when it still exists, because the index holds the exact file; otherwise
// the path is rebuilt from the hash the id carries, then from the directory the
// caller named, and only then looked for under every project.
func (p *Provider) locate(ref provider.SessionRef) (string, error) {
	if path := ref.StoragePath; path != "" && strings.HasSuffix(path, ".jsonl") {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}
	hash, id := splitSessionKey(ref.ID)
	// A CodeM id could in principle look like a qualified one. Both readings
	// are tried, so an id that happens to start with sixteen hex characters
	// and an underscore is still found.
	var ids []string
	for _, candidate := range []string{id, ref.ID} {
		if validSessionID(candidate) && (len(ids) == 0 || ids[0] != candidate) {
			ids = append(ids, candidate)
		}
	}
	if len(ids) == 0 {
		return "", provider.ErrNotFound
	}
	var candidates []string
	for _, candidate := range ids {
		if hash != "" {
			candidates = append(candidates, filepath.Join(p.sessionsRoot(), hash, candidate+".jsonl"))
		}
		if ref.ProjectPath != "" {
			candidates = append(candidates, p.transcriptPath(ref.ProjectPath, candidate))
		}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	dirs, err := os.ReadDir(p.sessionsRoot())
	if err != nil {
		return "", provider.ErrNotFound
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		for _, candidate := range ids {
			path := filepath.Join(p.sessionsRoot(), dir.Name(), candidate+".jsonl")
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	return "", provider.ErrNotFound
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
	id := newSessionID()
	hash := projectHash(project)
	dir := filepath.Join(p.sessionsRoot(), hash)
	path := filepath.Join(dir, id+".jsonl")
	key := sessionKey(hash, id)
	if opts.DryRun {
		return &provider.WriteResult{SessionID: key, StoragePath: path, ProjectPath: project}, nil
	}

	now := time.Now().UTC()
	started := conv.CreatedAt
	if started.IsZero() {
		started = now
	}
	rows := make([]map[string]any, 0, len(portable)+2)
	rows = append(rows, map[string]any{
		"type": recordHeader, "schema_version": schemaVersion, "session_id": id,
		"started_at": stamp(started), "cwd": project,
		"profile": "[active]", "provider": "openai_compat", "model": defaultModel,
	})
	// CodeM ignores a record type it does not know, which is what lets the
	// migration marker travel inside the transcript instead of beside it.
	rows = append(rows, map[string]any{
		"type": model.MigrationType, "at": stamp(started), "data": model.NewMigrationMeta(conv),
	})
	for i, msg := range portable {
		ts := msg.Timestamp
		if ts.IsZero() {
			ts = started.Add(time.Duration(i+1) * time.Millisecond)
		}
		row := map[string]any{"type": recordUserMessage, "at": stamp(ts), "content": msg.PlainText()}
		if msg.Role == model.RoleAssistant {
			row = map[string]any{"type": recordAssistantText, "at": stamp(ts), "text": msg.PlainText()}
		}
		rows = append(rows, row)
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(encoded))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: key, StoragePath: path, ProjectPath: project}, nil
}

// newSessionID matches the 32-character hex id CodeM generates for a session of
// its own, so a migrated session is not recognizable by the shape of its name.
func newSessionID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ResumeCommand carries the directory because CodeM's sessions are keyed by it:
// `--resume` only looks in the hash of the current working directory, and fails
// outright when the id is not there. The directory hash another carries in the
// id is dropped here — CodeM computes it from where the command runs.
func (p *Provider) ResumeCommand(result provider.WriteResult) string {
	_, id := splitSessionKey(result.SessionID)
	cmd := "codem --resume " + util.ShellQuote(id)
	if result.ProjectPath != "" {
		return "cd " + util.ShellQuote(result.ProjectPath) + " && " + cmd
	}
	return cmd
}

func (p *Provider) CleanupWrite(_ context.Context, result provider.WriteResult) error {
	_, id := splitSessionKey(result.SessionID)
	path := result.StoragePath
	if path == "" {
		path = p.transcriptPath(result.ProjectPath, id)
	}
	if !validSessionID(id) || filepath.Base(path) != id+".jsonl" || !p.insideStore(path) {
		return fmt.Errorf("codem: refusing cleanup outside session store: %s", path)
	}
	if err := refuseSymlink(path); err != nil {
		return err
	}
	return remove(path)
}

// DeleteSession removes the transcript and the working directory CodeM keeps
// beside it under the same name, which holds that session's scratchpad and tool
// results and names nothing else once the transcript is gone.
func (p *Provider) DeleteSession(_ context.Context, ref provider.SessionRef) error {
	path, err := p.locate(ref)
	if err != nil {
		return err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if !validSessionID(id) || !p.insideStore(path) {
		return fmt.Errorf("codem: refusing to delete outside session store: %s", path)
	}
	if err := refuseSymlink(path); err != nil {
		return err
	}
	// The transcript goes first: while it is there the session is still listed,
	// and a failure halfway would otherwise leave a session whose scratchpad had
	// already been taken from under it.
	errs := []error{remove(path)}
	sidecar := filepath.Join(filepath.Dir(path), id)
	if st, err := os.Lstat(sidecar); err == nil && st.IsDir() {
		errs = append(errs, os.RemoveAll(sidecar))
	}
	return errors.Join(errs...)
}

// insideStore is the fence every destructive path passes: a ref is user
// supplied, and its id becomes a file name.
func (p *Provider) insideStore(path string) bool {
	rel, err := filepath.Rel(p.sessionsRoot(), path)
	return err == nil && rel != "." && rel != ".." &&
		!filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// validSessionID covers every id CodeM issues — timestamped runs, plain UUIDs,
// bare hex, and the sess_* ids the Feishu client assigns — while refusing
// anything that could steer a path somewhere else.
func validSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// refuseSymlink stops a removal from following a link out of the store. The
// path is checked as a link, not as what it points at.
func refuseSymlink(path string) error {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codem: refusing to follow a symlinked session: %s", path)
	}
	return nil
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
