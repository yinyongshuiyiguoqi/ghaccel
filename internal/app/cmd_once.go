package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/domains"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	roundpkg "github.com/yinyongshuiyiguoqi/ghaccel/internal/round"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

func cmdOnce(args []string) int {
	fs := flag.NewFlagSet("once", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	dryRun := fs.Bool("dry-run", false, "resolve and probe without writing hosts")
	domainFlag := fs.String("domains", "", "comma-separated domain list (overrides config)")
	timeout := fs.Duration("timeout", 0, "round timeout, e.g. 60s")
	noHTTP := fs.Bool("no-http", false, "skip the HTTP HEAD verification")
	noFlush := fs.Bool("no-flush", false, "do not flush the DNS cache")
	hostsPath := fs.String("hosts", "", "override hosts file path (testing)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	log := g.logger()
	cfg, _, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}

	override := []string{}
	if *domainFlag != "" {
		for _, d := range strings.Split(*domainFlag, ",") {
			if d = strings.TrimSpace(d); d != "" {
				override = append(override, d)
			}
		}
	}
	if len(override) == 0 && len(cfg.Domains) > 0 {
		override = cfg.Domains
	}
	list := domains.Effective(override, cfg.ExtraDomains, cfg.ExcludeDomains)
	if len(list) == 0 {
		return fail(errors.New("no domains to process"), exitUsage)
	}

	if *hostsPath != "" {
		cfg.Hosts.Path = *hostsPath
	}
	if *noFlush {
		cfg.Hosts.FlushDNS = false
	}
	if *noHTTP {
		cfg.Probe.HTTPHead = false
	}

	manager := newHostsManager(cfg)

	if !*dryRun {
		if err := manager.CheckWritable(); err != nil {
			adminHint("ghaccel once")
			if errors.Is(err, hosts.ErrNeedAdmin) {
				return exitNeedAdmin
			}
			return fail(err, exitError)
		}
	}

	prev, err := state.Load(cfg.Daemon.StateFile)
	if err != nil {
		log.Warn("could not read previous state", "err", err)
	}

	budget := *timeout
	if budget <= 0 {
		budget = time.Duration(cfg.Refresh.RoundTimeoutSeconds) * time.Second
	}
	if budget <= 0 {
		budget = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	res, err := roundpkg.Execute(ctx, cfg, list, prev, *dryRun, manager, log)
	if err != nil {
		if errors.Is(err, hosts.ErrMalformedBlock) {
			fmt.Fprintln(os.Stderr, "hosts contains a malformed ghaccel block; run: ghaccel restore")
			return exitConflict
		}
		if errors.Is(err, hosts.ErrNeedAdmin) {
			adminHint("ghaccel once")
			return exitNeedAdmin
		}
		return fail(err, exitError)
	}
	res.DryRun = *dryRun

	if !*dryRun {
		if err := state.Save(cfg.Daemon.StateFile, roundpkg.StateFrom(res)); err != nil {
			log.Warn("could not save state", "err", err)
		}
	}

	if g.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return fail(err, exitError)
		}
		fmt.Println(string(b))
	} else {
		printRound(res, cfg.Hosts.Path)
	}

	if roundpkg.CoreDegraded(res) {
		return exitDegraded
	}
	return exitOK
}

func newHostsManager(cfg config.Config) *hosts.Manager {
	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	m.ConflictPolicy = cfg.Hosts.ConflictPolicy
	return m
}

func printRound(r roundpkg.Result, hostsPath string) {
	fmt.Printf("round %s  (%.1fs)\n\n", r.Round, float64(r.DurationMS)/1000)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DOMAIN\tIP\tSCORE\tSOURCES\tNOTE")
	for _, d := range r.Domains {
		mark := " "
		if d.Degraded {
			mark = "!"
		}
		score := "-"
		if d.IP != "" {
			score = fmt.Sprintf("%.0f", d.Score)
		}
		note := d.Error
		if len(note) > 60 {
			note = note[:57] + "..."
		}
		fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\t%s\n", mark, d.Domain, d.IP, score, strings.Join(d.Sources, ","), note)
	}
	w.Flush()

	fmt.Println()
	if r.DryRun {
		fmt.Println("dry-run: hosts not written")
		return
	}
	if r.Changed {
		fmt.Printf("hosts updated: %s\n", hostsPath)
	} else {
		fmt.Println("hosts unchanged")
	}
	if len(r.Skipped) > 0 {
		fmt.Printf("skipped (existing user entries): %s\n", strings.Join(r.Skipped, ", "))
	}
	if len(r.Degraded) > 0 {
		fmt.Printf("degraded: %s\n", strings.Join(r.Degraded, ", "))
	}
}
