package tui

import (
	"os"
	"os/exec"
)

// defaultShell is powershell.exe and not cmd.exe: the resume commands another
// renders use single-quoted literals and ; chaining, which cmd.exe reads as
// noise. powershell.exe ships with every supported Windows, while pwsh and a
// POSIX shell are both optional installs another must not assume.
func defaultShell() string { return "powershell.exe" }

// shellCommand runs a rendered resume command as a child of this process. The
// command arrives as one argument to -Command so its quoting survives intact;
// splitting it into words here would re-parse what the providers already
// quoted.
func shellCommand(shell, command string) *exec.Cmd {
	return exec.Command(shell, "-NoLogo", "-NoProfile", "-Command", command)
}

// replaceProcess is the Windows shape of handing the terminal over.
// syscall.Exec is a stub that returns EWINDOWS here, so there is no becoming
// the agent: it runs as a child on this console with the standard streams
// inherited, and another exits once it does. A nonzero agent exit comes back
// as the error, which keeps a failed resume visible instead of silently
// dropping back to a prompt.
func replaceProcess(shell, command string) error {
	cmd := shellCommand(shell, command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
