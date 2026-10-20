package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
)

type dohAnswer struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	TTL  int    `json:"ttl"`
	Data string `json:"data"`
}

type dohResponse struct {
	Status int         `json:"Status"`
	Answer []dohAnswer `json:"Answer"`
}

type DoHClient struct {
	providers []config.Provider
	client    *http.Client
	breaker   *Breaker
	log       *slog.Logger
}

func NewDoH(providers []config.Provider, useSystemProxy bool, breaker *Breaker, log *slog.Logger) *DoHClient {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
	}
	if useSystemProxy {
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &DoHClient{
		providers: providers,
		client:    &http.Client{Transport: transport},
		breaker:   breaker,
		log:       log,
	}
}

// ResolveAll queries every enabled provider concurrently and merges the A records.
// The returned map is provider name -> IPs; provider errors are reported in errs.
func (c *DoHClient) ResolveAll(ctx context.Context, domain string) (map[string][]string, map[string]error) {
	out := make(map[string][]string)
	errs := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range c.providers {
		if !p.Enabled {
			continue
		}
		if !c.breaker.Allow(p.Name) {
			mu.Lock()
			errs[p.Name] = errors.New("circuit open")
			mu.Unlock()
			continue
		}
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			ips, err := c.query(ctx, p, domain, 0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[p.Name] = err
				c.breaker.Failure(p.Name)
				if c.log != nil {
					c.log.Debug("doh failed", "provider", p.Name, "domain", domain, "err", err)
				}
				return
			}
			c.breaker.Success(p.Name)
			if len(ips) > 0 {
				out[p.Name] = ips
			}
		}()
	}
	wg.Wait()
	return out, errs
}

func (c *DoHClient) query(ctx context.Context, p config.Provider, domain string, depth int) ([]string, error) {
	if depth > 3 {
		return nil, errors.New("cname chain too deep")
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("name", domain)
	q.Set("type", "A")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(qctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	req.Header.Set("User-Agent", "ghaccel/0.1")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var dr dohResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return nil, fmt.Errorf("bad json: %w", err)
	}
	switch dr.Status {
	case 0:
	case 3:
		return nil, nil
	default:
		return nil, fmt.Errorf("dns status %d", dr.Status)
	}

	var ips []string
	var cname string
	for _, a := range dr.Answer {
		switch a.Type {
		case 1:
			if ip, ok := parseIPv4(a.Data); ok {
				ips = append(ips, ip)
			}
		case 5:
			if cname == "" {
				cname = strings.TrimSuffix(a.Data, ".")
			}
		}
	}
	if len(ips) == 0 && cname != "" && !strings.EqualFold(cname, domain) {
		return c.query(ctx, p, cname, depth+1)
	}
	return ips, nil
}

func parseIPv4(s string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", false
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return "", false
	}
	return addr.String(), true
}

func (c *DoHClient) ProviderNames() []string {
	var out []string
	for _, p := range c.providers {
		if p.Enabled {
			out = append(out, p.Name)
		}
	}
	return out
}

func (c *DoHClient) Breaker() *Breaker { return c.breaker }
