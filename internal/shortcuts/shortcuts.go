// Package shortcuts installs optional, conflict-aware interactive shell aliases.
package shortcuts

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

const marker = "__ANOTHER_SHORTCUT__"

type shellPlan struct {
	executable string
	args       []string
	profile    string
	kind       string
}

func planFor(shell string) (shellPlan, error) {
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" && runtime.GOOS == "windows" {
			shell = "powershell.exe"
		}
	}
	p := shellPlan{executable: shell, kind: strings.TrimSuffix(strings.ToLower(filepath.Base(shell)), ".exe")}
	switch p.kind {
	case "zsh":
		p.args = []string{"-ic"}
		p.profile = `printf '\n` + marker + `%s\n' "${ZDOTDIR:-$HOME}/.zshrc"`
	case "bash":
		p.args = []string{"-ic"}
		p.profile = `printf '\n` + marker + `%s\n' "$HOME/.bashrc"`
		if runtime.GOOS == "darwin" {
			p.args = []string{"-lic"}
			p.profile = `p="$HOME/.bash_profile"; for f in .bash_profile .bash_login .profile; do if [ -f "$HOME/$f" ]; then p="$HOME/$f"; break; fi; done; printf '\n` + marker + `%s\n' "$p"`
		}
	case "fish":
		p.args = []string{"-ic"}
		p.profile = `printf '\n` + marker + `%s/config.fish\n' "$__fish_config_dir"`
	case "powershell", "pwsh":
		p.args = []string{"-NoLogo", "-NonInteractive", "-OutputFormat", "Text", "-Command"}
		p.profile = `Write-Output ('` + marker + `' + $PROFILE.CurrentUserAllHosts)`
	default:
		return p, fmt.Errorf("shortcut aliases: unsupported shell %q; skipped", shell)
	}
	return p, nil
}

func (p shellPlan) probe(name string) string {
	switch p.kind {
	case "powershell", "pwsh":
		return `if (Get-Command '` + name + `' -ErrorAction SilentlyContinue) { Write-Output '` + marker + `taken' } else { Write-Output '` + marker + `free' }`
	case "fish":
		return `if type -q ` + name + `; echo ` + marker + `taken; else; echo ` + marker + `free; end`
	default:
		return `if command -v ` + name + ` >/dev/null 2>&1; then printf '\n` + marker + `taken\n'; else printf '\n` + marker + `free\n'; fi`
	}
}

func (p shellPlan) definition(name, target string) string {
	switch p.kind {
	case "powershell", "pwsh":
		return `if (-not (Get-Command '` + name + `' -ErrorAction SilentlyContinue)) { Set-Alias -Name '` + name + `' -Value '` + target + `' -Scope Global }`
	case "fish":
		return `if not type -q ` + name + `; alias ` + name + ` '` + target + `'; end`
	default:
		return `if ! command -v ` + name + ` >/dev/null 2>&1; then alias ` + name + `='` + target + `'; fi`
	}
}

func runShell(ctx context.Context, p shellPlan, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if p.kind == "powershell" || p.kind == "pwsh" {
		script = `[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new(); ` + script
	}
	args := append(append([]string{}, p.args...), script)
	data, err := exec.CommandContext(ctx, p.executable, args...).Output()
	if err != nil {
		return "", fmt.Errorf("inspect %s startup: %w", p.kind, err)
	}
	// Startup banners are allowed; only the final explicit response is ours.
	for _, line := range reverseLines(string(data)) {
		if strings.HasPrefix(line, marker) {
			return strings.TrimPrefix(line, marker), nil
		}
	}
	return "", fmt.Errorf("%s startup did not finish; no aliases written", p.kind)
}

func reverseLines(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

// Windows PowerShell profiles are often UTF-16. Keep their byte order instead
// of appending UTF-8 bytes to an otherwise valid owner-managed profile.
func profileText(data []byte) (string, binary.ByteOrder) {
	var order binary.ByteOrder
	if len(data) >= 2 {
		switch {
		case data[0] == 0xff && data[1] == 0xfe:
			order = binary.LittleEndian
		case data[0] == 0xfe && data[1] == 0xff:
			order = binary.BigEndian
		}
	}
	if order == nil {
		return string(data), nil
	}
	units := make([]uint16, 0, (len(data)-2)/2)
	for i := 2; i+1 < len(data); i += 2 {
		units = append(units, order.Uint16(data[i:i+2]))
	}
	return string(utf16.Decode(units)), order
}

func encodeProfileAppend(text string, order binary.ByteOrder) []byte {
	if order == nil {
		return []byte(text)
	}
	units := utf16.Encode([]rune(text))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		order.PutUint16(data[2*i:2*i+2], unit)
	}
	return data
}

// Ensure checks once per shell and binary kind on an interactive launch. This
// runs in the real user's environment, unlike Homebrew's sandboxed install
// hooks. A conflict is a completed check; explicit Install can retry later.
func Ensure(ctx context.Context, shell string, dev bool, stateDir string, out io.Writer) error {
	p, err := planFor(shell)
	if err != nil {
		return err
	}
	kind := "release"
	if dev {
		kind = "dev"
	}
	checked := filepath.Join(stateDir, "aliases", p.kind+"-"+kind+"-v1")
	if _, err := os.Stat(checked); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := Install(ctx, p.executable, dev, out); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(checked), 0o700); err != nil {
		return err
	}
	return os.WriteFile(checked, []byte("checked\n"), 0o600)
}

// Install probes the actual interactive startup before writing. Definitions are
// guarded too, so a new command installed later takes precedence next startup.
// Names and targets are fixed, never interpolated from session or user content.
func Install(ctx context.Context, shell string, dev bool, out io.Writer) error {
	p, err := planFor(shell)
	if err != nil {
		return err
	}
	profile, err := runShell(ctx, p, p.profile)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(profile) {
		return fmt.Errorf("invalid %s profile path %q; skipped", p.kind, profile)
	}
	names, target := []string{"a"}, "another"
	if dev {
		names, target = []string{"a-dev", "adev"}, "another-dev"
	}
	for _, name := range names {
		state, err := runShell(ctx, p, p.probe(name))
		if err != nil {
			return err
		}
		if state != "free" {
			fmt.Fprintf(out, "Skipped %s: name already in use (or unavailable).\n", name)
			continue
		}
		definition := p.definition(name, target)
		content, err := os.ReadFile(profile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		text, order := profileText(content)
		if order != nil && len(content)%2 != 0 {
			return fmt.Errorf("invalid UTF-16 profile %s; skipped", profile)
		}
		if strings.Contains(text, definition) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(profile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(encodeProfileAppend("\n# another shortcut: "+name+"\n"+definition+"\n", order))
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(out, "Installed %s -> %s in %s. Open a new shell to use it.\n", name, target, profile)
	}
	return nil
}
