package model_test

import (
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
)

func TestSnapshotAndContentDigestsStable(t *testing.T) {
	conv := &model.Conversation{
		ID: "session-1", Provider: "codex",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello", Timestamp: time.Unix(1, 0)},
			{Role: model.RoleAssistant, Content: "world", Timestamp: time.Unix(2, 0)},
		},
	}
	d1 := model.SnapshotDigest(conv)
	d2 := model.SnapshotDigest(conv)
	if d1 != d2 || len(d1) != 64 {
		t.Fatalf("digest unstable: %s %s", d1, d2)
	}
}

func TestContentDigestPreservesTextAndNormalizesTransportDetails(t *testing.T) {
	ts := time.Unix(1, 123456789)
	a := &model.Conversation{ID: "a", Provider: "codex", Messages: []model.Message{
		{Role: model.RoleSystem, Content: "ignored"},
		{Role: model.RoleUser, Content: "  hello\r\nworld  ", Timestamp: ts},
	}}
	b := &model.Conversation{ID: "b", Provider: "claude-code", Messages: []model.Message{
		{Role: model.RoleUser, Content: "  hello\r\nworld  ", Timestamp: ts.Add(400 * time.Microsecond)},
		{Role: model.RoleTool, Content: "ignored"},
	}}
	if model.ContentDigest(a) != model.ContentDigest(b) {
		t.Fatal("content snapshot digest should ignore transport timestamps and non-conversation roles")
	}
	if model.SnapshotDigest(a) == model.SnapshotDigest(b) {
		t.Fatal("distinct source sessions with identical content must not collide")
	}
	b.Messages[0].Content = "hello\r\nworld"
	if model.ContentDigest(a) == model.ContentDigest(b) {
		t.Fatal("leading and trailing whitespace loss must fail verification")
	}
}

func TestContentDigestDetectsTimestampCorruptionAtMilliseconds(t *testing.T) {
	a := &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "hello", Timestamp: time.UnixMilli(1000)}}}
	b := &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "hello", Timestamp: time.UnixMilli(1001)}}}
	if model.ContentDigest(a) == model.ContentDigest(b) {
		t.Fatal("millisecond timestamp corruption was ignored")
	}
	a.Messages[0].Timestamp = time.Time{}
	b.Messages[0].Timestamp = time.Time{}
	if model.ContentDigest(a) != model.ContentDigest(b) {
		t.Fatal("zero timestamps should remain unspecified")
	}
}

func TestLegacyOriginDigestRetainsTimestampSensitivity(t *testing.T) {
	a := &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "hello", Timestamp: time.Unix(1, 0)}}}
	b := &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "hello", Timestamp: time.Unix(2, 0)}}}
	if model.LegacyOriginDigest(a) == model.LegacyOriginDigest(b) {
		t.Fatal("legacy compatibility digest should preserve its timestamp behavior")
	}
}

func TestSummaryShortID(t *testing.T) {
	s := model.Summary{ID: "01234567-89ab-cdef-0123-456789abcdef"}
	if s.ShortID() != "01234567-89ab-cd" {
		t.Fatalf("short id = %q", s.ShortID())
	}
	s2 := model.Summary{ID: "20260628_151621_cd7bdb"}
	if s2.ShortID() != "151621_cd7bdb" {
		t.Fatalf("hermes short id = %q", s2.ShortID())
	}
}
