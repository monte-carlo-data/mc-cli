package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

const transientProblem = `{"code":"upstream_unavailable","detail":"Monte Carlo timed out waiting for your deployment. Try again shortly.","request_id":"req-9","status":503}`

// flakyServer answers `failures` times with the given status, then 200 with a user.
func flakyServer(t *testing.T, failures int32, status int, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n <= failures {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(transientProblem))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"account_frozen":false,"account_id":"a","email":"e","identity_type":"user","user_id":"u"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func retryFixture(t *testing.T, srv *httptest.Server) (*cobra.Command, *bytes.Buffer, func() (*sdk.CurrentUserOut, *http.Response, error)) {
	t.Helper()
	shrink := func(timeout, interval, floor time.Duration) {
		transientRetryTimeout, transientRetryInterval, retryAfterFloor = timeout, interval, floor
	}
	prev := []time.Duration{transientRetryTimeout, transientRetryInterval, retryAfterFloor}
	shrink(2*time.Second, 20*time.Millisecond, 10*time.Millisecond)
	t.Cleanup(func() { shrink(prev[0], prev[1], prev[2]) })

	cmd := &cobra.Command{Use: "t"}
	cmd.SetContext(context.Background())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	api, err := sdk.NewClient(context.Background(), sdk.Options{Endpoint: srv.URL, TokenID: "i", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	return cmd, &stderr, api.UsersAPI.GetCurrentUser(context.Background()).Execute
}

func TestRetryOnTransientRetriesA503ThenSucceeds(t *testing.T) {
	srv, calls := flakyServer(t, 2, http.StatusServiceUnavailable, "")
	cmd, stderr, call := retryFixture(t, srv)

	out, _, err := retryOnTransient(cmd, call)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetEmail() != "e" || calls.Load() != 3 {
		t.Fatalf("out %+v after %d calls", out, calls.Load())
	}
	// users/me declares no 503, so the SDK decodes no problem and the message takes the
	// request form; the detail still comes through from the body.
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 4 || lines[1] != "Retrying in 20ms." || lines[3] != "Retrying in 20ms." {
		t.Fatalf("stderr:\n%s", stderr.String())
	}
	for _, line := range []string{lines[0], lines[2]} {
		if !strings.HasPrefix(line, "GET "+srv.URL) || !strings.Contains(line, "503 Service Unavailable: Monte Carlo timed out") {
			t.Fatalf("stderr:\n%s", stderr.String())
		}
	}
}

func TestRetryOnTransientHonoursRetryAfter(t *testing.T) {
	srv, _ := flakyServer(t, 1, http.StatusTooManyRequests, "1")
	cmd, stderr, call := retryFixture(t, srv)
	transientRetryTimeout = 5 * time.Second

	start := time.Now()
	if _, _, err := retryOnTransient(cmd, call); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < time.Second || !strings.Contains(stderr.String(), "Retrying in 1s.") {
		t.Fatalf("waited %s; stderr:\n%s", time.Since(start), stderr.String())
	}
}

func TestRetryOnTransientGivesUpWhenTheBudgetRunsOut(t *testing.T) {
	srv, calls := flakyServer(t, 1000, http.StatusServiceUnavailable, "")
	cmd, _, call := retryFixture(t, srv)
	transientRetryTimeout = 100 * time.Millisecond

	_, resp, err := retryOnTransient(cmd, call)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("err %v, resp %v", err, resp)
	}
	if n := calls.Load(); n < 2 || n > 10 {
		t.Fatalf("%d calls", n)
	}
}

func TestRetryOnTransientDoesNotRetryOtherStatuses(t *testing.T) {
	srv, calls := flakyServer(t, 5, http.StatusNotFound, "")
	cmd, stderr, call := retryFixture(t, srv)

	if _, _, err := retryOnTransient(cmd, call); err == nil {
		t.Fatal("a 404 was swallowed")
	}
	if calls.Load() != 1 || stderr.Len() != 0 {
		t.Fatalf("%d calls, stderr %q", calls.Load(), stderr.String())
	}
}

func TestRetryOnTransientStopsWhenCancelled(t *testing.T) {
	srv, _ := flakyServer(t, 1000, http.StatusServiceUnavailable, "")
	cmd, _, call := retryFixture(t, srv)
	// A wait shorter than the budget, so the helper sleeps rather than giving up.
	transientRetryTimeout, transientRetryInterval = 10*time.Minute, time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	_, _, err := retryOnTransient(cmd, call)
	if err != context.Canceled {
		t.Fatalf("err %v", err)
	}
}
