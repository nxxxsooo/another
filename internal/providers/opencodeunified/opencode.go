// Package opencodeunified presents released OpenCode V2 and retained V1
// history as one provider without rewriting either agent-owned database.
package opencodeunified

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	legacy "github.com/nxxxsooo/another/internal/providers/opencode"
	v2 "github.com/nxxxsooo/another/internal/providers/opencode2"
)

const ProviderID = "opencode"

type backend struct {
	p       provider.Provider
	path    string
	v2      bool
	primary bool
	command string
}

type Provider struct{ backends []backend }

func New() *Provider {
	root := config.EnvOrDefault("XDG_DATA_HOME", filepath.Join(config.HomeDir(), ".local", "share"))
	standard := config.EnvOrDefault("OPENCODE_DB_PATH", filepath.Join(root, "opencode", "opencode.db"))
	oldV2 := config.EnvOrDefault("OPENCODE2_DB_PATH", filepath.Join(root, "opencode", "opencode2.db"))
	opencode := config.EnvOrDefault("OPENCODE_COMMAND", "opencode")
	opencode2 := config.EnvOrDefault("OPENCODE2_COMMAND", "opencode2")
	return &Provider{backends: []backend{
		{p: v2.NewAt(standard, opencode), path: standard, v2: true, primary: true, command: opencode},
		{p: legacy.NewAt(standard, opencode), path: standard, command: opencode},
		{p: v2.NewAt(oldV2, opencode2), path: oldV2, v2: true, command: opencode2},
	}}
}

func (p *Provider) ID() string           { return ProviderID }
func (p *Provider) DisplayName() string  { return "OpenCode" }
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	seen := map[string]bool{}
	var out []provider.PathSpec
	for _, b := range p.backends {
		if b.path == "" || seen[b.path] {
			continue
		}
		seen[b.path] = true
		label := "compat database"
		if b.primary {
			label = "primary database"
		}
		out = append(out, provider.PathSpec{Label: label, Path: b.path})
	}
	return out
}

func (p *Provider) Installed() bool {
	for _, b := range p.backends {
		if b.p.Installed() {
			return true
		}
	}
	return false
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	var out []model.Summary
	seen := map[string]bool{}
	var failures []string
	for _, b := range p.backends {
		if !b.p.Installed() {
			continue
		}
		rows, err := b.p.Discover(ctx, opts)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		for _, sm := range rows {
			// Prefer the released standard store if an old isolated setup holds
			// the same native session ID.
			if seen[sm.ID] {
				continue
			}
			seen[sm.ID] = true
			sm.Provider = ProviderID
			marker := "v1:"
			if b.v2 {
				marker = "v2:"
			}
			sm.StoragePath = b.path + "#" + marker + sm.ID
			out = append(out, sm)
			if opts.Limit > 0 && len(out) >= opts.Limit {
				return out, nil
			}
		}
	}
	if len(out) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("OpenCode: %s", strings.Join(failures, "; "))
	}
	return out, nil
}

func storageFile(path string) string {
	file, _, _ := strings.Cut(path, "#")
	return filepath.Clean(file)
}

func storageGeneration(path string) string {
	_, tail, ok := strings.Cut(path, "#")
	if !ok {
		return ""
	}
	if strings.HasPrefix(tail, "v1:") {
		return "v1"
	}
	if strings.HasPrefix(tail, "v2:") {
		return "v2"
	}
	return ""
}

func (p *Provider) backendFor(ref provider.SessionRef) (backend, error) {
	want := storageFile(ref.StoragePath)
	if ref.StoragePath != "" {
		generation := storageGeneration(ref.StoragePath)
		for _, b := range p.backends {
			if filepath.Clean(b.path) != want || !b.p.Installed() || generation == "v1" && b.v2 || generation == "v2" && !b.v2 {
				continue
			}
			if generation != "" {
				return b, nil
			}
			if _, err := b.p.Load(context.Background(), ref); err == nil {
				return b, nil
			}
		}
	}
	for _, b := range p.backends {
		if !b.p.Installed() {
			continue
		}
		if _, err := b.p.Load(context.Background(), ref); err == nil {
			return b, nil
		}
	}
	return backend{}, provider.ErrNotFound
}

func (p *Provider) writeBackend() (backend, error) {
	for _, b := range p.backends {
		ready, ok := b.p.(interface{ ReadyToWrite() bool })
		if b.v2 && b.primary && b.p.Installed() && commandAvailable(b.command) && ok && ready.ReadyToWrite() {
			return b, nil
		}
	}
	for _, b := range p.backends {
		ready, ok := b.p.(interface{ ReadyToWrite() bool })
		if b.v2 && b.p.Installed() && commandAvailable(b.command) && ok && ready.ReadyToWrite() {
			return b, nil
		}
	}
	return backend{}, fmt.Errorf("OpenCode V2 has no model default yet; run opencode once first")
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	b, err := p.backendFor(ref)
	if err != nil {
		return nil, err
	}
	conv, err := b.p.Load(ctx, ref)
	if conv != nil {
		conv.Provider = ProviderID
	}
	return conv, err
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	b, err := p.writeBackend()
	if err != nil {
		return nil, err
	}
	return b.p.Write(ctx, conv, opts)
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	b, err := p.backendFor(provider.SessionRef{ID: r.SessionID, StoragePath: r.StoragePath, ProjectPath: r.ProjectPath})
	if err == nil {
		return b.p.ResumeCommand(r)
	}
	if b, err = p.writeBackend(); err == nil {
		return b.p.ResumeCommand(r)
	}
	return ""
}

