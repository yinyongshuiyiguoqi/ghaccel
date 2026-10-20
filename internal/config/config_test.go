package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestInvalidDomain(t *testing.T) {
	c := Default()
	c.ExtraDomains = []string{"not a domain"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid domain error")
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hosts.Path == "" || len(c.DoH.Providers) == 0 {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestLoadConfigWithBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := []byte{0xEF, 0xBB, 0xBF}
	body = append(body, []byte(`{"schema_version":1}`)...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("BOM config rejected: %v", err)
	}
	if c.Probe.Attempts != 2 {
		t.Fatalf("defaults not merged: %+v", c.Probe)
	}
}
