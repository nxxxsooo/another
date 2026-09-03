package registry

import (
	"fmt"
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

func New() *Registry {
	providers := []provider.Provider{
		claude.New(),
		codex.New(),
		cursor.New(),
		opencode.New(),
		opencode2.New(),
		commandcode.New(),
		hermes.New(),
		pi.New(),
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
