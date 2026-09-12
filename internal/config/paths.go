package config

import (
	"os"
	"path/filepath"
	"runtime"
)

func HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func ExpandPath(p string) string {
	if p == "" {
		return ""
	}
	if p[0] == '~' {
		return filepath.Join(HomeDir(), p[1:])
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(HomeDir(), p)
}

func CacheDir() string {
	// A Windows user looks for application state under %LOCALAPPDATA%, not in
	// dot-directories: os.UserCacheDir resolves exactly there, while the
	// fallback below keeps the long-standing ~/.cache/another everywhere else.
	// The macOS and Linux paths are deliberately untouched — moving an
	// existing index would orphan it, and there is no Windows index to orphan.
	if runtime.GOOS == "windows" {
		if dir, err := os.UserCacheDir(); err == nil && dir != "" {
			return filepath.Join(dir, "another")
		}
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "another")
	}
	return filepath.Join(HomeDir(), ".cache", "another")
}

func ConfigDir() string {
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "another")
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "another")
	}
	return filepath.Join(HomeDir(), ".config", "another")
}

func IndexPath() string {
	return filepath.Join(CacheDir(), "index.db")
}

func EnvOrDefault(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}

// AgentDataRoot resolves the data home of an agent that keeps one layout per
// platform under a shared directory (<root>/<app>), which is where OpenCode
// and OpenCode 2 put their databases.
//
// An explicit XDG_DATA_HOME wins on every platform. Otherwise Windows prefers
// %LOCALAPPDATA% — a native install lands there — while ~/.local/share stays
// valid for agents that keep one layout everywhere or run under a POSIX layer.
// The first layout whose app directory exists wins; when neither does, the
// platform-preferred one is returned so paths still point somewhere sensible
// and Installed() simply reports false.
func AgentDataRoot(app string) string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return xdg
	}
	legacy := filepath.Join(HomeDir(), ".local", "share")
	if runtime.GOOS != "windows" {
		return legacy
	}
	native, err := os.UserCacheDir()
	if err != nil || native == "" {
		return legacy
	}
	return pickDataRoot(native, legacy, app, isDir)
}

// pickDataRoot is the Windows choice on its own so every branch stays testable
// where Windows is not: a fake exists function stands in for the filesystem.
func pickDataRoot(native, legacy, app string, exists func(string) bool) string {
	if exists(filepath.Join(native, app)) {
		return native
	}
	if exists(filepath.Join(legacy, app)) {
		return legacy
	}
	return native
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
