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
	// TitleModel is the agent CLI another asks for title suggestions. It is
	// absent until a person picks one, and absent means the feature is off:
	// another never calls a model on its own initiative.
	TitleModel *TitleModel `json:"title_model,omitempty"`
	// TitlePolicy is shared with native-client adapters such as OpenCode 2
	// and Pi. It remains available even when another's suggestion model is
	// disabled. Missing language means the new default, auto.
	TitlePolicy TitlePolicy `json:"title_policy,omitempty"`
	// UI is how another speaks to the person running it, which is a
	// different question from what language it writes titles in. A config
	// written before this existed has no ui at all, and that means auto.
	UI UI `json:"ui,omitempty"`
	// Integrations records which adapters another may install into another
	// agent's own configuration. Writing into someone else's config
	// directory needs consent that outlives the run that gave it, so the
	// answer is stored rather than asked again on every upgrade. Absent
	// means no, which is what a person who has never been asked expects.
	Integrations Integrations `json:"integrations,omitempty"`
}

// Integrations is one flag per adapter another can install. The flag is
// permission and intent, not state: whether the files are actually in place,
// current, or edited is read from the agent's configuration directory.
type Integrations struct {
	OpenCode2TitlePolicy bool `json:"opencode2_title_policy,omitempty"`
}

type TitlePolicy struct {
	Language string `json:"language,omitempty"`
}

// UI collects preferences about another's own screens rather than the content
// it produces. Language is "en", "zh", or "auto" to follow the terminal's
// locale; absent means auto.
type UI struct {
	Language string `json:"language,omitempty"`
}

// TitleModel names an installed agent CLI, not an API credential. another
// stays local-first by borrowing an agent the user already authenticated
// instead of holding provider keys of its own. An empty Model uses that CLI's
// own default model.
type TitleModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	// Language is the vocabulary suggested titles are written in: "zh",
	// "en", or "auto" to follow each session. Absent means auto.
	Language string `json:"language,omitempty"`
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
	// Migrate either representation in memory. Keeping the policy separate
	// lets native clients share it when TitleModel is nil.
	if settings.TitlePolicy.Language == "" && settings.TitleModel != nil {
		settings.TitlePolicy.Language = settings.TitleModel.Language
	}
	if settings.TitleModel != nil && settings.TitleModel.Language == "" {
		settings.TitleModel.Language = settings.TitlePolicy.Language
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
	if settings.TitleModel != nil {
		if settings.TitlePolicy.Language == "" {
			settings.TitlePolicy.Language = settings.TitleModel.Language
		}
		settings.TitleModel.Language = settings.TitlePolicy.Language
	}
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
