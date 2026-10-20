package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/domains"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/probe"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/resolver"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

type domainOutcome struct {
	Domain     string   `json:"domain"`
	IP         string   `json:"ip,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Candidates int      `json:"candidates"`
	Verified   bool     `json:"verified"`
	Degraded   bool     `json:"degraded"`
	Error      string   `json:"error,omitempty"`
}

type roundResult struct {
	Round      string          `json:"round"`
	DryRun     bool            `json:"dry_run"`
	DurationMS int64           `json:"duration_ms"`
	Changed    bool            `json:"changed"`
	Skipped    []string        `json:"skipped,omitempty"`
	Degraded   []string        `json:"degraded,omitempty"`
	Domains    []domainOutcome `json:"domains"`
}

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
	cfg, cfgPath, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}
	_ = cfgPath

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

	backupDir, _ := config.BackupDir()
	manager := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	manager.ConflictPolicy = cfg.Hosts.ConflictPolicy

	if !*dryRun {
		if err := manager.CheckWritable(); err != nil {
			adminHint("ghaccel once")
			if errors.Is(err, hosts.ErrNeedAdmin) {
				return exitNeedAdmin
			}
			return fail(err, exitError)
		}
	}

	statePath := cfg.Daemon.StateFile
	prev, err := state.Load(statePath)
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

	round, err := executeRound(ctx, cfg, list, prev, *dryRun, manager, log)
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
	round.DryRun = *dryRun

	if !*dryRun {
		sel := map[string]state.Selection{}
		for _, d := range round.Domains {
			if d.IP != "" {
				sel[d.Domain] = state.Selection{IP: d.IP, Score: d.Score, Verified: d.Verified, Sources: d.Sources}
			}
		}
		st := state.State{
			SchemaVersion: 1,
			LastRound:     time.Now(),
			DurationMS:    round.DurationMS,
			Selected:      sel,
			Degraded:      round.Degraded,
			Changed:       round.Changed,
		}
		if err := state.Save(statePath, st); err != nil {
			log.Warn("could not save state", "err", err)
		}
	}

	if g.json {
		b, err := json.MarshalIndent(round, "", "  ")
		if err != nil {
			return fail(err, exitError)
		}
		fmt.Println(string(b))
	} else {
		printRound(round, cfg.Hosts.Path)
	}

	for _, d := range round.Domains {
		if d.Degraded && domains.IsRequired(d.Domain) {
			return exitDegraded
		}
	}
	return exitOK
}

func executeRound(ctx context.Context, cfg config.Config, list []string, prev state.State, dryRun bool, manager *hosts.Manager, log *slog.Logger) (roundResult, error) {
	start := time.Now()
	round := roundResult{Round: start.Format(time.RFC3339)}

	breaker := resolver.NewBreaker(cfg.DoH.Breaker.FailThreshold, time.Duration(cfg.DoH.Breaker.OpenSeconds)*time.Second)
	var doh *resolver.DoHClient
	if cfg.DoH.Enabled {
		doh = resolver.NewDoH(cfg.DoH.Providers, cfg.DoH.UseSystemProxy, breaker, log)
	}
	res := resolver.New(doh, cfg.Probe.MaxCandidatesPerDomain, log)

	type resolved struct {
		domain string
		cands  []resolver.Candidate
		err    error
	}
	outCh := make(chan resolved, len(list))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, d := range list {
		prevIPs := []string{}
		if s, ok := prev.Selected[d]; ok && s.IP != "" {
			prevIPs = append(prevIPs, s.IP)
		}
		wg.Add(1)
		go func(domain string, previous []string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				outCh <- resolved{domain: domain, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			cands, err := res.Resolve(ctx, domain, previous)
			outCh <- resolved{domain: domain, cands: cands, err: err}
		}(d, prevIPs)
	}
	wg.Wait()
	close(outCh)

	byDomain := make(map[string][]resolver.Candidate)
	resolveErr := make(map[string]error)
	for r := range outCh {
		if r.err != nil {
			resolveErr[r.domain] = r.err
			continue
		}
		byDomain[r.domain] = r.cands
	}

	var jobs []probe.Job
	for _, d := range list {
		for _, c := range byDomain[d] {
			jobs = append(jobs, probe.Job{Domain: d, IP: c.IP, Sources: c.Sources})
		}
	}
	opts := probe.Options{
		Attempts:       cfg.Probe.Attempts,
		TCPTimeout:     cfg.Probe.TCPTimeout(),
		TLSTimeout:     cfg.Probe.TLSTimeout(),
		AttemptTimeout: cfg.Probe.AttemptTimeout(),
		HTTPHead:       cfg.Probe.HTTPHead,
		HTTPTimeout:    cfg.Probe.HTTPTimeout(),
		HTTPMaxStatus:  cfg.Probe.HTTPMaxStatus,
		MinSuccesses:   cfg.Probe.MinSuccesses,
		FailurePenalty: cfg.Probe.FailurePenaltyMS,
		JitterWeight:   cfg.Probe.JitterWeight,
	}
	p := probe.New(opts, cfg.Probe.Concurrency)
	results := p.Run(ctx, jobs)
	byResult := make(map[string][]probe.Result)
	for _, r := range results {
		byResult[r.Domain] = append(byResult[r.Domain], r)
	}

	mappings := make(map[string]string)

	for _, d := range list {
		rs := byResult[d]
		sort.SliceStable(rs, func(i, j int) bool {
			if rs[i].Viable != rs[j].Viable {
				return rs[i].Viable
			}
			return rs[i].Score < rs[j].Score
		})
		var best *probe.Result
		for i := range rs {
			if rs[i].Viable {
				best = &rs[i]
				break
			}
		}

		prevSel, hasPrev := prev.Selected[d]
		if best != nil && hasPrev && prevSel.Verified {
			var prevRes *probe.Result
			for i := range rs {
				if rs[i].IP == prevSel.IP {
					prevRes = &rs[i]
					break
				}
			}
			if prevRes != nil && prevRes.Viable && best.IP != prevSel.IP {
				if best.Score >= prevRes.Score-cfg.Selection.MinImprovementMS {
					best = prevRes
				}
			}
		}

		out := domainOutcome{Domain: d, Candidates: len(rs)}
		switch {
		case best != nil:
			out.IP = best.IP
			out.Score = best.Score
			out.Sources = best.Sources
			out.Verified = true
		case hasPrev && cfg.Selection.OnNoCandidate == "keep" && prevSel.IP != "":
			out.IP = prevSel.IP
			out.Sources = prevSel.Sources
			out.Degraded = true
			out.Error = "kept previous IP; no verified candidate"
		default:
			out.Degraded = true
			if err := resolveErr[d]; err != nil {
				out.Error = err.Error()
			} else {
				var errs []string
				for _, r := range rs {
					if r.LastError != "" {
						errs = append(errs, r.IP+": "+r.LastError)
					}
				}
				if len(errs) == 0 {
					out.Error = "no viable candidate"
				} else {
					out.Error = strings.Join(errs, "; ")
				}
			}
		}
		if out.IP != "" {
			mappings[d] = out.IP
		}
		if out.Degraded {
			round.Degraded = append(round.Degraded, d)
		}
		round.Domains = append(round.Domains, out)
	}

	if !dryRun {
		applyRes, err := manager.Apply(mappings)
		if err != nil {
			return round, err
		}
		round.Changed = applyRes.Changed
		round.Skipped = applyRes.Skipped
	}
	round.DurationMS = time.Since(start).Milliseconds()
	sort.Strings(round.Degraded)
	return round, nil
}

func printRound(r roundResult, hostsPath string) {
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
