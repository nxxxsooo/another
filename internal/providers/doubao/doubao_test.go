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
	"runtime"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
)

const testID = "38451559494915842"

func writeFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileSelection(t *testing.T) {
	root := t.TempDir()
	def, other := filepath.Join(root, "Default"), filepath.Join(root, "Profile 2")
	writeFile(t, filepath.Join(def, "Cookies"), "")
	if got, err := selectProfile(root, ""); err != nil || got != def {
		t.Fatalf("single profile %q %v", got, err)
	}
	writeFile(t, filepath.Join(other, "Cookies"), "")
	if _, err := selectProfile(root, ""); err == nil {
		t.Fatal("ambiguous profiles accepted")
	}
	writeFile(t, filepath.Join(root, "Local State"), `{"profile":{"last_used":"Default"}}`)
	if got, err := selectProfile(root, ""); err != nil || got != def {
		t.Fatalf("active profile %q %v", got, err)
	}
	if got, err := selectProfile(root, other); err != nil || got != other {
		t.Fatalf("explicit profile %q %v", got, err)
	}
	writeFile(t, filepath.Join(root, "Local State"), `{"profile":{"last_used":"Profile 1"}}`)
	if _, err := selectProfile(root, ""); err == nil {
		t.Fatal("stale active profile fell back to another account")
	}
	writeFile(t, filepath.Join(root, "Local State"), `{"profile":{"last_used":"../Default"}}`)
	if _, err := selectProfile(root, ""); err == nil {
		t.Fatal("profile traversal accepted")
	}
	if _, err := selectProfile(root, filepath.Join(root, "missing")); err == nil {
		t.Fatal("explicit profile fell back")
	}
}

func TestCLIListParser(t *testing.T) {
	got, err := parseCLIList("Sessions:\n" + testID + "  中文 title with  spaces\n99999999999999999\tOther\n" + testID + "  中文 title with  spaces\n")
	if err != nil || len(got) != 2 || got[testID] != "中文 title with  spaces" || got["99999999999999999"] != "Other" {
		t.Fatalf("parsed %v %v", got, err)
	}
	if _, err := parseCLIList(testID + " One\n" + testID + " Two"); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	if validID("../1") || validID("1e17") || validID("") {
		t.Fatal("invalid identity accepted")
	}
	if _, err := parseCLIList("Please sign in"); err == nil {
		t.Fatal("unexpected CLI output treated as empty sessions")
	}
}

func TestExplicitProfileNeverFallsBack(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("DOUBAO_PROFILE", profile)
	if got := New().profile; got != profile {
		t.Fatalf("explicit profile silently changed: %q", got)
	}
}

func testProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	p := New()
	p.profile = t.TempDir()
	p.chats = filepath.Join(t.TempDir(), "Doubao", "chats")
	writeFile(t, filepath.Join(p.profile, "Cookies"), "")
	p.base, p.http = s.URL, s.Client()
	p.auth = func(context.Context, string) (http.Header, error) { return make(http.Header), nil }
	p.cliInstalled = func() bool { return true }
	p.listCLI = func(context.Context) (map[string]string, error) { return map[string]string{testID: "Native"}, nil }
	return p
}

