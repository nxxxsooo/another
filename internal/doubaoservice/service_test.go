package doubaoservice

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Model macOS execute permissions even when tests run on Windows, where native
// file modes do not expose POSIX execute bits. Real file existence/type remains
// verified by os.Stat; launchctl is independently mocked.
type executableInfo struct{ os.FileInfo }

func (i executableInfo) Mode() os.FileMode { return i.FileInfo.Mode() | 0100 }

func fixture(t *testing.T) (manager, string, *[][]string) {
	t.Helper()
	root := t.TempDir()
	m := manager{home: filepath.Join(root, "home"), cache: filepath.Join(root, "cache"), pathEnv: "a&b<path>\"", uid: 501, goos: "darwin"}
	m.statExecutable = func(p string) (os.FileInfo, error) {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		return executableInfo{info}, nil
	}
	calls := &[][]string{}
	m.run = func(args ...string) (string, error) {
		*calls = append(*calls, append([]string(nil), args...))
		if args[0] == "print" {
			return "Could not find service", errors.New("not found")
		}
		return "", nil
	}
	exe := filepath.Join(root, "another-dev")
	if err := os.WriteFile(exe, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	return m, exe, calls
}

func TestPlistEscapingAndArguments(t *testing.T) {
	m, exe, _ := fixture(t)
	exe += " & <title>"
	m.env = func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return filepath.Join(m.home, "a&b")
		}
		return ""
	}
	data := m.plist(exe)
	d := xml.NewDecoder(bytes.NewReader(data))
	var values []string
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML: %v\n%s", err, data)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "string" {
			var value string
			if err := d.DecodeElement(&value, &start); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
	}
	want := []string{label, exe, "title-policy", "doubao", "--watch", m.home, m.home, m.pathEnv, filepath.Join(m.home, "a&b"), "/dev/null", "/dev/null"}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("values = %#v; want %#v", values, want)
	}
	if bytes.Contains(data, []byte("StartInterval")) || !bytes.Contains(data, []byte("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>")) || !bytes.Contains(data, []byte("<key>RunAtLoad</key><true/>")) {
		t.Fatalf("missing schedule: %s", data)
	}
	if !bytes.Contains(data, []byte("<key>SoftResourceLimits</key><dict><key>NumberOfFiles</key><integer>16384</integer></dict>")) {
		t.Fatal("missing file descriptor resource limit")
	}
}

func TestInstallStatusRemove(t *testing.T) {
	m, exe, calls := fixture(t)
	if err := m.install(exe); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"print", "gui/501/" + label}, {"bootstrap", "gui/501", m.plistPath()}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v", *calls)
	}
	m.run = func(args ...string) (string, error) {
		*calls = append(*calls, append([]string(nil), args...))
		return "\tstate = running\n\tpid = 42\n\tlast exit code = 0\n", nil
	}
	status, err := m.status()
	if err != nil || !strings.Contains(status, "last exit code = 0") || !strings.Contains(status, "state = running") {
		t.Fatalf("status %q, %v", status, err)
	}
	if err := m.remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist remains: %v", err)
	}
	if got := (*calls)[len(*calls)-1]; !reflect.DeepEqual(got, []string{"bootout", "gui/501/" + label}) {
		t.Fatalf("remove command: %v", got)
	}
	if err := m.remove(); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesForeignAndModifiedFiles(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "modified"}[managed], func(t *testing.T) {
			m, exe, calls := fixture(t)
			original := m.plist(exe)
			if err := atomicWrite(m.plistPath(), original); err != nil {
				t.Fatal(err)
			}
			if managed {
				if err := atomicWrite(m.manifestPath(), digest(original)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(m.plistPath(), append(original, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(m.plistPath())
			if err := m.remove(); err == nil {
				t.Fatal("remove accepted foreign/modified file")
			}
			if _, err := m.status(); err == nil {
				t.Fatal("status accepted foreign/modified file")
			}
			if err := m.install(exe); err == nil {
				t.Fatal("install accepted foreign/modified file")
			}
			after, _ := os.ReadFile(m.plistPath())
			if !bytes.Equal(before, after) || len(*calls) != 0 {
				t.Fatal("changed file or contacted launchctl")
			}
		})
	}
}

func TestUnsupportedAndRoot(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		m, exe, calls := fixture(t)
		m.goos = goos
		if goos == "darwin" {
			m.uid = 0
		}
		if err := m.install(exe); err == nil {
			t.Fatal("install unexpectedly supported")
		}
		if err := m.remove(); err == nil {
			t.Fatal("remove unexpectedly supported")
		}
		if _, err := m.status(); err == nil {
			t.Fatal("status unexpectedly supported")
		}
		if len(*calls) != 0 {
			t.Fatal("launchctl invoked")
		}
	}
}

