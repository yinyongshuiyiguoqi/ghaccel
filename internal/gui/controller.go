package gui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/domains"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	roundpkg "github.com/yinyongshuiyiguoqi/ghaccel/internal/round"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

type Status struct {
	Running   bool
	Available int
	Total     int
	Degraded  int
	Last      time.Time
	Message   string
}

type Callbacks struct {
	OnRound  func(roundpkg.Result)
	OnStatus func(Status)
	OnLog    func(string)
}

type Controller struct {
	cfg      config.Config
	manager  *hosts.Manager
	logger   *slog.Logger
	logFile  *os.File
	cb       Callbacks
	interval time.Duration

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
	refresh chan struct{}
}

func NewController(cfg config.Config, cb Callbacks) *Controller {
	backupDir, _ := config.BackupDir()
	m := hosts.New(cfg.Hosts.Path, hosts.Markers{
		Begin: cfg.Hosts.Markers.Begin,
		End:   cfg.Hosts.Markers.End,
	}, backupDir, cfg.Hosts.BackupCount, cfg.Hosts.FlushDNS, cfg.Hosts.MaxFileBytes)
	m.ConflictPolicy = cfg.Hosts.ConflictPolicy

	var writers []io.Writer
	var logFile *os.File
	if cfg.Logging.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Logging.File), 0o755); err == nil {
			if f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				logFile = f
				writers = append(writers, f)
			}
		}
	}
	if cb.OnLog != nil {
		writers = append(writers, &lineWriter{emit: cb.OnLog})
	}
	if len(writers) == 0 {
		writers = append(writers, io.Discard)
	}

	interval := cfg.Refresh.Interval()
	if interval < time.Minute {
		interval = time.Minute
	}

	return &Controller{
		cfg:      cfg,
		manager:  m,
		logger:   slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: slog.LevelInfo})),
		logFile:  logFile,
		cb:       cb,
		interval: interval,
		refresh:  make(chan struct{}, 1),
	}
}

func (c *Controller) Config() config.Config { return c.cfg }

func (c *Controller) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *Controller) Start() error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	if err := c.manager.CheckWritable(); err != nil {
		c.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	c.done = make(chan struct{})
	done := c.done
	c.mu.Unlock()

	go func() {
		defer close(done)
		c.loop(ctx)
	}()

	if c.cb.OnStatus != nil {
		c.cb.OnStatus(Status{Running: true})
	}
	return nil
}

func (c *Controller) Stop(restore bool) error {
	c.mu.Lock()
	running := c.running
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()

	if running && cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
		}
	}

	c.mu.Lock()
	c.running = false
	c.cancel = nil
	c.mu.Unlock()

	var err error
	if restore {
		_, err = c.manager.Remove()
		if err == nil {
			if st, e := state.Load(c.cfg.Daemon.StateFile); e == nil {
				st.Selected = map[string]state.Selection{}
				st.Degraded = nil
				st.LastRound = time.Time{}
				_ = state.Save(c.cfg.Daemon.StateFile, st)
			}
		}
	}

	if c.cb.OnStatus != nil {
		c.cb.OnStatus(Status{Running: false, Message: statusMessage(err)})
	}
	return err
}

func (c *Controller) Refresh() {
	if !c.IsRunning() {
		if err := c.manager.CheckWritable(); err != nil {
			c.report(Status{Running: false, Message: err.Error()})
			return
		}
		go c.runOnce(context.Background())
		return
	}
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

func (c *Controller) Restore() error {
	changed, err := c.manager.Remove()
	if err != nil {
		return err
	}
	if changed {
		c.logger.Info("hosts block removed")
	}
	return nil
}

func (c *Controller) Close() {
	_ = c.Stop(false)
	if c.logFile != nil {
		_ = c.logFile.Close()
	}
}

func (c *Controller) loop(ctx context.Context) {
	c.runOnce(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runOnce(ctx)
		case <-c.refresh:
			c.runOnce(ctx)
		}
	}
}

func (c *Controller) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	list := domains.Effective(c.cfg.Domains, c.cfg.ExtraDomains, c.cfg.ExcludeDomains)
	prev, err := state.Load(c.cfg.Daemon.StateFile)
	if err != nil {
		c.logger.Warn("load state", "err", err)
	}

	res, err := roundpkg.Execute(ctx, c.cfg, list, prev, false, c.manager, c.logger)
	if err != nil {
		c.logger.Error("round failed", "err", err)
		c.report(Status{Running: c.IsRunning(), Message: err.Error()})
		return
	}
	if err := state.Save(c.cfg.Daemon.StateFile, roundpkg.StateFrom(res)); err != nil {
		c.logger.Warn("save state", "err", err)
	}

	available := 0
	for _, d := range res.Domains {
		if d.IP != "" {
			available++
		}
	}
	if c.cb.OnRound != nil {
		c.cb.OnRound(res)
	}
	c.report(Status{
		Running:   c.IsRunning(),
		Available: available,
		Total:     len(res.Domains),
		Degraded:  len(res.Degraded),
		Last:      time.Now(),
	})
}

func (c *Controller) report(s Status) {
	if c.cb.OnStatus != nil {
		c.cb.OnStatus(s)
	}
}

func statusMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := indexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if w.emit != nil {
			w.emit(line)
		}
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
