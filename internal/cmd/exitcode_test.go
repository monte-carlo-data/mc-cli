package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExitCodeMapsErrors(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	notFound := withExitCode(exitNotFound, errors.New("gone"))
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want int
	}{
		{"success", context.Background(), nil, exitOK},
		{"unclassified", context.Background(), errors.New("boom"), exitFailure},
		{"coded", context.Background(), notFound, exitNotFound},
		{"coded and wrapped", context.Background(), fmt.Errorf("step: %w", notFound), exitNotFound},
		{"cancelled error", context.Background(), fmt.Errorf("call: %w", context.Canceled), exitInterrupted},
		{"cancelled context", cancelled, errors.New("flattened"), exitInterrupted},
		{"cancelled context beats a code", cancelled, notFound, exitInterrupted},
		{"success after an interrupt", cancelled, nil, exitOK},
	}
	for _, c := range cases {
		if got := exitCode(c.ctx, c.err); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestExitErrorKeepsTheMessage(t *testing.T) {
	inner := errors.New("the message")
	err := withExitCode(exitUsage, inner)
	if err.Error() != "the message" || !errors.Is(err, inner) {
		t.Fatalf("got %q", err)
	}
	if withExitCode(exitUsage, nil) != nil {
		t.Fatal("a nil error gained a code")
	}
}

func TestRunPrintsTheErrorAndReturnsItsCode(t *testing.T) {
	code, _, stderr := runExit(t, context.Background(), "whoami", "--endpoint", "http://127.0.0.1:1", "--api-id", "i", "--api-token", "s")
	if code != exitFailure || !strings.HasPrefix(stderr, binaryName+": ") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestRunReturnsZeroOnSuccess(t *testing.T) {
	if code, _, stderr := runExit(t, context.Background(), "version"); code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestRunReturns130WhenInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _, _ := runExit(t, ctx, "whoami", "--endpoint", "http://127.0.0.1:1", "--api-id", "i", "--api-token", "s")
	if code != exitInterrupted {
		t.Fatalf("exit %d", code)
	}
}

// whoamiAgainst runs whoami against srv as the binary would.
func whoamiAgainst(t *testing.T, srv *httptest.Server) (int, string) {
	t.Helper()
	code, _, stderr := runExit(t, context.Background(), "whoami", "--endpoint", srv.URL, "--api-id", "i", "--api-token", "s")
	return code, stderr
}

func TestApiFailuresExitWithTheirStatusCode(t *testing.T) {
	cases := []struct {
		status      int
		contentType string
		body        string
		want        int
	}{
		{http.StatusNotFound, "application/json", `{"message":"Not Found"}`, exitNotFound},
		{http.StatusUnauthorized, "application/json", `{"message":"Unauthorized"}`, exitAuth},
		{http.StatusForbidden, "application/problem+json", `{"code":"account_frozen","detail":"Paused.","request_id":"r","status":403,"title":"Forbidden","type":"urn:montecarloai:error:account_frozen"}`, exitAuth},
		// users/me is a read, which is not retried: a 503 still means "try again later".
		{http.StatusServiceUnavailable, "application/problem+json", transientProblem, exitTransient},
		{http.StatusTooManyRequests, "text/plain", "slow down", exitTransient},
		{http.StatusInternalServerError, "text/plain", "oops", exitFailure},
		{http.StatusConflict, "text/plain", "conflict", exitFailure},
		// A success status whose body the SDK cannot decode.
		{http.StatusOK, "application/json", `{"unexpected":true}`, exitFailure},
	}
	for _, c := range cases {
		srv := problemServer(t, c.status, c.contentType, c.body)
		if code, stderr := whoamiAgainst(t, srv); code != c.want {
			t.Errorf("%d: exit %d, want %d; stderr %q", c.status, code, c.want, stderr)
		}
	}
}

func TestApiErrTakesTheStatusFromTheProblemWithoutAResponse(t *testing.T) {
	srv := problemServer(t, http.StatusForbidden, "application/problem+json",
		`{"code":"account_frozen","detail":"Paused.","request_id":"r","status":403,"title":"Forbidden","type":"urn:montecarloai:error:account_frozen"}`)
	_, err := callWhoami(t, srv)
	rendered := apiErr(nil, err)
	if rendered.Error() != "Paused. (account_frozen, request r)" {
		t.Fatalf("the problem was not decoded: %q", rendered)
	}
	if got := exitCode(context.Background(), rendered); got != exitAuth {
		t.Fatalf("exit %d", got)
	}
}

func TestATransientFailureAfterTheRetryBudgetExitsWith5(t *testing.T) {
	srv, _ := flakyServer(t, 100, http.StatusServiceUnavailable, "")
	cmd, _, call := retryFixture(t, srv, 50*time.Millisecond, 20*time.Millisecond, 10*time.Millisecond)
	_, resp, err := retryOnTransient(cmd, call)
	if got := exitCode(context.Background(), apiErr(resp, err)); got != exitTransient {
		t.Fatalf("exit %d", got)
	}
}

func TestAnUnwoundFailureKeepsTheStepsCode(t *testing.T) {
	step := withExitCode(exitNotFound, errors.New("gone"))
	err := &unwoundError{err: step, report: "\nnothing to delete"}
	if got := exitCode(context.Background(), err); got != exitNotFound {
		t.Fatalf("exit %d", got)
	}
}

func TestAnUnknownSubcommandOfAGroupIsAUsageError(t *testing.T) {
	code, stdout, stderr := runExit(t, context.Background(), "connections", "bogus")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, `unknown command "bogus" for "montecarlo connections"`) {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, stdout, stderr)
	}
}

func TestAnUnknownSubcommandSuggestsTheNearest(t *testing.T) {
	_, _, stderr := runExit(t, context.Background(), "connections", "lst")
	if !strings.Contains(stderr, "Did you mean this?") || !strings.Contains(stderr, "\tlist\n") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestAGroupAloneStillShowsItsHelp(t *testing.T) {
	code, stdout, _ := runExit(t, context.Background(), "connections")
	if code != exitOK || !strings.Contains(stdout, "Available Commands:") {
		t.Fatalf("exit %d\nstdout %q", code, stdout)
	}
}
