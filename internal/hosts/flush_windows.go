//go:build windows

package hosts

import (
	"os/exec"
	"syscall"
)

func Flush() error {
	cmd := exec.Command("ipconfig", "/flushdns")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
