package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Attempts       int
	TCPTimeout     time.Duration
	TLSTimeout     time.Duration
	AttemptTimeout time.Duration
	HTTPHead       bool
	HTTPTimeout    time.Duration
	HTTPMaxStatus  int
	MinSuccesses   int
	FailurePenalty float64
	JitterWeight   float64
	// RootCAs is nil in production (system roots). Tests inject a pool.
	RootCAs *x509.CertPool
}

type Result struct {
	Domain     string
	IP         string
	Sources    []string
	Attempts   int
	Successes  int
	Failures   int
	TCP        []time.Duration
	TLS        []time.Duration
	HTTP       []time.Duration
	HTTPStatus int
	HTTPError  string
	LastError  string
	Viable     bool
	Score      float64
}

type Job struct {
	Domain  string
	IP      string
	Port    int
	Sources []string
}

type Prober struct {
	opts Options
	sem  chan struct{}
}

func New(opts Options, concurrency int) *Prober {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Prober{opts: opts, sem: make(chan struct{}, concurrency)}
}

func (p *Prober) Run(ctx context.Context, jobs []Job) []Result {
	results := make([]Result, 0, len(jobs))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			wg.Wait()
			return results
		case p.sem <- struct{}{}:
		}
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			defer func() { <-p.sem }()
			res := p.Probe(ctx, j)
			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}(job)
	}
	wg.Wait()
	return results
}

func (p *Prober) Probe(ctx context.Context, job Job) Result {
	r := Result{Domain: job.Domain, IP: job.IP, Sources: job.Sources}
	attempts := p.opts.Attempts
	if attempts < 1 {
		attempts = 1
	}

	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			r.LastError = err.Error()
			break
		}
		r.Attempts++
		tcp, tlsDur, httpDur, status, httpErr, err := p.attempt(ctx, job.Domain, job.IP, job.Port)
		if err != nil {
			r.Failures++
			r.LastError = err.Error()
			if classifyError(err) == ErrCertMismatch {
				break
			}
			if i+1 < attempts {
				select {
				case <-ctx.Done():
				case <-time.After(150 * time.Millisecond):
				}
			}
			continue
		}
		r.Successes++
		r.TCP = append(r.TCP, tcp)
		r.TLS = append(r.TLS, tlsDur)
		if httpDur > 0 {
			r.HTTP = append(r.HTTP, httpDur)
		}
		if status > 0 {
			r.HTTPStatus = status
		}
		if httpErr != "" {
			r.HTTPError = httpErr
		}
	}

	minSuccess := p.opts.MinSuccesses
	if minSuccess < 1 {
		minSuccess = 1
	}
	r.Viable = r.Successes >= minSuccess
	if r.Viable {
		r.Score = score(&r, p.opts)
	} else {
		r.Score = math.Inf(1)
	}
	return r
}

func (p *Prober) attempt(ctx context.Context, domain, ip string, port int) (tcp, tlsDur, httpDur time.Duration, status int, httpErr string, err error) {
	actx, cancel := context.WithTimeout(ctx, p.opts.AttemptTimeout)
	defer cancel()

	if port == 0 {
		port = 443
	}
	dialer := &net.Dialer{Timeout: p.opts.TCPTimeout}
	start := time.Now()
	raw, err := dialer.DialContext(actx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	tcp = time.Since(start)
	defer raw.Close()

	// A socket deadline is mandatory: context cancellation alone can leave a
	// TLS read blocked indefinitely on a hostile path.
	deadline := time.Now().Add(p.opts.AttemptTimeout)
	_ = raw.SetDeadline(deadline)

	cfg := &tls.Config{
		ServerName: domain,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		RootCAs:    p.opts.RootCAs,
	}
	tlsConn := tls.Client(raw, cfg)
	hctx, hcancel := context.WithTimeout(actx, p.opts.TLSTimeout)
	defer hcancel()
	start = time.Now()
	if err := tlsConn.HandshakeContext(hctx); err != nil {
		return tcp, 0, 0, 0, "", err
	}
	tlsDur = time.Since(start)

	if p.opts.HTTPHead {
		httpDur, status, httpErr = head(tlsConn, domain, p.opts.HTTPTimeout)
	}
	return tcp, tlsDur, httpDur, status, httpErr, nil
}

func head(conn *tls.Conn, domain string, timeout time.Duration) (time.Duration, int, string) {
	start := time.Now()
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	req := fmt.Sprintf("HEAD / HTTP/1.1\r\nHost: %s\r\nUser-Agent: ghaccel/0.1\r\nAccept: */*\r\nConnection: close\r\n\r\n", domain)
	if _, err := io.WriteString(conn, req); err != nil {
		return time.Since(start), 0, errString(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	dur := time.Since(start)
	if err != nil {
		return dur, 0, errString(err)
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return dur, 0, "malformed status line"
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return dur, 0, "malformed status code"
	}
	return dur, code, ""
}

type ErrorClass string

const (
	ErrTimeout      ErrorClass = "timeout"
	ErrRefused      ErrorClass = "refused"
	ErrReset        ErrorClass = "reset"
	ErrCertMismatch ErrorClass = "cert_mismatch"
	ErrOther        ErrorClass = "other"
)

func classifyError(err error) ErrorClass {
	if err == nil {
		return ErrOther
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return ErrTimeout
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return ErrTimeout
	}
	var hostnameErr x509.HostnameError
	var unknownErr x509.UnknownAuthorityError
	var invalidErr x509.CertificateInvalidError
	if errors.As(err, &hostnameErr) || errors.As(err, &unknownErr) || errors.As(err, &invalidErr) {
		return ErrCertMismatch
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "certificate"):
		return ErrCertMismatch
	case strings.Contains(s, "connection reset"), strings.Contains(s, "broken pipe"), strings.Contains(s, "eof"):
		return ErrReset
	case strings.Contains(s, "refused"):
		return ErrRefused
	case strings.Contains(s, "timeout"):
		return ErrTimeout
	}
	return ErrOther
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func score(r *Result, opts Options) float64 {
	ms := float64(time.Millisecond)
	tcp := float64(median(r.TCP)) / ms
	tls := float64(median(r.TLS)) / ms
	http := float64(median(r.HTTP)) / ms
	var base float64
	if len(r.HTTP) > 0 {
		base = 0.25*tcp + 0.55*tls + 0.20*http
	} else {
		base = 0.30*tcp + 0.70*tls
	}

	jitter := 0.0
	if len(r.TCP) == len(r.TLS) && len(r.TCP) > 1 {
		totals := make([]float64, len(r.TCP))
		for i := range r.TCP {
			totals[i] = float64(r.TCP[i]+r.TLS[i]) / ms
			if i < len(r.HTTP) {
				totals[i] += float64(r.HTTP[i]) / ms
			}
		}
		jitter = maxF(totals) - minF(totals)
	}
	penalty := float64(r.Failures) * opts.FailurePenalty
	score := base + opts.JitterWeight*jitter + penalty - sourceBonus(r.Sources)
	if r.HTTPStatus > 0 {
		max := opts.HTTPMaxStatus
		if max <= 0 {
			max = 499
		}
		if r.HTTPStatus > max {
			score += 300
		}
	}
	return score
}

func sourceBonus(sources []string) float64 {
	best := 0.0
	for _, s := range sources {
		switch {
		case s == "seed":
			if best < 30 {
				best = 30
			}
		case strings.HasPrefix(s, "doh:"):
			if best < 15 {
				best = 15
			}
		}
	}
	return best
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), d...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp[len(cp)/2]
}

func maxF(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func minF(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}
