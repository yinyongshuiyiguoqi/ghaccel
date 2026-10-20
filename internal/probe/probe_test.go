package probe

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeTLSVerified(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	domain := srv.Certificate().DNSNames[0]
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	p := New(Options{
		Attempts:       1,
		TCPTimeout:     2 * time.Second,
		TLSTimeout:     2 * time.Second,
		AttemptTimeout: 3 * time.Second,
		HTTPHead:       true,
		HTTPTimeout:    time.Second,
		MinSuccesses:   1,
		RootCAs:        pool,
	}, 1)
	res := p.Probe(context.Background(), Job{Domain: domain, IP: "127.0.0.1", Port: port})
	if !res.Viable || res.Successes != 1 {
		t.Fatalf("expected viable probe, got %+v", res)
	}
	if res.HTTPStatus != 200 {
		t.Fatalf("expected HTTP 200, got %d (%s)", res.HTTPStatus, res.HTTPError)
	}
}

func TestProbeStalledBodyNotViable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	domain := srv.Certificate().DNSNames[0]
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	p := New(Options{
		Attempts:       1,
		TCPTimeout:     2 * time.Second,
		TLSTimeout:     2 * time.Second,
		AttemptTimeout: 3 * time.Second,
		HTTPHead:       true,
		HTTPTimeout:    500 * time.Millisecond,
		MinSuccesses:   1,
		RootCAs:        pool,
	}, 1)
	res := p.Probe(context.Background(), Job{Domain: domain, IP: "127.0.0.1", Port: port})
	if res.Viable {
		t.Fatal("an IP whose HTTP body stalls must not be viable")
	}
}

func TestProbeCertMismatchNotViable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	p := New(Options{
		Attempts:       2,
		TCPTimeout:     2 * time.Second,
		TLSTimeout:     2 * time.Second,
		AttemptTimeout: 3 * time.Second,
		MinSuccesses:   1,
		RootCAs:        pool,
	}, 1)
	res := p.Probe(context.Background(), Job{Domain: "wrong.example", IP: "127.0.0.1", Port: port})
	if res.Viable {
		t.Fatal("certificate mismatch must not be viable")
	}
	if res.Attempts != 1 {
		t.Fatalf("cert mismatch should not be retried, attempts=%d", res.Attempts)
	}
}

func TestScorePrefersFaster(t *testing.T) {
	opts := Options{FailurePenalty: 1500, JitterWeight: 0.3}
	fast := Result{TCP: []time.Duration{50 * time.Millisecond}, TLS: []time.Duration{80 * time.Millisecond}, Successes: 1}
	slow := Result{TCP: []time.Duration{300 * time.Millisecond}, TLS: []time.Duration{900 * time.Millisecond}, Successes: 1}
	fs := score(&fast, opts)
	ss := score(&slow, opts)
	if fs >= ss {
		t.Fatalf("expected fast score < slow score, got %.0f vs %.0f", fs, ss)
	}
}

func TestScorePenalizesFailures(t *testing.T) {
	opts := Options{FailurePenalty: 1500, JitterWeight: 0.3}
	clean := Result{TCP: []time.Duration{100 * time.Millisecond}, TLS: []time.Duration{200 * time.Millisecond}, Successes: 1}
	flaky := Result{TCP: []time.Duration{100 * time.Millisecond}, TLS: []time.Duration{200 * time.Millisecond}, Successes: 1, Failures: 1}
	if score(&flaky, opts) <= score(&clean, opts) {
		t.Fatal("failure penalty not applied")
	}
}

func TestClassifyCertMismatch(t *testing.T) {
	if got := classifyError(errors.New("x509: certificate is valid for example.com, not github.com")); got != ErrCertMismatch {
		t.Fatalf("got %s", got)
	}
	if got := classifyError(errors.New("dial tcp: connect: connection refused")); got != ErrRefused {
		t.Fatalf("got %s", got)
	}
}

func TestMedian(t *testing.T) {
	d := []time.Duration{3 * time.Millisecond, 1 * time.Millisecond, 2 * time.Millisecond}
	if median(d) != 2*time.Millisecond {
		t.Fatalf("got %v", median(d))
	}
	if median(nil) != 0 {
		t.Fatal("empty median should be zero")
	}
}
