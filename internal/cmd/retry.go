package cmd

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The retry budget. Variables so a test can shrink them; nothing writes them at runtime.
var (
	transientRetryTimeout  = 5 * time.Minute
	transientRetryInterval = 15 * time.Second
	retryAfterFloor        = 1 * time.Second
)

// retryOnTransient repeats the call while the API answers 503 or 429, up to a bounded budget,
// saying so on stderr before each wait. Any other outcome, or the budget running out, returns
// the last result. Ctrl-C cancels the wait through the command's context.
func retryOnTransient[T any](cmd *cobra.Command, call func() (T, *http.Response, error)) (T, *http.Response, error) {
	deadline := time.Now().Add(transientRetryTimeout)
	for {
		out, resp, err := call()
		if err == nil || resp == nil || !retryableStatus(resp.StatusCode) {
			return out, resp, err
		}
		wait := retryAfter(resp)
		if time.Until(deadline) <= wait {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s\ngiving up after %s.\n", firstLine(apiErr(resp, err)), transientRetryTimeout)
			return out, resp, err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\nRetrying in %s.\n", firstLine(apiErr(resp, err)), wait)
		select {
		case <-cmd.Context().Done():
			var zero T
			return zero, resp, cmd.Context().Err()
		case <-time.After(wait):
		}
	}
}

// retryableStatus is true for the statuses that mean the request was not carried out.
func retryableStatus(status int) bool {
	return status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests
}

// retryAfter is the wait the Retry-After header asks for, seconds or an HTTP date, never less
// than a second. A missing or unreadable header gives the fixed interval.
func retryAfter(resp *http.Response) time.Duration {
	header := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if header == "" {
		return transientRetryInterval
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds < 0 {
			return transientRetryInterval
		}
		return withFloor(time.Duration(seconds) * time.Second)
	}
	if when, err := http.ParseTime(header); err == nil {
		wait := time.Until(when)
		if wait < 0 {
			wait = 0
		}
		return withFloor(wait)
	}
	return transientRetryInterval
}

// withFloor never returns less than the one-second floor.
func withFloor(wait time.Duration) time.Duration {
	if wait < retryAfterFloor {
		return retryAfterFloor
	}
	return wait
}

func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
