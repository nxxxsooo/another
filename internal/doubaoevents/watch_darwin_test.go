//go:build darwin

package doubaoevents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	o := Options{DesktopDir: filepath.Join(root, "desktop", "logs"), CLIDir: filepath.Join(root, "cli", "logs"), StatePath: filepath.Join(root, "state", "events.json")}
	return o
}

func appendLog(t *testing.T, p, data string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(data)
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("deadline waiting for event")
		var zero T
		return zero
	}
}

func startWatcher(t *testing.T, o Options, cb func(context.Context, Event) error) (context.CancelFunc, <-chan error) {
	t.Helper()
	ready := make(chan struct{})
	o.Ready = func() { close(ready) }
	w, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Watch(ctx, cb) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("readiness deadline")
	}
	t.Cleanup(cancel)
	return cancel, done
}

func TestWatchNewFilesMissingDirectoriesAndSerialQueue(t *testing.T) {
	o := fixtureOptions(t)
	events := make(chan Event, 8)
	release := make(chan struct{})
	var calls int
	cancel, done := startWatcher(t, o, func(ctx context.Context, e Event) error {
		calls++
		events <- e
		if calls == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	if err := os.MkdirAll(o.CLIDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Creation, write and close all finish before the consumer handles notification.
	p := filepath.Join(o.CLIDir, "doubao-new.log")
	appendLog(t, p, cliLine("c1", "t1"))
	if e := receive(t, events); e.TurnID != "t1" {
		t.Fatalf("first: %+v", e)
	}
	appendLog(t, p, cliLine("c1", "t1")+cliLine("c1", "t2"))
	if err := os.MkdirAll(o.DesktopDir, 0700); err != nil {
		t.Fatal(err)
	}
	appendLog(t, filepath.Join(o.DesktopDir, "saman_frontend_new.log"), desktopLine("c2", "t3"))
	close(release)
	seen := map[string]bool{}
	for range 2 {
		e := receive(t, events)
		seen[e.TurnID] = true
	}
	if !seen["t2"] || !seen["t3"] {
		t.Fatalf("queue/duplicate: %v", seen)
	}
	cancel()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestWatchBaselinePartialRotationTruncateAndDedup(t *testing.T) {
	o := fixtureOptions(t)
	if err := os.MkdirAll(o.CLIDir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(o.CLIDir, "doubao-active.log")
	appendLog(t, p, cliLine("c", "past"))
	events := make(chan Event, 16)
	cb := func(_ context.Context, e Event) error { events <- e; return nil }
	cancel, done := startWatcher(t, o, cb)
	line := cliLine("c", "partial")
	appendLog(t, p, line[:len(line)/2])
	appendLog(t, p, line[len(line)/2:])
	if e := receive(t, events); e.TurnID != "partial" {
		t.Fatalf("baseline/partial: %+v", e)
	}
	rotated := filepath.Join(o.CLIDir, "doubao-rotated.log")
	if err := os.Rename(p, rotated); err != nil {
		t.Fatal(err)
	}
	appendLog(t, p, cliLine("c", "replacement"))
	if e := receive(t, events); e.TurnID != "replacement" {
		t.Fatalf("rotation replay: %+v", e)
	}
	if err := os.WriteFile(p, []byte(cliLine("c", "truncated-new-longer")), 0600); err != nil {
		t.Fatal(err)
	}
	if e := receive(t, events); e.TurnID != "truncated-new-longer" {
		t.Fatalf("truncate: %+v", e)
	}
	// Barrier guarantees earlier callback successes have been saved before stop.
	appendLog(t, p, cliLine("c", "barrier"))
	receive(t, events)
	cancel()
	_ = receive(t, done)
	cancel, done = startWatcher(t, o, cb)
	appendLog(t, p, cliLine("c", "partial")+cliLine("c", "replacement")+cliLine("c", "fresh"))
	if e := receive(t, events); e.TurnID != "fresh" {
		t.Fatalf("persisted dedup: %+v", e)
	}
	cancel()
	_ = receive(t, done)
}

func TestWatchErrorNotMarkedAndInvalidRecordsIgnored(t *testing.T) {
	o := fixtureOptions(t)
	if err := os.MkdirAll(o.CLIDir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(o.CLIDir, "doubao-events.log")
	want := errors.New("callback failed")
	cancel, done := startWatcher(t, o, func(_ context.Context, e Event) error {
		if e.TurnID != "valid" {
			t.Errorf("unexpected event: %+v", e)
		}
		return want
	})
	appendLog(t, p, strings.ReplaceAll(cliLine("c", "sub"), `"agent_type":"main"`, `"agent_type":"sub"`)+strings.ReplaceAll(cliLine("c", "error"), `"has_error":false`, `"has_error":true`)+cliLine("c", "valid"))
	if err := receive(t, done); !errors.Is(err, want) {
		t.Fatalf("callback error: %v", err)
	}
	cancel()
	l, err := loadLedger(o.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Keys) != 0 {
		t.Fatal("failed callback marked")
	}
	events := make(chan Event, 1)
	cancel, done = startWatcher(t, o, func(_ context.Context, e Event) error { events <- e; return nil })
	appendLog(t, p, cliLine("c", "valid"))
	receive(t, events)
	cancel()
	_ = receive(t, done)
}

func TestWatchIdleCancellation(t *testing.T) {
	cancel, done := startWatcher(t, fixtureOptions(t), func(context.Context, Event) error { return nil })
	cancel()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWatchReleasesDescriptors(t *testing.T) {
	o := fixtureOptions(t)
	if err := os.MkdirAll(o.CLIDir, 0700); err != nil {
		t.Fatal(err)
	}
	appendLog(t, filepath.Join(o.CLIDir, "doubao-fd.log"), cliLine("c", "past"))
	run := func() {
		cancel, done := startWatcher(t, o, func(context.Context, Event) error { return nil })
		cancel()
		if err := receive(t, done); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	run() // Warm up Go's own poller/timer descriptors before measuring.
	count := func() int {
		t.Helper()
		entries, err := os.ReadDir("/dev/fd") // Explicit Darwin-only runtime assertion.
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for range 20 {
		run()
	}
	if after := count(); after != before {
		t.Fatalf("descriptor leak: before=%d after=%d", before, after)
	}
}

func TestWatchDesktopResumeDuplicates(t *testing.T) {
	o := fixtureOptions(t)
	events := make(chan Event, 8)
	cancel, done := startWatcher(t, o, func(_ context.Context, e Event) error { events <- e; return nil })
	if err := os.MkdirAll(o.DesktopDir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(o.DesktopDir, "saman_frontend_resume.log")
	first := desktopFirstLine("conversation", "message-1")
	appendLog(t, p, first+desktopLine("conversation", "message-1")+desktopLine("conversation", "message-1")+desktopLine("conversation", "message-2"))
	for _, turn := range []string{"message-1", "message-2"} {
		if e := receive(t, events); e.TurnID != turn || e.Source != Desktop {
			t.Fatalf("desktop dedup: %+v", e)
		}
	}
	cancel()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWatchOnErrorContinuesQueueAndAllowsRedelivery(t *testing.T) {
	o := fixtureOptions(t)
	if err := os.MkdirAll(o.CLIDir, 0700); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 2)
	reported := make(chan Event, 2)
	called := make(chan Event, 8)
	want := errors.New("model unavailable")
	o.Ready = func() { ready <- struct{}{} }
	o.OnError = func(e Event, err error) {
		if !errors.Is(err, want) {
			t.Errorf("reported error: %v", err)
		}
		reported <- e
	}
	w, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		failed := false
		done <- w.Watch(ctx, func(_ context.Context, e Event) error {
			called <- e
			if e.TurnID == "first" && !failed {
				failed = true
				return want
			}
			return nil
		})
	}()
	receive(t, ready)
	p := filepath.Join(o.CLIDir, "doubao-onerror.log")
	appendLog(t, p, cliLine("session-a", "first")+cliLine("session-b", "second"))
	if e := receive(t, reported); e.TurnID != "first" {
		t.Fatalf("reported event: %+v", e)
	}
	for _, turn := range []string{"first", "second"} {
		if e := receive(t, called); e.TurnID != turn {
			t.Fatalf("queue: %+v", e)
		}
	}
	l, err := loadLedger(o.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := eventKey(Event{Source: CLI, ConversationID: "session-a", TurnID: "first"})
	if l.contains(firstKey) {
		t.Fatal("failed event persisted as success")
	}
	appendLog(t, p, cliLine("session-a", "first")+cliLine("session-c", "barrier"))
	for _, turn := range []string{"first", "barrier"} {
		if e := receive(t, called); e.TurnID != turn {
			t.Fatalf("redelivery: %+v", e)
		}
	}
	l, err = loadLedger(o.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !l.contains(firstKey) || !l.contains(eventKey(Event{Source: CLI, ConversationID: "session-b", TurnID: "second"})) {
		t.Fatal("successful redelivery or next session not persisted")
	}
	cancel()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(ready) != 0 || len(reported) != 0 {
		t.Fatal("unexpected readiness re-entry or retry")
	}
}

func TestWatchRegistrationLimit(t *testing.T) {
	r := eventReader{entries: make(map[string]*watched)}
	for i := range maxWatches {
		r.entries[strconv.Itoa(i)] = nil
	}
	err := r.add(filepath.Join(t.TempDir(), "doubao-new.log"), CLI, false, false)
	if err == nil || !strings.Contains(err.Error(), "watch limit exceeded") {
		t.Fatalf("watch limit: %v", err)
	}
}
