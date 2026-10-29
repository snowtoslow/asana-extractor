package extractor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
	"github.com/snowtoslow/asana-extractor/internal/storage"
	"golang.org/x/sync/errgroup"
)

// Writer publishes both entity lists from one successful extraction.
type Writer interface {
	Write(ctx context.Context, snapshot storage.Snapshot) error
}

// Clock supplies timestamps and elapsed run time.
type Clock interface {
	Now() time.Time
}

// Extractor performs one complete fetch-and-write operation.
// Construct it with New. Scheduling and run serialization belong to the caller.
type Extractor struct {
	client Client
	writer Writer
	clock  Clock
	logger *slog.Logger
}

// New creates an extractor with explicit dependencies for isolated runs and tests.
func New(client Client, writer Writer, clock Clock, logger *slog.Logger) (*Extractor, error) {
	if client == nil {
		return nil, errors.New("extractor: client is required")
	}
	if writer == nil {
		return nil, errors.New("extractor: writer is required")
	}
	if clock == nil {
		return nil, errors.New("extractor: clock is required")
	}
	if logger == nil {
		return nil, errors.New("extractor: logger is required")
	}
	return &Extractor{client: client, writer: writer, clock: clock, logger: logger}, nil
}

// Run fetches both complete lists concurrently. A fetch failure cancels its
// sibling and prevents all writes. FetchedAt records fetch completion in UTC.
// Errors are returned for the caller to log; run logs contain only timing,
// counts and success status, never configuration, records or error details.
func (e *Extractor) Run(ctx context.Context) (err error) {
	started := e.clock.Now()
	users := []asana.User{}
	projects := []asana.Project{}
	e.logger.InfoContext(ctx, "extraction started")
	defer func() {
		e.logger.InfoContext(
			ctx,
			"extraction finished",
			"duration",
			e.clock.Now().Sub(started),
			"users",
			len(users),
			"projects",
			len(projects),
			"success",
			err == nil,
		)
	}()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("extract data: %w", err)
	}
	group, fetchCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		fetched, err := e.client.ListUsers(fetchCtx)
		if err != nil {
			return fmt.Errorf("fetch users: %w", err)
		}
		users = fetched
		return nil
	})
	group.Go(func() error {
		fetched, err := e.client.ListProjects(fetchCtx)
		if err != nil {
			return fmt.Errorf("fetch projects: %w", err)
		}
		projects = fetched
		return nil
	})
	if err := group.Wait(); err != nil {
		return fmt.Errorf("extract data: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("extract data: %w", err)
	}
	// errgroup cancels fetchCtx when Wait returns, even on success. The writer
	// must use the original run context so successful fetches can be published.
	snapshot := storage.Snapshot{FetchedAt: e.clock.Now().UTC(), Users: users, Projects: projects}
	if err := e.writer.Write(ctx, snapshot); err != nil {
		return fmt.Errorf("write extraction: %w", err)
	}
	return nil
}

var _ Writer = (*storage.Writer)(nil)
