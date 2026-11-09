//go:build !darwin

package doubaoevents

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestUnsupportedPlatform(t *testing.T) {
	root := t.TempDir()
	w, err := New(Options{DesktopDir: filepath.Join(root, "desktop"), CLIDir: filepath.Join(root, "cli"), StatePath: filepath.Join(root, "state.json")})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Watch(context.Background(), func(context.Context, Event) error { t.Fatal("unexpected callback"); return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}
