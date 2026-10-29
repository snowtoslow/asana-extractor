package extractor

import (
	"context"

	"github.com/snowtoslow/asana-extractor/internal/asana"
)

// Client supplies the workspace data consumed by an extraction run.
type Client interface {
	ListUsers(ctx context.Context) ([]asana.User, error)
	ListProjects(ctx context.Context) ([]asana.Project, error)
}

// The interface belongs to the consumer; the Asana client need not import it.
var _ Client = (*asana.Client)(nil)
