package commandcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CyrusSE/agenthop/internal/config"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
)

const ProviderID = "commandcode"

// CommandCode uses a Claude Code-like JSONL layout under ~/.commandcode/projects.
type Provider struct {
	root string
}

func New() *Provider {
	root := config.EnvOrDefault("COMMANDCODE_HOME", filepath.Join(config.HomeDir(), ".commandcode"))
	return &Provider{root: root}
}

func (p *Provider) projectsRoot() string {
	return filepath.Join(p.root, "projects")
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "CommandCode" }
func (p *Provider) Installed() bool {
	st, err := os.Stat(p.projectsRoot())
	return err == nil && st.IsDir()
}
func (p *Provider) SupportsResume() bool { return true }

func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "projects", Path: p.projectsRoot(), Env: "COMMANDCODE_HOME"}}
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	return discoverWithRoot(ctx, p.projectsRoot(), ProviderID, opts)
}

func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	conv, err := loadWithRoot(ref, p.projectsRoot())
	if err != nil {
		return nil, err
	}
	conv.Provider = ProviderID
	return conv, nil
}

func (p *Provider) Write(ctx context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	return writeWithRoot(ctx, conv, opts, p.projectsRoot())
}

func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	if r.ProjectPath != "" {
		return "cd " + util.ShellQuote(r.ProjectPath) + " && commandcode --resume " + util.ShellQuote(r.SessionID)
	}
	return "commandcode --resume " + util.ShellQuote(r.SessionID)
}

func (p *Provider) CleanupWrite(_ context.Context, r provider.WriteResult) error {
	path := r.StoragePath
	if path == "" {
		path = filepath.Join(p.projectsRoot(), util.EncodeClaudeProjectPath(r.ProjectPath), r.SessionID+".jsonl")
	}
	if !commandCodeCleanupPath(p.projectsRoot(), path) {
		return fmt.Errorf("commandcode: refusing cleanup outside projects root: %s", path)
	}
	var first error
	for _, target := range []string{path, strings.TrimSuffix(path, ".jsonl") + ".meta.json"} {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
	}
	return first
}

func commandCodeCleanupPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && strings.HasSuffix(rel, ".jsonl")
}
