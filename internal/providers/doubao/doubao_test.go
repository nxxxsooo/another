package doubao

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
)

func testProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	p := New()
	p.profile = t.TempDir()
	p.chats = filepath.Join(t.TempDir(), "Doubao", "chats")
	if err := os.MkdirAll(p.chats, 0700); err != nil {
		t.Fatal(err)
	}
	p.base = s.URL
	p.http = s.Client()
	p.auth = func(context.Context, string) (http.Header, error) { return make(http.Header), nil }
	for _, id := range []string{"12345678901234567", "22345678901234567", "32345678901234567"} {
		if err := os.MkdirAll(filepath.Join(p.sessionsRoot(), id), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func respond(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func TestDesktopDiscoveryLoadAndNativeRename(t *testing.T) {
	title := "Original title"
	messageCalls := 0
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/samantha/conversation/list":
			// A short page can still have more. The native cursor is authoritative.
			if string(req["index"]) == "0" {
				respond(t, w, map[string]any{"code": 0, "data": map[string]any{"has_more": true, "next_index": 7, "thread_list": []any{
					map[string]any{"thread_id_str": "92345678901234567", "conversation": map[string]any{"conversation_id": "12345678901234567", "conversation_type": 3, "name": title, "update_time": 1789993061}},
				}}})
			} else {
				if string(req["index"]) != "7" {
					t.Errorf("wrong list cursor: %s", req["index"])
				}
				respond(t, w, map[string]any{"code": 0, "data": map[string]any{"has_more": false, "thread_list": []any{
					map[string]any{"conversation": map[string]any{"conversation_id": "22345678901234567", "conversation_type": 3, "name": "Archived"}},
					map[string]any{"conversation": map[string]any{"conversation_id": "32345678901234567", "conversation_type": 1, "name": "System chat"}},
					map[string]any{"conversation": map[string]any{"conversation_id": "42345678901234567", "conversation_type": 3, "name": "Other device"}},
				}}})
			}
		case "/im/chain/recent_conv":
			respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_recent_conv_chain_downlink_body": map[string]any{
				"has_more": false, "cells": []any{
					map[string]any{"conversation": map[string]any{"conversation_id": "12345678901234567", "create_time": "1789000000", "conversation_status": 1}},
					map[string]any{"conversation": map[string]any{"conversation_id": "22345678901234567", "create_time": "1789000000", "conversation_status": 8}},
				},
			}}})
		case "/im/chain/single":
			messageCalls++
			if r.Header.Get("Content-Type") != "application/json; encoding=utf-8" || r.Header.Get("Agw-Js-Conv") != "str" {
				t.Error("missing native IM encoding headers")
			}
			var body map[string]struct {
				Cursor integer `json:"anchor_index"`
			}
			if err := json.Unmarshal(req["uplink_body"], &body); err != nil {
				t.Error(err)
			}
			m := func(id string, index int, user int, text string) map[string]any {
				b, _ := json.Marshal(map[string]string{"text": text})
				return map[string]any{"message_id": id, "index_in_conv": index, "user_type": user, "content": string(b), "create_time": "1789000001"}
			}
			data := map[string]any{"messages": []any{m("m4", 4, 2, "Answer"), m("m3", 3, 1, "Follow-up")}, "has_more": true, "next_index": "3"}
			if body["pull_singe_chain_uplink_body"].Cursor == 3 {
				hidden := m("hidden", 0, 1, "deleted")
				hidden["status"] = 1
				data = map[string]any{"messages": []any{m("m3", 3, 1, "Follow-up"), m("m2", 2, 2, "First answer"), m("m1", 1, 1, "Question"), hidden}, "has_more": false}
			}
			respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_singe_chain_downlink_body": data}})
		case "/samantha/thread/update":
			var id string
			_ = json.Unmarshal(req["thread_id"], &id)
			if id != "92345678901234567" || len(req) != 2 {
				t.Errorf("rename must only send native thread ID and name: %v", req)
			}
			_ = json.Unmarshal(req["name"], &title)
			respond(t, w, map[string]any{"code": 0})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	rows, err := p.Discover(ctx, provider.DiscoverOpts{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("discovery = %+v, %v", rows, err)
	}
	if rows[0].CreatedAt.Unix() != 1789000000 || rows[0].ProjectPath != p.chats || rows[0].MessageCount != 0 {
		t.Fatalf("invented metadata: %+v", rows[0])
	}
	ref := provider.SessionRef{ID: rows[0].ID}
	conv, err := p.Load(ctx, ref)
	if err != nil || len(conv.Messages) != 4 || conv.Messages[0].Content != "Question" || conv.Messages[3].Role != model.RoleAssistant {
		t.Fatalf("load = %+v, %v", conv, err)
	}
	messageCalls = 0
	preview, err := p.LoadPreview(ctx, ref, 2)
	if err != nil || messageCalls != 1 || len(preview.Messages) != 2 {
		t.Fatalf("preview = %+v, calls %d, %v", preview, messageCalls, err)
	}
	if err := p.RenameSession(ctx, ref, "New title"); err != nil {
		t.Fatal(err)
	}
	rows, err = p.Discover(ctx, provider.DiscoverOpts{})
	if err != nil || rows[0].Title != "New title" {
		t.Fatalf("rename not rediscovered: %+v, %v", rows, err)
	}
	if _, err := p.Load(ctx, provider.SessionRef{ID: "../../escape"}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("invalid ref: %v", err)
	}
	if p.SupportsResume() || p.ResumeCommand(provider.WriteResult{}) != "" {
		t.Fatal("unverified native resume exposed")
	}
	if _, err := p.Write(ctx, conv, provider.WriteOpts{}); err == nil {
		t.Fatal("source-only provider accepted import")
	}
}

