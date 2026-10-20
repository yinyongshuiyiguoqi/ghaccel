package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var ErrNeedAdmin = errors.New("administrator privileges are required to write the hosts file")

type ApplyResult struct {
	Changed bool
	Skipped []string
}

type Manager struct {
	Path           string
	Markers        Markers
	BackupDir      string
	BackupCount    int
	FlushDNS       bool
	MaxFileBytes   int64
	ConflictPolicy string
}

func New(path string, markers Markers, backupDir string, backupCount int, flush bool, maxFileBytes int64) *Manager {
	return &Manager{
		Path:           path,
		Markers:        markers,
		BackupDir:      backupDir,
		BackupCount:    backupCount,
		FlushDNS:       flush,
		MaxFileBytes:   maxFileBytes,
		ConflictPolicy: "skip",
	}
}

func (m *Manager) CheckWritable() error {
	f, err := os.OpenFile(m.Path, os.O_WRONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: %v", ErrNeedAdmin, err)
		}
		return fmt.Errorf("open hosts for writing: %w", err)
	}
	return f.Close()
}

func (m *Manager) Read() ([]byte, error) {
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return nil, err
	}
	if m.MaxFileBytes > 0 && int64(len(b)) > m.MaxFileBytes {
		return nil, fmt.Errorf("hosts file is larger than %d bytes", m.MaxFileBytes)
	}
	return b, nil
}

func (m *Manager) Current() (map[string]string, bool, error) {
	b, err := m.Read()
	if err != nil {
		return nil, false, err
	}
	return Mappings(b, m.Markers)
}

func (m *Manager) Apply(mappings map[string]string) (ApplyResult, error) {
	var res ApplyResult
	if err := m.CheckWritable(); err != nil {
		return res, err
	}
	unlock, err := m.lock()
	if err != nil {
		return res, err
	}
	defer unlock()

	original, err := m.Read()
	if err != nil {
		return res, err
	}
	work := make(map[string]string, len(mappings))
	for k, v := range mappings {
		work[k] = v
	}

	policy := m.ConflictPolicy
	if policy == "" {
		policy = "skip"
	}
	updated, skipped, err := Splice(original, work, m.Markers, policy)
	if err != nil {
		return res, err
	}
	res.Skipped = skipped
	if bytes.Equal(original, updated) {
		return res, nil
	}

	if err := m.backup(original); err != nil {
		return res, fmt.Errorf("backup hosts: %w", err)
	}
	if err := m.atomicWrite(updated); err != nil {
		return res, err
	}
	res.Changed = true
	if m.FlushDNS {
		_ = Flush()
	}
	return res, nil
}

func (m *Manager) Remove() (bool, error) {
	if err := m.CheckWritable(); err != nil {
		return false, err
	}
	unlock, err := m.lock()
	if err != nil {
		return false, err
	}
	defer unlock()

	original, err := m.Read()
	if err != nil {
		return false, err
	}
	updated, changed, err := Remove(original, m.Markers)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := m.backup(original); err != nil {
		return false, fmt.Errorf("backup hosts: %w", err)
	}
	if err := m.atomicWrite(updated); err != nil {
		return false, err
	}
	if m.FlushDNS {
		_ = Flush()
	}
	return true, nil
}

func (m *Manager) atomicWrite(content []byte) error {
	dir := filepath.Dir(m.Path)
	tmp, err := os.CreateTemp(dir, ".ghaccel-hosts-*")
	if err != nil {
		return fmt.Errorf("create temp hosts: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	var renameErr error
	for _, delay := range []time.Duration{0, 50 * time.Millisecond, 150 * time.Millisecond, 400 * time.Millisecond} {
		if delay > 0 {
			time.Sleep(delay)
		}
		renameErr = os.Rename(tmpName, m.Path)
		if renameErr == nil {
			break
		}
	}
	if renameErr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace hosts: %w", renameErr)
	}

	check, err := os.ReadFile(m.Path)
	if err != nil {
		return fmt.Errorf("verify hosts: %w", err)
	}
	if !bytes.Equal(check, content) {
		return errors.New("verify hosts: content differs after write")
	}
	return nil
}

func (m *Manager) lock() (func(), error) {
	dir := m.BackupDir
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "hosts.lock")
	for i := 0; i < 2; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, statErr := os.Stat(path); statErr == nil && time.Since(st.ModTime()) > 10*time.Minute {
			_ = os.Remove(path)
			continue
		}
		return nil, errors.New("another ghaccel process is writing the hosts file")
	}
	return nil, errors.New("could not acquire hosts lock")
}
