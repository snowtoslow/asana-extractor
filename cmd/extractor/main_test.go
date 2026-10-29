package main

import (
	"bytes"
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
		{
			name: "valid configuration", args: []string{"--once", "--output=result.json"},
			pat: "synthetic-test-pat", gid: "123", wantMessage: "configuration valid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			t.Setenv("ASANA_PAT", tt.pat)
			t.Setenv("ASANA_WORKSPACE_GID", tt.gid)
			t.Setenv("ASANA_BASE_URL", "https://example.invalid")
			t.Setenv("OUTPUT_PATH", "data/asana.json")
			var output bytes.Buffer
			if code := run(tt.args, &output); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; %s", code, tt.wantCode, output.String())
			}
			if !strings.Contains(output.String(), tt.wantMessage) {
				t.Fatalf("output missing %q: %s", tt.wantMessage, output.String())
			}
			if strings.Contains(output.String(), "synthetic-test-pat") {
				t.Fatal("startup output exposes PAT")
			}
			if _, err := os.Stat(filepath.Join(dir, "result.json")); !os.IsNotExist(err) {
				t.Fatalf("milestone 1 must not write extraction output: %v", err)
			}
		})
	}
}
