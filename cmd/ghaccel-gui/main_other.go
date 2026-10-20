//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "ghaccel-gui is only available on Windows; use the ghaccel CLI instead")
	os.Exit(1)
}
