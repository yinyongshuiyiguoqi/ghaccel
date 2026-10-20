package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	dryRun := fs.Bool("dry-run", false, "show what would be removed")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, _, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}
	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	m.ConflictPolicy = cfg.Hosts.ConflictPolicy

	original, err := m.Read()
	if err != nil {
		return fail(err, exitError)
	}
	current, present, err := hosts.Mappings(original, m.Markers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hosts block is malformed:", err)
		return exitConflict
	}
	if !present {
		fmt.Println("no ghaccel block found; nothing to do")
		return exitOK
	}
	if *dryRun {
		fmt.Printf("would remove %d entries from %s:\n", len(current), cfg.Hosts.Path)
		for d, ip := range current {
			fmt.Printf("  %s %s\n", ip, d)
		}
		return exitOK
	}

	changed, err := m.Remove()
	if err != nil {
		if errors.Is(err, hosts.ErrNeedAdmin) {
			adminHint("ghaccel restore")
			return exitNeedAdmin
		}
		if errors.Is(err, hosts.ErrMalformedBlock) {
			fmt.Fprintln(os.Stderr, "hosts block is malformed and cannot be removed automatically")
			return exitConflict
		}
		return fail(err, exitError)
	}
	if changed {
		fmt.Printf("removed ghaccel block from %s\n", cfg.Hosts.Path)
		if st, err := state.Load(cfg.Daemon.StateFile); err == nil {
			st.Selected = map[string]state.Selection{}
			st.Degraded = nil
			st.Changed = false
			st.LastRound = time.Time{}
			_ = state.Save(cfg.Daemon.StateFile, st)
		}
	} else {
		fmt.Println("hosts unchanged")
	}
	return exitOK
}
