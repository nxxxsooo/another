package util_test

import (
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

// Both renderings have to neutralize the same hostile input, because the whole
// point of quoting is that a session id or path never becomes code. The POSIX
// column pins the long-standing output byte-for-byte; the PowerShell column
// pins the syntax another hands to powershell.exe on Windows.
func TestQuoteArgForBothShells(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		posix    string
		pwsh     string
		contains []string
	}{
		{
			name:     "plain path",
			input:    "/tmp/a b",
			posix:    "'/tmp/a b'",
			pwsh:     "'/tmp/a b'",
			contains: []string{"/tmp/a b"},
		},
		{
			name:     "command injection",
			input:    "id; echo bad",
			posix:    "'id; echo bad'",
			pwsh:     "'id; echo bad'",
			contains: []string{"id; echo bad"},
		},
		{
			name:     "embedded single quote",
			input:    "/tmp/it's here",
			posix:    `'/tmp/it'"'"'s here'`,
			pwsh:     `'/tmp/it''s here'`,
			contains: []string{"it", "s here"},
		},
		{
			name:     "dollar and backtick stay literal",
			input:    "$HOME/`whoami`",
			posix:    "'$HOME/`whoami`'",
			pwsh:     "'$HOME/`whoami`'",
			contains: []string{"$HOME/"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := util.QuoteArgFor(util.ShellPOSIX, tc.input); got != tc.posix {
				t.Fatalf("POSIX QuoteArgFor(%q) = %q, want %q", tc.input, got, tc.posix)
			}
			if got := util.QuoteArgFor(util.ShellPowerShell, tc.input); got != tc.pwsh {
				t.Fatalf("PowerShell QuoteArgFor(%q) = %q, want %q", tc.input, got, tc.pwsh)
			}
			// Either rendering must keep the payload inside quotes: stripping
			// the outer pair has to leave no bare statement separator behind
			// that the shell would act on.
			for _, kind := range []util.ShellKind{util.ShellPOSIX, util.ShellPowerShell} {
				got := util.QuoteArgFor(kind, tc.input)
				if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
					t.Fatalf("QuoteArgFor(%v, %q) = %q, not wrapped in single quotes", kind, tc.input, got)
				}
			}
		})
	}
}

// The chaining syntax is what differs: && is POSIX (and cmd.exe), while the
// default Windows PowerShell 5.1 only understands ; — so the Windows form must
// not contain &&, or the copied line fails on a stock machine.
func TestCdAndForUsesEachShellsChaining(t *testing.T) {
	if got, want := util.CdAndFor(util.ShellPOSIX, "/p", "run"), "cd '/p' && run"; got != want {
		t.Fatalf("POSIX CdAndFor = %q, want %q", got, want)
	}
	if got, want := util.CdAndFor(util.ShellPowerShell, "/p", "run"), "Set-Location -LiteralPath '/p'; run"; got != want {
		t.Fatalf("PowerShell CdAndFor = %q, want %q", got, want)
	}
	if got := util.CdAndFor(util.ShellPowerShell, "/p", "run"); strings.Contains(got, "&&") {
		t.Fatalf("PowerShell chaining must not use &&: %q", got)
	}
}

func TestEnvAndForUsesEachShellsAssignment(t *testing.T) {
	if got, want := util.EnvAndFor(util.ShellPOSIX, "K", "/v v", "run"), "K='/v v' run"; got != want {
		t.Fatalf("POSIX EnvAndFor = %q, want %q", got, want)
	}
	if got, want := util.EnvAndFor(util.ShellPowerShell, "K", "/v v", "run"), "$env:K='/v v'; run"; got != want {
		t.Fatalf("PowerShell EnvAndFor = %q, want %q", got, want)
	}
}

// The wrappers follow the machine, which is the only thing a test can assert
// about them without faking the OS: on this machine they agree with the
// explicit kind for LocalShell().
func TestWrappersFollowLocalShell(t *testing.T) {
	kind := util.LocalShell()
	if got := util.QuoteArg("a'b"); got != util.QuoteArgFor(kind, "a'b") {
		t.Fatalf("QuoteArg = %q, QuoteArgFor(LocalShell()) = %q", got, util.QuoteArgFor(kind, "a'b"))
	}
	if got := util.CdAnd("/p", "run"); got != util.CdAndFor(kind, "/p", "run") {
		t.Fatalf("CdAnd = %q, CdAndFor(LocalShell()) = %q", got, util.CdAndFor(kind, "/p", "run"))
	}
	if got := util.EnvAnd("K", "v", "run"); got != util.EnvAndFor(kind, "K", "v", "run") {
		t.Fatalf("EnvAnd = %q, EnvAndFor(LocalShell()) = %q", got, util.EnvAndFor(kind, "K", "v", "run"))
	}
}
