package registry_test

import (
	"testing"

	"github.com/nxxxsooo/another/internal/registry"
)

func TestNormalizeID(t *testing.T) {
	cases := map[string]string{
		"claude": "claude-code", "Claude-Code": "claude-code",
		"cursor-agent": "cursor", "open-code": "opencode", "o2": "opencode2", "open-code-2": "opencode2",
	}
	for in, want := range cases {
		if got := registry.NormalizeID(in); got != want {
			t.Fatalf("%q => %q want %q", in, got, want)
		}
	}
}

func TestRegistryProviders(t *testing.T) {
	reg := registry.New()
	if len(reg.All()) < 8 {
		t.Fatalf("expected at least 8 providers, got %d", len(reg.All()))
	}
	if _, err := reg.Get("codex"); err != nil {
		t.Fatal(err)
	}
}
