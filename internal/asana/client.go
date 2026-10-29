package asana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/config"
	"resty.dev/v3"
)

// Client lists users and projects from one Asana workspace.
// Construct it with New; the zero value is not ready for use.
type Client struct {
	http         *resty.Client
	workspaceGID string
}

// New configures a private Resty client from configuration validated by the caller.
// A zero timeout selects 30 seconds. Options can override retry and logging defaults.
func New(cfg config.Config, timeout time.Duration, options ...Option) (*Client, error) {
	if timeout < 0 {
		return nil, errors.New("asana: request timeout must not be negative")
	}

	if timeout == 0 {
		timeout = 30 * time.Second
	}
	settings := clientOptions{
		retries: 3, wait: time.Second, maxWait: 30 * time.Second,
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("asana: client option must not be nil")
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("configure asana client: %w", err)
		}
	}
	transportClient := new(http.Client)
	transportClient.Transport = settings.transport
	// Keep the PAT on the configured endpoint; pagination uses offsets only.
	transportClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	restyHttpClient := resty.NewWithClient(transportClient).
		SetBaseURL(strings.TrimRight(cfg.BaseURL, "/")).
		SetAuthToken(cfg.PAT).
		SetHeader("Accept", "application/json").
		SetTimeout(timeout).
		SetLogger(httpLogger{logger: settings.logger}).
		SetDebug(false).
		SetRetryCount(settings.retries).
		SetRetryDefaultConditions(false).
		AddRetryConditions(retryable).
		SetRetryDelayStrategy(retryDelay(settings.wait, settings.maxWait))

	return &Client{http: restyHttpClient, workspaceGID: cfg.WorkspaceGID}, nil
}

// Close releases the private Resty client's resources.
func (c *Client) Close() error {
	if err := c.http.Close(); err != nil {
		return fmt.Errorf("close asana client: %w", err)
	}
	return nil
}

// ListUsers returns all users in the configured workspace.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	return list[User](
		ctx,
		c,
		"/users",
		"gid,name,email",
	)
}

// ListProjects returns all projects in the configured workspace.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	return list[Project](
		ctx,
		c,
		"/projects",
		"gid,name,archived",
	)
}

type page[T User | Project] struct {
	Data     []T       `json:"data"`
	NextPage *nextPage `json:"next_page"`
}

type nextPage struct {
	Offset string `json:"offset"`
}

func list[T User | Project](ctx context.Context, c *Client, path, fields string) ([]T, error) {
	items := []T{}
	offset := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list %s: %w", strings.TrimPrefix(path, "/"), &requestError{cause: err})
		}
		result := page[T]{Data: []T{}}
		request := c.http.R().SetContext(ctx).
			SetQueryParams(map[string]string{
				"workspace":  c.workspaceGID,
				"limit":      "100",
				"opt_fields": fields,
			}).SetResult(&result)
		if offset != "" {
			request.SetQueryParam("offset", offset)
		}
		response, err := request.Get(path)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", strings.TrimPrefix(path, "/"), &requestError{cause: err})
		}
		if !response.IsStatusSuccess() {
			return nil, fmt.Errorf("list %s: %w",
				strings.TrimPrefix(path, "/"), &HTTPError{StatusCode: response.StatusCode()})
		}
		items = append(items, result.Data...)
		if result.NextPage == nil || result.NextPage.Offset == "" {
			return items, nil
		}
		offset = result.NextPage.Offset
	}
}
