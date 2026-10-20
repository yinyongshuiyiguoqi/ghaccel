//go:build !windows

package hosts

import "os/exec"

func Flush() error {
	candidates := [][]string{
		{"resolvectl", "flush-caches"},
		{"systemd-resolve", "--flush-caches"},
		{"dscacheutil", "-flushcache"},
	}
	for _, cmd := range candidates {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			continue
		}
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err == nil {
			return nil
		}
	}
	return exec.Command("killall", "-HUP", "mDNSResponder").Run()
}
