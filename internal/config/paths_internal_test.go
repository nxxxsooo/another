package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// An explicit data-home override wins on every platform, because it is the
// operator saying where the agent actually is — no probing second-guesses it.
func TestAgentDataRootHonorsExplicitOverride(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	if got := AgentDataRoot("opencode"); got != os.Getenv("XDG_DATA_HOME") {
		t.Fatalf("AgentDataRoot ignored XDG_DATA_HOME: %q", got)
	}
}

// A provider data-root env that names a direct child of the temp root is a
// sandbox another tool spun up around one agent run, not user configuration.
// Adopting it re-roots discovery at that sandbox while reconcile erases every
// real session row, so it must fall back to the real home. Test fixtures and
// deliberate relocations nest deeper or live outside the temp tree entirely,
// and stay honored.
func TestEnvRootOrDefaultRefusesTempTreeSandboxes(t *testing.T) {
	const env = "ANOTHER_TEST_AGENT_HOME"

	t.Setenv(env, filepath.Join(os.TempDir(), "hindsight-claude-code-sandbox"))
	if got := EnvRootOrDefault(env, "/real/home"); got != "/real/home" {
		t.Fatalf("sandboxed %s adopted: %q", env, got)
	}

	// A database file placed directly in such a sandbox is the same accident.
	t.Setenv(env, filepath.Join(os.TempDir(), "hindsight-opencode2.db"))
	if got := EnvRootOrDefault(env, "/real/home"); got != "/real/home" {
		t.Fatalf("sandboxed %s file adopted: %q", env, got)
	}

	t.Setenv(env, filepath.Join(t.TempDir(), "agent-home"))
	if got := EnvRootOrDefault(env, "/real/home"); got != os.Getenv(env) {
		t.Fatalf("nested temp fixture refused: %q", got)
	}

	// A database file below a nested fixture is what the provider tests use;
	// it must keep working.
	t.Setenv(env, filepath.Join(t.TempDir(), "opencode2.db"))
	if got := EnvRootOrDefault(env, "/real/home"); got != os.Getenv(env) {
		t.Fatalf("nested temp fixture file refused: %q", got)
	}

	t.Setenv(env, "/opt/agent-home")
	if got := EnvRootOrDefault(env, "/real/home"); got != "/opt/agent-home" {
		t.Fatalf("ordinary override refused: %q", got)
	}

	t.Setenv(env, "")
	if got := EnvRootOrDefault(env, "/real/home"); got != "/real/home" {
		t.Fatalf("empty %s did not fall back: %q", env, got)
	}
}

// The probing only runs on Windows; everywhere else the legacy layout is the
// whole answer, and this pins that so a refactor cannot silently move
// existing Unix stores.
func TestAgentDataRootDefaultsToPlatformLayout(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	got := AgentDataRoot("opencode")
	if runtime.GOOS == "windows" {
		dir, err := os.UserCacheDir()
		if err != nil {
			t.Skip("no cache directory on this machine")
		}
		if got != dir {
			t.Fatalf("AgentDataRoot = %q, want %%LOCALAPPDATA%% %q", got, dir)
		}
		return
	}
	if want := filepath.Join(HomeDir(), ".local", "share"); got != want {
		t.Fatalf("AgentDataRoot = %q, want %q", got, want)
	}
}

// pickDataRoot carries the Windows choice so a fake filesystem can exercise
// every branch where Windows itself is unavailable.
func TestPickDataRootPrefersNativeFallsBackToLegacy(t *testing.T) {
	native, legacy := `C:\Users\a\AppData\Local`, `/home/a/.local/share`
	exists := func(existing ...string) func(string) bool {
		return func(p string) bool {
			for _, e := range existing {
				if p == e {
					return true
				}
			}
			return false
		}
	}
	// A native install is found where a native install lands.
	if got := pickDataRoot(native, legacy, "opencode", exists(filepath.Join(native, "opencode"))); got != native {
		t.Fatalf("existing native layout lost to %q", got)
	}
	// An agent that keeps one layout everywhere is still found.
	if got := pickDataRoot(native, legacy, "opencode", exists(filepath.Join(legacy, "opencode"))); got != legacy {
		t.Fatalf("existing legacy layout lost to %q", got)
	}
	// Neither existing is not an error: paths point at the platform-preferred
	// home and Installed() reports false.
	if got := pickDataRoot(native, legacy, "opencode", exists()); got != native {
		t.Fatalf("empty machine resolved to %q, want the native home", got)
	}
}

func TestOwnDirsFollowThePlatform(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if runtime.GOOS == "windows" {
		if dir, err := os.UserCacheDir(); err == nil {
			if got := CacheDir(); got != filepath.Join(dir, "another") {
				t.Fatalf("CacheDir = %q, want %%LOCALAPPDATA%%-based path", got)
			}
		}
		if dir, err := os.UserConfigDir(); err == nil {
			if got := ConfigDir(); got != filepath.Join(dir, "another") {
				t.Fatalf("ConfigDir = %q, want %%APPDATA%%-based path", got)
			}
		}
		return
	}
	if got := CacheDir(); got != filepath.Join(HomeDir(), ".cache", "another") {
		t.Fatalf("CacheDir moved existing Unix state to %q", got)
	}
	if got := ConfigDir(); got != filepath.Join(HomeDir(), ".config", "another") {
		t.Fatalf("ConfigDir moved existing Unix state to %q", got)
	}
}
