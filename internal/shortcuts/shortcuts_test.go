package shortcuts

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func shellHome(t *testing.T, shell, initial string) (string, shellPlan) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix interactive startup test")
	}
	exe, err := exec.LookPath(shell)
	if err != nil {
		t.Skip(shell + " is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, file := range []string{".zshrc", ".bashrc", ".bash_profile", ".config/fish/config.fish"} {
		path := filepath.Join(home, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := planFor(exe)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runShell(context.Background(), p, p.profile)
	if err != nil || !strings.HasPrefix(profile, home+string(filepath.Separator)) {
		t.Fatalf("test shell escaped its isolated home: %q, %v", profile, err)
	}
	return profile, p
}

func TestInstallAliasesIsIdempotentAndForwardsArguments(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			profile, p := shellHome(t, shell, "# existing settings\n")
			var out bytes.Buffer
			for _, dev := range []bool{false, true, false, true} {
				if err := Install(context.Background(), p.executable, dev, &out); err != nil {
					t.Fatal(err)
				}
			}
			content, _ := os.ReadFile(profile)
			for _, name := range []string{"a", "a-dev", "adev"} {
				if strings.Count(string(content), "# another shortcut: "+name+"\n") != 1 {
					t.Fatalf("%s missing or duplicated: %s", name, content)
				}
			}
			script := `function another { printf '\n` + marker + `%s\n' "$1"; }; a 'argument with spaces'`
			if shell == "fish" {
				script = `function another; printf '\n` + marker + `%s\n' "$argv[1]"; end; a 'argument with spaces'`
			}
			got, err := runShell(context.Background(), p, script)
			if err != nil || got != "argument with spaces" {
				t.Fatalf("alias arguments: %q, %v", got, err)
			}
		})
	}
}

