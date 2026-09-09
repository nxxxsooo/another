//go:build !darwin && !linux

package agy

import "fmt"

// Deleting an existing conversation is rejected where AGY's presence lock
// cannot yet be held for the complete mutation, because nothing here can tell a
// conversation AGY has open from one it does not, and a delete pulled out from
// under a running CLI is unrecoverable. CleanupWrite rolls back only a fresh
// migration, and a rename replaces a metadata file AGY does not hold open, so
// both stay available.
func acquireConversationLock(string) (func() error, bool, error) {
	return nil, false, fmt.Errorf("agy: changing an existing conversation is not supported on this platform")
}

// describeLockHolder has nothing to report where the lock itself is unavailable.
func describeLockHolder(string) string { return "" }
