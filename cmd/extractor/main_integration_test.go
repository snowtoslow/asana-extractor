//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
	"github.com/snowtoslow/asana-extractor/internal/config"
	"github.com/snowtoslow/asana-extractor/internal/extractor"
	"github.com/snowtoslow/asana-extractor/internal/storage"
)

func TestRunEndToEnd(t *testing.T) {
	// Environment and working-directory changes are intentionally serial and
	// confined to t.TempDir. The repository's .env is never opened.
	tests := []struct {
		name           string
		count          int
		failure        string
		useEnvOverride bool
	}{
		{name: "dotenv to populated files", count: 3},
		{name: "empty workspace"},
		{name: "large paginated workspace", count: 1250, useEnvOverride: true},
		{name: "malformed users second page", count: 150, failure: "users"},
		{name: "malformed projects second page", count: 150, failure: "projects"},
		{name: "slow server timeout", count: 1, failure: "slow"},
		{name: "invalid pat", count: 1, failure: "unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			for _, key := range []string{"ASANA_PAT", "ASANA_WORKSPACE_GID", "ASANA_BASE_URL", "OUTPUT_PATH"} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			pat, workspace := "synthetic-e2e-file-pat", "file-workspace"
			if tt.useEnvOverride {
				pat, workspace = "synthetic-e2e-env-pat", "env-workspace"
				t.Setenv("ASANA_PAT", pat)
				t.Setenv("ASANA_WORKSPACE_GID", workspace)
			}
			envData := "ASANA_PAT=synthetic-e2e-file-pat\nASANA_WORKSPACE_GID=file-workspace\n" +
				"ASANA_BASE_URL=http://asana.test/api/1.0\nOUTPUT_PATH=export\n"
			if err := os.WriteFile(".env", []byte(envData), 0o600); err != nil {
				t.Fatal(err)
			}
			outputDir := filepath.Join(root, "export")
			args := []string{"--once"}
			if tt.useEnvOverride {
				t.Setenv("OUTPUT_PATH", "ignored-by-flag")
				args = append(args, "--output=custom-export")
				outputDir = filepath.Join(root, "custom-export")
			}
			previous := []byte(`{"previous":true}`)
			if tt.failure != "" {
				if err := os.MkdirAll(outputDir, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"users.json", "projects.json"} {
					if err := os.WriteFile(filepath.Join(outputDir, name), previous, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			synctest.Test(t, func(t *testing.T) {
				var userCalls, projectCalls atomic.Int32
				server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					entity := strings.TrimPrefix(r.URL.Path, "/api/1.0/")
					fields := "gid,name,email"
					prefix := "u"
					switch entity {
					case "users":
						userCalls.Add(1)
					case "projects":
						projectCalls.Add(1)
						fields, prefix = "gid,name,archived", "p"
					default:
						t.Errorf("unexpected endpoint: %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
						t.Error("incorrect HTTP method or accept header")
					}
					if r.Header.Get("Authorization") != "Bearer "+pat {
						t.Error("incorrect PAT or dotenv/env precedence")
					}
					query := r.URL.Query()
					for key, want := range map[string]string{"workspace": workspace, "limit": "100", "opt_fields": fields} {
						if query.Get(key) != want {
							t.Errorf("query %s = %q, want %q", key, query.Get(key), want)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if tt.failure == "slow" && entity == "users" {
						<-r.Context().Done()
						return
					}
					if tt.failure == "unauthorized" && entity == "users" {
						w.WriteHeader(401)
						return
					}
					offset := 0
					if value := query.Get("offset"); value != "" {
						parsed, err := strconv.Atoi(value)
						if err != nil {
							t.Error("invalid pagination offset")
							w.WriteHeader(400)
							return
						}
						offset = parsed
					}
					isOutOfBounds := offset < 0 || offset > tt.count
					if isOutOfBounds || offset%100 != 0 {
						t.Error("unexpected pagination offset")
						w.WriteHeader(400)
						return
					}
					if tt.failure == entity && offset == 100 {
						if _, err := fmt.Fprint(w, `{"data":invalid}`); err != nil {
							t.Error(err)
						}
						return
					}
					end := min(offset+100, tt.count)
					records := []map[string]any{}
					for index := offset; index < end; index++ {
						record := map[string]any{"gid": fmt.Sprintf("%s-%04d", prefix, index), "name": fmt.Sprintf("Record %d", index)}
						if entity == "users" {
							record["email"] = fmt.Sprintf("user%d@example.invalid", index)
						} else {
							record["archived"] = index%2 == 0
						}
						records = append(records, record)
					}
					var next any
					if end < tt.count {
						next = map[string]string{"offset": strconv.Itoa(end)}
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"data": records, "next_page": next}); err != nil {
						t.Error(err)
					}
				}))
				deps := dependencies{
					loadConfig: config.Load,
					newClient: func(cfg config.Config, logger *slog.Logger) (apiClient, error) {
						return asana.New(
							cfg,
							time.Second,
							asana.WithLogger(logger),
							asana.WithTransport(server.Client().Transport),
							asana.WithRetries(1, 0, 0),
						)
					},
					newWriter: func(path string) extractor.Writer { return storage.New(path) },
					clock:     extractor.RealClock{},
				}
				var logs bytes.Buffer
				start := time.Now()
				code := runWith(
					t.Context(),
					args,
					&logs,
					deps,
				)
				if strings.Contains(logs.String(), pat) || strings.Contains(logs.String(), "synthetic-e2e-file-pat") {
					t.Fatal("PAT exposed in logs")
				}
				wantCode := 0
				if tt.failure != "" {
					wantCode = 1
				}
				if code != wantCode {
					t.Fatalf("exit = %d, want %d; %s", code, wantCode, logs.String())
				}
				if tt.failure == "slow" && time.Since(start) != 2*time.Second {
					t.Fatalf("synthetic timeout duration = %s", time.Since(start))
				}
				var fetchedAt string
				for _, entity := range []string{"users", "projects"} {
					data, err := os.ReadFile(filepath.Join(outputDir, entity+".json"))
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(data, []byte(pat)) {
						t.Fatal("PAT exposed in output")
					}
					if tt.failure != "" {
						if !bytes.Equal(data, previous) {
							t.Fatalf("failed extraction replaced %s", entity)
						}
						continue
					}
					var document map[string]json.RawMessage
					if err := json.Unmarshal(data, &document); err != nil {
						t.Fatal(err)
					}
					if len(document) != 2 {
						t.Fatalf("incorrect %s file shape: %s", entity, data)
					}
					var records []map[string]any
					if err := json.Unmarshal(document[entity], &records); err != nil {
						t.Fatal(err)
					}
					if len(records) != tt.count {
						t.Fatalf("%s count = %d, want %d", entity, len(records), tt.count)
					}
					if tt.count == 0 && string(document[entity]) != "[]" {
						t.Fatal("empty workspace produced null")
					}
					for index, record := range records {
						prefix := "u"
						if entity == "projects" {
							prefix = "p"
						}
						if record["gid"] != fmt.Sprintf("%s-%04d", prefix, index) {
							t.Fatal("pagination lost or reordered records")
						}
						if len(record) != 3 {
							t.Fatal("stored record fields differ from approved schema")
						}
					}
					var timestamp string
					if err := json.Unmarshal(document["fetched_at"], &timestamp); err != nil {
						t.Fatal(err)
					}
					if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
						t.Fatal(err)
					}
					if fetchedAt != "" && timestamp != fetchedAt {
						t.Fatal("output timestamps differ")
					}
					fetchedAt = timestamp
				}
				entries, err := os.ReadDir(outputDir)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 {
					t.Fatal("unexpected output or leaked temporary files")
				}
				if tt.failure == "" {
					pages := int32(max(1, (tt.count+99)/100))
					if userCalls.Load() != pages || projectCalls.Load() != pages {
						t.Fatal("incorrect page request counts")
					}
				}
			})
		})
	}
}
