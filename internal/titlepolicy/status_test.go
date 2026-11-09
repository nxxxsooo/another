package titlepolicy

import (
	"os"
	"testing"
	"time"
)

func TestListenerReadyIsSeparateFromLastEventDiagnostic(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	before := Status{At: time.Now().UTC(), Failed: 1, Error: "event naming failed"}
	if err := saveStatus(before); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	if err := RecordListenerReady(); err != nil {
		t.Fatal(err)
	}
	listener, err := LoadListenerStatus()
	if err != nil {
		t.Fatal(err)
	}
	if listener.ReadyAt.Before(started) || listener.PID != os.Getpid() {
		t.Fatalf("listener=%+v", listener)
	}
	after, err := LoadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !after.At.Equal(before.At) || after.Error != before.Error || after.Failed != before.Failed {
		t.Fatalf("event diagnostic overwritten: %+v", after)
	}
}
