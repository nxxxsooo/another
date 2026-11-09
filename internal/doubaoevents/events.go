// Package doubaoevents observes native Doubao completion logs, without polling
// sessions or making network requests. Logs present at startup start at EOF:
// completions written while the watcher was stopped are deliberately not replayed.
package doubaoevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Source string

const (
	Desktop    Source = "desktop"
	CLI        Source = "cli"
	maxLine           = 256 * 1024
	maxPending        = 8192
	maxDedup          = 4096
)

var ErrUnsupported = errors.New("doubao native event watching requires macOS")

// Event contains identifiers only, never conversation content or log text.
type Event struct {
	ConversationID string
	TurnID         string
	Source         Source
}

type Options struct {
	DesktopDir string
	CLIDir     string
	// StatePath is a private, atomically replaced JSON ledger. Only one Watcher
	// may own a given ledger. Cursors are not persisted.
	StatePath string
	// Ready runs after the startup EOF baselines and watches are installed.
	Ready func()
	// OnError reports callback failures and allows subsequent queued events to
	// continue. Failed turns remain eligible for later native redelivery; there
	// are no automatic retries. The hook must not block. Nil makes callback
	// errors terminate Watch instead.
	OnError func(Event, error)
}

func DefaultOptions() (Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Options{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Options{}, err
	}
	return Options{
		DesktopDir: filepath.Join(home, "Library", "Application Support", "Doubao", "sdk_storage", "log"),
		CLIDir:     filepath.Join(home, "Library", "Application Support", "doubao_cli", "logs", "native"),
		StatePath:  filepath.Join(cache, "another", "doubao-events.json"),
	}, nil
}

type Watcher struct{ options Options }

func New(options Options) (*Watcher, error) {
	for _, p := range []string{options.DesktopDir, options.CLIDir, options.StatePath} {
		if !filepath.IsAbs(p) {
			return nil, errors.New("doubao event paths must be absolute and nonempty")
		}
	}
	if filepath.Clean(options.DesktopDir) == filepath.Clean(options.CLIDir) {
		return nil, errors.New("doubao desktop and CLI log directories must differ")
	}
	return &Watcher{options: options}, nil
}

// Watch invokes callbacks serially while a separate reader keeps consuming native
// events. A callback must honor ctx. Callback, persistence, I/O and queue overflow
// errors terminate Watch visibly, except callback errors handled by OnError;
// failed callbacks are never marked successful.
// Delivery is at-least-once across a crash between callback success and ledger save.
func (w *Watcher) Watch(ctx context.Context, callback func(context.Context, Event) error) error {
	if callback == nil {
		return errors.New("doubao event callback is required")
	}
	return w.watch(ctx, callback)
}

type ledger struct {
	Keys []string `json:"keys"`
}

func loadLedger(path string) (*ledger, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ledger{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	s, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if s.Size() > maxDedup*70 {
		return nil, errors.New("doubao event ledger exceeds limit")
	}
	var l ledger
	if err := json.NewDecoder(f).Decode(&l); err != nil {
		return nil, fmt.Errorf("decode Doubao event ledger: %w", err)
	}
	if len(l.Keys) > maxDedup {
		return nil, errors.New("doubao event ledger exceeds entry limit")
	}
	for _, k := range l.Keys {
		if b, err := hex.DecodeString(k); err != nil || len(b) != sha256.Size {
			return nil, errors.New("invalid Doubao event ledger key")
		}
	}
	return &l, nil
}

func eventKey(e Event) string {
	h := sha256.Sum256([]byte(string(e.Source) + "\x00" + e.ConversationID + "\x00" + e.TurnID))
	return hex.EncodeToString(h[:])
}

func (l *ledger) contains(key string) bool {
	for _, k := range l.Keys {
		if k == key {
			return true
		}
	}
	return false
}

func (l *ledger) save(path, key string) error {
	keys := append(append([]string(nil), l.Keys...), key)
	if len(keys) > maxDedup {
		keys = keys[len(keys)-maxDedup:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".doubao-events-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	err = json.NewEncoder(f).Encode(ledger{Keys: keys})
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	l.Keys = keys
	return nil
}
