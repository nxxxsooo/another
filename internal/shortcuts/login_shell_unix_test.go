//go:build !windows

package shortcuts

import (
	"path/filepath"
	"testing"
)

func TestPasswdShell(t *testing.T) {
	const passwd = "root:x:0:0:root:/root:/bin/bash\n" +
		"daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin\n" +
		"build:x:998:998::/home/build:/bin/false\n" +
		"dev:x:1001:1001::/home/dev:/usr/bin/zsh\n" +
		"broken:x:1002:1002:/home/broken\n"

	for _, tc := range []struct {
		user string
		want string
	}{
		{"dev", "/usr/bin/zsh"},
		{"root", "/bin/bash"},
		{"daemon", ""},
		{"build", ""},
		{"broken", ""},
		{"absent", ""},
	} {
		if got := passwdShell(passwd, tc.user); got != tc.want {
			t.Errorf("passwdShell(%q) = %q, want %q", tc.user, got, tc.want)
		}
	}
}

// An empty SHELL is the normal case for `curl … | bash`, cron, and
// `docker exec`: bash keeps SHELL as a plain shell variable, so the installer
// reads it and the binary it runs does not. The alias step has to survive that.
func TestPlanForFallsBackToLoginShell(t *testing.T) {
	t.Setenv("SHELL", "")
	login := loginShell()
	if login == "" {
		t.Skip("no login shell in the passwd database for this user")
	}
	p, err := planFor("")
	if err != nil {
		t.Fatalf("planFor with empty SHELL: %v", err)
	}
	if p.executable != login {
		t.Errorf("executable = %q, want %q", p.executable, login)
	}
	if want := filepath.Base(login); p.kind != want {
		t.Errorf("kind = %q, want %q", p.kind, want)
	}
}
