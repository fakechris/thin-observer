// Package paths centralizes XDG directory resolution for thin-observer.
package paths

import (
	"os"
	"path/filepath"
)

// StateDir returns ~/.local/state/thin-observer (or $XDG_STATE_HOME equivalent).
func StateDir() string {
	if v := os.Getenv("THIN_OBSERVER_STATE"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "thin-observer")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "thin-observer")
}

// ConfigDir returns ~/.config/thin-observer.
func ConfigDir() string {
	if v := os.Getenv("THIN_OBSERVER_CONFIG"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "thin-observer")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "thin-observer")
}

func DBPath() string   { return filepath.Join(StateDir(), "db.sqlite") }
func EventsLog() string { return filepath.Join(StateDir(), "events.jsonl") }
func SnapshotsDir() string { return filepath.Join(StateDir(), "snapshots") }
func ConfigFile() string   { return filepath.Join(ConfigDir(), "config.yaml") }
