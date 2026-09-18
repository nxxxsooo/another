//go:build windows

package shortcuts

// loginShell has no Windows equivalent: there is no passwd database, and the
// empty-SHELL fallback there is powershell.exe.
func loginShell() string { return "" }
