package resolver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/domains"
)

const DefaultMaxCandidates = 6

type Candidate struct {
	IP      string
	Sources []string
}

type Resolver struct {
	doh *DoHClient
	max int
	log *slog.Logger
}

func New(doh *DoHClient, maxCandidates int, log *slog.Logger) *Resolver {
	if maxCandidates <= 0 {
		maxCandidates = DefaultMaxCandidates
	}
	return &Resolver{doh: doh, max: maxCandidates, log: log}
}

// Resolve collects candidate IPs for one domain from previous selection, built-in
// seeds, and DoH providers, in that priority order. A DoH failure is not fatal
// when seeds are available.
func (r *Resolver) Resolve(ctx context.Context, domain string, previous []string) ([]Candidate, error) {
	var merged []Candidate
	index := make(map[string]int)

	add := func(ip, source string) {
		ip = normalizeIP(ip)
		if ip == "" {
			return
		}
		if i, ok := index[ip]; ok {
			for _, s := range merged[i].Sources {
				if s == source {
					return
				}
			}
			merged[i].Sources = append(merged[i].Sources, source)
			return
		}
		index[ip] = len(merged)
		merged = append(merged, Candidate{IP: ip, Sources: []string{source}})
	}

	for _, ip := range previous {
		add(ip, "previous")
	}
	for _, ip := range domains.SeedsFor(domain) {
		add(ip, "seed")
	}

	var dohErr error
	if r.doh != nil {
		results, errs := r.doh.ResolveAll(ctx, domain)
		names := make([]string, 0, len(results))
		for name := range results {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, ip := range results[name] {
				add(ip, "doh:"+name)
			}
		}
		if len(results) == 0 && len(errs) > 0 {
			dohErr = fmt.Errorf("all DoH providers failed for %s", domain)
		}
	}

	if len(merged) > r.max {
		merged = merged[:r.max]
	}
	if len(merged) == 0 {
		if dohErr != nil {
			return nil, dohErr
		}
		return nil, errors.New("no candidate IPs")
	}
	return merged, nil
}

func normalizeIP(s string) string {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return ""
	}
	return addr.String()
}
