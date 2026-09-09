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
	m.plugins = []SetupPlugin{{
		Integration: integrations.OpenCode2,
		Provider:    "opencode2",
		Supported:   true,
		Dir:         "/tmp/opencode2/plugins/another-title-policy",
		State:       state,
	}}
	m.pluginWanted = map[string]bool{}
	m.pluginTouched = map[string]bool{}
	m.pluginConsented = map[string]bool{}
	return m
}

// bothPluginsFixture is the same page for someone who manages OpenCode 2 and
// Pi: two rows, each with its own agent, directory, and answer.
func bothPluginsFixture(o2, pi integrations.State) setupModel {
	m := pluginFixture(o2)
	m.plugins = append(m.plugins, SetupPlugin{
		Integration: integrations.Pi,
		Provider:    "pi",
		Supported:   true,
		Dir:         "/tmp/pi/agent/extensions/another-title-policy",
		State:       pi,
	})
	return m
}

// Writing into another agent's configuration directory is not something
// detection alone may authorise, so a row starts off and stays off until
// someone turns it on.
func TestPluginRowStartsOffAndTogglesOn(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	if m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("a plugin nobody asked for was going to be installed")
	}
	next, _ := m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = next.(setupModel)
	if !m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("1 did not turn the plugin on")
	}
	next, _ = m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if next.(setupModel).wantsPlugin(integrations.OpenCode2) {
		t.Fatal("1 did not turn the plugin back off")
	}
}

// Each row is switched by the digit it prints, and the digits follow the rows
// that are actually on the page: one key must never reach the other agent's
// configuration directory.
func TestNumberKeysToggleTheRowTheyName(t *testing.T) {
	m := bothPluginsFixture(integrations.StateMissing, integrations.StateMissing)
	next, _ := m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = next.(setupModel)
	if !m.wantsPlugin(integrations.Pi) {
		t.Fatal("2 did not turn the Pi extension on")
	}
	if m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("2 also turned on the row it does not name")
	}
	next, _ = m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = next.(setupModel)
	if !m.wantsPlugin(integrations.OpenCode2) || !m.wantsPlugin(integrations.Pi) {
		t.Fatal("the two rows do not hold answers of their own")
	}
	// A digit past the last row is not a row, and must not wrap onto one.
	next, _ = m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if got := next.(setupModel); !got.wantsPlugin(integrations.OpenCode2) || !got.wantsPlugin(integrations.Pi) {
		t.Fatal("a digit with no row behind it changed an answer")
	}
}

// The numbering is positional, so dropping the first agent moves the second
// adapter onto key 1 rather than leaving a hole.
func TestNumberKeysFollowTheVisibleRows(t *testing.T) {
	m := bothPluginsFixture(integrations.StateMissing, integrations.StateMissing)
	m.selected["opencode2"] = false
	if rows := m.visiblePlugins(); len(rows) != 1 || rows[0].Integration != integrations.Pi {
		t.Fatalf("visible rows = %+v, want only Pi", rows)
	}
	next, _ := m.updateTitlePage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if !next.(setupModel).wantsPlugin(integrations.Pi) {
		t.Fatal("1 did not reach the only row on the page")
	}
	if row := ansi.Strip(next.(setupModel).pluginRows(72, false)); !strings.Contains(row, txt.setupPluginLabelPi) {
		t.Fatalf("rows = %q, want the Pi row named", row)
	}
}

// A row belongs to the agent it writes into: an OpenCode 2 this configuration
// does not manage is not asked about.
func TestPluginRowFollowsTheAgentSelection(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	m.selected["opencode2"] = false
	if m.pluginVisible(integrations.OpenCode2) {
		t.Fatal("setup offered a plugin for an agent it does not manage")
	}
	m.pluginWanted[integrations.OpenCode2] = true
	if m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("an unmanaged agent still had a plugin installed")
	}
}

// Finding the plugin means asking OpenCode 2 where its configuration lives,
// which can start a stopped service. The page draws first and fills each row
// in when its own answer arrives.
func TestPluginRowWaitsForTheLookup(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	m.plugins = []SetupPlugin{{Integration: integrations.OpenCode2, Provider: "opencode2", Supported: true}}
	if m.plugin(integrations.OpenCode2).Known() || m.pluginToggleable(integrations.OpenCode2) {
		t.Fatal("the row was a choice before another knew what was installed")
	}
	if row := ansi.Strip(m.pluginRows(72, false)); !strings.Contains(row, txt.setupPluginChecking) {
		t.Fatalf("row = %q, want it to say the lookup is running", row)
	}
	next, _ := m.Update(pluginStatusMsg{plugin: SetupPlugin{
		Integration: integrations.OpenCode2, Provider: "opencode2",
		Supported: true, Dir: "/tmp/o2", State: integrations.StateCurrent,
	}})
	m = next.(setupModel)
	if !m.pluginToggleable(integrations.OpenCode2) || !m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("an installed plugin did not open the row when the lookup landed")
	}
}

