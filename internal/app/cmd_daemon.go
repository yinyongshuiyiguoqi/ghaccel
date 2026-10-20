package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/daemon"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/domains"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	child := fs.Bool("child", false, "internal: run in the foreground as the daemon child")
	interval := fs.Duration("interval", 0, "refresh interval")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *child {
		return runDaemon(g, *interval)
	}
	return startDaemon(g, *interval)
}

func startDaemon(g *globals, interval time.Duration) int {
	cfg, cfgPath, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}
	if interval <= 0 {
		interval = cfg.Refresh.Interval()
	}
	if interval < time.Minute {
		interval = time.Minute
	}

	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	if err := m.CheckWritable(); err != nil {
		adminHint("ghaccel start")
		if errors.Is(err, hosts.ErrNeedAdmin) {
			return exitNeedAdmin
		}
		return fail(err, exitError)
	}

	if info, err := daemon.LoadInfo(cfg.Daemon.StateFile); err == nil && daemon.Healthy(info) {
		fmt.Printf("already running: pid=%d port=%d\n", info.PID, info.Port)
		return exitOK
	}
	daemon.ClearInfo(cfg.Daemon.StateFile)

	exe, err := os.Executable()
	if err != nil {
		return fail(err, exitError)
	}
	logPath := cfg.Logging.File
	if logPath == "" {
		p, _ := config.LogPath()
		logPath = p
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fail(err, exitError)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fail(err, exitError)
	}
	defer logFile.Close()

	cmd := exec.Command(exe,
		"daemon", "--child",
		"--config", cfgPath,
		"--interval", interval.String(),
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	daemon.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return fail(err, exitError)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := daemon.LoadInfo(cfg.Daemon.StateFile); err == nil && daemon.Healthy(info) {
			fmt.Printf("started: pid=%d port=%d interval=%s\nlog: %s\n", info.PID, info.Port, interval, logPath)
			return exitOK
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "daemon did not become healthy; check %s\n", logPath)
	return exitError
}

func runDaemon(g *globals, interval time.Duration) int {
	cfg, _, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}
	if interval <= 0 {
		interval = cfg.Refresh.Interval()
	}
	if interval < time.Minute {
		interval = time.Minute
	}

	unlock, err := acquireDaemonLock(daemon.LockPath(cfg.Daemon.StateFile))
	if err != nil {
		return fail(err, exitError)
	}
	defer unlock()

	token, err := randomToken()
	if err != nil {
		return fail(err, exitError)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	loopCtx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()
	port, stopCh, err := daemon.Serve(ctx, token)
	if err != nil {
		return fail(err, exitError)
	}
	go func() {
		select {
		case <-stopCh:
			loopCancel()
		case <-ctx.Done():
		}
	}()
	info := daemon.Info{PID: os.Getpid(), Port: port, Token: token, Started: time.Now()}
	if err := daemon.SaveInfo(cfg.Daemon.StateFile, info); err != nil {
		return fail(err, exitError)
	}
	defer daemon.ClearInfo(cfg.Daemon.StateFile)

	logFile, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fail(err, exitError)
	}
	defer logFile.Close()
	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("daemon started", "pid", info.PID, "port", port, "interval", interval.String())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	run := func() {
		if err := daemonRound(loopCtx, cfg, log); err != nil {
			log.Error("round failed", "err", err)
		}
	}
	run()

	for {
		select {
		case <-ctx.Done():
			log.Info("daemon stopped by signal")
			return exitOK
		case <-stopCh:
			log.Info("daemon stopped by request")
			return exitOK
		case <-ticker.C:
			run()
		}
	}
}

func daemonRound(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	list := domains.Effective(cfg.Domains, cfg.ExtraDomains, cfg.ExcludeDomains)
	if len(list) == 0 {
		return errors.New("no domains configured")
	}
	prev, err := state.Load(cfg.Daemon.StateFile)
	if err != nil {
		log.Warn("load state", "err", err)
	}
	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	m.ConflictPolicy = cfg.Hosts.ConflictPolicy

	round, err := executeRound(ctx, cfg, list, prev, false, m, log)
	if err != nil {
		return err
	}
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
	if err := state.Save(cfg.Daemon.StateFile, st); err != nil {
		log.Warn("save state", "err", err)
	}
	log.Info("round complete",
		"domains", len(round.Domains),
		"degraded", len(round.Degraded),
		"changed", round.Changed,
		"duration_ms", round.DurationMS,
	)
	return nil
}

func cmdStop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	g := &globals{}
	g.bind(fs)
	restore := fs.Bool("restore", false, "also remove the ghaccel block from hosts")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	cfg, _, err := loadConfig(g)
	if err != nil {
		return fail(err, exitUsage)
	}

	info, err := daemon.LoadInfo(cfg.Daemon.StateFile)
	if err != nil || !daemon.Healthy(info) {
		daemon.ClearInfo(cfg.Daemon.StateFile)
		fmt.Println("daemon is not running")
	} else {
		if err := daemon.Stop(info); err != nil {
			return fail(err, exitError)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if !daemon.Healthy(info) {
				if _, err := daemon.LoadInfo(cfg.Daemon.StateFile); err != nil {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if daemon.Healthy(info) {
			fmt.Fprintln(os.Stderr, "warning: daemon still responding, it may be finishing a round")
		} else {
			daemon.ClearInfo(cfg.Daemon.StateFile)
			fmt.Println("daemon stopped")
		}
	}

	if *restore {
		backupDir, _ := config.BackupDir()
		m := hosts.New(cfg.Hosts.Path, hosts.Markers{
			Begin: cfg.Hosts.Markers.Begin,
			End:   cfg.Hosts.Markers.End,
		}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
		m.ConflictPolicy = cfg.Hosts.ConflictPolicy
		changed, err := m.Remove()
		if err != nil {
			if errors.Is(err, hosts.ErrNeedAdmin) {
				adminHint("ghaccel stop --restore")
				return exitNeedAdmin
			}
			return fail(err, exitError)
		}
		if changed {
			fmt.Println("hosts block removed")
		}
	}
	return exitOK
}

func acquireDaemonLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for i := 0; i < 2; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.WriteString(strconv.Itoa(os.Getpid()))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, statErr := os.Stat(path); statErr == nil && time.Since(st.ModTime()) > 10*time.Minute {
			_ = os.Remove(path)
			continue
		}
		return nil, errors.New("another ghaccel daemon is already running")
	}
	return nil, errors.New("could not acquire daemon lock")
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
