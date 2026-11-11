package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/herdr"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/opencode"
	"github.com/nxxxsooo/another/internal/registry"
)

func herdrTestModel(t *testing.T) modelState {
	t.Helper()
	m := layoutTestModel()
	m.ctx = context.Background()
	m.reg = registry.NewWith(opencode.NewAt(filepath.Join(t.TempDir(), "opencode.db"), "opencode"))
	m.setSessionItems([]list.Item{sessionItem{summary: model.Summary{
		Provider: "opencode", ID: "ses_quoted'; echo nope; #", Title: "Native session", ProjectPath: filepath.Join(t.TempDir(), "project [one] 'quoted'"),
	}}})
	return m
}

func TestHerdrKeyIsAsyncKeepsTUIAndPreservesNativeResume(t *testing.T) {
	m := herdrTestModel(t)
	var launch struct {
		Space, Folder string
		Argv          []string
	}
	calls := 0
	m.herdrClient = herdr.Client{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		switch name + " " + strings.Join(args, " ") {
		case "hc server status --json":
			return []byte(`{"pid":1}`), nil
		case "hc ls --json":
			return []byte(`{"terminals":[]}`), nil
		case "herdr-app ls":
			return []byte(`{"spaces":[{"id":"local","machine":null}]}`), nil
		}
		if name == "herdr-app" && len(args) == 3 && args[0] == "do" && args[1] == "tab.new" {
			if err := json.Unmarshal([]byte(args[2]), &launch); err != nil {
				return nil, err
			}
			return []byte(`{"terminal":"new"}`), nil
		}
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}}
	updated, cmd := m.Update(runeKey('H'))
	opening := updated.(modelState)
	if cmd == nil || !opening.herdrOpening || opening.launch != "" || calls != 0 {
		t.Fatalf("H was not async: opening=%v launch=%q calls=%d", opening.herdrOpening, opening.launch, calls)
	}
	// Repeated H while the command is pending cannot launch a second tab.
	if _, duplicate := opening.Update(runeKey('H')); duplicate != nil {
		t.Fatal("repeated H scheduled a duplicate")
	}
	msg, ok := cmd().(herdrOpenedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("message=%+v", msg)
	}
	sm := m.sessions.SelectedItem().(sessionItem).summary
	p, _ := m.reg.Get("opencode")
	native := p.ResumeCommand(provider.WriteResult{SessionID: sm.ID, ProjectPath: sm.ProjectPath, StoragePath: sm.StoragePath})
	want := shellCommand(defaultShell(), native).Args
	if !reflect.DeepEqual(launch.Argv, want) || launch.Space != "local" || launch.Folder != sm.ProjectPath {
		t.Fatalf("launch=%+v want argv=%v", launch, want)
	}
	if runtime.GOOS == "windows" {
		if len(launch.Argv) != 5 || launch.Argv[3] != "-Command" || !strings.Contains(launch.Argv[4], "Set-Location -LiteralPath") {
			t.Fatalf("native Windows launch: %v", launch.Argv)
		}
	} else if len(launch.Argv) != 3 || launch.Argv[1] != "-c" {
		t.Fatalf("native POSIX launch: %v", launch.Argv)
	}
	finished, refresh := opening.Update(msg)
	got := finished.(modelState)
	if got.herdrOpening || got.launch != "" || got.status == "" || refresh == nil || got.sessions.SelectedItem().(sessionItem).summary.ID != sm.ID {
		t.Fatalf("H failed to preserve browser: %+v", got)
	}
	// Enter still hands this same native command to the original terminal.
	entered, quit := m.Update(enterKey())
	if entered.(modelState).launch != native || quit == nil {
		t.Fatal("Enter behavior changed")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("Enter no longer quits for native resume")
	}
}

