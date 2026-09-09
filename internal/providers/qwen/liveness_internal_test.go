package qwen

import (
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

// The sidecar is a live Qwen Code's claim on the session. Where another cannot
// check whether the process behind that claim survived, the old boolean check
// reported "not running" and the archive or delete went ahead against a session
// a live agent may still own. An unknown answer has to refuse.
func TestUnknownLivenessRefusesInsteadOfProceeding(t *testing.T) {
	err := refusalFor(util.ProcessUnknown, 4242, "s1", "/chats/s1.runtime.json")
	if err == nil {
		t.Fatal("a session another could not check was treated as free to modify")
	}
	// The refusal is only useful if it says what to quit and what to remove.
	for _, want := range []string{"4242", "/chats/s1.runtime.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not mention %q: %v", want, err)
		}
	}
}

func TestRunningLivenessRefuses(t *testing.T) {
	if refusalFor(util.ProcessRunning, 1, "s1", "/chats/s1.runtime.json") == nil {
		t.Fatal("a running Qwen Code was treated as free to modify")
	}
}

// The guard still has to let the ordinary case through, or archive and delete
// stop working for every session whose agent has exited.
func TestGoneLivenessAllowsTheMutation(t *testing.T) {
	if err := refusalFor(util.ProcessGone, 0, "s1", "/chats/s1.runtime.json"); err != nil {
		t.Fatalf("refused a session nobody is running: %v", err)
	}
}
