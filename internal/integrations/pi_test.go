package integrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nxxxsooo/another/internal/providers/pi"
)

// another has to install the extension into the directory Pi actually loads
// from, which is the one the provider indexes. Resolution is checked here
// rather than assumed, because installing into a directory Pi never reads
// looks exactly like a successful installation.
func TestPiConfigDirFollowsPiOwnEnv(t *testing.T) {
	agent := t.TempDir()
	legacy := t.TempDir()

	t.Setenv(pi.LegacyAgentDirEnv, legacy)
	if got := PiConfigDir(); got != legacy {
		t.Fatalf("PiConfigDir() = %q, want another's older name %q honored", got, legacy)
	}

	// Pi builds this name from its own application name and reads nothing
	// else, so it outranks the name another used before anyone checked.
	t.Setenv(pi.AgentDirEnv, agent)
	if got := PiConfigDir(); got != agent {
		t.Fatalf("PiConfigDir() = %q, want Pi's own %s %q", got, pi.AgentDirEnv, agent)
	}

	// The adapter's own override still wins: it exists for the Pi another
	// cannot locate at all.
	override := t.TempDir()
	t.Setenv(PiConfigDirEnv, override)
	if got := PiConfigDir(); got != override {
		t.Fatalf("PiConfigDir() = %q, want the override %q", got, override)
	}
}

// The Pi extension goes through the same states as every other adapter. The
// value of these states is that another can tell its own installation from a
// directory it did not write, so each one is checked against real files.
func TestPiStatusReadsWhatIsOnDisk(t *testing.T) {
	root := t.TempDir()
	t.Setenv(PiConfigDirEnv, root)
	if got := PiConfigDir(); got != root {
		t.Fatalf("PiConfigDir() = %q, want the override %q", got, root)
	}

	status := PiStatus(root)
	if status.State != StateMissing {
		t.Fatalf("an empty directory reads as %q, want missing", status.State)
	}

	installed, err := InstallPi(status, "v9.9.9", "zh", false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != StateCurrent {
		t.Fatalf("a fresh install reads as %q, want current", installed.State)
	}
	if installed.Version != "v9.9.9" || installed.Language != "zh" {
		t.Fatalf("manifest lost its stamp: %+v", installed)
	}

	// An edited file is not another's to overwrite silently.
	edited := filepath.Join(installed.Dir, "index.ts")
	if err := os.WriteFile(edited, []byte("// someone changed this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PiStatus(root).State; got != StateModified {
		t.Fatalf("an edited extension reads as %q, want modified", got)
	}

	if err := RemovePi(PiStatus(root), true); err != nil {
		t.Fatal(err)
	}
	if got := PiStatus(root).State; got != StateMissing {
		t.Fatalf("after removal the directory reads as %q, want missing", got)
	}
}

// Files with no manifest are the hand-installed case. Identical content can be
// adopted; different content belongs to someone else.
func TestPiStatusSeparatesAdoptableFromForeign(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content []byte
		want    State
	}{
		{"the same extension installed by hand", nil, StateAdoptable},
		{"somebody else's extension", []byte("export default function register() {}\n"), StateForeign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := PiDir(root)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			content := tc.content
			if content == nil {
				content = piBundle.Files()["index.ts"]
			}
			if err := os.WriteFile(filepath.Join(dir, "index.ts"), content, 0o644); err != nil {
				t.Fatal(err)
			}
			if got := PiStatus(root).State; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}
