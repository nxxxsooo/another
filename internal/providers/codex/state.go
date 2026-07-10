package codex

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	_ "modernc.org/sqlite"
)

// resolveStateDB returns the newest codex state database, or "" (no error) when
// codex has no SQLite state yet (fresh install or pre-threads CLI version).
func (p *Provider) resolveStateDB() (string, error) {
	root := filepath.Dir(p.sessionsRoot)
	if h := os.Getenv("CODEX_SQLITE_HOME"); h != "" {
		root = h
	}
	matches, err := filepath.Glob(filepath.Join(root, "state_*.sqlite"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return stateSchemaVersion(matches[i]) > stateSchemaVersion(matches[j])
	})
	return matches[0], nil
}

func stateSchemaVersion(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), ".sqlite")
	base = strings.TrimPrefix(base, "state_")
	n, _ := strconv.Atoi(base)
	return n
}

func (p *Provider) codexDefaults() (cliVersion, modelProvider string) {
	cliVersion = "0.0.0"
	modelProvider = "openai"
	home := filepath.Dir(p.sessionsRoot)
	if b, err := os.ReadFile(filepath.Join(home, "version.json")); err == nil {
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &v) == nil && v.Version != "" {
			cliVersion = v.Version
		}
	}
	dbPath, err := p.resolveStateDB()
	if err != nil || dbPath == "" {
		return cliVersion, modelProvider
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return cliVersion, modelProvider
	}
	defer db.Close()
	var mp string
	if err := db.QueryRow(`SELECT model_provider FROM threads ORDER BY updated_at DESC LIMIT 1`).Scan(&mp); err == nil && mp != "" {
		modelProvider = mp
	}
	return cliVersion, modelProvider
}

// EnsureResumable registers an existing rollout in Codex's threads table so `codex resume <id>` works.
// Legacy agenthop rollouts (pre-v2 format) are rewritten in place from conv before registration.
func (p *Provider) EnsureResumable(conv *model.Conversation, ref provider.WriteResult) error {
	if ref.SessionID == "" || ref.StoragePath == "" {
		return fmt.Errorf("codex: missing session id or rollout path")
	}
	if _, err := os.Stat(ref.StoragePath); err != nil {
		return fmt.Errorf("codex rollout missing: %w", err)
	}
	if len(conv.Messages) == 0 {
		return fmt.Errorf("codex: cannot repair empty conversation")
	}
	project := ref.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	// Only rewrite legacy agenthop rollouts: a v2-format file may since have
	// real codex turns appended after resume — rewriting would destroy them.
	if rolloutNeedsV2Rewrite(ref.StoragePath) && rolloutIsAgenthopMigration(ref.StoragePath) {
		if err := p.rewriteRolloutFile(ref.StoragePath, ref.SessionID, project, conv); err != nil {
			return fmt.Errorf("rewrite rollout: %w", err)
		}
	}
	title := strings.TrimSpace(conv.Title)
	if title == "" {
		title = "agenthop migration"
	}
	firstUser := ""
	for _, m := range conv.Messages {
		if m.Role == model.RoleUser {
			firstUser = strings.TrimSpace(m.PlainText())
			if firstUser != "" {
				break
			}
		}
	}
	if firstUser == "" {
		firstUser = title
	}
	return p.registerThread(ref.SessionID, ref.StoragePath, project, title, firstUser, time.Now().UTC())
}

func rolloutIsAgenthopMigration(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false
	}
	const tailSize = 8192
	off := int64(0)
	if st.Size() > tailSize {
		off = st.Size() - tailSize
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return false
	}
	tail := string(buf)
	return strings.Contains(tail, `"type":"agenthop_migration"`) ||
		strings.Contains(tail, `"type": "agenthop_migration"`)
}

func rolloutNeedsV2Rewrite(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return true
	}
	line := string(b)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	var row map[string]any
	if json.Unmarshal([]byte(line), &row) != nil {
		return true
	}
	if _, ok := row["payload"]; ok {
		return false
	}
	return true
}

func (p *Provider) rewriteRolloutFile(path, sessionID, project string, conv *model.Conversation) error {
	cliVersion, modelProvider := p.codexDefaults()
	now := time.Now().UTC()
	lines, err := buildV2RolloutLines(conv, sessionID, project, now, cliVersion, modelProvider)
	if err != nil {
		return err
	}
	tmp := path + ".agenthop.tmp"
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (p *Provider) registerThread(sessionID, rolloutPath, project, title, firstUser string, now time.Time) error {
	dbPath, err := p.resolveStateDB()
	if err != nil {
		return err
	}
	if dbPath == "" {
		// No threads DB — older codex resumes by scanning rollout files directly.
		return nil
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()

	cliVersion, modelProvider := p.codexDefaults()
	unix := now.Unix()
	preview := truncateRunes(title, 120)
	title = truncateRunes(title, 200)
	firstUser = truncateRunes(firstUser, 500)

	_, err = db.Exec(`
INSERT INTO threads (
  id, rollout_path, created_at, updated_at, source, model_provider, cwd, title,
  sandbox_policy, approval_mode, tokens_used, has_user_event, archived,
  cli_version, first_user_message, memory_mode, thread_source, preview,
  recency_at, recency_at_ms
) VALUES (
  ?, ?, ?, ?, 'cli', ?, ?, ?,
  '{"type":"disabled"}', 'never', 0, 1, 0,
  ?, ?, 'enabled', 'user', ?,
  ?, ?
)
ON CONFLICT(id) DO UPDATE SET
  rollout_path = excluded.rollout_path,
  updated_at = excluded.updated_at,
  cwd = excluded.cwd,
  title = excluded.title,
  preview = excluded.preview,
  first_user_message = excluded.first_user_message,
  recency_at = excluded.recency_at,
  recency_at_ms = excluded.recency_at_ms`,
		sessionID, rolloutPath, unix, unix, modelProvider, project, title,
		cliVersion, firstUser, preview,
		unix, unix*1000,
	)
	return err
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
