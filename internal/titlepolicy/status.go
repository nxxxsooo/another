package titlepolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nxxxsooo/another/internal/config"
)

// Status is one bounded diagnostic, never session content, model output or an
// upstream error string (which may contain authenticated URLs or prompt text).
type Status struct {
	At      time.Time `json:"at"`
	Checked int       `json:"checked"`
	Renamed int       `json:"renamed"`
	Failed  int       `json:"failed"`
	Error   string    `json:"error,omitempty"`
	// LastRenamedAt and LastRenamedTitle survive later no-op events, which are
	// common when the desktop client also observes a CLI turn. Without them a
	// successful rename can be hidden by the next conforming check.
	LastRenamedAt    time.Time `json:"last_renamed_at,omitempty"`
	LastRenamedTitle string    `json:"last_renamed_title,omitempty"`
}

func StatusPath() string { return filepath.Join(config.CacheDir(), "doubao-title-policy-status.json") }

type ListenerStatus struct {
	ReadyAt time.Time `json:"listener_ready"`
	PID     int       `json:"pid"`
}

func ListenerStatusPath() string {
	return filepath.Join(config.CacheDir(), "doubao-title-policy-listener.json")
}

// RecordListenerReady is called only after the watcher has baselined logs and
// installed its watches. It does not overwrite the last event's diagnostic.
func RecordListenerReady() error {
	data, err := json.Marshal(ListenerStatus{ReadyAt: time.Now().UTC(), PID: os.Getpid()})
	if err != nil {
		return err
	}
	return atomicStatus(ListenerStatusPath(), data)
}

func LoadListenerStatus() (ListenerStatus, error) {
	var status ListenerStatus
	data, err := os.ReadFile(ListenerStatusPath())
	if err != nil {
		return status, err
	}
	err = json.Unmarshal(data, &status)
	return status, err
}

func LoadStatus() (Status, error) {
	var status Status
	data, err := os.ReadFile(StatusPath())
	if err != nil {
		return status, err
	}
	err = json.Unmarshal(data, &status)
	return status, err
}

func saveStatus(status Status) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return atomicStatus(StatusPath(), data)
}

func atomicStatus(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".doubao-title-policy-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
