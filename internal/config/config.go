package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const schemaVersion = 1

type Provider struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	Priority  int    `json:"priority"`
	TimeoutMS int    `json:"timeout_ms"`
}

type Breaker struct {
	FailThreshold int `json:"fail_threshold"`
	OpenSeconds   int `json:"open_seconds"`
}

type DoH struct {
	Enabled        bool       `json:"enabled"`
	UseSystemProxy bool       `json:"use_system_proxy"`
	Providers      []Provider `json:"providers"`
	Breaker        Breaker    `json:"breaker"`
}

type Probe struct {
	Attempts               int     `json:"attempts"`
	MaxCandidatesPerDomain int     `json:"max_candidates_per_domain"`
	Concurrency            int     `json:"concurrency"`
	TCPTimeoutMS           int     `json:"tcp_timeout_ms"`
	TLSTimeoutMS           int     `json:"tls_timeout_ms"`
	AttemptTimeoutMS       int     `json:"attempt_timeout_ms"`
	HTTPHead               bool    `json:"http_head"`
	HTTPTimeoutMS          int     `json:"http_timeout_ms"`
	HTTPMaxStatus          int     `json:"http_max_status"`
	MinSuccesses           int     `json:"min_successes"`
	EarlyStopSuccesses     int     `json:"early_stop_successes"`
	EarlyStopLatencyMS     int     `json:"early_stop_latency_ms"`
	FailurePenaltyMS       float64 `json:"failure_penalty_ms"`
	JitterWeight           float64 `json:"jitter_weight"`
}

type Selection struct {
	MinImprovementMS float64 `json:"min_improvement_ms"`
	OnNoCandidate    string  `json:"on_no_candidate"`
	MaxIPsPerDomain  int     `json:"max_ips_per_domain"`
}

type Refresh struct {
	IntervalMinutes       int   `json:"interval_minutes"`
	JitterSeconds         int   `json:"jitter_seconds"`
	RoundTimeoutSeconds   int   `json:"round_timeout_seconds"`
	FailureBackoffMinutes []int `json:"failure_backoff_minutes"`
}

type Markers struct {
	Begin string `json:"begin"`
	End   string `json:"end"`
}

type Hosts struct {
	Path           string  `json:"path"`
	Markers        Markers `json:"markers"`
	BackupCount    int     `json:"backup_count"`
	ConflictPolicy string  `json:"conflict_policy"`
	FlushDNS       bool    `json:"flush_dns"`
	WriteIPv6      bool    `json:"write_ipv6"`
	MaxFileBytes   int64   `json:"max_file_bytes"`
}

type Logging struct {
	Level      string `json:"level"`
	File       string `json:"file"`
	MaxSizeMB  int    `json:"max_size_mb"`
	MaxBackups int    `json:"max_backups"`
}

type Daemon struct {
	StateFile string `json:"state_file"`
	LockFile  string `json:"lock_file"`
}

type Config struct {
	SchemaVersion  int       `json:"schema_version"`
	Domains        []string  `json:"domains"`
	ExtraDomains   []string  `json:"extra_domains"`
	ExcludeDomains []string  `json:"exclude_domains"`
	DoH            DoH       `json:"doh"`
	Probe          Probe     `json:"probe"`
	Selection      Selection `json:"selection"`
	Refresh        Refresh   `json:"refresh"`
	Hosts          Hosts     `json:"hosts"`
	Logging        Logging   `json:"logging"`
	Daemon         Daemon    `json:"daemon"`
}

