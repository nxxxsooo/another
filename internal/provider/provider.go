package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

// ErrPartial marks a change that landed in the agent's own state but could not
// reach every surface that agent reads. The change is real and must not be
// reported as a failure; the caveat must not be swallowed either, because the
// person is looking at the surface that still shows the old value.
var ErrPartial = errors.New("applied with a caveat")

// SessionRenamer updates the title in the provider's native source of truth.
// another never stores private display aliases that disappear on refresh.
type SessionRenamer interface {
	RenameSession(context.Context, SessionRef, string) error
}

// SessionArchiver toggles a provider's native reversible archive state. It is
// intentionally separate from deletion: archived sessions remain recoverable.
type SessionArchiver interface {
	ArchiveSession(context.Context, SessionRef, bool) error
}

// RelocateMode selects whether the source session survives a relocate.
type RelocateMode string

const (
	// RelocateFork copies the session into the target directory and leaves the
	// source exactly where it was.
	RelocateFork RelocateMode = "fork"
	// RelocateMove carries the session itself into the target directory. The
	// old directory no longer has it.
	RelocateMove RelocateMode = "move"
)

func ParseRelocateMode(value string) (RelocateMode, error) {
	mode := RelocateMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		mode = RelocateFork
	}
	switch mode {
	case RelocateFork, RelocateMove:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid relocate mode %q (use fork or move)", value)
	}
}

type RelocateOpts struct {
	// Directory is an existing absolute path the session should belong to.
	Directory string
	Mode      RelocateMode
	DryRun    bool
}

type RelocateResult struct {
	SessionID   string
	StoragePath string
	ProjectPath string
	// Moved reports whether the source session was carried rather than copied.
	Moved bool
}

// SessionRelocator changes the project directory a session belongs to, in the
// provider's own state. Fork leaves the source untouched; move does not. A
// provider implements this only where it owns a native, verifiable contract —
// re-rendering the conversation through the portable model is migration, not
// relocation, and would silently drop tool calls and reasoning.
type SessionRelocator interface {
	RelocateSession(context.Context, SessionRef, RelocateOpts) (*RelocateResult, error)
	// SupportsRelocate reports per-mode support, because a provider may own a
	// native copy without owning a native move.
	SupportsRelocate(RelocateMode) bool
}

// ErrRelocateUnsupported is returned when a provider cannot relocate at all, or
// cannot perform the requested mode.
var ErrRelocateUnsupported = errors.New("relocate is not supported")
