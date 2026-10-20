package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

func Serve(ctx context.Context, token string) (int, <-chan struct{}, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stopCh := make(chan struct{})
	var once sync.Once
	requestStop := func() { once.Do(func() { close(stopCh) }) }

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		auth := "Bearer " + token
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(auth)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("stopping"))
		requestStop()
	})

	srv := &http.Server{Handler: mux}
	go func() {
		<-stopCh
		cancel()
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go srv.Serve(ln)

	port := ln.Addr().(*net.TCPAddr).Port
	return port, stopCh, nil
}

func Healthy(info Info) bool {
	if info.Port <= 0 {
		return false
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + itoa(info.Port) + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func Stop(info Info) error {
	if info.Port <= 0 || info.Token == "" {
		return errors.New("no running daemon")
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+itoa(info.Port)+"/stop", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+info.Token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("daemon refused stop request")
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
