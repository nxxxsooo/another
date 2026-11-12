package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeSessionsInstallStates(t *testing.T) {
	dir := t.TempDir()
	status := OpenCodeSessionsStatus(dir)
	if status.State != StateMissing {
		t.Fatal(status)
	}
	status, err := InstallOpenCodeSessions(status, "test", "", false)
	if err != nil || status.State != StateCurrent {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	runtime, err := os.ReadFile(filepath.Join(status.Dir, "runtime.ts"))
	if err != nil || len(runtime) == 0 {
		t.Fatalf("runtime=%q err=%v", runtime, err)
	}
	if err := os.WriteFile(filepath.Join(status.Dir, "command.ts"), []byte("local changes"), 0o644); err != nil {
		t.Fatal(err)
	}
	status = OpenCodeSessionsStatus(dir)
	if status.State != StateModified {
		t.Fatal(status)
	}
	if _, err := InstallOpenCodeSessions(status, "test", "", false); err == nil {
		t.Fatal("overwrote modified plugin")
	}
	if err := RemoveOpenCodeSessions(status, false); err == nil {
		t.Fatal("removed modified plugin")
	}
	if _, err := InstallOpenCodeSessions(status, "test", "", true); err != nil {
		t.Fatal(err)
	}
	status = OpenCodeSessionsStatus(dir)
	// An older owned bundle differs from today's payload, but its own manifest
	// still matches what is installed, so it is outdated rather than modified.
	files := sessionsBundle.Hashes()
	files["command.ts"] = sessionsBundle.Hash([]byte("old plugin"))
	if err := os.WriteFile(filepath.Join(status.Dir, "command.ts"), []byte("old plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest{Integration: OpenCodeSessions, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(status.Dir, manifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	status = OpenCodeSessionsStatus(dir)
	if status.State != StateOutdated {
		t.Fatal(status)
	}
	if err := os.Remove(filepath.Join(status.Dir, manifestName)); err != nil {
		t.Fatal(err)
	}
	status = OpenCodeSessionsStatus(dir)
	if status.State != StateForeign {
		t.Fatal(status)
	}
	if _, err := InstallOpenCodeSessions(status, "test", "", false); err == nil {
		t.Fatal("overwrote foreign plugin")
	}
	if _, err := InstallOpenCodeSessions(status, "test", "", true); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOpenCodeSessions(OpenCodeSessionsStatus(dir), false); err != nil {
		t.Fatal(err)
	}
	if status := OpenCodeSessionsStatus(dir); status.State != StateMissing {
		t.Fatal(status)
	}
}
