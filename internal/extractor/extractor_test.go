package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
	"github.com/snowtoslow/asana-extractor/internal/storage"
)

func TestNew(t *testing.T) {
	t.Parallel()
	client := successfulClient()
	writer := writerFunc(func(context.Context, storage.Snapshot) error { return nil })
	clock := clockFunc(func() time.Time { return time.Time{} })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name   string
		client Client
		writer Writer
		clock  Clock
		logger *slog.Logger
	}{
		{name: "missing client", writer: writer, clock: clock, logger: logger},
		{name: "missing writer", client: client, clock: clock, logger: logger},
		{name: "missing clock", client: client, writer: writer, logger: logger},
		{name: "missing logger", client: client, writer: writer, clock: clock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(
				tt.client,
				tt.writer,
				tt.clock,
				tt.logger,
			)
			if err == nil {
				t.Fatal("missing dependency accepted")
			}
		})
	}
}

func TestExtractorRunSuccess(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		usersStarted, projectsStarted := make(chan struct{}), make(chan struct{})
		users := []asana.User{{GID: "u1", Name: "Greg", Email: "greg@example.invalid"}}
		projects := []asana.Project{{GID: "p1", Name: "Current"}, {GID: "p2", Name: "Archived", Archived: true}}
		client := &fakeClient{
			pat: "synthetic-test-pat",
			users: func(ctx context.Context) ([]asana.User, error) {
				close(usersStarted)
				select {
				case <-projectsStarted:
					return users, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			projects: func(ctx context.Context) ([]asana.Project, error) {
				close(projectsStarted)
				select {
				case <-usersStarted:
					return projects, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}
		dir := t.TempDir()
		diskWriter := storage.New(dir)
		writes := 0
		var captured storage.Snapshot
		writer := writerFunc(func(ctx context.Context, snapshot storage.Snapshot) error {
			if ctx.Err() != nil {
				t.Fatal("successful fetches passed a canceled writer context")
			}
			writes++
			captured = snapshot
			return diskWriter.Write(ctx, snapshot)
		})
		start := time.Date(2026, 10, 29, 16, 0, 0, 0, time.FixedZone("local", 2*60*60))
		times := []time.Time{start, start.Add(2 * time.Second), start.Add(5 * time.Second)}
		index := 0
		clock := clockFunc(func() time.Time {
			if index >= len(times) {
				t.Fatal("unexpected clock call")
			}
			value := times[index]
			index++
			return value
		})
		var logs bytes.Buffer
		runner, err := New(
			client,
			writer,
			clock,
			slog.New(slog.NewJSONHandler(&logs, nil)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		if writes != 1 {
			t.Fatalf("writer calls = %d, want 1", writes)
		}
		if !reflect.DeepEqual(captured.Users, users) || !reflect.DeepEqual(captured.Projects, projects) {
			t.Fatal("writer did not receive both complete lists")
		}
		if captured.FetchedAt != times[1].UTC() {
			t.Fatalf("fetched_at = %s, want %s", captured.FetchedAt, times[1].UTC())
		}
		for _, entity := range []string{"users", "projects"} {
			data, err := os.ReadFile(filepath.Join(dir, entity+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]json.RawMessage
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if len(document) != 2 || string(document["fetched_at"]) != `"2026-10-29T14:00:02Z"` {
				t.Fatalf("incorrect %s document: %s", entity, data)
			}
			wantCount := 1
			if entity == "projects" {
				wantCount = 2
			}
			var records []json.RawMessage
			if err := json.Unmarshal(document[entity], &records); err != nil {
				t.Fatal(err)
			}
			if len(records) != wantCount {
				t.Fatalf("%s count = %d", entity, len(records))
			}
			if bytes.Contains(data, []byte(client.pat)) {
				t.Fatal("output exposed PAT")
			}
		}
		entries := decodeLogs(t, &logs)
		if len(entries) != 2 || entries[0]["msg"] != "extraction started" || entries[1]["msg"] != "extraction finished" {
			t.Fatalf("incorrect lifecycle logs: %+v", entries)
		}
		finished := entries[1]
		if finished["users"] != float64(1) || finished["projects"] != float64(2) || finished["success"] != true {
			t.Fatalf("incorrect completion counts: %+v", finished)
		}
		if finished["duration"] != float64(5*time.Second) {
			t.Fatalf("duration = %v", finished["duration"])
		}
	})
}

func TestExtractorRunFetchFailure(t *testing.T) {
	t.Parallel()
	for _, entity := range []string{"users", "projects"} {
		t.Run(entity, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				previous := []byte(`{"previous":true}`)
				for _, name := range []string{"users.json", "projects.json"} {
					if err := os.WriteFile(filepath.Join(dir, name), previous, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				cause := errors.New("synthetic-test-pat: fake API failure")
				started := make(chan struct{})
				isSiblingCanceled := false
				client := successfulClient()
				if entity == "users" {
					client.users = func(ctx context.Context) ([]asana.User, error) {
						<-started
						return nil, cause
					}
					client.projects = func(ctx context.Context) ([]asana.Project, error) {
						close(started)
						<-ctx.Done()
						isSiblingCanceled = true
						return nil, ctx.Err()
					}
				} else {
					client.projects = func(ctx context.Context) ([]asana.Project, error) {
						<-started
						return nil, cause
					}
					client.users = func(ctx context.Context) ([]asana.User, error) {
						close(started)
						<-ctx.Done()
						isSiblingCanceled = true
						return nil, ctx.Err()
					}
				}
				writes := 0
				diskWriter := storage.New(dir)
				writer := writerFunc(func(ctx context.Context, snapshot storage.Snapshot) error {
					writes++
					return diskWriter.Write(ctx, snapshot)
				})
				var logs bytes.Buffer
				runner, err := New(
					client,
					writer,
					clockFunc(func() time.Time { return time.Time{} }),
					slog.New(slog.NewJSONHandler(&logs, nil)),
				)
				if err != nil {
					t.Fatal(err)
				}
				err = runner.Run(t.Context())
				if !errors.Is(err, cause) || !strings.Contains(err.Error(), "fetch "+entity) {
					t.Fatalf("missing fetch context or cause: %v", err)
				}
				if !isSiblingCanceled || writes != 0 {
					t.Fatalf("sibling canceled = %v, writer calls = %d", isSiblingCanceled, writes)
				}
				for _, name := range []string{"users.json", "projects.json"} {
					data, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(data, previous) {
						t.Fatalf("fetch failure changed %s", name)
					}
				}
				entries := decodeLogs(t, &logs)
				if entries[len(entries)-1]["success"] != false {
					t.Fatal("failed run logged success")
				}
			})
		})
	}
}

func TestExtractorRunWriterFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("synthetic-test-pat: fake writer failure")
	writes := 0
	writer := writerFunc(func(context.Context, storage.Snapshot) error { writes++; return cause })
	var logs bytes.Buffer
	runner, err := New(
		successfulClient(),
		writer,
		clockFunc(func() time.Time { return time.Time{} }),
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context()); !errors.Is(err, cause) || !strings.Contains(err.Error(), "write extraction") {
		t.Fatalf("missing writer cause or context: %v", err)
	}
	if writes != 1 {
		t.Fatalf("writer calls = %d, want 1", writes)
	}
	entries := decodeLogs(t, &logs)
	if entries[len(entries)-1]["success"] != false {
		t.Fatal("writer failure logged success")
	}
}

func TestExtractorRunCanceled(t *testing.T) {
	t.Parallel()
	client := &fakeClient{
		users: func(context.Context) ([]asana.User, error) { t.Error("canceled run fetched users"); return nil, nil },
		projects: func(context.Context) ([]asana.Project, error) {
			t.Error("canceled run fetched projects")
			return nil, nil
		},
	}
	writer := writerFunc(func(context.Context, storage.Snapshot) error { t.Error("canceled run wrote output"); return nil })
	runner, err := New(
		client,
		writer,
		clockFunc(func() time.Time { return time.Time{} }),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
}

type fakeClient struct {
	pat      string
	users    func(context.Context) ([]asana.User, error)
	projects func(context.Context) ([]asana.Project, error)
}

func (c *fakeClient) ListUsers(ctx context.Context) ([]asana.User, error) { return c.users(ctx) }
func (c *fakeClient) ListProjects(ctx context.Context) ([]asana.Project, error) {
	return c.projects(ctx)
}

func successfulClient() *fakeClient {
	return &fakeClient{
		pat:      "synthetic-test-pat",
		users:    func(context.Context) ([]asana.User, error) { return []asana.User{}, nil },
		projects: func(context.Context) ([]asana.Project, error) { return []asana.Project{}, nil },
	}
}

type writerFunc func(context.Context, storage.Snapshot) error

func (f writerFunc) Write(ctx context.Context, snapshot storage.Snapshot) error {
	return f(ctx, snapshot)
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

func decodeLogs(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	if strings.Contains(logs.String(), "synthetic-test-pat") {
		t.Fatal("run logs exposed PAT")
	}
	entries := []map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	for {
		entry := map[string]any{}
		if err := decoder.Decode(&entry); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}
