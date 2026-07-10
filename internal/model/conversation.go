package model

import (
	"crypto/sha256"
	"encoding/hex"
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
	SourceMtime  int64     `json:"source_mtime,omitempty"`
}

func (s Summary) ShortID() string {
	if len(s.ID) <= 16 {
		return s.ID
	}
	if i := strings.IndexByte(s.ID, '_'); i > 0 && i < len(s.ID)-4 {
		tail := s.ID[i+1:]
		if len(tail) <= 20 {
			return "…" + tail
		}
		return "…" + tail[len(tail)-16:]
	}
	if len(s.ID) <= 20 {
		return s.ID
	}
	return "…" + s.ID[len(s.ID)-16:]
}

func OriginDigest(conv *Conversation) string {
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

func NewMigrationMeta(conv *Conversation) MigrationMeta {
	return MigrationMeta{
		Type:               MigrationType,
		OriginID:           conv.ID,
		OriginSource:       conv.Provider,
		OriginMessageCount: len(conv.Messages),
		OriginDigest:       OriginDigest(conv),
	}
}