func respond(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func listResponse(title string) map[string]any {
	return map[string]any{"code": 0, "data": map[string]any{"thread_list": []any{
		map[string]any{"thread_id_str": testID, "conversation": map[string]any{"conversation_id": testID, "conversation_type": 3, "name": title, "update_time": 1789993061}},
		map[string]any{"thread_id_str": "99999999999999999", "conversation": map[string]any{"conversation_id": "99999999999999999", "conversation_type": 3, "name": "Private cloud chat"}},
	}}}
}

func TestScopedDiscoveryLoadAndRename(t *testing.T) {
	title := "Native"
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json; encoding=utf-8" || r.Header.Get("Agw-Js-Conv") != "str" {
			t.Error("native IM requires the exact encoding Content-Type and string identity header")
		}
		switch r.URL.Path {
		case "/samantha/conversation/list":
			respond(t, w, listResponse(title))
		case "/im/chain/recent_conv":
			respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_recent_conv_chain_downlink_body": map[string]any{"cells": []any{map[string]any{"conversation": map[string]any{"conversation_id": testID, "conversation_status": 1, "create_time": "1789000000", "conv_version": "1789000000123456"}}}}}})
		case "/im/chain/single":
			respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_singe_chain_downlink_body": map[string]any{"messages": []any{
				map[string]any{"message_id": "m2", "user_type": 2, "index_in_conv": "2", "content": `{"text":"Answer"}`},
				map[string]any{"message_id": "m1", "user_type": 1, "index_in_conv": "1", "content": `{"text":"Question"}`},
			}}}})
		case "/samantha/thread/update":
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req["conversation_id"] != testID || req["thread_id"] != testID || len(req) != 3 {
				t.Errorf("wrong rename identity %v", req)
			}
			title = req["name"]
			respond(t, w, map[string]int{"code": 0})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	// Desktop shares the same identity; listing still yields only one row.
	if err := os.MkdirAll(filepath.Join(sessionsRoot(p.profile), testID), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rows, err := p.Discover(ctx, provider.DiscoverOpts{})
	if err != nil || len(rows) != 1 || rows[0].ID != testID || rows[0].SourceMtime != 1789000000123456000 {
		t.Fatalf("discovery %+v %v", rows, err)
	}
	ref := provider.SessionRef{ID: testID}
	conv, err := p.Load(ctx, ref)
	if err != nil || len(conv.Messages) != 2 || conv.Messages[0].Content != "Question" {
		t.Fatalf("load %+v %v", conv, err)
	}
	if err := p.RenameSession(ctx, ref, "中文 Native rename"); err != nil {
		t.Fatal(err)
	}
	if got, err := p.NativeTitle(ctx, ref); err != nil || got != title {
		t.Fatalf("native title %q %v", got, err)
	}
	if p.DisplayName() != "Doubao" || !strings.Contains(p.ResumeCommand(provider.WriteResult{SessionID: testID}), testID) {
		t.Fatal("name/resume contract")
	}
}

func TestAccountMismatchAndAuthFailureDoNotFallback(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) { respond(t, w, listResponse("Native")) })
	p.listCLI = func(context.Context) (map[string]string, error) {
		return map[string]string{"11111111111111111": "Native"}, nil
	}
	if _, err := p.Discover(context.Background(), provider.DiscoverOpts{}); err == nil || !strings.Contains(err.Error(), "same account") {
		t.Fatalf("mismatch: %v", err)
	}
	calls := 0
	p.auth = func(context.Context, string) (http.Header, error) {
		calls++
		return nil, errors.New("selected profile expired")
	}
	if _, err := p.Discover(context.Background(), provider.DiscoverOpts{}); err == nil || calls != 1 {
		t.Fatalf("auth calls %d err %v", calls, err)
	}
	if !p.Installed() || calls != 1 {
		t.Fatal("Installed used authentication")
	}
}

func TestNonWorkCLIEntriesAreFiltered(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		response := listResponse("Native")
		data := response["data"].(map[string]any)
		data["thread_list"] = append(data["thread_list"].([]any), map[string]any{"conversation": map[string]any{"conversation_id": "11111111111111111", "conversation_type": 1}})
		respond(t, w, response)
	})
	p.listCLI = func(context.Context) (map[string]string, error) {
		return map[string]string{testID: "Native", "11111111111111111": "Enterprise bot"}, nil
	}
	c, profile, err := p.client(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.nativeConversations(context.Background(), c, profile)
	if err != nil || len(rows) != 1 || rows[0].ID != testID {
		t.Fatalf("scoped rows %v err %v", rows, err)
	}
}

