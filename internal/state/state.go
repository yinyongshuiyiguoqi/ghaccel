package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Selection struct {
	IP       string   `json:"ip"`
	Score    float64  `json:"score"`
	Verified bool     `json:"verified"`
	Sources  []string `json:"sources,omitempty"`
}

type State struct {
	SchemaVersion int                  `json:"schema_version"`
	LastRound     time.Time            `json:"last_round"`
	DurationMS    int64                `json:"duration_ms"`
	Selected      map[string]Selection `json:"selected"`
	Degraded      []string             `json:"degraded,omitempty"`
	Changed       bool                 `json:"changed"`
}

func Load(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{SchemaVersion: 1, Selected: map[string]Selection{}}, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Selected == nil {
		s.Selected = map[string]Selection{}
	}
	return s, nil
}

func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
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
