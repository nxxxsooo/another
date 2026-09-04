//go:build !darwin && !linux

package agy

import "fmt"

// CleanupWrite remains available everywhere because it only rolls back a fresh
// migration. Native rename is rejected where AGY's presence lock cannot yet be
// held for the complete mutation.
func acquireConversationLock(string) (func() error, bool, error) {
	return nil, false, fmt.Errorf("agy: rename is not supported on this platform")
}
