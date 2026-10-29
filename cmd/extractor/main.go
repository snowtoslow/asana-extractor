package main

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"

	"github.com/snowtoslow/asana-extractor/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, output io.Writer) int {
	opts, err := parseFlags(args, output)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	logger := slog.New(slog.NewTextHandler(output, nil))
	if err != nil {
		logger.Error("invalid command line", "error", err)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 1
	}
	if opts.output != "" {
		cfg.OutputPath = opts.output
	}
	logger.Info("configuration valid; extraction is not implemented yet")

	return 0
}
