package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	// Exercise the production entry point with an isolated .env, never the repo's.
	t.Chdir(t.TempDir())
	t.Setenv("ASANA_PAT", "test-only-pat")
	t.Setenv("ASANA_WORKSPACE_GID", "test-workspace")
	t.Setenv("ASANA_BASE_URL", "https://example.invalid/api")
	t.Setenv("OUTPUT_PATH", "custom.json")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PAT != "test-only-pat" || cfg.WorkspaceGID != "test-workspace" {
		t.Fatal("required environment values were not loaded")
	}
	if cfg.BaseURL != "https://example.invalid/api" || cfg.OutputPath != "custom.json" {
		t.Fatalf("optional environment values were not loaded: %v", cfg)
	}
}

func TestLoadSources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		file       string
		hasFile    bool
		env        map[string]string
		wantPAT    string
		wantGID    string
		wantURL    string
		wantOutput string
		wantError  string
	}{
		{
			name:    "missing file uses defaults",
			env:     map[string]string{"ASANA_PAT": "test-pat", "ASANA_WORKSPACE_GID": "123"},
			wantPAT: "test-pat", wantGID: "123",
			wantURL: "https://app.asana.com/api/1.0", wantOutput: "data/asana.json",
		},
		{
			name:    "dotenv values",
			hasFile: true,
			file: "ASANA_PAT=file-pat\nASANA_WORKSPACE_GID=456\n" +
				"ASANA_BASE_URL=https://example.invalid/file\nOUTPUT_PATH=file.json\n",
			env: map[string]string{}, wantPAT: "file-pat", wantGID: "456",
			wantURL: "https://example.invalid/file", wantOutput: "file.json",
		},
		{
			name:    "environment overrides dotenv",
			hasFile: true,
			file: "ASANA_PAT=file-pat\nASANA_WORKSPACE_GID=456\n" +
				"ASANA_BASE_URL=https://example.invalid/file\nOUTPUT_PATH=file.json\n",
			env: map[string]string{
				"ASANA_PAT": "env-pat", "ASANA_WORKSPACE_GID": "789",
				"ASANA_BASE_URL": "https://example.invalid/env", "OUTPUT_PATH": "env.json",
			},
			wantPAT: "env-pat", wantGID: "789",
			wantURL: "https://example.invalid/env", wantOutput: "env.json",
		},
		{
			name: "missing pat", env: map[string]string{"ASANA_WORKSPACE_GID": "123"},
			wantError: "ASANA_PAT is required",
		},
		{
			name: "blank pat", env: map[string]string{"ASANA_PAT": " \t", "ASANA_WORKSPACE_GID": "123"},
			wantError: "ASANA_PAT is required",
		},
		{
			name: "missing workspace", env: map[string]string{"ASANA_PAT": "test-pat"},
			wantError: "ASANA_WORKSPACE_GID is required",
		},
		{
			name:      "blank workspace",
			env:       map[string]string{"ASANA_PAT": "test-pat", "ASANA_WORKSPACE_GID": " \t"},
			wantError: "ASANA_WORKSPACE_GID is required",
		},
		{
			name:    "empty environment pat overrides file",
			hasFile: true, file: "ASANA_PAT=file-pat\nASANA_WORKSPACE_GID=123\n",
			env: map[string]string{"ASANA_PAT": ""}, wantError: "ASANA_PAT is required",
		},
		{
			name:    "malformed dotenv hides source",
			hasFile: true, file: "ASANA_PAT=\"test-secret-in-malformed-file",
			env: map[string]string{}, wantError: "invalid environment syntax",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), ".env")
			if tt.hasFile {
				if err := os.WriteFile(path, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lookup := func(key string) (string, bool) {
				value, ok := tt.env[key]
				return value, ok
			}
			cfg, err := load(path, lookup)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
				if strings.Contains(err.Error(), "test-pat") || strings.Contains(err.Error(), "test-secret") {
					t.Fatal("error exposes a test credential")
				}
				if cfg != (Config{}) {
					t.Fatal("failed load returned partial configuration")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PAT != tt.wantPAT || cfg.WorkspaceGID != tt.wantGID {
				t.Fatal("required configuration differs from expected values")
			}
			if cfg.BaseURL != tt.wantURL || cfg.OutputPath != tt.wantOutput {
				t.Fatalf("optional configuration differs: %v", cfg)
			}
		})
	}
}

func TestLoadReadError(t *testing.T) {
	t.Parallel()
	_, err := load(t.TempDir(), func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "read environment file") {
		t.Fatalf("error = %v, want read error", err)
	}
}

func TestConfigMasking(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-test-pat"
	cfg := Config{
		PAT: secret, WorkspaceGID: "123",
		BaseURL: "https://example.invalid/" + secret, OutputPath: "data/" + secret,
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		value := fmt.Sprintf(format, cfg)
		if strings.Contains(value, secret) || !strings.Contains(value, "[REDACTED]") {
			t.Fatalf("format %s did not mask configuration", format)
		}
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("test config", "config", cfg)
	if strings.Contains(logs.String(), secret) || !strings.Contains(logs.String(), "[REDACTED]") {
		t.Fatal("structured log did not mask configuration")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["PAT"]; ok {
		t.Fatal("PAT field was serialized")
	}
	if got := (Config{}).String(); !strings.Contains(got, "[REDACTED]") {
		t.Fatal("empty config formatting lacks masked PAT")
	}
}