func TestAPIFailuresNeverBecomeEmptySuccess(t *testing.T) {
	for _, body := range []string{`{"code":7}`, `{}`, `{"code":0,"data":{"has_more":true,"next_index":0}}`, `<html>login</html>`} {
		t.Run(body, func(t *testing.T) {
			p := testProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			rows, err := p.Discover(context.Background(), provider.DiscoverOpts{})
			if err == nil || rows != nil {
				t.Fatalf("failure reported as successful empty scan: %v %v", rows, err)
			}
		})
	}
}

func TestMessagesRejectStalledPagination(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_singe_chain_downlink_body": map[string]any{"has_more": true}}})
	})
	c, _ := p.client(context.Background())
	if _, err := c.messages(context.Background(), "123", 0); err == nil {
		t.Fatal("truncated history accepted as complete")
	}
}

func TestRenameRejectedOrUnverifiedIsNotSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		partial bool
	}{{"permission", 710010101, false}, {"readback mismatch", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/samantha/thread/update" {
					respond(t, w, map[string]any{"code": tc.code})
					return
				}
				respond(t, w, map[string]any{"code": 0, "data": map[string]any{"thread_list": []any{
					map[string]any{"conversation": map[string]any{"conversation_id": "12345678901234567", "conversation_type": 3, "name": "Original"}},
				}}})
			})
			err := p.RenameSession(context.Background(), provider.SessionRef{ID: "12345678901234567"}, "New title")
			if err == nil || errors.Is(err, provider.ErrPartial) != tc.partial {
				t.Fatalf("rename error = %v, want partial=%v", err, tc.partial)
			}
		})
	}
}

func TestMissingMetadataDoesNotEraseIndex(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/samantha/conversation/list" {
			respond(t, w, map[string]any{"code": 0, "data": map[string]any{"thread_list": []any{
				map[string]any{"conversation": map[string]any{"conversation_id": "12345678901234567", "conversation_type": 3}},
			}}})
			return
		}
		respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_recent_conv_chain_downlink_body": map[string]any{"cells": []any{}}}})
	})
	if _, err := p.Discover(context.Background(), provider.DiscoverOpts{}); err == nil {
		t.Fatal("incomplete metadata accepted as empty discovery")
	}
}

