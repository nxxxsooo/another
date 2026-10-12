package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionSnapshot is a session's bytes plus the metadata a faithful restore
// needs. Data is opaque to whoever stores it — the journal keeps it as a blob
// and only the provider that captured it interprets it, so the format can
// follow the provider's own storage.
type SessionSnapshot struct {
	Data        []byte      `json:"data"`
	StoragePath string      `json:"storage_path"`
	Mode        os.FileMode `json:"mode"`
	ModTime     time.Time   `json:"mod_time"`
}

// Encode serializes a snapshot for durable storage.
func (s *SessionSnapshot) Encode() ([]byte, error) {
	return json.Marshal(s)
}

// DecodeSnapshot reads a snapshot the provider itself encoded.
func DecodeSnapshot(data []byte) (*SessionSnapshot, error) {
	var s SessionSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return &s, nil
}

// SessionSnapshotter captures a session durably enough to restore it after
// this process has exited. It is the persistent counterpart of SessionRestore,
// which lives only as long as the process that produced it. A provider
// implements it only where it owns the session's bytes outright; a
// server-backed provider must not, for the same reason it must not implement
// ReversibleSessionDeleter.
type SessionSnapshotter interface {
	SnapshotSession(context.Context, SessionRef) (*SessionSnapshot, error)
	// RestoreSession writes the snapshot back into the provider's own state.
	// It must refuse rather than overwrite when something is already at the
	// destination.
	RestoreSession(context.Context, SessionRef, *SessionSnapshot) error
}

// SnapshotFileSession captures a single-file session: the bytes, permissions,
// and modification time all travel. A restored session that came back with
// today's timestamp would jump to the top of a recency-sorted list.
func SnapshotFileSession(storagePath string) (*SessionSnapshot, error) {
	if storagePath == "" {
		return nil, fmt.Errorf("snapshot: missing storage path")
	}
	data, err := os.ReadFile(storagePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(storagePath)
	if err != nil {
		return nil, err
	}
	return &SessionSnapshot{
		Data:        data,
		StoragePath: storagePath,
		Mode:        info.Mode().Perm(),
		ModTime:     info.ModTime(),
	}, nil
}

// RestoreFileSession writes a single-file snapshot back where it came from. It
// refuses to overwrite: if something is at the path again, the agent has moved
// on and the old bytes are no longer the truth there.
func RestoreFileSession(snap *SessionSnapshot) error {
	if snap == nil || snap.StoragePath == "" {
		return fmt.Errorf("restore: empty snapshot")
	}
	path := snap.StoragePath
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("restore: %s already exists", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, snap.Data, snap.Mode); err != nil {
		return err
	}
	return os.Chtimes(path, snap.ModTime, snap.ModTime)
}
