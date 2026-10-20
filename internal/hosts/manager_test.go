package hosts

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerApplyAndRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	original := []byte("127.0.0.1 localhost\n::1 localhost\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(path, testMarkers, filepath.Join(dir, "backups"), 3, false, 1<<20)

	res, err := m.Apply(map[string]string{"github.com": "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected first apply to change the file")
	}
	got, _ := os.ReadFile(path)
	if !bytes.HasPrefix(got, original) {
		t.Fatalf("original content changed:\n%q", got)
	}
	if !bytes.Contains(got, []byte("1.2.3.4 github.com")) {
		t.Fatalf("block missing:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", "hosts.before-ghaccel")); err != nil {
		t.Fatalf("baseline backup missing: %v", err)
	}

	res2, err := m.Apply(map[string]string{"github.com": "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Changed {
		t.Fatal("identical mappings should not rewrite the file")
	}

	changed, err := m.Remove()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected remove to change the file")
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, original) {
		t.Fatalf("restore mismatch:\nwant=%q\ngot =%q", original, restored)
	}
}
