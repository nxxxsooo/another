package util

// Liveness answers whether a process is still there.
//
// The third state is the whole point of this type. A boolean forces a check
// that cannot tell into saying "gone", and every caller in this project reads
// "gone" as permission to rename, move, or delete a file the process may still
// own. Keeping "I cannot tell" separate lets a caller refuse instead of acting
// on a guess.
type Liveness int

const (
	// ProcessUnknown is the zero value deliberately. A platform without a real
	// check, or a path that returns before making one, should land on the
	// answer that makes callers cautious rather than the one that makes them
	// act.
	ProcessUnknown Liveness = iota
	// ProcessGone means the process was looked for and is not there.
	ProcessGone
	// ProcessRunning means it is there, whether or not this user may touch it.
	ProcessRunning
)

func (l Liveness) String() string {
	switch l {
	case ProcessGone:
		return "gone"
	case ProcessRunning:
		return "running"
	default:
		return "unknown"
	}
}
