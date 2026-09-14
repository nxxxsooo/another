//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"syscall"
)

// defaultShell is the interpreter resume commands are built for. It follows
// the user's shell when one is configured and falls back to /bin/sh.
func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// shellCommand runs a rendered resume command as a child of this process,
// used for the Claude trust re-run where another has to stay alive to inspect
// the result before handing the terminal over.
func shellCommand(shell, command string) *exec.Cmd {
	return exec.Command(shell, "-c", command)
}

// replaceProcess hands the terminal to the target agent by becoming it. There
// is no goodbye screen after this returns because on success it never does.
func replaceProcess(shell, command string) error {
	return syscall.Exec(shell, []string{shell, "-c", command}, os.Environ())
}
