package asana

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/config"
)

func TestClientRetries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		failures   int
		retryAfter string
		dateAfter  time.Duration
		options    []Option
		wantCalls  int32
		wantWait   time.Duration
		wantError  bool
	}{
		{name: "500 then success", status: 500, failures: 1, wantCalls: 2, wantWait: time.Second},
		{name: "501 then success", status: 501, failures: 1, wantCalls: 2, wantWait: time.Second},
		{name: "599 then success", status: 599, failures: 1, wantCalls: 2, wantWait: time.Second},
		{
			name: "429 retry after seconds", status: 429, failures: 1, retryAfter: "4",
			wantCalls: 2, wantWait: 4 * time.Second,
		},
		{
			name: "429 retry after date exceeds cap", status: 429, failures: 1, dateAfter: 45 * time.Second,
			wantCalls: 2, wantWait: 45 * time.Second,
		},
		{
			name: "503 retry after", status: 503, failures: 1, retryAfter: "5",
			wantCalls: 2, wantWait: 5 * time.Second,
		},
		{
			name: "invalid retry after uses backoff", status: 429, failures: 1, retryAfter: "invalid",
			wantCalls: 2, wantWait: time.Second,
		},
		{
			name: "default retry exhaustion", status: 500, failures: 10,
			wantCalls: 4, wantWait: 7 * time.Second, wantError: true,
		},
		{
			name: "configured backoff cap", status: 500, failures: 10,
			options:   []Option{WithRetries(4, time.Second, 2*time.Second)},
			wantCalls: 5, wantWait: 7 * time.Second, wantError: true,
		},
		{
			name: "retries disabled", status: 500, failures: 1,
			options: []Option{WithRetries(0, 0, 0)}, wantCalls: 1, wantError: true,
		},
		{
			name: "immediate retries", status: 500, failures: 1,
			options: []Option{WithRetries(2, 0, 0)}, wantCalls: 2,
		},
		{name: "400 not retried", status: 400, failures: 1, wantCalls: 1, wantError: true},
		{name: "401 not retried", status: 401, failures: 1, wantCalls: 1, wantError: true},
		{name: "403 not retried", status: 403, failures: 1, wantCalls: 1, wantError: true},
		{name: "404 not retried", status: 404, failures: 1, wantCalls: 1, wantError: true},
	}
	for _, resource := range []string{"users", "projects"} {
		t.Run(resource, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						var calls atomic.Int32
						header := tt.retryAfter
						if tt.dateAfter != 0 {
							header = time.Now().Add(tt.dateAfter).UTC().Format(http.TimeFormat)
						}
						server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if r.Header.Get("Authorization") != "Bearer synthetic-pat" {
								t.Error("retry lost bearer authentication")
							}
							w.Header().Set("Content-Type", "application/json")
							if int(calls.Add(1)) <= tt.failures {
								w.Header().Set("Retry-After", header)
								w.WriteHeader(tt.status)
								return
							}
							if _, err := fmt.Fprint(w, `{"data":[],"next_page":null}`); err != nil {
								t.Error(err)
							}
						}))
						client := newRetryTestClient(t, server, tt.options...)
						start := time.Now()
						_, err := listJSON(t.Context(), client, resource)
						if (err != nil) != tt.wantError {
							t.Fatalf("error = %v, want error = %v", err, tt.wantError)
						}
						if tt.wantError {
							var statusError *HTTPError
							if !errors.As(err, &statusError) || statusError.StatusCode != tt.status {
								t.Fatalf("typed status missing: %v", err)
							}
						}
						if calls.Load() != tt.wantCalls {
							t.Fatalf("calls = %d, want %d", calls.Load(), tt.wantCalls)
						}
						if elapsed := time.Since(start); elapsed != tt.wantWait {
							t.Fatalf("synthetic wait = %s, want %s", elapsed, tt.wantWait)
						}
					})
				})
			}
		})
	}
}

func TestClientNetworkRetries(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if _, err := fmt.Fprint(w, `{"data":[]}`); err != nil {
				t.Error(err)
			}
		}))
		client := newRetryTestClient(t, server)
		transport := server.Client().Transport
		calls := 0
		client.http.SetTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("synthetic connection failure")}
			}
			return transport.RoundTrip(r)
		}))
		start := time.Now()
		if _, err := client.ListUsers(t.Context()); err != nil {
			t.Fatal(err)
		}
		if calls != 2 || time.Since(start) != time.Second {
			t.Fatalf("network recovery: calls = %d, elapsed = %s", calls, time.Since(start))
		}
	})
}

func TestClientRetryCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		client := newRetryTestClient(t, server)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		start := time.Now()
		_, err := client.ListUsers(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want deadline exceeded", err)
		}
		if calls.Load() != 1 || time.Since(start) != 2*time.Second {
			t.Fatalf("retry cancellation: calls = %d, elapsed = %s", calls.Load(), time.Since(start))
		}
	})
}

func newRetryTestClient(t *testing.T, server *httptest.Server, options ...Option) *Client {
	t.Helper()
	transport := server.Client().Transport
	cfg := config.Config{PAT: "synthetic-pat", WorkspaceGID: "123", BaseURL: "http://asana.test/api/1.0"}
	client, err := New(cfg, 0, options...)
	if err != nil {
		t.Fatal(err)
	}
	client.http.SetTransport(transport)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}
