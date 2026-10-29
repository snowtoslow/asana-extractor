package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want options
	}{
		{name: "defaults", args: []string{}, want: options{interval: 5 * time.Minute}},
		{name: "five minutes", args: []string{"--interval=5m"}, want: options{interval: 5 * time.Minute}},
		{
			name: "all flags", args: []string{"--interval=30s", "--once", "--output", "custom.json"},
			want: options{interval: 30 * time.Second, isOnce: true, output: "custom.json"},
		},
		{name: "explicit false", args: []string{"--once=false"}, want: options{interval: 5 * time.Minute}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			got, err := parseFlags(tt.args, &output)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("options = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseFlagsInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "unsupported interval", args: []string{"--interval=1m"}},
		{name: "equivalent duration rejected", args: []string{"--interval=300s"}},
		{name: "empty interval", args: []string{"--interval="}},
		{name: "invalid duration", args: []string{"--interval=banana"}},
		{name: "unknown flag", args: []string{"--unknown"}},
		{name: "missing argument", args: []string{"--output"}},
		{name: "invalid boolean", args: []string{"--once=maybe"}},
		{name: "positional argument", args: []string{"extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			if _, err := parseFlags(tt.args, &output); err == nil {
				t.Fatal("invalid flags accepted")
			}
			if !strings.Contains(output.String(), "Usage of extractor") {
				t.Fatalf("usage missing: %s", output.String())
			}
		})
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	_, err := parseFlags([]string{"--help"}, &output)
	if !errors.Is(err, flag.ErrHelp) || !strings.Contains(output.String(), "Usage of extractor") {
		t.Fatalf("help: error = %v, output = %s", err, output.String())
	}
}
