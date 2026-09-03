package registry

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/claude"
	"github.com/nxxxsooo/another/internal/providers/codex"
	"github.com/nxxxsooo/another/internal/providers/commandcode"
	"github.com/nxxxsooo/another/internal/providers/cursor"
	"github.com/nxxxsooo/another/internal/providers/hermes"
	"github.com/nxxxsooo/another/internal/providers/opencode"
	"github.com/nxxxsooo/another/internal/providers/opencode2"
	"github.com/nxxxsooo/another/internal/providers/pi"
)

type Registry struct {
	byID map[string]provider.Provider
	all  []provider.Provider
}

func New() *Registry { return newRegistry(nil) }

func NewEnabled(enabled []string) *Registry {
	allowed := make(map[string]bool, len(enabled))
	for _, id := range enabled {
		allowed[NormalizeID(id)] = true
	}
	return newRegistry(allowed)
}

func newRegistry(allowed map[string]bool) *Registry {
	available := []provider.Provider{
		claude.New(),
		codex.New(),
		cursor.New(),
		opencode.New(),
		opencode2.New(),
		commandcode.New(),
		hermes.New(),
		pi.New(),
	}
	providers := make([]provider.Provider, 0, len(available))
	for _, p := range available {
		if allowed == nil || allowed[p.ID()] {
			providers = append(providers, p)
		}
	}
	byID := make(map[string]provider.Provider, len(providers))
	for _, p := range providers {
		byID[p.ID()] = p
	}
	return &Registry{byID: byID, all: providers}
}

func (r *Registry) All() []provider.Provider {
	out := make([]provider.Provider, len(r.all))
	copy(out, r.all)
	return out
}

func (r *Registry) Get(id string) (provider.Provider, error) {
	id = NormalizeID(id)
	p, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", id)
	}
	return p, nil
}

func (r *Registry) Installed() []provider.Provider {
	var out []provider.Provider
	for _, p := range r.all {
		if p.Installed() {
			out = append(out, p)
		}
	}
	return out
}

func (r *Registry) IDs() []string {
	ids := make([]string, 0, len(r.all))
	for _, p := range r.all {
		ids = append(ids, p.ID())
	}
	sort.Strings(ids)
	return ids
}

// DisplayName returns the human-readable provider name for an ID.
func CLICommand(id string) string {
	commands := map[string]string{
		"claude-code": "claude",
		"codex":       "codex",
		"cursor":      "cursor-agent",
		"opencode":    "opencode",
		"opencode2":   "opencode2",
		"commandcode": "commandcode",
		"hermes":      "hermes",
		"pi":          "pi",
	}
	return commands[NormalizeID(id)]
}

func CLIAvailable(id string) bool {
	command := CLICommand(id)
	if command == "" {
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}

func DisplayName(reg *Registry, id string) string {
	id = NormalizeID(id)
	if p, err := reg.Get(id); err == nil {
		return p.DisplayName()
	}
	return id
}

func NormalizeID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	replacements := map[string]string{
		"claude": "claude-code", "claude_code": "claude-code", "claude-code": "claude-code",
		"codex":  "codex",
		"cursor": "cursor", "cursor-agent": "cursor",
		"opencode": "opencode", "open-code": "opencode",
		"opencode2": "opencode2", "open-code-2": "opencode2", "o2": "opencode2",
		"commandcode": "commandcode", "command-code": "commandcode",
		"hermes": "hermes", "hermes-agent": "hermes",
		"pi": "pi", "pi-coding-agent": "pi",
	}
	if v, ok := replacements[id]; ok {
		return v
	}
	return id
}
