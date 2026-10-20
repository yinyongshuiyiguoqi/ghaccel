package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/OWNER/ghaccel/internal/config"
	"github.com/OWNER/ghaccel/internal/hosts"
	"github.com/OWNER/ghaccel/internal/probe"
	"github.com/OWNER/ghaccel/internal/resolver"
	"github.com/OWNER/ghaccel/internal/state"
	"github.com/OWNER/ghaccel/internal/version"
)

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorOutput struct {
	OK     bool          `json:"ok"`
	Strict bool          `json:"strict"`
	Checks []doctorCheck `json:"checks"`
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	strict := fs.Bool("strict", false, "treat warnings as failures")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	log := g.logger()
	cfg, cfgPath, cfgErr := loadConfig(g)
	if cfgErr != nil {
		return fail(cfgErr, exitUsage)
	}

	out := doctorOutput{Strict: *strict}
	add := func(name, status, detail string) {
		out.Checks = append(out.Checks, doctorCheck{Name: name, Status: status, Detail: detail})
	}

	add("version", "ok", version.String())
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		add("config", "warn", "config not found, using defaults; run: ghaccel init")
	} else {
		add("config", "ok", cfgPath)
	}

	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	m.ConflictPolicy = cfg.Hosts.ConflictPolicy
	if err := m.CheckWritable(); err != nil {
		add("hosts", "warn", cfg.Hosts.Path+" (write needs administrator; --dry-run works without it)")
	} else {
		add("hosts", "ok", cfg.Hosts.Path+" (writable)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	breaker := resolver.NewBreaker(cfg.DoH.Breaker.FailThreshold, time.Duration(cfg.DoH.Breaker.OpenSeconds)*time.Second)
	var doh *resolver.DoHClient
	if cfg.DoH.Enabled {
		doh = resolver.NewDoH(cfg.DoH.Providers, cfg.DoH.UseSystemProxy, breaker, log)
	}
	start := time.Now()
	var results map[string][]string
	var errs map[string]error
	if doh != nil {
		results, errs = doh.ResolveAll(ctx, "github.com")
	}
	names := make([]string, 0, len(results))
	for n := range results {
		names = append(names, n)
	}
	sort.Strings(names)
	if !cfg.DoH.Enabled {
		add("doh", "warn", "DoH disabled; relying on built-in seeds and previous IPs")
	} else if len(names) > 0 {
		parts := make([]string, 0, len(names))
		for _, n := range names {
			parts = append(parts, fmt.Sprintf("%s=%s", n, strings.Join(results[n], ",")))
		}
		add("doh", "ok", fmt.Sprintf("%dms %s", time.Since(start).Milliseconds(), strings.Join(parts, " ")))
	} else {
		detail := "no DoH provider returned an answer"
		if len(errs) > 0 {
			keys := make([]string, 0, len(errs))
			for k := range errs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, k+": "+errs[k].Error())
			}
			detail += " (" + strings.Join(parts, "; ") + ")"
		}
		add("doh", "fail", detail)
	}

	res := resolver.New(doh, 4, log)
	cands, err := res.Resolve(ctx, "github.com", nil)
	if err != nil {
		add("tls", "fail", "could not collect candidates: "+err.Error())
	} else {
		opts := probe.Options{
			Attempts:       cfg.Probe.Attempts,
			TCPTimeout:     cfg.Probe.TCPTimeout(),
			TLSTimeout:     cfg.Probe.TLSTimeout(),
			AttemptTimeout: cfg.Probe.AttemptTimeout(),
			HTTPHead:       cfg.Probe.HTTPHead,
			HTTPTimeout:    cfg.Probe.HTTPTimeout(),
			HTTPMaxStatus:  cfg.Probe.HTTPMaxStatus,
			MinSuccesses:   1,
			FailurePenalty: cfg.Probe.FailurePenaltyMS,
			JitterWeight:   cfg.Probe.JitterWeight,
		}
		jobs := make([]probe.Job, 0, len(cands))
		for _, c := range cands {
			jobs = append(jobs, probe.Job{Domain: "github.com", IP: c.IP, Sources: c.Sources})
		}
		results := probe.New(opts, minInt(4, len(jobs))).Run(ctx, jobs)
		best := probe.Result{}
		for _, r := range results {
			if r.Viable && (best.IP == "" || r.Score < best.Score) {
				best = r
			}
		}
		if best.IP != "" {
			add("tls", "ok", fmt.Sprintf("github.com -> %s score=%.0f", best.IP, best.Score))
		} else {
			add("tls", "fail", "no candidate passed TLS verification for github.com")
		}
	}

	if st, err := state.Load(cfg.Daemon.StateFile); err == nil && !st.LastRound.IsZero() {
		add("state", "ok", fmt.Sprintf("last round %s, %d domains", st.LastRound.Local().Format(time.RFC3339), len(st.Selected)))
	} else {
		add("state", "warn", "no completed round yet")
	}

	fails, warns := 0, 0
	for _, c := range out.Checks {
		switch c.Status {
		case "fail":
			fails++
		case "warn":
			warns++
		}
	}
	out.OK = fails == 0 && (!*strict || warns == 0)

	if g.json {
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fail(err, exitError)
		}
		fmt.Println(string(b))
	} else {
		for _, c := range out.Checks {
			fmt.Printf("[%-4s] %-8s %s\n", c.Status, c.Name, c.Detail)
		}
		if out.OK {
			fmt.Println("\nall checks passed")
		} else {
			fmt.Println("\nproblems found")
		}
	}

	if fails > 0 || (*strict && warns > 0) {
		return exitDegraded
	}
	return exitOK
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
