package registry_test

import (
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

func TestNormalizeID(t *testing.T) {
	cases := map[string]string{
		"claude": "claude-code", "Claude-Code": "claude-code",
		"cursor-agent": "cursor", "open-code": "opencode", "o2": "opencode2", "open-code-2": "opencode2",
		"agy": "agy", "antigravity": "agy", "antigravity-cli": "agy", "antigravity_cli": "agy",
		"qwen": "qwen", "qwen-code": "qwen", "qwencode": "qwen",
	}
	for in, want := range cases {
		if got := registry.NormalizeID(in); got != want {
			t.Fatalf("%q => %q want %q", in, got, want)
		}
	}
}

func TestNewEnabledKeepsOnlyConfiguredProviders(t *testing.T) {
	reg := registry.NewEnabled([]string{"pi", "o2", "unknown"})
	ids := reg.IDs()
	if len(ids) != 2 || ids[0] != "opencode2" || ids[1] != "pi" {
		t.Fatalf("enabled ids = %v", ids)
	}
	if _, err := reg.Get("codex"); err == nil {
		t.Fatal("disabled provider remains addressable")
	}
}

func TestNewEnabledPreservesConfiguredOrder(t *testing.T) {
	reg := registry.NewEnabled([]string{"pi", "o2", "codex", "opencode2", "unknown"})
	providers := reg.All()
	got := make([]string, len(providers))
	for i, p := range providers {
		got[i] = p.ID()
	}
	want := []string{"pi", "opencode2", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured provider order = %v, want %v", got, want)
	}
}

func TestNewOrderedPutsSavedProvidersFirstAndKeepsTheRest(t *testing.T) {
	providers := registry.NewOrdered([]string{"pi", "o2", "pi"}).All()
	got := make([]string, len(providers))
	seen := map[string]bool{}
	for i, p := range providers {
		got[i] = p.ID()
		if seen[p.ID()] {
			t.Fatalf("provider %q appears twice: %v", p.ID(), got)
		}
		seen[p.ID()] = true
	}
	if len(got) < 10 || !reflect.DeepEqual(got[:2], []string{"pi", "opencode2"}) {
		t.Fatalf("ordered providers = %v", got)
	}
}

func TestRegistryProviders(t *testing.T) {
	reg := registry.New()
	if len(reg.All()) < 10 {
		t.Fatalf("expected at least 10 providers, got %d", len(reg.All()))
	}
	if _, err := reg.Get("codex"); err != nil {
		t.Fatal(err)
	}
	if p, err := reg.Get("antigravity"); err != nil || p.ID() != "agy" {
		t.Fatalf("agy provider missing or misrouted: provider=%v err=%v", p, err)
	}
	if got := registry.CLICommand("agy"); got != "agy" {
		t.Fatalf("agy CLI command = %q", got)
	}
	if got := registry.CLICommand("qwen-code"); got != "qwen" {
		t.Fatalf("qwen CLI command = %q", got)
	}
	if ids := registry.NewEnabled([]string{"antigravity-cli"}).IDs(); len(ids) != 1 || ids[0] != "agy" {
		t.Fatalf("enabled agy ids = %v", ids)
	}
}

// Relocation is deliberately unequal. Only providers that own a native,
// verifiable contract may claim it; everywhere else another reports the action
// as unsupported rather than re-rendering the conversation into a new
// directory and calling that a move.
func TestOnlyNativeProvidersClaimRelocate(t *testing.T) {
	want := map[string]bool{"pi": true, "opencode2": true}
	for _, p := range registry.New().All() {
		relocator, ok := p.(provider.SessionRelocator)
		if ok != want[p.ID()] {
			t.Errorf("%s relocate support = %v, want %v", p.ID(), ok, want[p.ID()])
		}
		if !ok {
			continue
		}
		for _, mode := range []provider.RelocateMode{provider.RelocateFork, provider.RelocateMove} {
			if !relocator.SupportsRelocate(mode) {
				t.Errorf("%s claims relocate but refuses %s", p.ID(), mode)
			}
		}
	}
}
