package util_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

func TestResolveExistingDirReportsMissingAsSentinel(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	_, err := util.ResolveExistingDir(missing)
	if !errors.Is(err, util.ErrDirMissing) {
		t.Fatalf("err = %v, want ErrDirMissing", err)
	}
}

func TestResolveExistingDirFileIsNotMissing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := util.ResolveExistingDir(file)
	if err == nil || errors.Is(err, util.ErrDirMissing) {
		t.Fatalf("err = %v, want a not-a-directory error", err)
	}
}

func TestResolveDirCreatesMissingDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "new", "nested")
	got, err := util.ResolveDir(target, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	if want := util.NormalizeProjectPath(target); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveDirWithoutCreateMatchesExisting(t *testing.T) {
	target := filepath.Join(t.TempDir(), "gone")
	_, err := util.ResolveDir(target, false)
	if !errors.Is(err, util.ErrDirMissing) {
		t.Fatalf("err = %v, want ErrDirMissing", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("directory must not be created when create is false")
	}
}

func TestResolveDirCreateRefusesFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := util.ResolveDir(file, true); err == nil {
		t.Fatal("expected error for a file path")
	}
}