func TestExistingNamesAreNotWrittenOrReplaced(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, kind := range []string{"alias", "function", "executable"} {
			t.Run(shell+"/"+kind, func(t *testing.T) {
				initial := "# owner settings\n"
				switch kind {
				case "alias":
					initial += "alias a='echo owner'\n"
				case "function":
					if shell == "fish" {
						initial += "function a; echo owner; end\n"
					} else {
						initial += "a() { echo owner; }\n"
					}
				}
				profile, p := shellHome(t, shell, initial)
				if kind == "executable" {
					bin := t.TempDir()
					if err := os.WriteFile(filepath.Join(bin, "a"), []byte("#!/bin/sh\necho owner\n"), 0o700); err != nil {
						t.Fatal(err)
					}
					if shell == "fish" {
						initial += "set -gx PATH '" + bin + "' $PATH\n"
					} else {
						initial += "export PATH='" + bin + "':$PATH\n"
					}
					if err := os.WriteFile(profile, []byte(initial), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				var out bytes.Buffer
				if err := Install(context.Background(), p.executable, false, &out); err != nil {
					t.Fatal(err)
				}
				after, _ := os.ReadFile(profile)
				if string(after) != initial || !strings.Contains(out.String(), "Skipped a") {
					t.Fatalf("existing %s was touched: %s; %s", kind, after, out.String())
				}
			})
		}
	}
}

func TestDeveloperAliasConflictsAreIndependent(t *testing.T) {
	profile, p := shellHome(t, "zsh", "alias adev='echo owner'\n")
	var out bytes.Buffer
	if err := Install(context.Background(), p.executable, true, &out); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(profile)
	if !strings.Contains(string(content), "# another shortcut: a-dev\n") || strings.Contains(string(content), "# another shortcut: adev\n") || strings.Contains(string(content), "# another shortcut: a\n") {
		t.Fatalf("dev conflicts were not independent: %s", content)
	}
}

func TestNewConflictWinsOnFutureStartup(t *testing.T) {
	profile, p := shellHome(t, "zsh", "")
	var out bytes.Buffer
	if err := Install(context.Background(), p.executable, false, &out); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	// A new owner appears before another's existing guarded definition.
	if err := os.WriteFile(profile, append([]byte("a() { echo owner; }\n"), content...), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := runShell(context.Background(), p, `printf '\n`+marker+`%s\n' "$(a)"`)
	if err != nil || got != "owner" {
		t.Fatalf("new owner was shadowed: %q, %v", got, err)
	}
}

func TestBrokenStartupDoesNotWrite(t *testing.T) {
	profile, p := shellHome(t, "zsh", "")
	initial := "exit 7\n"
	if err := os.WriteFile(profile, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(context.Background(), p.executable, false, &out); err == nil {
		t.Fatal("incomplete startup was treated as conflict-free")
	}
	content, _ := os.ReadFile(profile)
	if string(content) != initial {
		t.Fatal("broken profile was modified")
	}
}

func TestEnsureChecksOnceAndKeepsReleaseAndDevSeparate(t *testing.T) {
	profile, p := shellHome(t, "zsh", "")
	state := t.TempDir()
	var out bytes.Buffer
	for _, dev := range []bool{false, true} {
		if err := Ensure(context.Background(), p.executable, dev, state, &out); err != nil {
			t.Fatal(err)
		}
	}
	content, _ := os.ReadFile(profile)
	for _, name := range []string{"a", "a-dev", "adev"} {
		if strings.Count(string(content), "# another shortcut: "+name+"\n") != 1 {
			t.Fatalf("shortcut missing or repeated: %s", content)
		}
	}
	// A later startup need not launch a child shell at all.
	if err := os.WriteFile(profile, []byte("exit 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dev := range []bool{false, true} {
		if err := Ensure(context.Background(), p.executable, dev, state, &out); err != nil {
			t.Fatalf("completed setup was repeated: %v", err)
		}
	}
}

func TestPowerShellDefinitionPreservesExistingFunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell execution requires Windows")
	}
	p, err := planFor("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	p.args = []string{"-NoProfile", "-NonInteractive", "-Command"}
	script := `function a { 'owner' }; ` + p.definition("a", "another") + `; Write-Output ('` + marker + `' + (a))`
	got, err := runShell(context.Background(), p, script)
	if err != nil || got != "owner" {
		t.Fatalf("PowerShell conflict: %q, %v", got, err)
	}
	for _, name := range []string{"a", "a-dev", "adev"} {
		script = p.definition(name, "another-dev") + `; Write-Output ('` + marker + `' + (Get-Alias '` + name + `').Definition)`
		got, err = runShell(context.Background(), p, script)
		if err != nil || got != "another-dev" {
			t.Fatalf("PowerShell alias %s: %q, %v", name, got, err)
		}
	}
	profile := filepath.Join(t.TempDir(), "profile.ps1")
	data := append([]byte{0xff, 0xfe}, encodeProfileAppend("# existing 中文\r\n", binary.LittleEndian)...)
	data = append(data, encodeProfileAppend(p.definition("a", "another")+"\r\n", binary.LittleEndian)...)
	if err := os.WriteFile(profile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	script = `. '` + strings.ReplaceAll(profile, "'", "''") + `'; Write-Output ('` + marker + `' + (Get-Alias 'a').Definition)`
	got, err = runShell(context.Background(), p, script)
	if err != nil || got != "another" {
		t.Fatalf("UTF-16 PowerShell profile: %q, %v", got, err)
	}
}

func TestProfileAppendPreservesUTF16Encoding(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		bom := make([]byte, 2)
		order.PutUint16(bom, 0xfeff)
		original := "# owner 中文\r\n"
		data := append(bom, encodeProfileAppend(original, order)...)
		text, detected := profileText(data)
		if text != original || detected != order {
			t.Fatal("profile encoding was not detected")
		}
		definition := "\nalias a='another'\n"
		combined := append(data, encodeProfileAppend(definition, detected)...)
		text, _ = profileText(combined)
		if text != original+definition {
			t.Fatalf("existing profile was corrupted: %q", text)
		}
	}
}
