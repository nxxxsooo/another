package provider

import (
	"context"
	"os"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
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

type StubProvider struct {
	id          string
	displayName string
	paths       []PathSpec
	docURL      string
}

func NewStub(id, displayName, docURL string, paths ...PathSpec) *StubProvider {
	return &StubProvider{id: id, displayName: displayName, paths: paths, docURL: docURL}
}

func (s *StubProvider) ID() string               { return s.id }
func (s *StubProvider) DisplayName() string      { return s.displayName }
func (s *StubProvider) DefaultPaths() []PathSpec { return s.paths }
func (s *StubProvider) Installed() bool          { return false }

func (s *StubProvider) Discover(context.Context, DiscoverOpts) ([]model.Summary, error) {
	return nil, nil
}

func (s *StubProvider) Load(context.Context, SessionRef) (*model.Conversation, error) {
	return nil, ErrNotInstalled
}

func (s *StubProvider) Write(context.Context, *model.Conversation, WriteOpts) (*WriteResult, error) {
	return nil, ErrNotInstalled
}

func (s *StubProvider) SupportsResume() bool             { return false }
func (s *StubProvider) ResumeCommand(WriteResult) string { return "" }

func (s *StubProvider) DocURL() string { return s.docURL }