func (p *Provider) ResumeUnavailableReason(r provider.WriteResult) string {
	b, err := p.backendFor(provider.SessionRef{ID: r.SessionID, StoragePath: r.StoragePath, ProjectPath: r.ProjectPath})
	if err != nil {
		return "OpenCode session is no longer present in a readable store"
	}
	if !commandAvailable(b.command) {
		return fmt.Sprintf("this OpenCode session belongs to %s, but that command is not installed", b.command)
	}
	return ""
}

func commandAvailable(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}

func (p *Provider) Capabilities(ref provider.SessionRef) provider.SessionCapabilities {
	b, err := p.backendFor(ref)
	if err != nil {
		return provider.SessionCapabilities{}
	}
	_, rename := b.p.(provider.SessionRenamer)
	_, archive := b.p.(provider.SessionArchiver)
	_, deleteOK := b.p.(provider.SessionDeleter)
	_, reversible := b.p.(provider.ReversibleSessionDeleter)
	c := provider.SessionCapabilities{Rename: rename, Archive: archive, Delete: deleteOK, ReversibleDelete: reversible}
	// V2 lifecycle operations are server calls through the matching CLI. V1
	// rename/archive are direct native-row updates, while deletion still needs
	// its CLI.
	if !commandAvailable(b.command) {
		if b.v2 {
			return provider.SessionCapabilities{}
		}
		c.Delete = false
	}
	if r, ok := b.p.(provider.SessionRelocator); ok {
		c.RelocateFork = r.SupportsRelocate(provider.RelocateFork)
		c.RelocateMove = r.SupportsRelocate(provider.RelocateMove)
	}
	return c
}

func (p *Provider) RenameSession(ctx context.Context, ref provider.SessionRef, title string) error {
	b, err := p.backendFor(ref)
	if err != nil {
		return err
	}
	r, ok := b.p.(provider.SessionRenamer)
	if !ok {
		return fmt.Errorf("OpenCode session does not support rename")
	}
	if b.v2 && !commandAvailable(b.command) {
		return fmt.Errorf("this OpenCode V2 session needs %s, but that command is not installed", b.command)
	}
	return r.RenameSession(ctx, ref, title)
}

func (p *Provider) ArchiveSession(ctx context.Context, ref provider.SessionRef, archived bool) error {
	b, err := p.backendFor(ref)
	if err != nil {
		return err
	}
	a, ok := b.p.(provider.SessionArchiver)
	if !ok {
		return fmt.Errorf("OpenCode session does not support archive")
	}
	return a.ArchiveSession(ctx, ref, archived)
}

func (p *Provider) DeleteSession(ctx context.Context, ref provider.SessionRef) error {
	b, err := p.backendFor(ref)
	if err != nil {
		return err
	}
	d, ok := b.p.(provider.SessionDeleter)
	if !ok {
		return fmt.Errorf("OpenCode session does not support deletion")
	}
	if !commandAvailable(b.command) {
		return fmt.Errorf("this OpenCode session needs %s for deletion, but that command is not installed", b.command)
	}
	return d.DeleteSession(ctx, ref)
}

func (p *Provider) CleanupWrite(ctx context.Context, r provider.WriteResult) error {
	b, err := p.backendFor(provider.SessionRef{ID: r.SessionID, StoragePath: r.StoragePath})
	if err != nil {
		return err
	}
	c, ok := b.p.(provider.WriteCleaner)
	if !ok {
		return fmt.Errorf("OpenCode write cannot be cleaned up")
	}
	return c.CleanupWrite(ctx, r)
}

func (p *Provider) SupportsRelocate(mode provider.RelocateMode) bool {
	return mode == provider.RelocateFork || mode == provider.RelocateMove
}

func (p *Provider) RelocateSession(ctx context.Context, ref provider.SessionRef, opts provider.RelocateOpts) (*provider.RelocateResult, error) {
	b, err := p.backendFor(ref)
	if err != nil {
		return nil, err
	}
	r, ok := b.p.(provider.SessionRelocator)
	if !ok || !r.SupportsRelocate(opts.Mode) {
		return nil, provider.ErrRelocateUnsupported
	}
	if !commandAvailable(b.command) {
		return nil, fmt.Errorf("this OpenCode V2 session needs %s for relocation, but that command is not installed", b.command)
	}
	return r.RelocateSession(ctx, ref, opts)
}

var _ provider.Provider = (*Provider)(nil)
var _ provider.SessionCapabilityProvider = (*Provider)(nil)
