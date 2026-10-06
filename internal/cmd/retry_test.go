// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
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

// retryFixture builds a command and a call bound to srv, with the retry budget set to the given
// timeout, interval and floor for the duration of the test. The call always uses the command's
// current context, so a test may cancel it after the fixture is built.
func retryFixture(t *testing.T, srv *httptest.Server, timeout, interval, floor time.Duration) (*cobra.Command, *bytes.Buffer, func() (*sdk.CurrentUserOut, *http.Response, error)) {
	t.Helper()
	prev := []time.Duration{transientRetryTimeout, transientRetryInterval, retryAfterFloor}
	transientRetryTimeout, transientRetryInterval, retryAfterFloor = timeout, interval, floor
	t.Cleanup(func() {
		transientRetryTimeout, transientRetryInterval, retryAfterFloor = prev[0], prev[1], prev[2]
	})

	cmd := &cobra.Command{Use: "t"}
	cmd.SetContext(context.Background())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	api, err := sdk.NewClient(context.Background(), sdk.Options{Endpoint: srv.URL, TokenID: "i", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	call := func() (*sdk.CurrentUserOut, *http.Response, error) {
		return api.UsersAPI.GetCurrentUser(cmd.Context()).Execute()
	}
	return cmd, &stderr, call
}

func TestRetryOnTransientRetriesA503ThenSucceeds(t *testing.T) {
	srv, calls := flakyServer(t, 2, http.StatusServiceUnavailable, "")
	cmd, stderr, call := retryFixture(t, srv, 2*time.Second, 20*time.Millisecond, 10*time.Millisecond)

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
	cmd, stderr, call := retryFixture(t, srv, 5*time.Second, 20*time.Millisecond, 10*time.Millisecond)

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
	cmd, stderr, call := retryFixture(t, srv, 100*time.Millisecond, 20*time.Millisecond, 10*time.Millisecond)

	_, resp, err := retryOnTransient(cmd, call)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("err %v, resp %v", err, resp)
	}
	if n := calls.Load(); n > 10 {
		t.Fatalf("%d calls", n)
	}
	if !strings.Contains(stderr.String(), "Retrying in") {
		t.Fatalf("no retry line in stderr:\n%s", stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "giving up after") {
		t.Fatalf("last line %q, stderr:\n%s", last, stderr.String())
	}
}

func TestRetryOnTransientDoesNotRetryOtherStatuses(t *testing.T) {
	srv, calls := flakyServer(t, 5, http.StatusNotFound, "")
	cmd, stderr, call := retryFixture(t, srv, 2*time.Second, 20*time.Millisecond, 10*time.Millisecond)

	if _, _, err := retryOnTransient(cmd, call); err == nil {
		t.Fatal("a 404 was swallowed")
	}
	if calls.Load() != 1 || stderr.Len() != 0 {
		t.Fatalf("%d calls, stderr %q", calls.Load(), stderr.String())
	}
}

func TestRetryOnTransientStopsWhenCancelled(t *testing.T) {
	srv, _ := flakyServer(t, 1000, http.StatusServiceUnavailable, "")
	// A wait shorter than the budget, so the helper sleeps rather than giving up.
	cmd, _, call := retryFixture(t, srv, 10*time.Minute, time.Minute, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	_, _, err := retryOnTransient(cmd, call)
	if err != context.Canceled {
		t.Fatalf("err %v", err)
	}
}

// TestRetryOnTransientCancelsARequestInFlight cancels while a request is in flight, not while
// waiting between retries: the server blocks until it observes the client's cancellation.
func TestRetryOnTransientCancelsARequestInFlight(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(release)
	}))
	t.Cleanup(srv.Close)

	cmd, _, call := retryFixture(t, srv, 2*time.Second, 20*time.Millisecond, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	_, _, err := retryOnTransient(cmd, call)
	select {
	case <-release:
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed the cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestRetryOnTransientGivesUpWhenCtxDeadlineIsSooner(t *testing.T) {
	srv, _ := flakyServer(t, 1000, http.StatusTooManyRequests, "120")
	cmd, stderr, call := retryFixture(t, srv, 5*time.Minute, 20*time.Millisecond, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	start := time.Now()
	_, resp, err := retryOnTransient(cmd, call)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %s; ctx's short deadline should have won", elapsed)
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err %v, resp %v", err, resp)
	}
	if strings.Contains(stderr.String(), "Retrying in 2m0s.") {
		t.Fatalf("should give up rather than promise a 2-minute wait; stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "giving up") {
		t.Fatalf("no giving-up line in stderr:\n%s", stderr.String())
	}
}

func TestRetryAfter(t *testing.T) {
	prevInterval, prevFloor := transientRetryInterval, retryAfterFloor
	transientRetryInterval, retryAfterFloor = 15*time.Second, time.Second
	t.Cleanup(func() { transientRetryInterval, retryAfterFloor = prevInterval, prevFloor })

	response := func(header string) *http.Response {
		h := http.Header{}
		if header != "" {
			h.Set("Retry-After", header)
		}
		return &http.Response{Header: h}
	}

	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"absent gives the interval", "", 15 * time.Second},
		{"one second", "1", time.Second},
		{"zero floors to a second", "0", time.Second},
		{"negative falls back to the interval", "-5", 15 * time.Second},
		{"a large value is returned as is", "600", 600 * time.Second},
		{"padding is trimmed", " 2 ", 2 * time.Second},
		{"unparsable falls back to the interval", "soon", 15 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := retryAfter(response(c.header)); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}

	t.Run("an HTTP date about 30s ahead", func(t *testing.T) {
		when := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
		got := retryAfter(response(when))
		if got < 25*time.Second || got > 30*time.Second {
			t.Fatalf("got %s", got)
		}
	})
}
