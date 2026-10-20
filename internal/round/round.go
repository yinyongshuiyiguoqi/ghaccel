package round

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/domains"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/probe"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/resolver"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

type Outcome struct {
	Domain     string   `json:"domain"`
	IP         string   `json:"ip,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Candidates int      `json:"candidates"`
	Verified   bool     `json:"verified"`
	Degraded   bool     `json:"degraded"`
	Error      string   `json:"error,omitempty"`
}

type Result struct {
	Round      string    `json:"round"`
	DryRun     bool      `json:"dry_run"`
	DurationMS int64     `json:"duration_ms"`
	Changed    bool      `json:"changed"`
	Skipped    []string  `json:"skipped,omitempty"`
	Degraded   []string  `json:"degraded,omitempty"`
	Domains    []Outcome `json:"domains"`
}

// Execute resolves, probes, selects, and (unless dryRun) writes the hosts block.
func Execute(ctx context.Context, cfg config.Config, list []string, prev state.State, dryRun bool, manager *hosts.Manager, log *slog.Logger) (Result, error) {
	start := time.Now()
	res := Result{Round: start.Format(time.RFC3339)}

	breaker := resolver.NewBreaker(cfg.DoH.Breaker.FailThreshold, time.Duration(cfg.DoH.Breaker.OpenSeconds)*time.Second)
	var doh *resolver.DoHClient
	if cfg.DoH.Enabled {
		doh = resolver.NewDoH(cfg.DoH.Providers, cfg.DoH.UseSystemProxy, breaker, log)
	}
	r := resolver.New(doh, cfg.Probe.MaxCandidatesPerDomain, log)

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
			cands, err := r.Resolve(ctx, domain, previous)
			outCh <- resolved{domain: domain, cands: cands, err: err}
		}(d, prevIPs)
	}
	wg.Wait()
	close(outCh)

	byDomain := make(map[string][]resolver.Candidate)
	resolveErr := make(map[string]error)
	for item := range outCh {
		if item.err != nil {
			resolveErr[item.domain] = item.err
			continue
		}
		byDomain[item.domain] = item.cands
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
	for _, pr := range results {
		byResult[pr.Domain] = append(byResult[pr.Domain], pr)
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

		out := Outcome{Domain: d, Candidates: len(rs)}
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
				for _, pr := range rs {
					if pr.LastError != "" {
						errs = append(errs, pr.IP+": "+pr.LastError)
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
			res.Degraded = append(res.Degraded, d)
		}
		res.Domains = append(res.Domains, out)
	}

	if !dryRun {
		applyRes, err := manager.Apply(mappings)
		if err != nil {
			return res, err
		}
		res.Changed = applyRes.Changed
		res.Skipped = applyRes.Skipped
	}
	res.DurationMS = time.Since(start).Milliseconds()
	sort.Strings(res.Degraded)
	return res, nil
}

func StateFrom(res Result) state.State {
	sel := map[string]state.Selection{}
	for _, d := range res.Domains {
		if d.IP != "" {
			sel[d.Domain] = state.Selection{IP: d.IP, Score: d.Score, Verified: d.Verified, Sources: d.Sources}
		}
	}
	return state.State{
		SchemaVersion: 1,
		LastRound:     time.Now(),
		DurationMS:    res.DurationMS,
		Selected:      sel,
		Degraded:      res.Degraded,
		Changed:       res.Changed,
	}
}

func CoreDegraded(res Result) bool {
	for _, d := range res.Domains {
		if d.Degraded && domains.IsRequired(d.Domain) {
			return true
		}
	}
	return false
}
