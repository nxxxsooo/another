package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/integrations"
)

// pluginFixture is the title page with OpenCode 2 managed and an adapter
// available to install into it.
func pluginFixture(state integrations.State) setupModel {
	m := setupFixture()
	m.page = setupPageTitle
	m.selected["opencode2"] = true
	m.titleOpts = []titleOption{{name: "Off"}, {id: "pi", name: "pi"}}
	m.plugin = SetupPlugin{Supported: true, Dir: "/tmp/opencode2/plugins/another-title-policy", State: state}
	return m
}

// Writing into another agent's configuration directory is not something
// detection alone may authorise, so the row starts off and stays off until
// someone turns it on.
func TestPluginRowStartsOffAndTogglesOn(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	if m.wantsPlugin() {
		t.Fatal("a plugin nobody asked for was going to be installed")
	}
	next, _ := m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = next.(setupModel)
	if !m.wantsPlugin() {
		t.Fatal("t did not turn the plugin on")
	}
	next, _ = m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if next.(setupModel).wantsPlugin() {
		t.Fatal("t did not turn the plugin back off")
	}
}

// The row belongs to the agent it writes into: an OpenCode 2 this
// configuration does not manage is not asked about.
func TestPluginRowFollowsTheAgentSelection(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	m.selected["opencode2"] = false
	if m.pluginVisible() {
		t.Fatal("setup offered a plugin for an agent it does not manage")
	}
	m.pluginWanted = true
	if m.wantsPlugin() {
		t.Fatal("an unmanaged agent still had a plugin installed")
	}
}

// Finding the plugin means asking OpenCode 2 where its configuration lives,
// which can start a stopped service. The page draws first and fills the row in
// when the answer arrives.
func TestPluginRowWaitsForTheLookup(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	m.plugin = SetupPlugin{Supported: true}
	if m.plugin.Known() || m.pluginToggleable() {
		t.Fatal("the row was a choice before another knew what was installed")
	}
	if row := ansi.Strip(m.pluginRow(72)); !strings.Contains(row, txt.setupPluginChecking) {
		t.Fatalf("row = %q, want it to say the lookup is running", row)
	}
	next, _ := m.Update(pluginStatusMsg{plugin: SetupPlugin{Supported: true, Dir: "/tmp/o2", State: integrations.StateCurrent}})
	m = next.(setupModel)
	if !m.pluginToggleable() || !m.wantsPlugin() {
		t.Fatal("an installed plugin did not open the row when the lookup landed")
	}
}

// Saving before the lookup lands must not withdraw a choice made earlier: the
// page could not offer the row yet, so it has no answer of its own to save.
func TestSlowLookupKeepsTheSavedChoice(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	m.plugin = SetupPlugin{Supported: true}
	m.pluginConsented = true
	if !m.wantsPlugin() {
		t.Fatal("saving during the lookup dropped the plugin another already maintains")
	}
	// The same holds while something another did not write is in the way: it
	// cannot act, and forgetting the intent would silently orphan the plugin.
	m.plugin = SetupPlugin{Supported: true, Dir: "/tmp/o2", State: integrations.StateForeign}
	if !m.wantsPlugin() {
		t.Fatal("a blocked directory withdrew a choice the person had made")
	}
}

// Files another did not write are reported, never claimed from this page.
func TestPluginRowWillNotOverwriteForeignFiles(t *testing.T) {
	for _, state := range []integrations.State{integrations.StateForeign, integrations.StateModified} {
		m := pluginFixture(state)
		m.pluginWanted = true
		if m.pluginToggleable() || m.wantsPlugin() {
			t.Fatalf("state %q was treated as another's to overwrite", state)
		}
		if !strings.Contains(ansi.Strip(m.pluginRow(72)), "/tmp/opencode2") {
			t.Fatalf("state %q hid where the files are", state)
		}
	}
}

// The page must still say where another would write, because that is the only
// place a person sees it before it happens.
func TestPluginRowNamesTheDirectory(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	row := ansi.Strip(m.pluginRow(72))
	if !strings.Contains(row, "/tmp/opencode2/plugins/another-title-policy") {
		t.Fatalf("row = %q, want the install path", row)
	}
	if !strings.Contains(row, txt.setupPluginLabel) {
		t.Fatalf("row = %q, want the label", row)
	}
}

// The row costs two lines the title page did not previously spend, and the
// panel is centred: one line too many and every later frame lands lower than
// the one before it.
func TestTitlePageWithPluginRowStaysInsideTheTerminal(t *testing.T) {
	for _, state := range []integrations.State{integrations.StateMissing, integrations.StateCurrent, integrations.StateForeign} {
		for h := 20; h <= 40; h++ {
			for _, w := range []int{48, 60, 80, 120, 200} {
				m := pluginFixture(state)
				m.width, m.height = w, h
				if got := lipgloss.Height(m.View()); got > h {
					t.Fatalf("state %q at %dx%d: title page renders %d lines", state, w, h, got)
				}
				// The same page with no agent able to suggest a title still
				// carries the row, because OpenCode 2 names sessions itself.
				m.titleOpts = m.titleOpts[:1]
				if got := lipgloss.Height(m.View()); got > h {
					t.Fatalf("state %q at %dx%d: title page without agents renders %d lines", state, w, h, got)
				}
			}
		}
	}
}
