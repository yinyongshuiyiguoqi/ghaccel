package main

import (
	"os"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:]))
}
