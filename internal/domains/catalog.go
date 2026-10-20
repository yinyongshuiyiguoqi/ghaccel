package domains

import "strings"

// Core domains decide whether a round is degraded: if one of them has no viable
// IP, the run exits with code 4.
var core = []string{
	"github.com",
	"api.github.com",
	"raw.githubusercontent.com",
	"codeload.github.com",
	"objects.githubusercontent.com",
	"gist.github.com",
	"gist.githubusercontent.com",
	"github.githubassets.com",
}

var optional = []string{
	"media.githubusercontent.com",
	"avatars.githubusercontent.com",
	"avatars0.githubusercontent.com",
	"avatars1.githubusercontent.com",
	"avatars2.githubusercontent.com",
	"avatars3.githubusercontent.com",
	"avatars4.githubusercontent.com",
	"avatars5.githubusercontent.com",
	"avatars6.githubusercontent.com",
	"avatars7.githubusercontent.com",
	"avatars8.githubusercontent.com",
	"avatars9.githubusercontent.com",
	"camo.githubusercontent.com",
	"cloud.githubusercontent.com",
	"user-images.githubusercontent.com",
	"private-user-images.githubusercontent.com",
	"desktop.githubusercontent.com",
	"favicons.githubusercontent.com",
	"github.io",
	"githubstatus.com",
	"www.githubstatus.com",
	"github.blog",
	"collector.github.com",
	"alive.github.com",
	"central.github.com",
	"education.github.com",
	"github.community",
	"live.github.com",
	"pipelines.actions.githubusercontent.com",
	"npm.pkg.github.com",
	"maven.pkg.github.com",
	"ghcr.io",
	"github.global.ssl.fastly.net",
}

var seedSet = []string{
	"185.199.108.133",
	"185.199.109.133",
	"185.199.110.133",
	"185.199.111.133",
}

var seeds = map[string][]string{
	"github.com":           {"20.205.243.166", "140.82.112.3", "140.82.113.3", "140.82.114.3", "140.82.112.4", "140.82.113.4", "140.82.114.4"},
	"api.github.com":       {"20.205.243.168", "140.82.112.5", "140.82.112.6", "140.82.113.5", "140.82.113.6"},
	"codeload.github.com":  {"20.205.243.165", "140.82.112.9", "140.82.113.9", "140.82.112.10", "140.82.113.10"},
	"gist.github.com":      {"140.82.112.3", "140.82.113.3", "140.82.112.4", "140.82.113.4", "20.205.243.166"},
	"ghcr.io":              {"20.205.243.164"},
	"npm.pkg.github.com":   {"20.205.243.164"},
	"maven.pkg.github.com": {"20.205.243.164"},
	"pipelines.actions.githubusercontent.com": {"13.107.42.16"},
	"github.blog":                  {"192.0.66.2"},
	"githubstatus.com":             {"185.199.108.153", "185.199.109.153", "185.199.110.153", "185.199.111.153"},
	"www.githubstatus.com":         {"18.65.3.43", "185.199.108.153", "185.199.109.153"},
	"github.community":             {"140.82.113.18"},
	"collector.github.com":         {"140.82.112.22"},
	"central.github.com":           {"140.82.112.22"},
	"education.github.com":         {"140.82.112.22"},
	"alive.github.com":             {"140.82.112.25"},
	"live.github.com":              {"140.82.112.25"},
	"github.global.ssl.fastly.net": {"118.193.202.219"},
}

var rawFamily = map[string]bool{
	"raw.githubusercontent.com":                 true,
	"objects.githubusercontent.com":             true,
	"media.githubusercontent.com":               true,
	"gist.githubusercontent.com":                true,
	"avatars.githubusercontent.com":             true,
	"avatars0.githubusercontent.com":            true,
	"avatars1.githubusercontent.com":            true,
	"avatars2.githubusercontent.com":            true,
	"avatars3.githubusercontent.com":            true,
	"avatars4.githubusercontent.com":            true,
	"avatars5.githubusercontent.com":            true,
	"avatars6.githubusercontent.com":            true,
	"avatars7.githubusercontent.com":            true,
	"avatars8.githubusercontent.com":            true,
	"avatars9.githubusercontent.com":            true,
	"camo.githubusercontent.com":                true,
	"cloud.githubusercontent.com":               true,
	"user-images.githubusercontent.com":         true,
	"private-user-images.githubusercontent.com": true,
	"desktop.githubusercontent.com":             true,
	"favicons.githubusercontent.com":            true,
}

var pagesFamily = map[string]bool{
	"github.io": true,
}

// Default returns the built-in catalog: core first, then optional.
func Default() []string {
	out := make([]string, 0, len(core)+len(optional))
	out = append(out, core...)
	out = append(out, optional...)
	return out
}

func Required() []string {
	return append([]string(nil), core...)
}

func IsRequired(domain string) bool {
	for _, d := range core {
		if d == domain {
			return true
		}
	}
	return false
}

// Effective resolves the domain list for a run.
// A non-empty base replaces the built-in catalog entirely.
func Effective(base []string, extra []string, exclude []string) []string {
	if len(base) == 0 {
		base = Default()
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(base)+len(extra))
	add := func(d string) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, d := range base {
		add(d)
	}
	for _, d := range extra {
		add(d)
	}
	for _, d := range exclude {
		delete(seen, strings.ToLower(strings.TrimSpace(d)))
	}
	filtered := out[:0]
	for _, d := range out {
		if seen[d] {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// SeedsFor returns built-in IP hints for a domain. They are candidates only:
// every one is probed and verified before use.
func SeedsFor(domain string) []string {
	if ips, ok := seeds[domain]; ok {
		return append([]string(nil), ips...)
	}
	if rawFamily[domain] {
		return append([]string(nil), seedSet...)
	}
	if pagesFamily[domain] {
		return []string{"185.199.108.153", "185.199.109.153", "185.199.110.153", "185.199.111.153"}
	}
	if strings.HasSuffix(domain, ".githubassets.com") {
		return []string{"185.199.108.215", "185.199.109.215", "185.199.110.215", "185.199.111.215"}
	}
	return nil
}

func AllSeeds() map[string][]string {
	out := make(map[string][]string, len(seeds))
	for k, v := range seeds {
		out[k] = append([]string(nil), v...)
	}
	return out
}
