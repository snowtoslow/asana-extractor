package asana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/config"
)

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		timeout time.Duration
	}{
		{name: "empty pat", mutate: func(c *config.Config) { c.PAT = "" }},
		{name: "empty workspace", mutate: func(c *config.Config) { c.WorkspaceGID = "" }},
		{name: "invalid url", mutate: func(c *config.Config) { c.BaseURL = "://bad" }},
		{name: "relative url", mutate: func(c *config.Config) { c.BaseURL = "/api" }},
		{name: "non http url", mutate: func(c *config.Config) { c.BaseURL = "ftp://example.invalid" }},
		{name: "url credentials", mutate: func(c *config.Config) { c.BaseURL = "https://user:pass@example.invalid" }},
		{name: "url query", mutate: func(c *config.Config) { c.BaseURL = "https://example.invalid?key=value" }},
		{name: "negative timeout", mutate: func(*config.Config) {}, timeout: -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{PAT: "synthetic-pat", WorkspaceGID: "123", BaseURL: "https://example.invalid"}
			tt.mutate(&cfg)
			if _, err := New(cfg, tt.timeout); err == nil {
				t.Fatal("invalid client configuration accepted")
			}
		})
	}
}

func TestClientLists(t *testing.T) {
	t.Parallel()
	resources := []struct {
		name   string
		fields string
		first  string
		second string
	}{
		{
			name: "users", fields: "gid,name,email",
			first:  `{"gid":"u1","name":"Greg Sanchez","email":"greg@example.invalid"}`,
			second: `{"gid":"u2","name":"Alice","email":"alice@example.invalid"}`,
		},
		{
			name: "projects", fields: "gid,name,archived",
			first:  `{"gid":"p1","name":"Current project","archived":false}`,
			second: `{"gid":"p2","name":"Archived project","archived":true}`,
		},
	}
	for _, resource := range resources {
		t.Run(resource.name, func(t *testing.T) {
			t.Parallel()
			tests := []struct {
				name  string
				pages []string
				want  string
			}{
				{
					name: "single page", pages: []string{`{"data":[` + resource.first + `],"next_page":null}`},
					want: `[` + resource.first + `]`,
				},
				{
					name: "multiple pages",
					pages: []string{
						`{"data":[` + resource.first + `],"next_page":{"offset":"opaque+/= token",` +
							`"path":"/unrelated","uri":"https://example.invalid/unrelated"}}`,
						`{"data":[` + resource.second + `],"next_page":null}`,
					},
					want: `[` + resource.first + `,` + resource.second + `]`,
				},
				{name: "empty list", pages: []string{`{"data":[],"next_page":null}`}, want: `[]`},
				{
					name: "missing next page", pages: []string{`{"data":[` + resource.first + `]}`},
					want: `[` + resource.first + `]`,
				},
				{
					name: "empty offset", pages: []string{`{"data":[` + resource.first + `],"next_page":{"offset":""}}`},
					want: `[` + resource.first + `]`,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						index := int(calls.Add(1)) - 1
						if r.Method != http.MethodGet || r.URL.Path != "/api/1.0/"+resource.name {
							t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						}
						if r.Header.Get("Authorization") != "Bearer synthetic-pat" {
							t.Error("missing or incorrect bearer header")
						}
						if r.Header.Get("Accept") != "application/json" {
							t.Error("missing accept header")
						}
						query := r.URL.Query()
						if query.Get("workspace") != "workspace-123" || query.Get("limit") != "100" {
							t.Error("workspace or pagination limit differs")
						}
						if query.Get("opt_fields") != resource.fields {
							t.Errorf("opt_fields = %q, want %q", query.Get("opt_fields"), resource.fields)
						}
						wantOffset := ""
						if index > 0 {
							wantOffset = "opaque+/= token"
						}
						if query.Get("offset") != wantOffset {
							t.Errorf("offset = %q, want %q", query.Get("offset"), wantOffset)
						}
						if index >= len(tt.pages) {
							t.Error("unexpected extra request")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						if _, err := fmt.Fprint(w, tt.pages[index]); err != nil {
							t.Errorf("write fake response: %v", err)
						}
					}))
					t.Cleanup(server.Close)
					client := newTestClient(t, server)
					got, err := listJSON(t.Context(), client, resource.name)
					if err != nil {
						t.Fatal(err)
					}
					if got != tt.want {
						t.Fatalf("result = %s, want %s", got, tt.want)
					}
					if int(calls.Load()) != len(tt.pages) {
						t.Fatalf("requests = %d, want %d", calls.Load(), len(tt.pages))
					}
				})
			}
		})
	}
}

func TestClientContextCancellation(t *testing.T) {
	t.Parallel()
	for _, resource := range []string{"users", "projects"} {
		t.Run(resource, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			aborted := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cancel()
				<-r.Context().Done()
				close(aborted)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := listJSON(ctx, client, resource)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context cancellation", err)
			}
			<-aborted
		})
	}
}

func TestClientTimeout(t *testing.T) {
	t.Parallel()
	for _, timeout := range []time.Duration{0, 7 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			t.Parallel()
			want := timeout
			if want == 0 {
				want = 30 * time.Second
			}
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Error("request has no deadline")
				}
				remaining := time.Until(deadline)
				if remaining < want-time.Second || remaining > want {
					t.Errorf("remaining timeout = %s, want approximately %s", remaining, want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       http.NoBody,
				}, nil
			})}
			cfg := config.Config{PAT: "synthetic-pat", WorkspaceGID: "123", BaseURL: "https://example.invalid"}
			client, err := New(cfg, timeout)
			if err != nil {
				t.Fatal(err)
			}
			client.http.SetTransport(httpClient.Transport)
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := client.ListUsers(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	cfg := config.Config{
		PAT: "synthetic-pat", WorkspaceGID: "workspace-123", BaseURL: server.URL + "/api/1.0/",
	}
	client, err := New(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}

func listJSON(ctx context.Context, client *Client, resource string) (string, error) {
	if resource == "users" {
		users, err := client.ListUsers(ctx)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(users)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(projects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
