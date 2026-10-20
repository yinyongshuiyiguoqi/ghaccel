//go:build windows

package main

import (
	"os"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}