func TestMessagePaginationAndUnknownCursorFailClosed(t *testing.T) {
	for _, cursorKnown := range []bool{true, false} {
		t.Run(map[bool]string{true: "known cursor", false: "unverified cursor"}[cursorKnown], func(t *testing.T) {
			calls := 0
			p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				data := map[string]any{"has_more": true, "messages": []any{map[string]any{"message_id": "m2", "index_in_conv": "2", "user_type": 2, "content": `{"text":"Second"}`}}}
				if cursorKnown {
					data["next_index"] = "2"
				}
				if calls == 2 {
					var req struct {
						Body map[string]struct {
							Anchor integer `json:"anchor_index"`
						} `json:"uplink_body"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					if req.Body["pull_singe_chain_uplink_body"].Anchor != 2 {
						t.Error("wrong message cursor")
					}
					data = map[string]any{"has_more": false, "messages": []any{map[string]any{"message_id": "m1", "index_in_conv": "1", "user_type": 1, "content": `{"text":"First"}`}}}
				}
				respond(t, w, map[string]any{"status_code": 0, "downlink_body": map[string]any{"pull_singe_chain_downlink_body": data}})
			})
			c, _, _ := p.client(context.Background())
			messages, err := c.messages(context.Background(), testID, 0)
			if !cursorKnown {
				if err == nil || len(messages) != 0 {
					t.Fatalf("truncated history accepted %v %v", messages, err)
				}
			} else if err != nil || len(messages) != 2 || messages[0].Content != "First" {
				t.Fatalf("history %v %v", messages, err)
			}
		})
	}
}

func TestIMFailureIsNotEmptyHistory(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/samantha/conversation/list" {
			respond(t, w, listResponse("Native"))
			return
		}
		respond(t, w, map[string]int{"status_code": 712012002})
	})
	if _, err := p.Discover(context.Background(), provider.DiscoverOpts{}); err == nil || !strings.Contains(err.Error(), "712012002") {
		t.Fatalf("IM status lost: %v", err)
	}
	if _, err := p.Load(context.Background(), provider.SessionRef{ID: testID}); err == nil {
		t.Fatal("failed metadata became empty history")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Discover(ctx, provider.DiscoverOpts{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestRenameReadbackFailure(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/samantha/conversation/list" {
			respond(t, w, listResponse("Unchanged"))
			return
		}
		respond(t, w, map[string]int{"code": 0})
	})
	if err := p.RenameSession(context.Background(), provider.SessionRef{ID: testID}, "New"); !errors.Is(err, provider.ErrPartial) {
		t.Fatalf("readback mismatch: %v", err)
	}
}

func TestListPaginationAndErrors(t *testing.T) {
	calls := 0
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Index integer `json:"index"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if calls == 1 {
			respond(t, w, map[string]any{"code": 0, "data": map[string]any{"has_more": true, "next_index": "99999999999999999"}})
			return
		}
		if req.Index != 99999999999999999 {
			t.Errorf("cursor rounded: %d", req.Index)
		}
		respond(t, w, listResponse("Native"))
	})
	c, _, err := p.client(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := c.list(context.Background()); err != nil || len(rows) != 2 || calls != 2 {
		t.Fatalf("pagination rows %v calls %d err %v", rows, calls, err)
	}
	for _, body := range []string{`{"code":1}`, `{}`, `not json`, `{"code":0,"data":{"has_more":true,"next_index":0}}`} {
		t.Run(body, func(t *testing.T) {
			q := testProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			client, _, _ := q.client(context.Background())
			if _, err := client.list(context.Background()); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestCookieDecryptionAndHeaders(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 16)
	digest := sha256.Sum256([]byte(".doubao.com"))
	plain := append(digest[:], []byte("fixture-session")...)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	encrypted := make([]byte, len(plain)+3)
	copy(encrypted, "v10")
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, 16)).CryptBlocks(encrypted[3:], plain)
	if got, err := decryptCookie(encrypted, key, ".doubao.com", 24); err != nil || got != "fixture-session" {
		t.Fatalf("decrypt %q %v", got, err)
	}
	if _, err := decryptCookie(encrypted, key, "other", 24); err == nil {
		t.Fatal("wrong host accepted")
	}
	if _, err := cookieHeaders(map[string]string{"sessionid": "bad;value"}); err == nil {
		t.Fatal("cookie injection accepted")
	}
	if _, err := cookieHeaders(nil); err == nil {
		t.Fatal("missing auth accepted")
	}
	if runtime.GOOS != "darwin" {
		if _, err := desktopAuth(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "macOS") {
			t.Fatalf("unsupported auth: %v", err)
		}
	}
}

func TestProjectPathContainment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "chats")
	workspace := filepath.Join(root, "2026-11-09", "new-chat-2")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if got := chatWorkspace(root, filepath.Join(workspace, "file.txt")); got != workspace {
		t.Fatalf("child %q", got)
	}
	for _, value := range []string{filepath.Join(root+"-sibling", "2026-11-09", "new-chat-2", "x"), filepath.Join(root, "..", "outside", "x"), "relative"} {
		if got := chatWorkspace(root, value); got != "" {
			t.Errorf("outside %q => %q", value, got)
		}
	}
}
