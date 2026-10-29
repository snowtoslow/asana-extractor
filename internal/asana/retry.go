package asana

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"resty.dev/v3"
)

func retryable(response *resty.Response, err error) bool {
	if response != nil && response.Request.Context().Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if response != nil && response.StatusCode() != 0 {
		status := response.StatusCode()
		return status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
	}
	var networkError net.Error
	if urlError, ok := errors.AsType[*url.Error](err); ok {
		err = urlError.Err
	}
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func retryDelay(wait, maxWait time.Duration) resty.RetryDelayStrategyFunc {
	return func(response *resty.Response, _ error) (time.Duration, error) {
		delay := wait
		if delay == 0 {
			return 0, nil
		}
		// Resty numbers the failed initial attempt as 1. Saturate before
		// doubling so large retry counts cannot overflow time.Duration.
		for attempt := 1; attempt < response.Request.Attempt && delay < maxWait; attempt++ {
			if delay > maxWait/2 {
				return maxWait, nil
			}
			delay *= 2
		}
		return delay, nil
	}
}
