package doubao

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func hasCookies(profile string) bool {
	info, err := os.Stat(filepath.Join(profile, "Cookies"))
	return err == nil && !info.IsDir()
}

// Select exactly one profile without trying credentials from other accounts.
// A stale active profile is an error, not permission to try a different login.
func selectProfile(root, explicit string) (string, error) {
	if explicit != "" {
		if !hasCookies(explicit) {
			return "", fmt.Errorf("DOUBAO_PROFILE has no Cookies database")
		}
		return explicit, nil
	}
	b, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err == nil {
		var state struct {
			Profile struct {
				LastUsed string `json:"last_used"`
			} `json:"profile"`
		}
		if json.Unmarshal(b, &state) != nil {
			return "", fmt.Errorf("invalid Doubao Local State; set DOUBAO_PROFILE explicitly")
		}
		name := state.Profile.LastUsed
		if name != "" {
			if filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
				return "", fmt.Errorf("invalid Doubao active profile")
			}
			profile := filepath.Join(root, name)
			if !hasCookies(profile) {
				return "", fmt.Errorf("active Doubao profile has no Cookies database; set DOUBAO_PROFILE explicitly")
			}
			return profile, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("doubao desktop profile unavailable; set DOUBAO_PROFILE: %w", err)
	}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() && (e.Name() == "Default" || strings.HasPrefix(e.Name(), "Profile ")) && hasCookies(filepath.Join(root, e.Name())) {
			candidates = append(candidates, filepath.Join(root, e.Name()))
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("doubao has %d candidate profiles; set DOUBAO_PROFILE explicitly", len(candidates))
	}
	return candidates[0], nil
}
