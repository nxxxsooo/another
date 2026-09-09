package integrations

import (
	"context"

	"github.com/nxxxsooo/another/internal/config"
)

// Adapter is one agent's title adapter as everything outside this package sees
// it: setup draws a row per adapter, the command line takes one by name, and
// `providers doctor` reports each beside the agent it belongs to. Those three
// callers used to name OpenCode 2 directly, which is why adding Pi meant
// editing all of them. They iterate this instead.
type Adapter struct {
	// ID is the adapter's own name, which is also the manifest id and the
	// word a person types on the command line.
	ID string
	// Provider is the agent this writes into. It gates the setup row: an
	// agent another does not manage is not asked about.
	Provider string
	// ConfigDir resolves the agent's configuration directory. OpenCode 2's
	// answer comes from a subprocess, so this takes a context and callers
	// treat it as slow.
	ConfigDir func(ctx context.Context) string
	// Status, Install, and Remove are the bundle operations for this
	// adapter, already bound to it.
	Status  func(configDir string) Status
	Install func(status Status, version, language string, force bool) (Status, error)
	Remove  func(status Status, force bool) error
	// Consent points at the stored answer for this adapter, so a caller can
	// read and write it without knowing which field it is.
	Consent func(*config.Integrations) *bool
}

// adapters is the list, in the order rows and reports appear. OpenCode 2 comes
// first because it was first; nothing else depends on the order.
var adapters = []Adapter{
	{
		ID:        OpenCode2,
		Provider:  "opencode2",
		ConfigDir: OpenCode2ConfigDir,
		Status:    OpenCode2Status,
		Install:   InstallOpenCode2,
		Remove:    RemoveOpenCode2,
		Consent:   func(i *config.Integrations) *bool { return &i.OpenCode2TitlePolicy },
	},
	{
		ID:        Pi,
		Provider:  "pi",
		ConfigDir: func(context.Context) string { return PiConfigDir() },
		Status:    PiStatus,
		Install:   InstallPi,
		Remove:    RemovePi,
		Consent:   func(i *config.Integrations) *bool { return &i.PiTitlePolicy },
	},
}

// Adapters lists every adapter another ships.
func Adapters() []Adapter { return adapters }

// Find returns the adapter with this id.
func Find(id string) (Adapter, bool) {
	for _, a := range adapters {
		if a.ID == id {
			return a, true
		}
	}
	return Adapter{}, false
}

// Providers lists the agents that have an adapter, which is also the word a
// person types on the command line: someone installing this is thinking about
// the agent it goes into, not about the manifest id it is recorded under.
func Providers() []string {
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Provider)
	}
	return names
}

// ForProvider returns the adapter that writes into this agent, if there is one.
func ForProvider(provider string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Provider == provider {
			return a, true
		}
	}
	return Adapter{}, false
}