func TestErrorsDoNotDeleteOrLaunch(t *testing.T) {
	m, exe, calls := fixture(t)
	if err := m.install("relative"); err == nil {
		t.Fatal("relative executable accepted")
	}
	if err := m.install(filepath.Join(m.home, "absent")); err == nil {
		t.Fatal("missing executable accepted")
	}
	if err := os.MkdirAll(filepath.Dir(m.plistPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.plistPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.owned(); err == nil {
		t.Fatal("directory accepted")
	}
	if len(*calls) != 0 {
		t.Fatal("launchctl invoked")
	}
	if err := os.Remove(m.plistPath()); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(m.plistPath(), m.plist(exe)); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(m.manifestPath(), digest(m.plist(exe))); err != nil {
		t.Fatal(err)
	}
	m.run = func(...string) (string, error) { return "permission denied", errors.New("denied") }
	if err := m.remove(); err == nil {
		t.Fatal("launchctl failure ignored")
	}
	if _, err := os.Stat(m.plistPath()); err != nil {
		t.Fatal("deleted file after launchctl failure")
	}
}

func TestAtomicWriteError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(p, "child"), []byte("bad")); err == nil {
		t.Fatal("invalid parent accepted")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "keep" {
		t.Fatal("parent modified")
	}
}

func TestBootstrapFailureLeavesRemovableFiles(t *testing.T) {
	m, exe, _ := fixture(t)
	m.run = func(args ...string) (string, error) {
		if args[0] == "print" {
			return "Could not find service", errors.New("absent")
		}
		return "bootstrap failed", errors.New("denied")
	}
	if err := m.install(exe); err == nil || !strings.Contains(err.Error(), "bootstrap failed") {
		t.Fatalf("error = %v", err)
	}
	if data, err := m.owned(); err != nil || data == nil {
		t.Fatalf("failed install not manageable: %v", err)
	}
	if status, err := m.status(); err != nil || !strings.Contains(status, "not loaded") {
		t.Fatalf("status = %q, %v", status, err)
	}
	if err := m.remove(); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesUnownedLoadedJob(t *testing.T) {
	m, exe, calls := fixture(t)
	m.run = func(args ...string) (string, error) {
		*calls = append(*calls, args)
		return "state = waiting", nil
	}
	if err := m.install(exe); err == nil {
		t.Fatal("unowned job accepted")
	}
	if len(*calls) != 1 || (*calls)[0][0] != "print" {
		t.Fatalf("commands = %v", *calls)
	}
	if _, err := os.Stat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote plist: %v", err)
	}
}

func TestBootoutFailurePreservesFiles(t *testing.T) {
	m, exe, _ := fixture(t)
	if err := m.install(exe); err != nil {
		t.Fatal(err)
	}
	m.run = func(args ...string) (string, error) {
		if args[0] == "print" {
			return "state = running", nil
		}
		return "denied", errors.New("permission denied")
	}
	if err := m.remove(); err == nil {
		t.Fatal("bootout failure ignored")
	}
	if data, err := m.owned(); data == nil || err != nil {
		t.Fatalf("ownership lost: %v", err)
	}
}
