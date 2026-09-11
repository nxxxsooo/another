package cli

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The script is the consumer entry point for machines without Homebrew. This
// test runs it with a local release archive so PATH setup is covered without a
// network request or a GitHub release.
func TestInstallScriptPersistsDefaultPathForZsh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is only supported on macOS and Linux")
	}

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "another.tar.gz")
	writeTestArchive(t, archive)

	curl := filepath.Join(fakeBin, "curl")
	if err := os.WriteFile(curl, []byte("#!/bin/sh\ncat \"$ANOTHER_TEST_ARCHIVE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func() string {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join(repo, "scripts", "install.sh"))
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"SHELL=/bin/zsh",
			"VERSION=v0.0.0-test",
			"ANOTHER_TEST_ARCHIVE="+archive,
			"PATH="+fakeBin+":/usr/bin:/bin",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install script: %v\n%s", err, out)
		}
		return string(out)
	}

	out := run()
	if !strings.Contains(out, "Added "+filepath.Join(home, ".local", "bin")+" to PATH in "+filepath.Join(home, ".zshrc")) {
		t.Fatalf("installer did not report persistent zsh PATH setup:\n%s", out)
	}
	installed, err := os.ReadFile(filepath.Join(home, ".local", "bin", "another"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != "test binary\n" {
		t.Fatalf("installed binary = %q", installed)
	}

	run() // Reinstalling must not duplicate the startup entry.
	rc, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	line := `export PATH="$HOME/.local/bin:$PATH"`
	if got := strings.Count(string(rc), line); got != 1 {
		t.Fatalf("PATH entry occurs %d times, want 1; .zshrc:\n%s", got, rc)
	}
}

func writeTestArchive(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("test binary\n")
	if err := tw.WriteHeader(&tar.Header{Name: "another", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
