//go:build !darwin && !linux

package util

// ProcessLiveness has no answer where another has not established how to ask.
//
// Windows is the case that matters: os.Process.Signal implements nothing but
// Kill and returns EWINDOWS for everything else, so the Unix check would report
// every process on the machine as gone — including the agent currently holding
// the session another was about to rewrite. Reporting that as unknown is the
// difference between refusing the mutation and performing it against a live
// agent.
//
// A non-positive pid is gone on every OS: no process can have one, so that
// answer needs no check.
func ProcessLiveness(pid int) Liveness {
	if pid <= 0 {
		return ProcessGone
	}
	return ProcessUnknown
}
