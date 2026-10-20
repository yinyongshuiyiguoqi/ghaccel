package hosts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *Manager) backup(original []byte) error {
	if m.BackupDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.BackupDir, 0o755); err != nil {
		return err
	}
	baseline := filepath.Join(m.BackupDir, "hosts.before-ghaccel")
	if _, err := os.Stat(baseline); os.IsNotExist(err) {
		if err := os.WriteFile(baseline, original, 0o644); err != nil {
			return err
		}
	}
	name := "hosts-" + time.Now().Format("20060102-150405.000") + ".bak"
	if err := os.WriteFile(filepath.Join(m.BackupDir, name), original, 0o644); err != nil {
		return err
	}
	return m.prune()
}

func (m *Manager) prune() error {
	if m.BackupCount <= 0 {
		return nil
	}
	entries, err := os.ReadDir(m.BackupDir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "hosts-") && strings.HasSuffix(n, ".bak") {
			names = append(names, n)
		}
	}
	if len(names) <= m.BackupCount {
		return nil
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-m.BackupCount] {
		_ = os.Remove(filepath.Join(m.BackupDir, n))
	}
	return nil
}
