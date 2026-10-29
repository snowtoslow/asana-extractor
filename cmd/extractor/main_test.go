package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing/synctest"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
	"github.com/snowtoslow/asana-extractor/internal/config"
	"github.com/snowtoslow/asana-extractor/internal/extractor"
	"github.com/snowtoslow/asana-extractor/internal/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		pat         string
		gid         string
		wantCode    int
		wantMessage string
	}{
		{name: "missing pat", args: []string{}, gid: "123", wantCode: 1, wantMessage: "ASANA_PAT is required"},
		{
			name: "missing workspace", args: []string{}, pat: "synthetic-test-pat",
			wantCode: 1, wantMessage: "ASANA_WORKSPACE_GID is required",
		},
		{
			name: "bad flags before config", args: []string{"--interval=1s"},
			wantCode: 2, wantMessage: "--interval must be 5m or 30s",
		},
		{name: "help without credentials", args: []string{"--help"}, wantMessage: "Usage of extractor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			t.Setenv("ASANA_PAT", tt.pat)
			t.Setenv("ASANA_WORKSPACE_GID", tt.gid)
			t.Setenv("ASANA_BASE_URL", "https://example.invalid")
			t.Setenv("OUTPUT_PATH", "data")
			var output bytes.Buffer
			if code := run(t.Context(), tt.args, &output); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; %s", code, tt.wantCode, output.String())
			}
			if !strings.Contains(output.String(), tt.wantMessage) {
				t.Fatalf("output missing %q: %s", tt.wantMessage, output.String())
			}
			if strings.Contains(output.String(), "synthetic-test-pat") {
				t.Fatal("startup output exposes PAT")
			}
			if _, err := os.Stat(filepath.Join(dir, "results")); !os.IsNotExist(err) {
				t.Fatalf("invalid arguments/configuration must not write extraction output: %v", err)
			}
		})
	}
}

func TestRunOnceWiring(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		initError   error
		fetchError  error
		writeError  error
		closeError  error
		wantCode    int
		wantWrites  int
		wantMessage string
	}{
		{name: "success", wantWrites: 1, wantMessage: "extraction finished"},
		{name: "client initialization failure", initError: errors.New("synthetic-test-pat"), wantCode: 1, wantMessage: "initialize asana client failed"},
		{name: "unauthorized", fetchError: &asana.HTTPError{StatusCode: 401}, wantCode: 1, wantMessage: "invalid or revoked personal access token"},
		{name: "unknown fetch failure", fetchError: errors.New("synthetic-test-pat"), wantCode: 1, wantMessage: "extraction failed"},
		{name: "not found", fetchError: &asana.HTTPError{StatusCode: 404}, wantCode: 1, wantMessage: "http_status=404"},
		{name: "writer failure", writeError: errors.New("synthetic-test-pat"), wantCode: 1, wantWrites: 1, wantMessage: "extraction failed"},
		{name: "close failure", closeError: errors.New("synthetic-test-pat"), wantCode: 1, wantWrites: 1, wantMessage: "close asana client failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &appTestClient{fetchError: tt.fetchError, closeError: tt.closeError}
			writer := &appTestWriter{err: tt.writeError}
			clock := &appTestClock{ticker: &appTestTicker{ticks: make(chan time.Time)}}
			deps := testDependencies(client, writer, clock)
			baseFactory := deps.newClient
			deps.newClient = func(cfg config.Config, logger *slog.Logger) (apiClient, error) {
				if cfg.PAT != "synthetic-test-pat" || cfg.WorkspaceGID != "workspace-123" || cfg.BaseURL != "https://example.invalid" {
					t.Error("client configuration differs from loaded configuration")
				}
				if tt.initError != nil {
					return nil, tt.initError
				}
				return baseFactory(cfg, logger)
			}
			var output bytes.Buffer
			code := runWith(
				t.Context(),
				[]string{"--once", "--interval=30s", "--output=custom-output"},
				&output,
				deps,
			)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; logs: %s", code, tt.wantCode, output.String())
			}
			if writer.calls != tt.wantWrites {
				t.Fatalf("writes = %d, want %d", writer.calls, tt.wantWrites)
			}
			if tt.initError == nil {
				if client.closed != 1 {
					t.Fatalf("client closed %d times", client.closed)
				}
				if writer.path != "custom-output" {
					t.Fatalf("writer path = %s", writer.path)
				}
			}
			if tt.wantWrites == 1 && (len(writer.snapshot.Users) != 1 || len(writer.snapshot.Projects) != 1) {
				t.Fatal("writer did not receive complete lists")
			}
			if clock.created != 0 {
				t.Fatal("--once created a scheduler ticker")
			}
			if !strings.Contains(output.String(), tt.wantMessage) {
				t.Fatalf("missing %q: %s", tt.wantMessage, output.String())
			}
			assertMaskedStartup(t, output.String())
		})
	}
}