// One adapter's lookup must not overwrite another's row, because the two
// arrive separately and out of order.
func TestLookupsLandOnTheirOwnRow(t *testing.T) {
	m := bothPluginsFixture(integrations.StateMissing, integrations.StateMissing)
	next, _ := m.Update(pluginStatusMsg{plugin: SetupPlugin{
		Integration: integrations.Pi, Provider: "pi",
		Supported: true, Dir: "/tmp/pi", State: integrations.StateCurrent,
	}})
	m = next.(setupModel)
	if m.plugin(integrations.Pi).State != integrations.StateCurrent {
		t.Fatal("the Pi lookup did not land on the Pi row")
	}
	if got := m.plugin(integrations.OpenCode2).State; got != integrations.StateMissing {
		t.Fatalf("OpenCode 2 row = %q, want the answer it already had", got)
	}
	if !m.wantsPlugin(integrations.Pi) || m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("a lookup opened a row it was not about")
	}
}

// Saving before the lookup lands must not withdraw a choice made earlier: the
// page could not offer the row yet, so it has no answer of its own to save.
func TestSlowLookupKeepsTheSavedChoice(t *testing.T) {
	m := pluginFixture(integrations.StateMissing)
	m.plugins = []SetupPlugin{{Integration: integrations.OpenCode2, Provider: "opencode2", Supported: true}}
	m.pluginConsented[integrations.OpenCode2] = true
	if !m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("saving during the lookup dropped the plugin another already maintains")
	}
	// The same holds while something another did not write is in the way: it
	// cannot act, and forgetting the intent would silently orphan the plugin.
	m.plugins = []SetupPlugin{{
		Integration: integrations.OpenCode2, Provider: "opencode2",
		Supported: true, Dir: "/tmp/o2", State: integrations.StateForeign,
	}}
	if !m.wantsPlugin(integrations.OpenCode2) {
		t.Fatal("a blocked directory withdrew a choice the person had made")
	}
}

// Files another did not write are reported, never claimed from this page.
func TestPluginRowWillNotOverwriteForeignFiles(t *testing.T) {
	for _, state := range []integrations.State{integrations.StateForeign, integrations.StateModified} {
		m := pluginFixture(state)
		m.pluginWanted[integrations.OpenCode2] = true
		if m.pluginToggleable(integrations.OpenCode2) || m.wantsPlugin(integrations.OpenCode2) {
			t.Fatalf("state %q was treated as another's to overwrite", state)
		}
		if !strings.Contains(ansi.Strip(m.pluginRows(72, false)), "/tmp/opencode2") {
			t.Fatalf("state %q hid where the files are", state)
		}
	}
}

// The page must still say where another would write, because that is the only
// place a person sees it before it happens.
func TestPluginRowNamesTheDirectory(t *testing.T) {
	m := bothPluginsFixture(integrations.StateMissing, integrations.StateMissing)
	rows := ansi.Strip(m.pluginRows(72, false))
	for _, want := range []string{
		"/tmp/opencode2/plugins/another-title-policy",
		"/tmp/pi/agent/extensions/another-title-policy",
		txt.setupPluginLabel,
		txt.setupPluginLabelPi,
	} {
		if !strings.Contains(rows, want) {
			t.Fatalf("rows = %q, want %q", rows, want)
		}
	}
}

// The rows cost lines the title page did not previously spend, and the panel is
// centred: one line too many and every later frame lands lower than the one
// before it. Two adapters cost twice as many, which is where a short terminal
// notices.
func TestTitlePageWithPluginRowStaysInsideTheTerminal(t *testing.T) {
	states := []integrations.State{integrations.StateMissing, integrations.StateCurrent, integrations.StateForeign}
	for _, state := range states {
		for h := 20; h <= 40; h++ {
			for _, w := range []int{48, 60, 80, 120, 200} {
				for _, m := range []setupModel{pluginFixture(state), bothPluginsFixture(state, state)} {
					m.width, m.height = w, h
					if got := lipgloss.Height(m.View()); got > h {
						t.Fatalf("state %q at %dx%d with %d rows: title page renders %d lines",
							state, w, h, len(m.plugins), got)
					}
					// The same page with no agent able to suggest a title
					// still carries the rows, because those agents name
					// sessions themselves.
					m.titleOpts = m.titleOpts[:1]
					if got := lipgloss.Height(m.View()); got > h {
						t.Fatalf("state %q at %dx%d with %d rows: title page without agents renders %d lines",
							state, w, h, len(m.plugins), got)
					}
				}
			}
		}
	}
}