func TestHerdrLiveIndicatorExactProviderScopedAndRefreshable(t *testing.T) {
	m := herdrTestModel(t)
	items := []list.Item{
		sessionItem{summary: model.Summary{ID: "exact", Provider: "opencode", Title: "OpenCode"}},
		sessionItem{summary: model.Summary{ID: "exact", Provider: "pi", Title: "Other provider"}},
		sessionItem{summary: model.Summary{ID: "exact_suffix", Provider: "opencode", Title: "Near match"}},
	}
	m.setSessionItems(items)
	updated, _ := m.Update(herdrSnapshotMsg{live: map[string][]string{"exact": {"t1", "t2"}}})
	got := updated.(modelState)
	for i, item := range got.sessions.Items() {
		row := item.(sessionItem)
		if row.herdrLive != (i == 0) {
			t.Fatalf("row %d incorrectly marked live: %+v", i, row)
		}
		var out bytes.Buffer
		(sessionDelegate{}).Render(&out, got.sessions, i, item)
		if strings.Contains(out.String(), "●") != (i == 0) {
			t.Fatalf("row %d live rendering: %q", i, out.String())
		}
	}
	updated, _ = got.Update(herdrSnapshotMsg{})
	for _, item := range updated.(modelState).sessions.Items() {
		if item.(sessionItem).herdrLive {
			t.Fatal("unavailable probe left stale live indicator")
		}
	}
}

func TestHerdrUnavailableOptionalProbeAndExplicitActionError(t *testing.T) {
	m := herdrTestModel(t)
	m.herdrClient = herdr.Client{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("hc unavailable") }}
	probe := m.herdrProbeCmd()().(herdrSnapshotMsg)
	if probe.live != nil {
		t.Fatalf("unavailable probe=%+v", probe)
	}
	updated, _ := m.Update(probe)
	if updated.(modelState).err != "" {
		t.Fatal("optional probe disrupted startup")
	}
	opening, cmd := m.Update(runeKey('H'))
	msg := cmd().(herdrOpenedMsg)
	if msg.err == nil {
		t.Fatal("explicit H must report unavailable integration")
	}
	finished, _ := opening.(modelState).Update(msg)
	got := finished.(modelState)
	if got.err == "" || got.status != "" || got.launch != "" || got.herdrOpening {
		t.Fatalf("error state: err=%q status=%q launch=%q opening=%v", got.err, got.status, got.launch, got.herdrOpening)
	}
}

func TestHerdrRejectsOtherProvidersBeforeProbing(t *testing.T) {
	m := layoutTestModel()
	m.herdrClient = herdr.Client{Run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unsupported provider probed Herdr")
		return nil, nil
	}}
	updated, cmd := m.Update(runeKey('H'))
	if cmd != nil || updated.(modelState).err == "" {
		t.Fatal("unsupported provider accepted")
	}
}

func TestHerdrPendingGatesLifecycleKeysAndOverlayConfirmations(t *testing.T) {
	for _, marked := range []bool{false, true} {
		for _, key := range []string{"a", "u", "r", "ctrl+r", "ctrl+t", "m", "d", "ctrl+d", "right", "enter"} {
			t.Run(fmt.Sprintf("list/marked=%v/%s", marked, key), func(t *testing.T) {
				m := herdrTestModel(t)
				opened, pending := m.Update(runeKey('H'))
				m = opened.(modelState)
				if pending == nil || !m.herdrOpening {
					t.Fatal("H did not become pending")
				}
				if marked {
					m.marked[m.sessions.SelectedItem().(sessionItem).summary.ID] = true
				}
				// Include undo and the post-migration native launch Enter path.
				summary := m.sessions.SelectedItem().(sessionItem).summary
				m.lastArchived = &summary
				m.lastResume = "native saved resume"
				cancelled := false
				m.cancel = func() { cancelled = true }
				updated, cmd := m.Update(herdrTestKey(key))
				got := updated.(modelState)
				if cmd != nil || got.loading || got.overlay != overlayNone || got.launch != "" || cancelled || got.status != txt.herdrBusy {
					t.Fatalf("pending H allowed %s: cmd=%v loading=%v overlay=%d launch=%q cancelled=%v status=%q", key, cmd, got.loading, got.overlay, got.launch, cancelled, got.status)
				}
			})
		}
	}
	for _, overlay := range []int{overlayRename, overlayDelete, overlayRelocate, overlayTarget, overlayHistory, overlayBatchTitle} {
		t.Run(fmt.Sprintf("overlay/%d", overlay), func(t *testing.T) {
			m := herdrTestModel(t)
			m.herdrOpening, m.overlay, m.deleteChoice = true, overlay, 1
			m.renameInput.SetValue("Changed title")
			updated, cmd := m.Update(enterKey())
			got := updated.(modelState)
			if cmd != nil || got.loading || got.overlay != overlay || got.status != txt.herdrBusy {
				t.Fatalf("confirmation escaped gate: overlay=%d cmd=%v status=%q", overlay, cmd, got.status)
			}
			if overlay == overlayBatchTitle {
				for _, key := range []rune{'r', 'R'} {
					updated, cmd = m.Update(runeKey(key))
					if cmd != nil || updated.(modelState).status != txt.herdrBusy {
						t.Fatal("batch retry escaped gate")
					}
				}
			}
		})
	}
}

