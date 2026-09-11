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

// retryAfter is the Retry-After header in seconds, floored, else the fixed interval. A value
// at or beyond the budget falls back to the interval too.
func retryAfter(resp *http.Response) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || seconds < 0 || seconds > int(transientRetryTimeout/time.Second) {
		return transientRetryInterval
	}
	if wait := time.Duration(seconds) * time.Second; wait > retryAfterFloor {
		return wait
	}
	return retryAfterFloor
}

func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
