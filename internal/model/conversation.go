package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type Message struct {
	Role      Role           `json:"role"`
	Content   string         `json:"content"`
	Blocks    []ContentBlock `json:"blocks,omitempty"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
}

func (m Message) PlainText() string {
	if m.Content != "" {
		return m.Content
	}
	var parts []string
	for _, b := range m.Blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type Conversation struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	ProjectPath  string    `json:"project_path,omitempty"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Messages     []Message `json:"messages"`
	StoragePath  string    `json:"storage_path,omitempty"`
	MessageCount int       `json:"message_count"`
	// Migration is set when a provider loads an agenthop-created target.
	Migration *MigrationMeta `json:"-"`
	// WriteMigration overrides the marker written for a resumable projection.
	WriteMigration *MigrationMeta `json:"-"`
}

type Summary struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	ProjectPath  string    `json:"project_path,omitempty"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	StoragePath  string    `json:"storage_path,omitempty"`
	// Kind is "root" unless the provider marks this as a child/subagent session.
	Kind           string `json:"kind,omitempty"`
	ParentID       string `json:"parent_id,omitempty"`
	SourceMtime    int64  `json:"source_mtime,omitempty"` // nanoseconds since Unix epoch
	SourceSize     int64  `json:"source_size,omitempty"`
	SourcePriority int    `json:"-"` // provider preference when one session has multiple representations
	// Migration is populated while discovering an agenthop-created target. It is
	// consumed by the index and intentionally not part of the sessions table.
	Migration *MigrationMeta `json:"-"`
}

const (
	SessionKindRoot     = "root"
	SessionKindSubagent = "subagent"
)

func (s Summary) ShortID() string {
	if len(s.ID) <= 16 {
		return s.ID
	}
	if i := strings.IndexByte(s.ID, '_'); i > 0 && i < len(s.ID)-4 {
		tail := s.ID[i+1:]
		if len(tail) <= 20 {
			return tail
		}
		return tail[len(tail)-16:]
	}
	return s.ID[:16]
}

// ContentDigest identifies the ordered user/assistant text and specified
// timestamps that must survive a provider round trip. System/tool messages are
// intentionally excluded; zero timestamps are unspecified.
func ContentDigest(conv *Conversation) string {
	var b strings.Builder
	for _, m := range conv.Messages {
		if m.Role != RoleUser && m.Role != RoleAssistant {
			continue
		}
		text := m.PlainText()
		timestamp := ""
		if !m.Timestamp.IsZero() {
			timestamp = m.Timestamp.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
		}
		fmt.Fprintf(&b, "%s\x1f%s\x1f%s\x1e", m.Role, timestamp, text)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// SnapshotDigest identifies one exact source-session snapshot. Including the
// source provider and ID prevents unrelated sessions with identical text from
// colliding during migration deduplication.
func SnapshotDigest(conv *Conversation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x1f%s\x1e%s", conv.Provider, conv.ID, ContentDigest(conv))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// OriginDigest is kept for callers compiled against the Phase 1 API.
// Deprecated: use SnapshotDigest for dedup or ContentDigest for round trips.
func OriginDigest(conv *Conversation) string { return SnapshotDigest(conv) }

// LegacyOriginDigest preserves the pre-content-normalization key so existing
// migration_dedup rows remain reusable after an upgrade.
func LegacyOriginDigest(conv *Conversation) string {
	var b strings.Builder
	for _, m := range conv.Messages {
		ts := ""
		if !m.Timestamp.IsZero() {
			ts = m.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprintf(&b, "%s|%s|%s\n", m.Role, ts, m.PlainText())
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

type MigrationMeta struct {
	Type                string `json:"type"`
	OriginID            string `json:"originId"`
	OriginSource        string `json:"originSource"`
	OriginMessageCount  int    `json:"originMessageCount"`
	OriginDigest        string `json:"originDigest,omitempty"`
	TargetFormatVersion *int   `json:"targetFormatVersion,omitempty"`
}

const MigrationType = "agenthop_migration"

const MigrationTargetFormatVersion = 4

// ParseMigrationMeta recognizes native agenthop markers and compatible ctxmv
// markers, either bare or wrapped in a provider progress/data object.
func ParseMigrationMeta(line []byte) (*MigrationMeta, bool) {
	var row map[string]any
	if json.Unmarshal(line, &row) != nil {
		return nil, false
	}
	data := row
	if nested, ok := row["data"].(map[string]any); ok {
		data = nested
	}
	typ, _ := data["type"].(string)
	if typ != MigrationType && typ != "ctxmv_migration" {
		return nil, false
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var meta MigrationMeta
	if json.Unmarshal(b, &meta) != nil {
		return nil, false
	}
	return &meta, true
}

func NewMigrationMeta(conv *Conversation) MigrationMeta {
	if conv.WriteMigration != nil {
		return *conv.WriteMigration
	}
	version := MigrationTargetFormatVersion
	return MigrationMeta{
		Type:                MigrationType,
		OriginID:            conv.ID,
		OriginSource:        conv.Provider,
		OriginMessageCount:  len(conv.Messages),
		OriginDigest:        SnapshotDigest(conv),
		TargetFormatVersion: &version,
	}
}
