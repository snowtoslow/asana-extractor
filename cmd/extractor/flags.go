package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

type options struct {
	interval time.Duration
	isOnce   bool
	output   string
}

func parseFlags(args []string, output io.Writer) (options, error) {
	flags := flag.NewFlagSet("extractor", flag.ContinueOnError)
	flags.SetOutput(output)
	interval := flags.String("interval", "5m", "extraction interval: 5m or 30s")
	isOnce := flags.Bool("once", false, "run one extraction and exit")
	path := flags.String("output", "", "output directory for users.json and projects.json (overrides OUTPUT_PATH)")
	if err := flags.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return options{}, errors.New("unexpected positional arguments")
	}
	var duration time.Duration
	switch *interval {
	case "5m":
		duration = 5 * time.Minute
	case "30s":
		duration = 30 * time.Second
	default:
		flags.Usage()
		return options{}, errors.New("--interval must be 5m or 30s")
	}
	return options{interval: duration, isOnce: *isOnce, output: *path}, nil
}
