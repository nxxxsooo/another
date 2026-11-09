// Package titlemark remembers the last title another wrote for each session.
//
// The mark is what keeps a dynamic title from replacing a person's choice: a
// session whose title still equals the mark is another's to refresh, and one
// whose title changed since was renamed by someone else and is left alone.
// Each session has its own small file so hooks firing for different sessions
// at once never contend for one document.
package titlemark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/titler"
)

// Store is a directory of marks, one file per provider and session.
type Store struct{ dir string }

// Open returns the default store. Marks live beside the settings rather than
// in the cache: losing them would turn every manual title back into one
// another may refresh.
func Open() Store { return At(filepath.Join(config.ConfigDir(), "title-marks")) }

// At returns a store rooted at dir, for tests.
func At(dir string) Store { return Store{dir: dir} }

// path hashes the session id: ids come from agents, and nothing guarantees
// they are valid file names on every platform.
func (s Store) path(provider, session string) string {
	sum := sha256.Sum256([]byte(session))
	return filepath.Join(s.dir, provider, hex.EncodeToString(sum[:16])+".json")
}

// Load returns the mark for a session, or nil when another has none. An
// unreadable mark counts as none, which falls back to judging the title by
// the policy alone.
func (s Store) Load(provider, session string) *titler.Mark {
	data, err := os.ReadFile(s.path(provider, session))
	if err != nil {
		return nil
	}
	var mark titler.Mark
	if json.Unmarshal(data, &mark) != nil || mark.Title == "" {
		return nil
	}
	return &mark
}

// Latest returns the most recently written mark for a provider. It is a
// diagnostic: event checks from another client can follow a successful rename
// and would otherwise leave only a no-op visible in the last-event status.
func (s Store) Latest(provider string) (titler.Mark, bool) {
	entries, err := os.ReadDir(filepath.Join(s.dir, provider))
	if err != nil {
		return titler.Mark{}, false
	}
	var latest titler.Mark
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, provider, entry.Name()))
		if err != nil {
			continue
		}
		var mark titler.Mark
		if json.Unmarshal(data, &mark) != nil || mark.Title == "" || !mark.At.After(latest.At) {
			continue
		}
		latest = mark
	}
	return latest, !latest.At.IsZero()
}

// Save replaces the mark for a session atomically.
func (s Store) Save(provider, session string, mark titler.Mark) error {
	if provider == "" || session == "" {
		return errors.New("titlemark: provider and session are required")
	}
	path := s.path(provider, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(mark)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mark-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
