package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	roundpkg "github.com/yinyongshuiyiguoqi/ghaccel/internal/round"
)

// Run manually with GHACCEL_GUI_TEST=1; it needs live access to GitHub.
func TestControllerStartStop(t *testing.T) {
	if os.Getenv("GHACCEL_GUI_TEST") == "" {
		t.Skip("set GHACCEL_GUI_TEST=1 to run the live controller test")
	}
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ApplyDefaults()
	cfg.Hosts.Path = hostsPath
	cfg.Hosts.FlushDNS = false
	cfg.Daemon.StateFile = filepath.Join(dir, "state.json")
	cfg.Logging.File = filepath.Join(dir, "gui.log")
	cfg.Domains = []string{"api.github.com", "codeload.github.com"}

	rounds := make(chan roundpkg.Result, 2)
	c := NewController(cfg, Callbacks{
		OnRound:  func(res roundpkg.Result) { rounds <- res },
		OnStatus: func(s Status) {},
		OnLog:    func(string) {},
	})
	defer c.Close()

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-rounds:
		if len(res.Domains) != 2 {
			t.Fatalf("expected 2 domains, got %d", len(res.Domains))
		}
	case <-time.After(90 * time.Second):
		t.Fatal("timed out waiting for a round")
	}
	if err := c.Stop(false); err != nil {
		t.Fatal(err)
	}
}
