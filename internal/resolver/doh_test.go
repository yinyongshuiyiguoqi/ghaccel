package resolver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OWNER/ghaccel/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDoHResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			t.Errorf("unexpected type %q", r.URL.Query().Get("type"))
		}
		w.Header().Set("Content-Type", "application/dns-json")
		io.WriteString(w, `{"Status":0,"Answer":[{"name":"github.com.","type":5,"data":"edge.github.com."},{"name":"edge.github.com.","type":1,"data":"1.2.3.4"},{"name":"github.com.","type":1,"data":"5.6.7.8"}]}`)
	}))
	defer srv.Close()

	c := NewDoH([]config.Provider{{Name: "test", URL: srv.URL, Enabled: true, Priority: 1, TimeoutMS: 1000}}, false, NewBreaker(2, time.Minute), testLogger())
	got, errs := c.ResolveAll(context.Background(), "github.com")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	ips := got["test"]
	if len(ips) != 2 || ips[0] != "1.2.3.4" || ips[1] != "5.6.7.8" {
		t.Fatalf("unexpected ips: %v", ips)
	}
}

func TestDoHNXDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Status":3,"Answer":[]}`)
	}))
	defer srv.Close()
	c := NewDoH([]config.Provider{{Name: "test", URL: srv.URL, Enabled: true, TimeoutMS: 1000}}, false, NewBreaker(2, time.Minute), testLogger())
	got, errs := c.ResolveAll(context.Background(), "nope.example")
	if len(errs) != 0 || len(got) != 0 {
		t.Fatalf("expected empty success, got ips=%v errs=%v", got, errs)
	}
}

func TestBreaker(t *testing.T) {
	b := NewBreaker(2, 20*time.Millisecond)
	if !b.Allow("p") {
		t.Fatal("should start closed")
	}
	b.Failure("p")
	b.Failure("p")
	if b.Allow("p") {
		t.Fatal("breaker should be open")
	}
	time.Sleep(30 * time.Millisecond)
	if !b.Allow("p") {
		t.Fatal("breaker should half-open after timeout")
	}
	b.Success("p")
	if !b.Allow("p") {
		t.Fatal("breaker should be closed after success")
	}
}

func TestParseIPv4(t *testing.T) {
	if ip, ok := parseIPv4("1.2.3.4"); !ok || ip != "1.2.3.4" {
		t.Fatal("valid IPv4 rejected")
	}
	if _, ok := parseIPv4("2001:db8::1"); ok {
		t.Fatal("IPv6 accepted")
	}
	if _, ok := parseIPv4("not-an-ip"); ok {
		t.Fatal("garbage accepted")
	}
}