func herdrTestKey(key string) tea.KeyMsg {
	switch key {
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return enterKey()
	}
	return runeKey([]rune(key)[0])
}

func TestHerdrPendingAllowsBrowsingAndGateReleases(t *testing.T) {
	for _, key := range []tea.KeyMsg{runeKey('/'), runeKey('?'), runeKey('g'), runeKey('x'), {Type: tea.KeyDown}, runeKey(' ')} {
		m := herdrTestModel(t)
		m.herdrOpening = true
		updated, _ := m.Update(key)
		if updated.(modelState).status == txt.herdrBusy {
			t.Fatalf("browsing blocked: %s", key.String())
		}
	}
	m := herdrTestModel(t)
	m.herdrOpening, m.overlay = true, overlayRename
	updated, _ := m.Update(runeKey('a'))
	if updated.(modelState).status == txt.herdrBusy {
		t.Fatal("rename text editing was mistaken for archive")
	}
	m.overlay, m.deleteChoice = overlayDelete, 0
	updated, _ = m.Update(enterKey())
	if updated.(modelState).overlay != overlayNone {
		t.Fatal("cancel deletion should remain available")
	}
	for _, failure := range []error{nil, errors.New("Herdr unavailable")} {
		m = herdrTestModel(t)
		m.herdrOpening = true
		blocked, cmd := m.Update(enterKey())
		if cmd != nil || blocked.(modelState).launch != "" || blocked.(modelState).status != txt.herdrBusy {
			t.Fatal("native Enter raced the pending Herdr action")
		}
		finished, _ := m.Update(herdrOpenedMsg{err: failure})
		m = finished.(modelState)
		if m.herdrOpening {
			t.Fatal("result did not release gate")
		}
		entered, quit := m.Update(enterKey())
		if entered.(modelState).launch == "" || quit == nil {
			t.Fatal("ordinary Enter stopped resuming after H completed")
		}
		if _, ok := quit().(tea.QuitMsg); !ok {
			t.Fatal("ordinary Enter stopped exiting")
		}
	}
}

func TestHerdrProbeKeepsBadgeDespiteUnrelatedIncompleteMetadata(t *testing.T) {
	m := herdrTestModel(t)
	row := m.sessions.SelectedItem().(sessionItem)
	row.summary.ID = "ses_exact"
	m.setSessionItems([]list.Item{row})
	m.herdrClient = herdr.Client{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name + " " + strings.Join(args, " ") {
		case "hc server status --json":
			return []byte(`{"pid":1}`), nil
		case "hc ls --json":
			return []byte(`{"terminals":[{"address":"t_idle","agent":"opencode","state":"live"},{"address":"t1","agent":"opencode","state":"live"}]}`), nil
		case "hc get t_idle --json":
			return []byte(`{"address":"t_idle","state":"live","status":{"app":"opencode","state":"idle"}}`), nil
		case "hc get t1 --json":
			return []byte(`{"address":"t1","state":"live","status":{"app":"opencode","state":"working"},"records":{"ses_exact":{"app":"opencode","state":"working"}}}`), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}}
	if live, err := m.herdrClient.Snapshot(context.Background()); err == nil || len(live["ses_exact"]) != 1 {
		t.Fatalf("fixture must return partial knowledge: live=%v err=%v", live, err)
	}
	probe := m.herdrProbeCmd()().(herdrSnapshotMsg)
	updated, _ := m.Update(probe)
	got := updated.(modelState)
	item := got.sessions.SelectedItem().(sessionItem)
	if !item.herdrLive || got.err != "" {
		t.Fatalf("partial probe lost known badge or disrupted browsing: live=%v err=%q", item.herdrLive, got.err)
	}
	var out bytes.Buffer
	(sessionDelegate{}).Render(&out, got.sessions, got.sessions.Index(), item)
	if !strings.Contains(out.String(), "●") {
		t.Fatalf("known live badge not rendered: %q", out.String())
	}
}
