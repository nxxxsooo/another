// Package doubaoservice manages an opt-in per-user macOS title event listener.
package doubaoservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/config"
)

const label = "com.another.doubao-title-policy"
const marker = "<!-- Managed by another: doubao-title-policy v1 -->"

type manager struct {
	home, cache, pathEnv string
	uid                  int
	goos                 string
	run                  func(...string) (string, error)
	env                  func(string) string
	statExecutable       func(string) (os.FileInfo, error)
}

func current() manager {
	return manager{home: config.HomeDir(), cache: config.CacheDir(), pathEnv: os.Getenv("PATH"), uid: os.Getuid(), goos: runtime.GOOS, run: launchctl, env: os.Getenv}
}

// Install registers a long-lived launchd event listener using the given absolute executable.
// It never invokes a shell. Existing files must match our ownership digest.
func Install(executable string) error { return current().install(executable) }

// Remove unloads the job and removes only unchanged, managed files.
func Remove() error { return current().remove() }

// Status reports installation, the plist path, and launchd's state/last exit.
func Status() (string, error) { return current().status() }

func launchctl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m manager) loaded() (bool, string, error) {
	out, err := m.run("print", m.domain()+"/"+label)
	if err == nil {
		return true, out, nil
	}
	var exit *exec.ExitError
	if (errors.As(err, &exit) && exit.ExitCode() == 113) || strings.Contains(out, "Could not find service") {
		return false, out, nil
	}
	return false, out, err
}

func (m manager) check() error {
	if m.goos != "darwin" {
		return fmt.Errorf("doubao background title policy requires macOS launchd (unsupported on %s)", m.goos)
	}
	if m.uid <= 0 {
		return errors.New("doubao background title policy must run as a non-root user")
	}
	if !filepath.IsAbs(m.home) || !filepath.IsAbs(m.cache) {
		return errors.New("user home and cache paths must be absolute")
	}
	return nil
}

func (m manager) plistPath() string {
	return filepath.Join(m.home, "Library", "LaunchAgents", label+".plist")
}
func (m manager) manifestPath() string { return filepath.Join(m.cache, "doubao-title-policy.sha256") }
func (m manager) domain() string       { return "gui/" + strconv.Itoa(m.uid) }
func digest(data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "\n")
}

// owned refuses symlinks, foreign plists, and edits made since installation.
func (m manager) owned() ([]byte, error) {
	p := m.plistPath()
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular service file %s", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	st, err = os.Lstat(m.manifestPath())
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing service without regular ownership manifest: %s", p)
	}
	saved, err := os.ReadFile(m.manifestPath())
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(data, []byte(marker)) || !bytes.Equal(saved, digest(data)) {
		return nil, fmt.Errorf("refusing modified or foreign service file %s", p)
	}
	return data, nil
}

func escaped(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (m manager) plist(executable string) []byte {
	var b strings.Builder
	b.WriteString(xml.Header + marker + "\n<plist version=\"1.0\"><dict>\n<key>Label</key><string>" + label + "</string>\n<key>ProgramArguments</key><array>\n")
	for _, arg := range []string{executable, "title-policy", "doubao", "--watch"} {
		b.WriteString("<string>" + escaped(arg) + "</string>\n")
	}
	b.WriteString("</array>\n<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n<key>RunAtLoad</key><true/>\n<key>WorkingDirectory</key><string>" + escaped(m.home) + "</string>\n<key>EnvironmentVariables</key><dict>\n<key>HOME</key><string>" + escaped(m.home) + "</string>\n<key>PATH</key><string>" + escaped(m.pathEnv) + "</string>\n")
	// Only non-secret directory overrides are retained, never the full environment.
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "DOUBAO_PROFILE"} {
		if m.env != nil {
			if value := m.env(key); value != "" {
				b.WriteString("<key>" + key + "</key><string>" + escaped(value) + "</string>\n")
			}
		}
	}
	// The listener can emit indefinitely. Discard output rather than create unbounded
	// launchd logs; Status exposes launchd's state and last exit code instead.
	b.WriteString("</dict>\n<key>SoftResourceLimits</key><dict><key>NumberOfFiles</key><integer>16384</integer></dict>\n<key>StandardOutPath</key><string>/dev/null</string>\n<key>StandardErrorPath</key><string>/dev/null</string>\n</dict></plist>\n")
	return []byte(b.String())
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".doubao-service-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (m manager) install(executable string) error {
	if err := m.check(); err != nil {
		return err
	}
	if !filepath.IsAbs(executable) {
		return errors.New("service executable must be an absolute path")
	}
	stat := m.statExecutable
	if stat == nil {
		stat = os.Stat
	}
	st, err := stat(executable)
	if err != nil {
		return fmt.Errorf("service executable: %w", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return errors.New("service executable must be an executable regular file")
	}
	old, err := m.owned()
	if err != nil {
		return err
	}
	if old == nil {
		if _, err := os.Lstat(m.manifestPath()); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("refusing existing ownership manifest %s", m.manifestPath())
		}
	}
	// Never unload an unowned job that happens to use our label.
	loaded, _, err := m.loaded()
	if err != nil {
		return err
	}
	if loaded {
		if old == nil {
			return errors.New("refusing existing launchd job without managed service file")
		}
		if _, err := m.run("bootout", m.domain()+"/"+label); err != nil {
			return err
		}
	}
	data := m.plist(executable)
	if err := atomicWrite(m.plistPath(), data); err != nil {
		return err
	}
	if err := atomicWrite(m.manifestPath(), digest(data)); err != nil {
		if old == nil {
			_ = os.Remove(m.plistPath())
		} else {
			_ = atomicWrite(m.plistPath(), old)
		}
		return err
	}
	if _, err := m.run("bootstrap", m.domain(), m.plistPath()); err != nil {
		return fmt.Errorf("service files installed but bootstrap failed (retry install or remove): %w", err)
	}
	return nil
}

func (m manager) remove() error {
	if err := m.check(); err != nil {
		return err
	}
	data, err := m.owned()
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	loaded, _, err := m.loaded()
	if err != nil {
		return err
	}
	if loaded {
		if _, err := m.run("bootout", m.domain()+"/"+label); err != nil {
			return err
		}
	}
	if err := os.Remove(m.plistPath()); err != nil {
		return err
	}
	return os.Remove(m.manifestPath())
}

func (m manager) status() (string, error) {
	if err := m.check(); err != nil {
		return "", err
	}
	data, err := m.owned()
	if err != nil {
		return "", err
	}
	if data == nil {
		return "not installed", nil
	}
	loaded, out, err := m.loaded()
	if err != nil {
		return "installed; launchd query failed: " + m.plistPath(), err
	}
	if !loaded {
		return "installed, not loaded: " + m.plistPath(), nil
	}
	lines := []string{"installed: " + m.plistPath(), "event listener; output: discarded (launchd last exit retained)"}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state =") || strings.HasPrefix(line, "pid =") || strings.HasPrefix(line, "last exit code =") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), nil
}