func Default() Config {
	return Config{
		SchemaVersion: schemaVersion,
		DoH: DoH{
			Enabled: true,
			Providers: []Provider{
				{Name: "ali", URL: "https://dns.alidns.com/resolve", Enabled: true, Priority: 1, TimeoutMS: 2500},
				{Name: "dnspod", URL: "https://doh.pub/dns-query", Enabled: true, Priority: 2, TimeoutMS: 2500},
				{Name: "cloudflare", URL: "https://cloudflare-dns.com/dns-query", Enabled: false, Priority: 3, TimeoutMS: 2000},
				{Name: "google", URL: "https://dns.google/resolve", Enabled: false, Priority: 4, TimeoutMS: 2000},
			},
			Breaker: Breaker{FailThreshold: 2, OpenSeconds: 300},
		},
		Probe: Probe{
			Attempts:               2,
			MaxCandidatesPerDomain: 6,
			Concurrency:            16,
			TCPTimeoutMS:           1500,
			TLSTimeoutMS:           2500,
			AttemptTimeoutMS:       4000,
			HTTPHead:               true,
			HTTPTimeoutMS:          1500,
			HTTPMaxStatus:          499,
			MinSuccesses:           1,
			EarlyStopSuccesses:     2,
			EarlyStopLatencyMS:     250,
			FailurePenaltyMS:       1500,
			JitterWeight:           0.3,
		},
		Selection: Selection{
			MinImprovementMS: 150,
			OnNoCandidate:    "keep",
			MaxIPsPerDomain:  1,
		},
		Refresh: Refresh{
			IntervalMinutes:       15,
			JitterSeconds:         30,
			RoundTimeoutSeconds:   60,
			FailureBackoffMinutes: []int{1, 2, 5, 10},
		},
		Hosts: Hosts{
			Markers: Markers{
				Begin: "# >>> ghaccel managed block (v1) >>>",
				End:   "# <<< ghaccel managed block (v1) <<<",
			},
			BackupCount:    10,
			ConflictPolicy: "skip",
			FlushDNS:       true,
			MaxFileBytes:   4 << 20,
		},
		Logging: Logging{Level: "info", MaxSizeMB: 5, MaxBackups: 3},
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ghaccel", "config.json"), nil
}

func StatePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ghaccel", "state.json"), nil
}

func LogPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ghaccel", "logs", "ghaccel.log"), nil
}

func BackupDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ghaccel", "backups"), nil
}

func DefaultHostsPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return cfg, err
		}
		path = p
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg.ApplyDefaults()
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

func Save(path string, cfg Config) error {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// ApplyDefaults fills platform-specific paths that are empty.
func (c *Config) ApplyDefaults() {
	if c.Hosts.Path == "" {
		c.Hosts.Path = DefaultHostsPath()
	}
	if c.Logging.File == "" {
		p, err := LogPath()
		if err == nil {
			c.Logging.File = p
		}
	}
	if c.Daemon.StateFile == "" {
		p, err := StatePath()
		if err == nil {
			c.Daemon.StateFile = p
		}
	}
}

func (c Config) Validate() error {
	if c.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", c.SchemaVersion, schemaVersion)
	}
	for _, d := range append(append([]string{}, c.Domains...), c.ExtraDomains...) {
		if !validDomain(d) {
			return fmt.Errorf("invalid domain %q", d)
		}
	}
	if c.DoH.Enabled {
		if len(c.DoH.Providers) == 0 {
			return errors.New("doh.enabled is true but no providers configured")
		}
		for _, p := range c.DoH.Providers {
			u, err := url.Parse(p.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("invalid DoH provider URL %q", p.URL)
			}
		}
	}
	if c.Probe.Attempts < 1 {
		return errors.New("probe.attempts must be >= 1")
	}
	if c.Probe.Concurrency < 1 {
		return errors.New("probe.concurrency must be >= 1")
	}
	if c.Probe.TCPTimeoutMS <= 0 || c.Probe.TLSTimeoutMS <= 0 || c.Probe.AttemptTimeoutMS <= 0 {
		return errors.New("probe tcp/tls/attempt timeouts must be > 0")
	}
	if c.Probe.HTTPHead && c.Probe.HTTPTimeoutMS <= 0 {
		return errors.New("probe.http_timeout_ms must be > 0 when http_head is enabled")
	}
	if c.Refresh.IntervalMinutes < 1 {
		return errors.New("refresh.interval_minutes must be >= 1")
	}
	if c.Hosts.Markers.Begin == "" || c.Hosts.Markers.End == "" {
		return errors.New("hosts markers must not be empty")
	}
	if c.Hosts.ConflictPolicy != "skip" && c.Hosts.ConflictPolicy != "warn" {
		return fmt.Errorf("invalid hosts.conflict_policy %q (skip|warn)", c.Hosts.ConflictPolicy)
	}
	return nil
}

func (p Probe) AttemptTimeout() time.Duration {
	return time.Duration(p.AttemptTimeoutMS) * time.Millisecond
}

func (p Probe) TCPTimeout() time.Duration {
	return time.Duration(p.TCPTimeoutMS) * time.Millisecond
}

func (p Probe) TLSTimeout() time.Duration {
	return time.Duration(p.TLSTimeoutMS) * time.Millisecond
}

func (p Probe) HTTPTimeout() time.Duration {
	return time.Duration(p.HTTPTimeoutMS) * time.Millisecond
}

func (r Refresh) Interval() time.Duration {
	return time.Duration(r.IntervalMinutes) * time.Minute
}

func validDomain(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || len(s) > 253 || strings.Contains(s, "://") || strings.ContainsAny(s, " /\\") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return strings.Contains(s, ".")
}
