package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
)

func TestWriterWrite(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested", "data")
	snapshot := Snapshot{
		FetchedAt: time.Date(2026, 10, 29, 16, 30, 0, 0, time.FixedZone("local", 2*60*60)),
		Users:     []asana.User{{GID: "u1", Name: "Greg Sanchez", Email: "greg@example.invalid"}},
		Projects:  []asana.Project{{GID: "p1", Name: "Example project", Archived: true}},
	}
	if err := New(dir).Write(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	for _, entity := range []string{"users", "projects"} {
		path := filepath.Join(dir, entity+".json")
		data := readOutput(t, path)
		if !strings.Contains(string(data), "\n  \""+entity+"\": [\n    {") || data[len(data)-1] != '\n' {
			t.Fatal("JSON is not indented and newline terminated")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 2 || string(fields["fetched_at"]) != `"2026-10-29T14:30:00Z"` {
			t.Fatalf("unexpected %s document shape or timestamp: %s", entity, data)
		}
		if entity == "users" {
			var users []asana.User
			if err := json.Unmarshal(fields[entity], &users); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(users, snapshot.Users) {
				t.Fatalf("users differ: %+v", users)
			}
		} else {
			var projects []asana.Project
			if err := json.Unmarshal(fields[entity], &projects); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(projects, snapshot.Projects) {
				t.Fatalf("projects differ: %+v", projects)
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("output permissions = %o", info.Mode().Perm())
		}
	}
	assertOnlyOutputs(t, dir)
}

func TestWriterEmptyListsAndOverwrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		snapshot Snapshot
	}{
		{name: "nil lists", snapshot: Snapshot{}},
		{name: "empty lists", snapshot: Snapshot{Users: []asana.User{}, Projects: []asana.Project{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writer := New(dir)
			initial := Snapshot{Users: []asana.User{{GID: "old-user"}}, Projects: []asana.Project{{GID: "old-project"}}}
			if err := writer.Write(t.Context(), initial); err != nil {
				t.Fatal(err)
			}
			if err := writer.Write(t.Context(), tt.snapshot); err != nil {
				t.Fatal(err)
			}
			for _, entity := range []string{"users", "projects"} {
				data := readOutput(t, filepath.Join(dir, entity+".json"))
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				if len(fields) != 2 || string(fields[entity]) != "[]" {
					t.Fatalf("incorrect empty document: %s", data)
				}
				if strings.Contains(string(data), "old-") {
					t.Fatal("previous records remained after overwrite")
				}
			}
			if tt.name == "nil lists" && (tt.snapshot.Users != nil || tt.snapshot.Projects != nil) {
				t.Fatal("writer mutated caller's snapshot")
			}
			assertOnlyOutputs(t, dir)
		})
	}
}

func TestWriterStagesBothBeforePublishing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seedOutputs(t, dir)
	writer := New(dir)
	temporary := []*testFile{}
	writer.createTemp = func(dir, pattern string) (tempFile, error) {
		file, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		wrapped := &testFile{File: file}
		temporary = append(temporary, wrapped)
		return wrapped, nil
	}
	publications := 0
	writer.rename = func(from, to string) error {
		if filepath.Dir(from) != dir || filepath.Dir(to) != dir {
			t.Fatal("temporary file is not beside output")
		}
		if len(temporary) != 2 {
			t.Fatal("publishing before both documents were staged")
		}
		for _, file := range temporary {
			if !file.isSynced || !file.isClosed {
				t.Fatal("publishing before both files were synced and closed")
			}
		}
		if string(readOutput(t, to)) != `{"previous":true}` {
			t.Fatal("destination changed before its rename")
		}
		if !json.Valid(readOutput(t, from)) {
			t.Fatal("publishing incomplete JSON")
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		publications++
		return nil
	}
	if err := writer.Write(t.Context(), Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if publications != 2 {
		t.Fatalf("published %d files, want 2", publications)
	}
	assertOnlyOutputs(t, dir)
}

func TestWriterFailuresPreserveOutputs(t *testing.T) {
	t.Parallel()
	for _, failedEntity := range []string{"users", "projects"} {
		t.Run(failedEntity, func(t *testing.T) {
			for _, stage := range []string{"create", "write", "short write", "sync", "close", "cancel"} {
				t.Run(stage, func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					seedOutputs(t, dir)
					cause := errors.New("injected filesystem failure")
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					writer := New(dir)
					temporary := []*testFile{}
					writer.createTemp = func(dir, pattern string) (tempFile, error) {
						isTarget := strings.HasPrefix(pattern, "."+failedEntity+".json")
						if isTarget && stage == "create" {
							return nil, cause
						}
						file, err := os.CreateTemp(dir, pattern)
						if err != nil {
							return nil, err
						}
						wrapped := &testFile{File: file}
						if isTarget {
							wrapped.stage, wrapped.cause = stage, cause
							if stage == "cancel" {
								wrapped.onSync = cancel
							}
						}
						temporary = append(temporary, wrapped)
						return wrapped, nil
					}
					want := cause
					if stage == "short write" {
						want = io.ErrShortWrite
					}
					if stage == "cancel" {
						want = context.Canceled
					}
					if err := writer.Write(ctx, Snapshot{}); !errors.Is(err, want) {
						t.Fatalf("error = %v, want %v", err, want)
					}
					assertPreviousOutputs(t, dir)
					for _, file := range temporary {
						if !file.isClosed {
							t.Fatal("failed write leaked temporary file handle")
						}
					}
					assertOnlyOutputs(t, dir)
				})
			}
		})
	}
}

func TestWriterRenameFailure(t *testing.T) {
	t.Parallel()
	for _, failedEntity := range []string{"users", "projects"} {
		t.Run(failedEntity, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			seedOutputs(t, dir)
			cause := errors.New("rename failed")
			writer := New(dir)
			writer.rename = func(from, to string) error {
				if filepath.Base(to) == failedEntity+".json" {
					return cause
				}
				return os.Rename(from, to)
			}
			if err := writer.Write(t.Context(), Snapshot{}); !errors.Is(err, cause) {
				t.Fatalf("error = %v", err)
			}
			projects := readOutput(t, filepath.Join(dir, "projects.json"))
			if string(projects) != `{"previous":true}` {
				t.Fatal("failed projects publication replaced old file")
			}
			users := readOutput(t, filepath.Join(dir, "users.json"))
			if failedEntity == "users" && string(users) != `{"previous":true}` {
				t.Fatal("failed users publication replaced old file")
			}
			if failedEntity == "projects" && (string(users) == `{"previous":true}` || !json.Valid(users)) {
				t.Fatal("first file should remain completely published after second rename fails")
			}
			assertOnlyOutputs(t, dir)
		})
	}
}

func TestWriterEarlyFailures(t *testing.T) {
	t.Parallel()
	t.Run("encode preserves both files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		seedOutputs(t, dir)
		snapshot := Snapshot{FetchedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
		if err := New(dir).Write(t.Context(), snapshot); err == nil {
			t.Fatal("unencodable timestamp accepted")
		}
		assertPreviousOutputs(t, dir)
		assertOnlyOutputs(t, dir)
	})
	t.Run("directory creation fails", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("previous"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := New(blocker).Write(t.Context(), Snapshot{}); err == nil {
			t.Fatal("file accepted as output directory")
		}
		if string(readOutput(t, blocker)) != "previous" {
			t.Fatal("directory failure changed existing file")
		}
	})
	t.Run("already canceled creates nothing", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "not-created")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := New(dir).Write(ctx, Snapshot{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled writer created directory: %v", err)
		}
	})
	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		if err := New("").Write(t.Context(), Snapshot{}); err == nil {
			t.Fatal("empty output directory accepted")
		}
	})
}

