package util

import (
	"os"
	"os/exec"
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

func TestOwnProcessIsRunning(t *testing.T) {
	if got := ProcessLiveness(os.Getpid()); got != ProcessRunning {
		t.Fatalf("this test's own pid reads as %v", got)
	}
}

// pid 1 exists on every machine this runs on and belongs to root, so the check
// comes back EPERM rather than success. Existing but untouchable is still
// existing, and reading it as gone would let another write under a live agent
// started by a different user.
func TestProcessThisUserMayNotSignalIsStillRunning(t *testing.T) {
	if got := ProcessLiveness(1); got != ProcessRunning {
		t.Fatalf("pid 1 reads as %v", got)
	}
}

func TestReapedProcessIsGone(t *testing.T) {
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
