// Package journal records the session actions another performs — archive,
// delete, and rename — so they can be reviewed and reversed later. It keeps an
// append-only log of what happened and, for deletes, a bounded store of the
// removed bytes so the deletion can be undone into the agent's own state after
// another has exited.
//
// It is a record, never a source of truth: the agent's own storage stays the
// only place a session lives, and every revert goes back through the provider's
// native operation rather than replaying another's copy.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/config"
)

// Action names one recorded operation.
type Action string

const (
	ActionArchive   Action = "archive"
	ActionUnarchive Action = "unarchive"
	ActionRename    Action = "rename"
	ActionDelete    Action = "delete"
	// ActionRevert records that an earlier entry was reversed. Its Target is
	// the entry it reversed; the overlay lists these for the record but does
	// not offer to reverse them in turn.
	ActionRevert Action = "revert"
)

// Entry is one line of the log. Fields not relevant to an action are omitted.
type Entry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Action   Action    `json:"action"`
	Provider string    `json:"provider"`
	Session  string    `json:"session"`
	Title    string    `json:"title,omitempty"`
	Project  string    `json:"project,omitempty"`
	Storage  string    `json:"storage,omitempty"`
	// Before and After carry titles for rename; Archived carries the resulting
	// state for archive/unarchive. Revertible says whether a durable inverse
	// exists (a delete whose bytes were captured, or any native archive/rename).
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
	Revertible bool   `json:"revertible,omitempty"`
	// Target and Result belong to ActionRevert.
	Target string `json:"target,omitempty"`
	Result string `json:"result,omitempty"`
}

// Snapshot is one captured delete, keyed by the entry that recorded it.
type Snapshot struct {
	EntryID string    `json:"entry_id"`
	File    string    `json:"file"`
	Size    int64     `json:"size"`
	At      time.Time `json:"at"`
}

// Limits bounds what history keeps. Logs are small; snapshot blobs are the
// sessions themselves and are what actually has to be bounded.
type Limits struct {
	MaxSnapshots int
	MaxAge       time.Duration
	MaxEntries   int
}

// DefaultLimits keeps the newest fifty deletes from the last thirty days, and
// the newest five thousand log lines. The values are a recovery aid's balance:
// enough to catch a mistake made recently, bounded so a large session cannot
// quietly fill the disk.
func DefaultLimits() Limits {
	return Limits{MaxSnapshots: 50, MaxAge: 30 * 24 * time.Hour, MaxEntries: 5000}
}

// Store is the history directory: an append-only journal.jsonl, a prunable
// snapshots.json, and blobs/ holding captured delete bytes.
type Store struct {
	dir    string
	limits Limits
}

// Open returns the default store under another's cache directory. A store that
// cannot be created is still returned; failures surface on the first write,
// because a person who never uses history should never see it fail.
func Open() (*Store, error) {
	return At(filepath.Join(config.CacheDir(), "history")), nil
}

// At returns a store rooted at dir. It exists for tests and for callers that
// know the directory.
func At(dir string) *Store {
	return &Store{dir: dir, limits: DefaultLimits()}
}

func (s *Store) journalPath() string   { return filepath.Join(s.dir, "journal.jsonl") }
func (s *Store) snapshotsPath() string { return filepath.Join(s.dir, "snapshots.json") }
func (s *Store) blobsDir() string      { return filepath.Join(s.dir, "blobs") }

func (s *Store) ensureDir() error {
	if err := os.MkdirAll(s.blobsDir(), 0o700); err != nil {
		return err
	}
	return os.Chmod(s.dir, 0o700)
}

// NewID is a sortable identifier: the time first, so an id ordered
// lexicographically is ordered in time, then a random tail.
func NewID(now time.Time) string {
	return fmt.Sprintf("%013d-%s", now.UnixMilli(), randomTail())
}

