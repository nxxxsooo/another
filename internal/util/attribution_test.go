package util_test

import (
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

// A session belongs to the directory it started in. Everything the agent
// visited afterwards — a subdirectory, a temporary checkout, another
// repository — is movement inside one session, not a change of owner.
func TestOriginDirectoryKeepsTheFirstRecordedDirectory(t *testing.T) {
	origin := util.NewOriginDirectory("/decoded/from/storage/path")
	origin.Note("/repo")
	origin.Note("/repo/src")
	origin.Note("/private/tmp")
	origin.Note("/other-repo")
	if got := origin.Path(); got != "/repo" {
		t.Fatalf("Path() = %q, want the first recorded directory /repo", got)
	}
	if !origin.Recorded() {
		t.Fatal("Recorded() = false after the session supplied a directory")
	}
}

// The fallback is a lossy guess decoded from a provider's storage directory
// name, so the first real directory must replace it.
func TestOriginDirectoryFallbackYieldsToRecordedEvidence(t *testing.T) {
	origin := util.NewOriginDirectory("/home/user/codex/minus")
	if origin.Recorded() {
		t.Fatal("Recorded() = true before any directory was noted")
	}
	origin.Note("")
	if got := origin.Path(); got != "/home/user/codex/minus" {
		t.Fatalf("Path() = %q, want the fallback while nothing was recorded", got)
	}
	origin.Note("/home/user/codex-minus")
	if got := origin.Path(); got != "/home/user/codex-minus" {
		t.Fatalf("Path() = %q, want the recorded directory to replace the fallback", got)
	}
}
