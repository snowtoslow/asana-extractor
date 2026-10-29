package asana

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// HTTPError reports an unsuccessful API status without retaining the response
// body, which may echo credentials. Use errors.As to inspect StatusCode.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	if e.StatusCode == http.StatusUnauthorized {
		return "asana: invalid or revoked personal access token (http 401)"
	}
	return fmt.Sprintf("asana: unexpected http status %d", e.StatusCode)
}

// requestError keeps unsafe transport and decoder details out of diagnostics.
// Is preserves matching of known causes without exposing a raw error through
// Unwrap, where a URL, header, or response fragment could reveal credentials.
type requestError struct {
	cause error
}

func (e *requestError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return "asana: request canceled"
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "asana: request deadline exceeded"
	}
	return "asana: request or response decoding failed"
}

func (e *requestError) GoString() string {
	return e.Error()
}

func (e *requestError) Is(target error) bool {
	return errors.Is(e.cause, target)
}
