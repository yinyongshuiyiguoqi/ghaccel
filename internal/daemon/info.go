package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Info struct {
	PID     int       `json:"pid"`
	Port    int       `json:"port"`
	Token   string    `json:"token"`
	Started time.Time `json:"started"`
}

func InfoPath(stateFile string) string {
	if stateFile == "" {
		return filepath.Join(os.TempDir(), "ghaccel-daemon.json")
	}
	return filepath.Join(filepath.Dir(stateFile), "daemon.json")
}

func LockPath(stateFile string) string {
	return filepath.Join(filepath.Dir(InfoPath(stateFile)), "daemon.lock")
}

func LoadInfo(stateFile string) (Info, error) {
	var i Info
	b, err := os.ReadFile(InfoPath(stateFile))
	if err != nil {
		return i, err
	}
	return i, json.Unmarshal(b, &i)
}

func SaveInfo(stateFile string, i Info) error {
	path := InfoPath(stateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".daemon-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func ClearInfo(stateFile string) {
	_ = os.Remove(InfoPath(stateFile))
}
