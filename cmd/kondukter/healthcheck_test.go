package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalHealthURL(t *testing.T) {
	for addr, want := range map[string]string{
		"":             "http://127.0.0.1:8080/healthz",
		":8080":        "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000": "http://127.0.0.1:9000/healthz",
		"[::]:9001":    "http://127.0.0.1:9001/healthz",
		"not an addr":  "http://127.0.0.1:8080/healthz",
	} {
		if got := localHealthURL(addr); got != want {
			t.Errorf("localHealthURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("database: down\n"))
	}))
	defer srv.Close()
	run := func() error {
		cmd := newRootCmd()
		cmd.SetArgs([]string{"healthcheck", "--url", srv.URL + "/healthz"})
		return cmd.Execute()
	}
	if err := run(); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := run(); err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "database: down") {
		t.Fatalf("unhealthy: %v", err)
	}
}
