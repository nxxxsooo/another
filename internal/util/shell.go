package util

import (
	"runtime"
	"strings"
)

// ShellKind is the command-line syntax a rendered command targets. Providers
// build resume commands for the machine another runs on — the same terminal
// the person will paste into or that another hands off to — so the kind
// follows the local OS, not the agent.
type ShellKind int

const (
	// ShellPOSIX is sh-style: single quotes are literal, commands chain with
	// &&, and VAR=value prefixes one command's environment.
	ShellPOSIX ShellKind = iota
	// ShellPowerShell is what Windows offers everywhere: powershell.exe ships
	// with the OS while sh does not. Single quotes are literal there too, but
	// an embedded quote doubles instead of closing and reopening, commands
	// chain with ; (which works in 5.1 where && does not), and per-command
	// environment goes through $env:.
	ShellPowerShell
)

// LocalShell reports the syntax for this machine.
func LocalShell() ShellKind {
	if runtime.GOOS == "windows" {
		return ShellPowerShell
	}
	return ShellPOSIX
}

// QuoteArg renders one argument so the shell passes it through literally.
func QuoteArg(s string) string { return QuoteArgFor(LocalShell(), s) }

// QuoteArgFor is QuoteArg for an explicit kind, so both syntaxes stay testable
// on every platform. The Windows CI leg cannot execute anything, but it can
// still prove what another would have handed PowerShell.
func QuoteArgFor(kind ShellKind, s string) string {
	if kind == ShellPowerShell {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return ShellQuote(s)
}

// CdAnd renders "move to dir, then run cmd".
func CdAnd(dir, cmd string) string { return CdAndFor(LocalShell(), dir, cmd) }

// CdAndFor is CdAnd for an explicit kind. The PowerShell form spells out
// Set-Location with -LiteralPath because bare cd chokes on the [brackets] a
// project path can legally contain.
func CdAndFor(kind ShellKind, dir, cmd string) string {
	quoted := QuoteArgFor(kind, dir)
	if kind == ShellPowerShell {
		return "Set-Location -LiteralPath " + quoted + "; " + cmd
	}
	return "cd " + quoted + " && " + cmd
}

// EnvAnd renders "run cmd with one extra environment variable".
func EnvAnd(key, value, cmd string) string { return EnvAndFor(LocalShell(), key, value, cmd) }

// EnvAndFor is EnvAnd for an explicit kind.
func EnvAndFor(kind ShellKind, key, value, cmd string) string {
	if kind == ShellPowerShell {
		return "$env:" + key + "=" + QuoteArgFor(kind, value) + "; " + cmd
	}
	return key + "=" + QuoteArgFor(kind, value) + " " + cmd
}
