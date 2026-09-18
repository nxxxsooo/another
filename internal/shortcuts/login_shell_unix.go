//go:build !windows

package shortcuts

import (
	"os"
	"os/exec"
	"os/user"
	"strings"
)

const passwdPath = "/etc/passwd"

// loginShell recovers the shell that SHELL would normally name. bash sets SHELL
// as an ordinary shell variable when the environment lacks it, so a script
// piped into `curl … | bash` reads the right value while every process it
// starts sees an empty SHELL. That is exactly how another is installed and
// updated inside containers, cron jobs, and `ssh host 'command'` runs, where
// the alias step used to fail with `unsupported shell ""`.
func loginShell() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	passwd, err := os.ReadFile(passwdPath)
	if err == nil {
		if shell := passwdShell(string(passwd), u.Username); shell != "" {
			return shell
		}
	}
	// Directory users (LDAP, SSSD) have no line in /etc/passwd. getent asks the
	// same resolver that logged them in.
	out, err := exec.Command("getent", "passwd", u.Username).Output()
	if err != nil {
		return ""
	}
	return passwdShell(string(out), u.Username)
}

// passwdShell reads the login shell out of passwd-format text. A nologin or
// false shell is an account without a shell, not a shell another can write an
// alias into, so it answers the same as a missing entry.
func passwdShell(passwd, username string) string {
	for _, line := range strings.Split(passwd, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] != username {
			continue
		}
		shell := strings.TrimSpace(fields[6])
		if strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "/false") {
			return ""
		}
		return shell
	}
	return ""
}
