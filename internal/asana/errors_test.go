package asana

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientSafeErrorsAndLogs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		body      string
		wantCalls int32
	}{
		{name: "unauthorized", status: 401, body: `{"error":"synthetic-pat"}`, wantCalls: 1},
		{name: "server error", status: 500, body: `{"error":"synthetic-pat"}`, wantCalls: 3},
		{name: "decoder error", status: 200, body: `{"data":[{"gid":"synthetic-pat","email":123}]}`, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Echo-Token", "synthetic-pat")
				w.WriteHeader(tt.status)
				if _, err := fmt.Fprint(w, tt.body); err != nil {
					t.Error(err)
				}
			}))
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			client := newRetryTestClient(t, server, WithRetries(2, 0, 0), WithLogger(logger))
			// Force wire diagnostics on: even echoed headers, bodies, and curl
			// commands must not bypass the client's safe logging boundary.
			client.http.SetDebug(true).SetCurlCmdDebugLog(true)
			users, err := client.ListUsers(t.Context())
			if err == nil || users != nil {
				t.Fatal("failed request returned data or no error")
			}
			assertSafeError(t, err)
			logger.Error("extraction failed", "error", err)
			if tt.status == 401 && !strings.Contains(err.Error(), "invalid or revoked personal access token") {
				t.Fatalf("401 message is unclear: %v", err)
			}
			if strings.Contains(logs.String(), "synthetic-pat") {
				t.Fatal("logs expose PAT")
			}
			if calls.Load() != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
		})
	}
}

func TestClientSafeTransportErrors(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server := httptest.NewTestServer(t, nil)
	client := newRetryTestClient(t, server, WithRetries(2, 0, 0), WithLogger(logger))
	client.http.SetDebug(true)
	cause := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("transport echoed synthetic-pat")}
	calls := 0
	client.http.SetTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, cause
	}))
	_, err := client.ListProjects(t.Context())
	if !errors.Is(err, cause) {
		t.Fatalf("known cause no longer matches: %v", err)
	}
	assertSafeError(t, err)
	logger.Error("extraction failed", "error", err)
	if strings.Contains(logs.String(), "synthetic-pat") {
		t.Fatal("transport logs expose PAT")
	}
	if calls != 3 {
		t.Fatalf("network attempts = %d, want 3", calls)
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, current), "synthetic-pat") {
				t.Fatal("error chain exposes PAT")
			}
		}
	}
}
