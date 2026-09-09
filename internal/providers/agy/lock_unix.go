//go:build darwin || linux

package agy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// acquireConversationLock uses the same advisory presence lock AGY holds while
// a conversation is open. The caller keeps the returned release function until
// its entire lifecycle mutation has completed, closing the check/action race.
func acquireConversationLock(path string) (release func() error, active bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, false, nil
}

// describeLockHolder names the process that already holds a presence lock.
//
// "Currently active" is true of the lock and useless to the reader: the window
// they associate with the conversation may be long closed while the process
// behind it survived — a terminal that exits without taking its children down
// leaves AGY holding this lock on a TTY nobody is looking at. Naming the
// process turns a refusal into something the reader can act on.
//
// Best effort by design. lsof is not part of this project's contract with the
// system, so a machine without it, or a lock held by a process this user
// cannot see, falls back to the path — which is still more than the id.
func describeLockHolder(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-t", path).Output()
	if err != nil {
		return ""
	}
	var described []string
	for _, field := range strings.Fields(string(out)) {
		pid, convErr := strconv.Atoi(field)
		if convErr != nil {
			continue
		}
		described = append(described, describeProcess(ctx, pid))
	}
	return strings.Join(described, ", ")
}

// describeProcess reads the command and terminal of one pid, so the message can
// say what to quit rather than only that something must be.
func describeProcess(ctx context.Context, pid int) string {
	out, err := exec.CommandContext(ctx, "ps", "-o", "comm=,tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "pid " + strconv.Itoa(pid)
	}
	fields := strings.Fields(string(out))
	switch len(fields) {
	case 0:
		return "pid " + strconv.Itoa(pid)
	case 1:
		return fields[0] + " (pid " + strconv.Itoa(pid) + ")"
	default:
		command := filepath.Base(fields[0])
		tty := fields[len(fields)-1]
		if tty == "??" || tty == "?" {
			return command + " (pid " + strconv.Itoa(pid) + ", no terminal)"
		}
		return command + " (pid " + strconv.Itoa(pid) + " on " + tty + ")"
	}
}
