package util

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// Every caller reads "gone" as permission to rename, move, or delete a file the
// process may still own, so a Liveness that was never set must not read as
// gone. This pins the zero value, which is the whole safety argument for the
// type.
func TestUnsetLivenessIsUnknownRatherThanGone(t *testing.T) {
	var unset Liveness
	if unset != ProcessUnknown {
		t.Fatalf("the zero Liveness is %v; a caller that forgets to set one would act on it", unset)
	}
}

// Signal 0 is unimplemented on Windows — os.Process.Signal answers EWINDOWS
// for everything but Kill — so even this test's own pid reads as unknown
// there. That is the contract working, not a gap: unknown is what keeps
// callers from acting on a check that cannot be made.
func TestOwnProcessLivenessMatchesPlatformCapability(t *testing.T) {
	want := ProcessRunning
	if runtime.GOOS == "windows" {
		want = ProcessUnknown
	}
	if got := ProcessLiveness(os.Getpid()); got != want {
		t.Fatalf("this test's own pid reads as %v, want %v", got, want)
	}
}

// pid 1 exists on every Unix machine this runs on and belongs to root, so the
// check comes back EPERM rather than success. Existing but untouchable is
// still existing, and reading it as gone would let another write under a live
// agent started by a different user. On Windows every pid check is
// unanswerable, so the same pid reads as unknown — which refuses rather than
// proceeds, the whole point of the third state.
func TestUnsignalableProcessIsNeverGone(t *testing.T) {
	got := ProcessLiveness(1)
	if got == ProcessGone {
		t.Fatal("pid 1 reads as gone; an unanswerable check must never grant permission")
	}
	if runtime.GOOS != "windows" && got != ProcessRunning {
		t.Fatalf("pid 1 reads as %v, want running", got)
	}
}

func TestReapedProcessIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal 0 is unimplemented on Windows, so even a reaped pid reads as unknown")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := ProcessLiveness(cmd.Process.Pid); got != ProcessGone {
		t.Fatalf("an exited and reaped pid reads as %v", got)
	}
}

func TestNonPositivePidIsGone(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if got := ProcessLiveness(pid); got != ProcessGone {
			t.Fatalf("pid %d reads as %v", pid, got)
		}
	}
}