func seedOutputs(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"users.json", "projects.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"previous":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertPreviousOutputs(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"users.json", "projects.json"} {
		if string(readOutput(t, filepath.Join(dir, name))) != `{"previous":true}` {
			t.Fatalf("staging failure changed %s", name)
		}
	}
}

type testFile struct {
	*os.File
	stage    string
	cause    error
	onSync   func()
	isSynced bool
	isClosed bool
}

func (f *testFile) Write(data []byte) (int, error) {
	if f.stage == "write" || f.stage == "short write" {
		n, err := f.File.Write(data[:1])
		if f.stage == "write" {
			return n, errors.Join(err, f.cause)
		}
		return n, err
	}
	return f.File.Write(data)
}

func (f *testFile) Sync() error {
	if f.stage == "sync" {
		return f.cause
	}
	if err := f.File.Sync(); err != nil {
		return err
	}
	f.isSynced = true
	if f.onSync != nil {
		f.onSync()
	}
	return nil
}

func (f *testFile) Close() error {
	f.isClosed = true
	err := f.File.Close()
	if f.stage == "close" {
		return errors.Join(err, f.cause)
	}
	return err
}

func readOutput(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertOnlyOutputs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "projects.json" || entries[1].Name() != "users.json" {
		t.Fatalf("unexpected output files or leftover temporary files: %v", entries)
	}
}