// Record appends one entry and prunes what the limits no longer keep. The log
// is the durable part: a failure here is returned so a destructive caller can
// decide, while a reversible caller treats it as a soft warning.
func (s *Store) Record(e Entry) error {
	if e.ID == "" {
		e.ID = NewID(time.Now())
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.journalPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.pruneLog()
	s.pruneSnapshots()
	return nil
}

// SaveSnapshot stores the captured bytes for a delete entry and records it in
// the snapshot index. It must run before the session is deleted.
func (s *Store) SaveSnapshot(entryID string, data []byte) error {
	if entryID == "" {
		return errors.New("journal: snapshot needs an entry id")
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	file := entryID + ".blob"
	if err := os.WriteFile(filepath.Join(s.blobsDir(), file), data, 0o600); err != nil {
		return err
	}
	index, err := s.snapshotIndex()
	if err != nil {
		return err
	}
	index[entryID] = Snapshot{EntryID: entryID, File: file, Size: int64(len(data)), At: time.Now()}
	if err := s.writeSnapshotIndex(index); err != nil {
		return err
	}
	// Prune here as well as on Record: a batch delete writes many snapshots
	// with no Record between them, and the cap is about the blobs on disk.
	s.pruneSnapshots()
	return nil
}

// DropSnapshot removes a captured delete that did not end up happening.
func (s *Store) DropSnapshot(entryID string) {
	index, err := s.snapshotIndex()
	if err != nil {
		return
	}
	if snap, ok := index[entryID]; ok {
		_ = os.Remove(filepath.Join(s.blobsDir(), snap.File))
		delete(index, entryID)
		_ = s.writeSnapshotIndex(index)
	}
}

// LoadSnapshot returns the captured bytes for an entry, or ok=false when none
// survives — pruned by the limits, or never taken because the provider cannot
// snapshot.
func (s *Store) LoadSnapshot(entryID string) ([]byte, bool) {
	index, err := s.snapshotIndex()
	if err != nil {
		return nil, false
	}
	snap, ok := index[entryID]
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(s.blobsDir(), snap.File))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Entries returns the whole log, oldest first.
func (s *Store) Entries() ([]Entry, error) {
	f, err := os.Open(s.journalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// A corrupt line must not take the rest of the history with it.
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Recovery reports whether an entry's delete bytes are still available. A
// non-delete entry is "recoverable" when it was native and thus always has an
// inverse.
func (s *Store) Recovery(e Entry) bool {
	if e.Action != ActionDelete {
		return e.Revertible
	}
	if !e.Revertible {
		return false
	}
	_, ok := s.LoadSnapshot(e.ID)
	return ok
}

func (s *Store) snapshotIndex() (map[string]Snapshot, error) {
	data, err := os.ReadFile(s.snapshotsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Snapshot{}, nil
		}
		return nil, err
	}
	index := map[string]Snapshot{}
	if err := json.Unmarshal(data, &index); err != nil {
		return map[string]Snapshot{}, nil
	}
	return index, nil
}

func (s *Store) writeSnapshotIndex(index map[string]Snapshot) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(s.dir, ".snapshots-*.json")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(path, s.snapshotsPath())
}

// pruneSnapshots keeps the newest MaxSnapshots within MaxAge and removes the
// bytes of everything else. The log line for a pruned delete stays; the overlay
// marks it no longer recoverable.
func (s *Store) pruneSnapshots() {
	index, err := s.snapshotIndex()
	if err != nil || len(index) == 0 {
		return
	}
	ordered := make([]Snapshot, 0, len(index))
	for _, snap := range index {
		ordered = append(ordered, snap)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].At.After(ordered[j].At) })
	cutoff := time.Now().Add(-s.limits.MaxAge)
	keep := map[string]Snapshot{}
	for i, snap := range ordered {
		if i >= s.limits.MaxSnapshots || snap.At.Before(cutoff) {
			_ = os.Remove(filepath.Join(s.blobsDir(), snap.File))
			continue
		}
		keep[snap.EntryID] = snap
	}
	if len(keep) != len(index) {
		_ = s.writeSnapshotIndex(keep)
	}
}

// pruneLog trims the log to its cap by rewriting it without the oldest lines.
func (s *Store) pruneLog() {
	entries, err := s.Entries()
	if err != nil || len(entries) <= s.limits.MaxEntries {
		return
	}
	kept := entries[len(entries)-s.limits.MaxEntries:]
	tmp, err := os.CreateTemp(s.dir, ".journal-*.jsonl")
	if err != nil {
		return
	}
	path := tmp.Name()
	defer func() { _ = os.Remove(path) }()
	for _, e := range kept {
		line, err := json.Marshal(e)
		if err != nil {
			_ = tmp.Close()
			return
		}
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			_ = tmp.Close()
			return
		}
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(path, s.journalPath())
}