func TestCookieDecryptionAndHostBinding(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 16)
	host := ".doubao.com"
	digest := sha256.Sum256([]byte(host))
	plain := append(digest[:], []byte("0123456789abcdef0123456789abcdef")...)
	pad := 16 - len(plain)%16
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, 16)).CryptBlocks(ciphertext, plain)
	encrypted := append([]byte("v10"), ciphertext...)
	got, err := decryptCookie(encrypted, key, host, 24)
	if err != nil || got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	if _, err := decryptCookie(encrypted, key, "attacker.example", 24); err == nil {
		t.Fatal("host mismatch accepted")
	}
	for _, bad := range [][]byte{[]byte("v20invalid"), []byte("v10bad"), append([]byte("v10"), bytes.Repeat([]byte{0}, 16)...)} {
		if _, err := decryptCookie(bad, key, host, 24); err == nil {
			t.Fatal("malformed cookie accepted")
		}
	}
	if _, err := cookieHeaders(map[string]string{"sessionid": "bad\r\nheader"}); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestBlockTextExcludesToolsAndReasoning(t *testing.T) {
	var m wireMessage
	err := json.Unmarshal([]byte(`{"content":"{\"text\":\"legacy duplicate\"}","content_block":[{"block_type":10000,"content":{"text_block":{"text":"visible"}}},{"block_type":10000,"parent_id":"tool","content":{"text_block":{"text":"nested tool"}}},{"block_type":10001,"content":{"text_block":{"text":"reasoning"}}}]}`), &m)
	if err != nil || m.text() != "visible" {
		t.Fatalf("block text = %q, %v", m.text(), err)
	}
}

func TestProjectPathUsesNativeChatWorkspaceOrChatsRoot(t *testing.T) {
	p := New()
	p.profile = t.TempDir()
	p.chats = filepath.Join(t.TempDir(), "Doubao", "chats")
	workspace := filepath.Join(p.chats, "2026-09-21", "new-chat-1")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	id := "12345678901234567"
	trajectory := filepath.Join(p.sessionsRoot(), id, "agents", "main", "system", "trajectory.jsonl")
	if err := os.MkdirAll(filepath.Dir(trajectory), 0700); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"role": "assistant", "tool_calls": []any{
		map[string]any{"function": map[string]any{"arguments": map[string]any{"file_path": filepath.Join(workspace, "site", "index.html")}}},
		map[string]any{"function": map[string]any{"arguments": map[string]any{"file_path": "/tmp/unrelated.txt"}}},
	}}
	b, _ := json.Marshal(row)
	if err := os.WriteFile(trajectory, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := p.projectPath(id)
	if err != nil || got != workspace {
		t.Fatalf("project path = %q, %v; want %q", got, err, workspace)
	}
	got, err = p.projectPath("22345678901234567")
	if err != nil || got != p.chats {
		t.Fatalf("fallback project path = %q, %v; want chats root", got, err)
	}
}

func TestChatWorkspaceRejectsLookalikesAndMissingDirectories(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "2026-09-21", "new-chat")
	if err := os.MkdirAll(valid, 0700); err != nil {
		t.Fatal(err)
	}
	if got := chatWorkspace(root, filepath.Join(valid, "a.txt")); got != valid {
		t.Fatalf("valid workspace = %q", got)
	}
	for _, value := range []string{
		filepath.Join(root, "2026-09-21", "chat", "a.txt"),
		filepath.Join(root, "2026-09-21", "new-chat-x", "a.txt"),
		filepath.Join(root, "2026-09-20", "new-chat-2", "missing.txt"),
		filepath.Join(root, "..", "escape", "a.txt"),
	} {
		if got := chatWorkspace(root, value); got != "" {
			t.Fatalf("lookalike %q accepted as %q", value, got)
		}
	}
}

// Opt-in read-only smoke: never runs on CI or contacts a real account by default.
func TestLiveDesktopRead(t *testing.T) {
	if os.Getenv("ANOTHER_DOUBAO_LIVE") != "1" {
		t.Skip("set ANOTHER_DOUBAO_LIVE=1 for a read-only desktop smoke")
	}
	p := New()
	rows, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(rows) == 0 {
		t.Fatalf("discover count=%d err=%v", len(rows), err)
	}
	for _, row := range rows {
		conv, err := p.Load(context.Background(), provider.SessionRef{ID: row.ID})
		if err != nil || conv.CreatedAt.IsZero() || len(conv.Messages) == 0 {
			t.Fatalf("load failed: err=%v", err)
		}
		if strings.TrimSpace(conv.Messages[0].Content) == "" {
			t.Fatal("empty first message")
		}
	}
	t.Logf("Read %d active desktop Work conversations and their complete text histories", len(rows))
}
