package provider

import (
	"context"
	"os"
	"time"

	"github.com/nxxxsooo/another/internal/model"
)

type PathSpec struct {
	Label string
	Path  string
	Env   string
}

type SessionRef struct {
	ID          string
	Provider    string
	StoragePath string
	ProjectPath string
}

type DiscoverOpts struct {
	ProjectFilter string
	Limit         int
	// SkipUnchanged, when set, lets file-walking providers skip summarizing a
	// storage path whose Unix-seconds mtime already matches the index.
	SkipUnchanged func(storagePath string, mtime int64) bool
	// SkipSource is the precise variant used by the index. mtime is UnixNano;
	// size catches filesystems whose timestamp resolution is coarse.
	SkipSource func(storagePath string, mtime, size int64) bool
}

// SQLiteSourceStamp includes uncheckpointed WAL data in a database fingerprint.
func SQLiteSourceStamp(path string, main os.FileInfo) (time.Time, int64, error) {
	mtime, size := main.ModTime(), main.Size()
	wal, err := os.Stat(path + "-wal")
	if os.IsNotExist(err) {
		return mtime, size, nil
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	if wal.ModTime().After(mtime) {
		mtime = wal.ModTime()
	}
	return mtime, size + wal.Size(), nil
}

type WriteOpts struct {
	ProjectPath string
	DryRun      bool
}

type WriteResult struct {
	SessionID     string
	StoragePath   string
	ProjectPath   string
	AlreadyExists bool
}

type Provider interface {
	ID() string
	DisplayName() string
	DefaultPaths() []PathSpec
	Installed() bool
	Discover(ctx context.Context, opts DiscoverOpts) ([]model.Summary, error)
	Load(ctx context.Context, ref SessionRef) (*model.Conversation, error)
	Write(ctx context.Context, conv *model.Conversation, opts WriteOpts) (*WriteResult, error)
	SupportsResume() bool
	ResumeCommand(result WriteResult) string
}

// PreviewLoader lets providers with large native stores load a bounded recent
// preview without weakening full loads used by migration and verification.
type PreviewLoader interface {
	LoadPreview(context.Context, SessionRef, int) (*model.Conversation, error)
}

// ResumeEnsurer is implemented by targets that must register sessions in a local index (e.g. Codex threads DB).
type ResumeEnsurer interface {
	Provider
	EnsureResumable(conv *model.Conversation, ref WriteResult) error
}

// WriteCleaner removes only the exact artifact returned by Write. Engines use
// it when post-write verification fails; providers must not perform broad scans.
type WriteCleaner interface {
	CleanupWrite(context.Context, WriteResult) error
}

// SessionDeleter is an explicit user-facing destructive capability. It is
// separate from WriteCleaner: rollback owns only an artifact just written by a
// failed migration, while deleting an existing session requires confirmation
// and may need to clean provider indexes as well as files.
type SessionDeleter interface {
	DeleteSession(context.Context, SessionRef) error
}
