package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const SettingsVersion = 1

type Settings struct {
	Version          int      `json:"version"`
	EnabledProviders []string `json:"enabled_providers"`
}

func SettingsPath() string { return filepath.Join(ConfigDir(), "config.json") }

func LoadSettings() (Settings, error) {
	data, err := os.ReadFile(SettingsPath())
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, err
	}
	if settings.Version != SettingsVersion {
		return Settings{}, errors.New("unsupported another config version")
	}
	return settings, nil
}

func SaveSettings(settings Settings) error {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(ConfigDir(), 0o700); err != nil {
		return err
	}
	settings.Version = SettingsVersion
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(ConfigDir(), ".config-*.json")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(path, SettingsPath())
}

func SettingsExist() bool {
	_, err := os.Stat(SettingsPath())
	return err == nil
}
