package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Settings struct {
	AutoStart bool `json:"auto_start"`
}

func defaultSettings() Settings {
	return Settings{AutoStart: true}
}

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ghaccel", "gui.json")
}

func loadSettings() Settings {
	s := defaultSettings()
	path := settingsPath()
	if path == "" {
		return s
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

func saveSettings(s Settings) error {
	path := settingsPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
