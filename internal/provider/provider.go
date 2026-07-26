package provider

import (
	"context"

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
	// storage path whose mtime already matches the index (incremental scans).
	SkipUnchanged func(storagePath string, mtime int64) bool
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

// ResumeEnsurer is implemented by targets that must register sessions in a local index (e.g. Codex threads DB).
type ResumeEnsurer interface {
	Provider
	EnsureResumable(conv *model.Conversation, ref WriteResult) error
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
