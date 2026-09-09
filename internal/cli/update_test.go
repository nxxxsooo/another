package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The update path is decided by where the binary lives, because that is what
// the installers disagree about. Getting this wrong is not cosmetic: telling a
// Homebrew install to re-run the install script leaves brew convinced it still
// owns a file that another replaced underneath it.
func TestClassifyExecutableRoutesEachInstaller(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cases := []struct {
		name string
		path string
		want installSource
	}{
		{"a homebrew cask on apple silicon", "/opt/homebrew/Caskroom/another/0.9.1/another", sourceHomebrew},
		{"a homebrew cellar build", "/usr/local/Cellar/another/0.9.1/bin/another", sourceHomebrew},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/another", sourceHomebrew},
		{"the install script's default directory", filepath.Join(home, ".local", "bin", "another"), sourceScript},
		{"a go install build", filepath.Join(goBinDir(), "another"), sourceGoInstall},
		{"something else entirely", "/tmp/scratch/renamed-binary", sourceUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyExecutable(tc.path); got != tc.want {
				t.Fatalf("classifyExecutable(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// A home directory with a space in it would otherwise split into two arguments
// and the script would install somewhere nobody asked for.
func TestShellQuoteSurvivesAwkwardPaths(t *testing.T) {
	for _, dir := range []string{"/Users/a b/.local/bin", "/tmp/it's here"} {
		quoted := shellQuote(dir)
		if quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
			t.Fatalf("shellQuote(%q) = %q, want it wrapped in single quotes", dir, quoted)
		}
	}
	if got := shellQuote("/tmp/it's here"); got != `'/tmp/it'\''s here'` {
		t.Fatalf("an embedded quote was not closed and reopened: %q", got)
	}
}
