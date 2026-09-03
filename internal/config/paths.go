package config

import (
	"os"
	"path/filepath"
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
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "another")
	}
	return filepath.Join(HomeDir(), ".cache", "another")
}

func ConfigDir() string {
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
