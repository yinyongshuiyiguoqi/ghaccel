package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/OWNER/ghaccel/internal/config"
	"github.com/OWNER/ghaccel/internal/daemon"
	"github.com/OWNER/ghaccel/internal/hosts"
	"github.com/OWNER/ghaccel/internal/state"
)

type statusOutput struct {
	ConfigPath    string                     `json:"config_path"`
	HostsPath     string                     `json:"hosts_path"`
	HostsWritable bool                       `json:"hosts_writable"`
	BlockPresent  bool                       `json:"block_present"`
	DaemonRunning bool                       `json:"daemon_running"`
	DaemonPID     int                        `json:"daemon_pid,omitempty"`
	LastRound     *time.Time                 `json:"last_round,omitempty"`
	DurationMS    int64                      `json:"duration_ms"`
	Degraded      []string                   `json:"degraded,omitempty"`
	Selected      map[string]state.Selection `json:"selected"`
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, cfgPath, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}
	st, stateErr := state.Load(cfg.Daemon.StateFile)

	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	m.ConflictPolicy = cfg.Hosts.ConflictPolicy
	current, present, hostsErr := m.Current()
	writable := m.CheckWritable() == nil

	out := statusOutput{
		ConfigPath:    cfgPath,
		HostsPath:     cfg.Hosts.Path,
		HostsWritable: writable,
		BlockPresent:  present,
		Selected:      map[string]state.Selection{},
	}
	if stateErr == nil {
		if !st.LastRound.IsZero() {
			t := st.LastRound
			out.LastRound = &t
		}
		out.DurationMS = st.DurationMS
		out.Degraded = st.Degraded
		out.Selected = st.Selected
	}
	if info, err := daemon.LoadInfo(cfg.Daemon.StateFile); err == nil && daemon.Healthy(info) {
		out.DaemonRunning = true
		out.DaemonPID = info.PID
	}

	if g.json {
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fail(err, exitError)
		}
		fmt.Println(string(b))
		return exitOK
	}

	fmt.Printf("config: %s\nhosts:  %s (%s)\n", cfgPath, cfg.Hosts.Path, map[bool]string{true: "writable", false: "needs admin"}[writable])
	if out.DaemonRunning {
		fmt.Printf("daemon: running (pid %d)\n", out.DaemonPID)
	} else {
		fmt.Println("daemon: not running")
	}
	if stateErr != nil {
		fmt.Printf("state:  unavailable (%v)\n", stateErr)
	} else if out.LastRound == nil {
		fmt.Println("state:  no completed round yet")
	} else {
		fmt.Printf("last:   %s (%.1fs)\n", out.LastRound.Local().Format(time.RFC3339), float64(st.DurationMS)/1000)
	}
	fmt.Printf("block:  %v (%d entries in hosts)\n\n", present, len(current))

	names := make([]string, 0, len(out.Selected))
	for d := range out.Selected {
		names = append(names, d)
	}
	sort.Strings(names)
	if len(names) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "DOMAIN\tIP\tSOURCES")
		for _, d := range names {
			s := out.Selected[d]
			fmt.Fprintf(w, "%s\t%s\t%v\n", d, s.IP, s.Sources)
		}
		w.Flush()
	}
	if hostsErr != nil {
		fmt.Fprintf(os.Stderr, "\nhosts read error: %v\n", hostsErr)
	}
	if len(out.Degraded) > 0 {
		fmt.Printf("\ndegraded: %v\n", out.Degraded)
	}
	return exitOK
}
