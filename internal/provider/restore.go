package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// SessionRestore puts back exactly the session a reversible delete removed:
// same session ID, same storage path, same bytes. It is valid only for the
// lifetime of the process that produced it, because another holds the removed
// session in memory and never writes a private trash store to disk. Once the
// list is closed, a delete is a delete.
type SessionRestore func(context.Context) error

// ReversibleSessionDeleter is implemented by providers that own the session's
// bytes outright, so a removal can be undone into the agent's real state rather
// than into another's own bookkeeping.
//
// A provider whose sessions live behind a server must not implement this. There
// the delete is the server's, and replaying the conversation back through an
// API would produce a new session with a new ID — a copy, not an undo — which
// is exactly the kind of emulated lifecycle state another refuses to invent.
type ReversibleSessionDeleter interface {
	SessionDeleter
	// DeleteSessionReversibly deletes the session and returns the restore that
	// undoes it. An error means nothing was deleted.
	DeleteSessionReversibly(context.Context, SessionRef) (SessionRestore, error)
}

// snapshotFile captures a single-file session so its deletion can be undone.
// The bytes, permissions, and modification time all travel: a restored session
// that came back with today's timestamp would jump to the top of a list sorted
// by recency and misreport when the work actually happened.
func snapshotFile(path string) (SessionRestore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	mode, modTime := info.Mode().Perm(), info.ModTime()
	return func(context.Context) error {
		// Refuse to overwrite. If something is at the path again, the agent has
		// moved on and the old bytes are no longer the truth there.
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("restore: %s already exists", path)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			return err
		}
		return os.Chtimes(path, modTime, modTime)
	}, nil
}

// DeleteFileSessionReversibly is the shared implementation for providers whose
// session is exactly one file: capture it, then delete through the provider's
// own guarded delete so the root checks and index cleanup still apply.
func DeleteFileSessionReversibly(ctx context.Context, d SessionDeleter, ref SessionRef, storagePath string) (SessionRestore, error) {
	if storagePath == "" {
		return nil, fmt.Errorf("reversible delete: missing storage path")
	}
	restore, err := snapshotFile(storagePath)
	if err != nil {
		return nil, err
	}
	if err := d.DeleteSession(ctx, ref); err != nil {
		return nil, err
	}
	return restore, nil
}
