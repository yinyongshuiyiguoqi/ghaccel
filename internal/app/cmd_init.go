package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/OWNER/ghaccel/internal/config"
)

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	force := fs.Bool("force", false, "overwrite an existing config")
	printOnly := fs.Bool("print", false, "print the default config and exit")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg := config.Default()
	cfg.ApplyDefaults()
	path := g.configPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return fail(err, exitError)
		}
		path = p
	}

	if *printOnly {
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fail(err, exitError)
		}
		fmt.Println(string(b))
		return exitOK
	}

	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "config already exists: %s\nuse --force to overwrite\n", path)
		return exitUsage
	}
	if err := config.Save(path, cfg); err != nil {
		return fail(err, exitError)
	}
	fmt.Printf("config written: %s\n\nnext:\n  ghaccel doctor\n  ghaccel once --dry-run\n", path)
	return exitOK
}
