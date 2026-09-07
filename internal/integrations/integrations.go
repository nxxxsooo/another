// Package integrations installs the adapters another ships for other agents'
// own naming pipelines. An adapter that has to be copied by hand drifts from
// the binary that documents it: the copy on disk keeps whatever version was
// current the day someone remembered to run cp, and upgrading another does not
// touch it. another therefore owns the installed copy, writes a manifest
// beside it, and can tell the two apart afterwards.
package integrations

// State is what another found where its adapter belongs.
type State string

const (
	// StateMissing means nothing is installed there.
	StateMissing State = "missing"
	// StateCurrent means the installed files are the ones this binary ships.
	StateCurrent State = "current"
	// StateOutdated means another installed these files and now ships newer
	// ones, which is the ordinary state after an upgrade.
	StateOutdated State = "outdated"
	// StateModified means another installed these files and someone has since
	// edited them. Their content is not another's to overwrite silently.
	StateModified State = "modified"
	// StateAdoptable means the files match what this binary ships but carry no
	// manifest: the hand-copied installation this feature replaces. Adopting it
	// only adds the manifest, so nothing a person can observe changes.
	StateAdoptable State = "adoptable"
	// StateForeign means something else is installed under the same name.
	StateForeign State = "foreign"
)

// Installed reports whether the adapter is present in any form, which is a
// different question from whether it is current.
func (s State) Installed() bool { return s != StateMissing }

// NeedsWrite reports whether installing would change the files on disk.
func (s State) NeedsWrite() bool { return s == StateMissing || s == StateOutdated }

// Blocked reports the states another refuses to overwrite without being told
// to, because the content on disk is not content another wrote.
func (s State) Blocked() bool { return s == StateModified || s == StateForeign }

// Status is one adapter as another found it.
type Status struct {
	// ConfigDir is the agent configuration directory another resolved, and
	// Dir is the adapter's own directory inside it. Both are absolute.
	ConfigDir string
	Dir       string
	State     State
	// Language is the title language stamped into the installed manifest.
	// It is what the installed plugin will use until it is rewritten, which
	// is how another notices that changing the language in setup has not
	// reached the agent yet.
	Language string
	// Version is the another release that wrote the installed copy.
	Version string
	// RedundantEntry reports that the agent's own configuration still names
	// this adapter explicitly. That entry predates automatic discovery of the
	// plugins directory; it is loaded once either way, so it is untidy rather
	// than broken. another reports it and never edits the file.
	RedundantEntry string
}

// manifest is the record another writes beside an adapter it installed. It is
// what separates "another put this here" from "someone else did", and what
// makes an upgrade detectable without asking the agent anything.
type manifest struct {
	Integration string            `json:"integration"`
	Plugin      string            `json:"plugin"`
	Version     string            `json:"another_version"`
	Language    string            `json:"title_language,omitempty"`
	Files       map[string]string `json:"files"`
}

// manifestName is dot-prefixed so the agent's plugin loader ignores it: the
// directory is a plugin package, and its entrypoint is index.ts.
const manifestName = ".another-install.json"