func TestRunSchedulerWiring(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{5 * time.Minute, 30 * time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &appTestClient{fetchError: &asana.HTTPError{StatusCode: 401}}
				writer := &appTestWriter{}
				clock := &appTestClock{ticker: &appTestTicker{ticks: make(chan time.Time)}}
				deps := testDependencies(client, writer, clock)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var output bytes.Buffer
				stopped := make(chan int)
				args := []string{}
				if interval == 30*time.Second {
					args = []string{"--interval=30s"}
				}
				go func() {
					stopped <- runWith(
						ctx,
						args,
						&output,
						deps,
					)
				}()
				synctest.Wait()
				if client.usersCalls.Load() != 1 || writer.calls != 0 {
					t.Fatal("immediate failed run was not executed safely")
				}
				if clock.created != 1 || clock.interval != interval {
					t.Fatal("CLI interval not passed to scheduler")
				}
				client.fetchError = nil
				clock.ticker.ticks <- time.Now()
				synctest.Wait()
				if client.usersCalls.Load() != 2 || writer.calls != 1 {
					t.Fatal("scheduler failed to recover on next tick")
				}
				if writer.path != "configured-output" {
					t.Fatal("configured output directory not passed to writer")
				}
				cancel()
				if code := <-stopped; code != 0 {
					t.Fatalf("normal shutdown exit = %d", code)
				}
				if client.closed != 1 || !clock.ticker.stopped {
					t.Fatal("shutdown did not release dependencies")
				}
				if !strings.Contains(output.String(), "invalid or revoked personal access token") {
					t.Fatal("401 log missing")
				}
				assertMaskedStartup(t, output.String())
			})
		})
	}
}

func TestRunCanceledOnce(t *testing.T) {
	t.Parallel()
	client := &appTestClient{}
	writer := &appTestWriter{}
	clock := &appTestClock{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	code := runWith(
		ctx,
		[]string{"--once"},
		&output,
		testDependencies(client, writer, clock),
	)
	if code != 1 || writer.calls != 0 || client.usersCalls.Load() != 0 || client.closed != 1 {
		t.Fatalf("canceled single-run cleanup failed: exit %d, writes %d, closed %d", code, writer.calls, client.closed)
	}
	assertMaskedStartup(t, output.String())
}

type appTestClient struct {
	fetchError error
	closeError error
	usersCalls atomic.Int32
	closed     int
}

func (c *appTestClient) ListUsers(context.Context) ([]asana.User, error) {
	c.usersCalls.Add(1)
	if c.fetchError != nil {
		return nil, c.fetchError
	}
	return []asana.User{{GID: "u1", Name: "Greg", Email: "greg@example.invalid"}}, nil
}
func (c *appTestClient) ListProjects(context.Context) ([]asana.Project, error) {
	return []asana.Project{{GID: "p1", Name: "Example project"}}, nil
}
func (c *appTestClient) Close() error { c.closed++; return c.closeError }

type appTestWriter struct {
	path     string
	calls    int
	snapshot storage.Snapshot
	err      error
}

func (w *appTestWriter) Write(ctx context.Context, snapshot storage.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.calls++
	w.snapshot = snapshot
	return w.err
}

type appTestClock struct {
	ticker   *appTestTicker
	interval time.Duration
	created  int
}

func (*appTestClock) Now() time.Time { return time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC) }
func (c *appTestClock) NewTicker(interval time.Duration) extractor.Ticker {
	c.interval = interval
	c.created++
	return c.ticker
}

type appTestTicker struct {
	ticks   chan time.Time
	stopped bool
}

func (t *appTestTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *appTestTicker) Stop()                   { t.stopped = true }

func testDependencies(client *appTestClient, writer *appTestWriter, clock *appTestClock) dependencies {
	return dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				PAT: "synthetic-test-pat", WorkspaceGID: "workspace-123",
				BaseURL: "https://example.invalid", OutputPath: "configured-output",
			}, nil
		},
		newClient: func(config.Config, *slog.Logger) (apiClient, error) { return client, nil },
		newWriter: func(path string) extractor.Writer { writer.path = path; return writer },
		clock:     clock,
	}
}

func assertMaskedStartup(t *testing.T, logs string) {
	t.Helper()
	if strings.Contains(logs, "synthetic-test-pat") {
		t.Fatal("startup or operational logs exposed PAT")
	}
	if !strings.Contains(logs, "extractor starting") || !strings.Contains(logs, "[REDACTED]") {
		t.Fatal("masked startup configuration missing")
	}
}

func TestRunDependencyFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"writer", "clock", "ticker"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			client := &appTestClient{}
			writer := &appTestWriter{}
			clock := &appTestClock{ticker: &appTestTicker{ticks: make(chan time.Time)}}
			deps := testDependencies(client, writer, clock)
			args := []string{"--once"}
			switch failure {
			case "writer":
				deps.newWriter = func(string) extractor.Writer { return nil }
			case "clock":
				deps.clock = nil
			case "ticker":
				clock.ticker.ticks = nil
				args = []string{}
			}
			var output bytes.Buffer
			code := runWith(
				t.Context(),
				args,
				&output,
				deps,
			)
			if code != 1 || client.closed != 1 || writer.calls != 0 {
				t.Fatalf("dependency failure: exit=%d, closed=%d, writes=%d", code, client.closed, writer.calls)
			}
			assertMaskedStartup(t, output.String())
		})
	}
}
