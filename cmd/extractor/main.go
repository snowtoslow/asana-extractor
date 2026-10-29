package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/snowtoslow/asana-extractor/internal/asana"
	"github.com/snowtoslow/asana-extractor/internal/config"
	"github.com/snowtoslow/asana-extractor/internal/extractor"
	"github.com/snowtoslow/asana-extractor/internal/storage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	stop() // os.Exit does not execute deferred functions.
	os.Exit(code)
}

type apiClient interface {
	extractor.Client
	Close() error
}

type appClock interface {
	extractor.Clock
	extractor.TickerFactory
}

type dependencies struct {
	loadConfig func() (config.Config, error)
	newClient  func(config.Config, *slog.Logger) (apiClient, error)
	newWriter  func(string) extractor.Writer
	clock      appClock
}

func run(ctx context.Context, args []string, output io.Writer) int {
	deps := dependencies{
		loadConfig: config.Load,
		newClient: func(cfg config.Config, logger *slog.Logger) (apiClient, error) {
			return asana.New(cfg, 0, asana.WithLogger(logger))
		},
		newWriter: func(path string) extractor.Writer { return storage.New(path) },
		clock:     extractor.RealClock{},
	}
	return runWith(
		ctx,
		args,
		output,
		deps,
	)
}

func runWith(ctx context.Context, args []string, output io.Writer, deps dependencies) (code int) {
	opts, err := parseFlags(args, output)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	logger := slog.New(slog.NewTextHandler(output, nil))
	if err != nil {
		logger.ErrorContext(
			ctx,
			"invalid command line",
			"error",
			err,
		)
		return 2
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		// The config loader returns credential-free validation/parser errors.
		logger.ErrorContext(
			ctx,
			"invalid configuration",
			"error",
			err,
		)
		return 1
	}
	if opts.output != "" {
		cfg.OutputPath = opts.output
	}
	logger.InfoContext(
		ctx,
		"extractor starting",
		"config",
		cfg,
		"interval",
		opts.interval,
		"once",
		opts.isOnce,
	)
	client, err := deps.newClient(cfg, logger)
	if err != nil {
		logFailure(
			ctx,
			logger,
			"initialize asana client failed",
			err,
		)
		return 1
	}
	defer func() {
		if err := client.Close(); err != nil {
			logFailure(
				ctx,
				logger,
				"close asana client failed",
				err,
			)
			code = 1
		}
	}()
	runner, err := extractor.New(
		client,
		deps.newWriter(cfg.OutputPath),
		deps.clock,
		logger,
	)
	if err != nil {
		logFailure(
			ctx,
			logger,
			"initialize extractor failed",
			err,
		)
		return 1
	}
	if opts.isOnce {
		if err := runner.Run(ctx); err != nil {
			logFailure(
				ctx,
				logger,
				"extraction failed",
				err,
			)
			return 1
		}
		return 0
	}
	scheduler, err := extractor.NewScheduler(
		runner,
		deps.clock,
		opts.interval,
		logger,
	)
	if err != nil {
		logFailure(
			ctx,
			logger,
			"initialize scheduler failed",
			err,
		)
		return 1
	}
	if err := scheduler.Run(ctx); err != nil {
		logFailure(
			ctx,
			logger,
			"scheduler failed",
			err,
		)
		return 1
	}
	logger.InfoContext(ctx, "extractor stopped")
	return 0
}

func logFailure(ctx context.Context, logger *slog.Logger, message string, err error) {
	// Storage paths and arbitrary dependency errors can contain credentials.
	// Only typed status codes and fixed descriptions cross the logging boundary.
	var statusError *asana.HTTPError
	if errors.As(err, &statusError) {
		if statusError.StatusCode == http.StatusUnauthorized {
			logger.ErrorContext(
				ctx,
				message,
				"reason",
				"invalid or revoked personal access token",
			)
			return
		}
		logger.ErrorContext(
			ctx,
			message,
			"http_status",
			statusError.StatusCode,
		)
		return
	}
	if errors.Is(err, context.Canceled) {
		logger.ErrorContext(
			ctx,
			message,
			"reason",
			"canceled",
		)
		return
	}
	logger.ErrorContext(ctx, message)
}
