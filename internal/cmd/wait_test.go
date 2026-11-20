// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

// waitFixture is a command with captured streams, and the wait's timing shrunk for the
// duration of the test: one timeout second lasts a millisecond.
func waitFixture(t *testing.T) (*cobra.Command, context.CancelFunc, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	prev := []time.Duration{statusPollInterval, statusTimeoutUnit, transientRetryInterval, retryAfterFloor}
	statusPollInterval, statusTimeoutUnit = time.Millisecond, time.Millisecond
	transientRetryInterval, retryAfterFloor = time.Millisecond, time.Millisecond
	t.Cleanup(func() {
		statusPollInterval, statusTimeoutUnit = prev[0], prev[1]
		transientRetryInterval, retryAfterFloor = prev[2], prev[3]
	})
	cmd := &cobra.Command{Use: "t"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd.SetContext(ctx)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return cmd, cancel, &stdout, &stderr
}

type resource struct{ status, detail string }

func statusOf(r *resource) (string, string) { return r.status, r.detail }

// states answers each read with the next state, then keeps answering the last.
func states(list ...resource) (func() (*resource, *http.Response, error), *int) {
	calls := 0
	return func() (*resource, *http.Response, error) {
		r := list[min(calls, len(list)-1)]
		calls++
		return &r, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}, &calls
}

var (
	ready  = []string{"READY"}
	failed = []string{"FAILED"}
)

func TestWaitForStatusReturnsAReadyFirstWithoutReading(t *testing.T) {
	cmd, _, stdout, stderr := waitFixture(t)
	read, calls := states(resource{status: "PROVISIONING"})
	first := &resource{status: "READY"}

	got, err := waitForStatus(cmd, first, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	if err != nil || got != first {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if *calls != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("%d reads, stdout %q, stderr %q", *calls, stdout, stderr)
	}
}

func TestWaitForStatusPollsUntilReadyWithProgressOnStderrOnly(t *testing.T) {
	cmd, _, stdout, stderr := waitFixture(t)
	read, calls := states(
		resource{"PROVISIONING", "Step 1 of 2."},
		resource{"PROVISIONING", "Step 1 of 2."},
		resource{"PROVISIONING", "Step 2 of 2."},
		resource{"READY", ""},
	)

	got, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	if err != nil || got.status != "READY" || *calls != 4 {
		t.Fatalf("got %+v, err %v, after %d reads", got, err, *calls)
	}
	want := "Waiting for dep-1 to be READY:\n" +
		"  PROVISIONING\n" +
		"  PROVISIONING: Step 1 of 2.\n" +
		"  PROVISIONING: Step 2 of 2.\n" +
		"  READY\n" +
		"dep-1 is READY.\n"
	if stderr.String() != want {
		t.Fatalf("stderr =\n%s\nwant\n%s", stderr, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestWaitForStatusPrintsNoStatusLineForAFirstThatIsNil(t *testing.T) {
	cmd, _, stdout, stderr := waitFixture(t)
	read, calls := states(resource{"READY", "Done."})

	got, err := waitForStatus(cmd, nil, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	if err != nil || got == nil || got.status != "READY" || *calls != 1 {
		t.Fatalf("got %+v, err %v, after %d reads", got, err, *calls)
	}
	want := "Waiting for dep-1 to be READY:\n" +
		"  READY: Done.\n" +
		"dep-1 is READY.\n"
	if stderr.String() != want {
		t.Fatalf("stderr =\n%q\nwant\n%q", stderr, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestWaitForStatusFailsAsAPlainErrorWhenAReadHoldsNothing(t *testing.T) {
	cmd, _, _, _ := waitFixture(t)
	read := func() (*resource, *http.Response, error) {
		return nil, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}

	got, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	if err == nil || err.Error() != "cannot read dep-1: the response held nothing" {
		t.Fatalf("err = %v", err)
	}
	if code := exitCode(cmd.Context(), err); code != exitFailure {
		t.Errorf("exit code %d, want %d", code, exitFailure)
	}
	if got == nil || got.status != "PROVISIONING" {
		t.Errorf("got %+v, want the value held before the read", got)
	}
}

func TestWaitForStatusFailsWithTheDetailAsAPlainError(t *testing.T) {
	cmd, _, stdout, stderr := waitFixture(t)
	read, _ := states(resource{"PROVISIONING", ""}, resource{"FAILED", "Step 2 of 2 did not finish.\nSee the log."})

	got, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	want := "dep-1 is FAILED: Step 2 of 2 did not finish. See the log.\ncheck it with: montecarlo deployments get dep-1"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if code := exitCode(cmd.Context(), err); code != exitFailure {
		t.Errorf("exit code %d", code)
	}
	if got == nil || got.status != "FAILED" {
		t.Errorf("got %+v", got)
	}
	if !strings.HasSuffix(stderr.String(), "  FAILED: Step 2 of 2 did not finish. See the log.\n") || stdout.Len() != 0 {
		t.Errorf("stderr %q, stdout %q", stderr, stdout)
	}
}

func TestWaitForStatusMakesTheIdSafeToPrint(t *testing.T) {
	cmd, _, _, stderr := waitFixture(t)
	read, _ := states(resource{"FAILED", "It did not finish."})

	_, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1\x1b[2J\nx", "deployments get")

	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(stderr.String(), "\x1b") || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("an escape reached the output: stderr %q, err %q", stderr, err)
	}
	if want := "\ncheck it with: montecarlo deployments get dep-1?[2J x"; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("err = %q, want it to end with %q", err, want)
	}
}

func TestWaitForStatusFailsWithoutReadingWhenFirstHasFailed(t *testing.T) {
	cmd, _, _, stderr := waitFixture(t)
	read, calls := states(resource{status: "READY"})
	first := &resource{"FAILED", "Step 1 of 2 did not finish."}

	got, err := waitForStatus(cmd, first, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	if err == nil || err.Error() != "dep-1 is FAILED: Step 1 of 2 did not finish.\ncheck it with: montecarlo deployments get dep-1" || got != first {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if *calls != 0 || stderr.Len() != 0 {
		t.Fatalf("%d reads, stderr %q", *calls, stderr)
	}
}

func TestWaitForStatusNamesTheTimeoutWhenItRunsOut(t *testing.T) {
	cmd, _, _, _ := waitFixture(t)
	read, calls := states(resource{"PROVISIONING", "first"}, resource{"PROVISIONING", "latest"})
	start := time.Now()

	got, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 30, "dep-1", "deployments get")

	want := "stopped waiting after 30s; dep-1 is still PROVISIONING\ncheck it with: montecarlo deployments get dep-1"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if code := exitCode(cmd.Context(), err); code != exitFailure {
		t.Errorf("exit code %d", code)
	}
	if *calls < 2 || got == nil || got.detail != "latest" {
		t.Errorf("got %+v after %d reads", got, *calls)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestWaitForStatusStopsOnCtrlC(t *testing.T) {
	cmd, cancel, _, _ := waitFixture(t)
	calls := 0
	read := func() (*resource, *http.Response, error) {
		calls++
		cancel()
		return &resource{"PROVISIONING", "latest"}, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}

	got, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")

	if !errors.Is(err, context.Canceled) || exitCode(cmd.Context(), err) != exitInterrupted {
		t.Fatalf("err = %v", err)
	}
	if want := "dep-1 is still PROVISIONING\ncheck it with: montecarlo deployments get dep-1"; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("err = %v\nwant it to end %s", err, want)
	}
	if calls != 1 || got == nil || got.detail != "latest" {
		t.Errorf("got %+v after %d reads", got, calls)
	}
}

func TestWaitForStatusKeepsPollingAStatusInNeitherList(t *testing.T) {
	cmd, _, _, _ := waitFixture(t)
	read, calls := states(resource{status: "UPGRADING"}, resource{status: "UPGRADING"}, resource{status: "READY"})

	got, err := waitForStatus(cmd, &resource{status: "UPGRADING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")
	if err != nil || got.status != "READY" || *calls != 3 {
		t.Fatalf("got %+v, err %v, after %d reads", got, err, *calls)
	}

	cmd, _, _, _ = waitFixture(t)
	read, calls = states(resource{status: "UPGRADING"})
	_, err = waitForStatus(cmd, &resource{status: "UPGRADING"}, statusOf, read, ready, failed, 30, "dep-1", "deployments get")
	if err == nil || !strings.HasPrefix(err.Error(), "stopped waiting after 30s; dep-1 is still UPGRADING") || *calls < 2 {
		t.Fatalf("err = %v after %d reads", err, *calls)
	}
}

func TestWaitForStatusNamesOnlyTheIdWithoutAReadCommand(t *testing.T) {
	cmd, cancel, _, _ := waitFixture(t)
	read, _ := states(resource{status: "FAILED"})
	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", ""); err == nil || err.Error() != "dep-1 is FAILED" {
		t.Errorf("failed: err = %v", err)
	}

	read, _ = states(resource{status: "PROVISIONING"})
	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 30, "dep-1", ""); err == nil || err.Error() != "stopped waiting after 30s; dep-1 is still PROVISIONING" {
		t.Errorf("timeout: err = %v", err)
	}

	cancel()
	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", ""); err == nil || err.Error() != "stopped waiting: context canceled; dep-1 is still PROVISIONING" {
		t.Errorf("interrupt: err = %v", err)
	}
}

// deploymentAnswer is one response: a status, a Retry-After header, and a body.
type deploymentAnswer struct {
	status     int
	retryAfter string
	body       string
}

// deploymentServer answers each GetDeployment with the next answer, then keeps answering the last.
func deploymentServer(t *testing.T, answers ...deploymentAnswer) (*sdk.APIClient, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		a := answers[min(int(calls.Add(1))-1, len(answers)-1)]
		if a.retryAfter != "" {
			w.Header().Set("Retry-After", a.retryAfter)
		}
		if a.status >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(srv.Close)
	api, err := sdk.NewClient(context.Background(), sdk.Options{Endpoint: srv.URL, TokenID: "i", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	return api, &calls
}

func deployment(status, detail string) string {
	return fmt.Sprintf(`{"id":"dep-1","name":"d","enabled":false,"status":%q,"status_detail":%q,"deletion_protection":false}`, status, detail)
}

func problemBody(status int) string {
	return fmt.Sprintf(`{"code":"c","detail":"status %d","request_id":"req-1","status":%d}`, status, status)
}

// waitForDeployment calls waitForStatus the way the generated deployments create does.
func waitForDeployment(cmd *cobra.Command, api *sdk.APIClient, out *sdk.DeploymentOut) (*sdk.DeploymentOut, error) {
	ctx := cmd.Context()
	createdID := out.GetId()
	return waitForStatus(cmd, out, func(o *sdk.DeploymentOut) (string, string) {
		return string(o.GetStatus()), o.GetStatusDetail()
	}, func() (*sdk.DeploymentOut, *http.Response, error) {
		return api.DeploymentsAPI.GetDeployment(ctx, createdID).Execute()
	}, []string{"READY"}, []string{"FAILED"}, 1800, createdID, "deployments get")
}

func TestWaitForStatusRetriesA503AndA429AsRetryAfterAsks(t *testing.T) {
	cmd, _, _, stderr := waitFixture(t)
	// Without Retry-After a retry would wait an hour, past the wait's whole timeout, so the
	// wait only succeeds when it honours the header, which asks for no wait at all.
	transientRetryInterval = time.Hour
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	api, calls := deploymentServer(t,
		deploymentAnswer{http.StatusServiceUnavailable, past, problemBody(503)},
		deploymentAnswer{http.StatusTooManyRequests, past, problemBody(429)},
		deploymentAnswer{http.StatusOK, "", deployment("READY", "")},
	)

	got, err := waitForDeployment(cmd, api, sdk.NewDeploymentOut("dep-1", "d", false, sdk.DEPLOYMENTSTATUS_PROVISIONING, false))

	if err != nil || got.GetStatus() != sdk.DEPLOYMENTSTATUS_READY || calls.Load() != 3 {
		t.Fatalf("got %+v, err %v, after %d reads\n%s", got, err, calls.Load(), stderr)
	}
	if n := strings.Count(stderr.String(), "Retrying in 1ms."); n != 2 {
		t.Fatalf("%d retries in stderr:\n%s", n, stderr)
	}
}

func TestWaitForStatusEndsOnAnyOtherReadFailure(t *testing.T) {
	cmd, _, _, _ := waitFixture(t)
	api, calls := deploymentServer(t,
		deploymentAnswer{http.StatusOK, "", deployment("PROVISIONING", "latest")},
		deploymentAnswer{http.StatusNotFound, "", problemBody(404)},
	)

	got, err := waitForDeployment(cmd, api, sdk.NewDeploymentOut("dep-1", "d", false, sdk.DEPLOYMENTSTATUS_PROVISIONING, false))

	if err == nil || exitCode(cmd.Context(), err) != exitNotFound || calls.Load() != 2 {
		t.Fatalf("err %v, after %d reads", err, calls.Load())
	}
	if got == nil || got.GetStatusDetail() != "latest" {
		t.Errorf("got %+v", got)
	}
}

// waitForDeploymentWithin is waitForDeployment with the wait's timeout in seconds given.
func waitForDeploymentWithin(cmd *cobra.Command, api *sdk.APIClient, timeoutSeconds int) (*sdk.DeploymentOut, error) {
	ctx := cmd.Context()
	return waitForStatus(cmd, sdk.NewDeploymentOut("dep-1", "d", false, sdk.DEPLOYMENTSTATUS_PROVISIONING, false),
		func(o *sdk.DeploymentOut) (string, string) {
			return string(o.GetStatus()), o.GetStatusDetail()
		}, func() (*sdk.DeploymentOut, *http.Response, error) {
			return api.DeploymentsAPI.GetDeployment(ctx, "dep-1").Execute()
		}, []string{"READY"}, []string{"FAILED"}, timeoutSeconds, "dep-1", "deployments get")
}

func TestWaitForStatusStopsEarlyWhenARetryWouldOutlastTheTimeout(t *testing.T) {
	for name, answer := range map[string]deploymentAnswer{
		"429 asking for 300s":       {http.StatusTooManyRequests, "300", problemBody(429)},
		"503 asking for 300s":       {http.StatusServiceUnavailable, "300", problemBody(503)},
		"429 at the fixed interval": {http.StatusTooManyRequests, "", problemBody(429)},
		"503 at the fixed interval": {http.StatusServiceUnavailable, "", problemBody(503)},
	} {
		t.Run(name, func(t *testing.T) {
			cmd, _, _, _ := waitFixture(t)
			// The wait lasts three seconds, far less than the retry budget, so the wait's own
			// deadline bounds the retries. Both the 300s Retry-After and the hour-long fixed
			// interval outlast it, so the wait stops at once, with most of its time left.
			statusTimeoutUnit = 100 * time.Millisecond
			prev := transientRetryTimeout
			t.Cleanup(func() { transientRetryTimeout = prev })
			transientRetryTimeout, transientRetryInterval = time.Hour, time.Hour
			api, calls := deploymentServer(t,
				deploymentAnswer{http.StatusOK, "", deployment("PROVISIONING", "latest")},
				answer,
			)
			start := time.Now()

			got, err := waitForDeploymentWithin(cmd, api, 30)

			if err == nil {
				t.Fatal("no error")
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("took %s, want it to stop before the timeout", took)
			}
			lines := strings.Split(err.Error(), "\n")
			want := "stopped waiting: retrying the read would outlast the 30s timeout; dep-1 is still PROVISIONING"
			if lines[0] != want {
				t.Errorf("first line = %q\nwant %q", lines[0], want)
			}
			if strings.Contains(err.Error(), "stopped waiting after") {
				t.Errorf("err = %v\nwant it not to claim the timeout ran out", err)
			}
			if len(lines) != 3 || !strings.HasPrefix(lines[1], "the last read failed: ") || !strings.Contains(lines[1], strconv.Itoa(answer.status)) {
				t.Errorf("err = %v\nwant its second line to name the failed read and %d", err, answer.status)
			}
			if last := lines[len(lines)-1]; last != "check it with: montecarlo deployments get dep-1" {
				t.Errorf("last line = %q", last)
			}
			if code := exitCode(cmd.Context(), err); code != exitFailure {
				t.Errorf("exit code %d, want %d", code, exitFailure)
			}
			if calls.Load() != 2 || got == nil || got.GetStatusDetail() != "latest" {
				t.Errorf("got %+v after %d reads, want the value the first read returned", got, calls.Load())
			}
		})
	}
}

func TestWaitForStatusNamesTheTimeoutWhenItRunsOutDuringARetry(t *testing.T) {
	cmd, _, _, _ := waitFixture(t)
	// The wait lasts thirty milliseconds, and the read answers a 503 only after it has passed,
	// so the timeout has run out by the time the retry gives up.
	read := func() (*resource, *http.Response, error) {
		time.Sleep(50 * time.Millisecond)
		return nil, &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}, errors.New("unavailable")
	}

	got, err := waitForStatus(cmd, &resource{"PROVISIONING", "latest"}, statusOf, read, ready, failed, 30, "dep-1", "deployments get")

	want := "stopped waiting after 30s; dep-1 is still PROVISIONING\ncheck it with: montecarlo deployments get dep-1"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if got == nil || got.detail != "latest" {
		t.Errorf("got %+v", got)
	}
	if code := exitCode(cmd.Context(), err); code != exitFailure {
		t.Errorf("exit code %d, want %d", code, exitFailure)
	}
}

func TestWaitForStatusNamesTheResourceWhenTheRetryBudgetRunsOut(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			cmd, _, _, _ := waitFixture(t)
			// The wait lasts eighteen seconds; the retry budget ten milliseconds, so the budget
			// runs out first, and a retry would wait an hour.
			statusTimeoutUnit = 10 * time.Millisecond
			prev := transientRetryTimeout
			t.Cleanup(func() { transientRetryTimeout = prev })
			transientRetryTimeout, transientRetryInterval = 10*time.Millisecond, time.Hour
			api, _ := deploymentServer(t, deploymentAnswer{status, "", problemBody(status)})

			_, err := waitForDeployment(cmd, api, sdk.NewDeploymentOut("dep-1", "d", false, sdk.DEPLOYMENTSTATUS_PROVISIONING, false))

			if err == nil {
				t.Fatal("no error")
			}
			lines := strings.Split(err.Error(), "\n")
			if len(lines) < 3 || !strings.Contains(lines[0], strconv.Itoa(status)) {
				t.Fatalf("err = %v\nwant it to begin with the API error naming %d", err, status)
			}
			if got := lines[len(lines)-2]; got != "dep-1 is still PROVISIONING" {
				t.Errorf("second to last line = %q", got)
			}
			if got := lines[len(lines)-1]; got != "check it with: montecarlo deployments get dep-1" {
				t.Errorf("last line = %q", got)
			}
			if code := exitCode(cmd.Context(), err); code != exitTransient {
				t.Errorf("exit code %d, want %d", code, exitTransient)
			}
		})
	}
}

func TestWaitForStatusStopsOnCtrlCDuringARetry(t *testing.T) {
	cmd, cancel, _, _ := waitFixture(t)
	// The wait lasts five hours (1800 units of ten seconds), so the two-hour retry budget binds
	// rather than the wait's deadline, and the one-hour retry fits in it and really sleeps.
	statusTimeoutUnit = 10 * time.Second
	prev := transientRetryTimeout
	t.Cleanup(func() { transientRetryTimeout = prev })
	transientRetryTimeout, transientRetryInterval = 2*time.Hour, time.Hour
	api, calls := deploymentServer(t, deploymentAnswer{http.StatusServiceUnavailable, "", problemBody(503)})
	// Interrupt once the server has answered the 503, while the retry sleeps.
	go func() {
		for calls.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	_, err := waitForDeployment(cmd, api, sdk.NewDeploymentOut("dep-1", "d", false, sdk.DEPLOYMENTSTATUS_PROVISIONING, false))

	if err == nil || exitCode(cmd.Context(), err) != exitInterrupted {
		t.Fatalf("err = %v", err)
	}
	if want := "dep-1 is still PROVISIONING\ncheck it with: montecarlo deployments get dep-1"; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("err = %v\nwant it to end %s", err, want)
	}
}

func TestTheStatusViewRedrawsTheLineInPlaceWhileTheStatusHolds(t *testing.T) {
	var out bytes.Buffer
	view := &statusView{w: &out, live: true}

	view.show("PROVISIONING", "Step 1 of 2.")
	view.frame("⠋")
	view.show("PROVISIONING", "Step 1 of 2.")
	view.frame("⠙")

	want := "\r\x1b[2K  ⠋ PROVISIONING: Step 1 of 2.\n" +
		"\r\x1b[2K\x1b[1A\x1b[2K  ⠙ PROVISIONING: Step 1 of 2.\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

func TestTheStatusViewSettlesTheOldLineWhenTheStatusChangesAndAtTheEnd(t *testing.T) {
	var out bytes.Buffer
	view := &statusView{w: &out, live: true}

	view.show("PROVISIONING", "Step 1 of 2.")
	view.frame("⠋")
	view.show("PROVISIONING", "Step 2 of 2.")
	view.frame("⠙")
	view.end()

	want := "\r\x1b[2K  ⠋ PROVISIONING: Step 1 of 2.\n" +
		"\r\x1b[2K\x1b[1A\x1b[2K  PROVISIONING: Step 1 of 2.\n" +
		"\r\x1b[2K  ⠙ PROVISIONING: Step 2 of 2.\n" +
		"\r\x1b[2K\x1b[1A\x1b[2K  PROVISIONING: Step 2 of 2.\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

func TestTheStatusViewDrawsBelowARetryMessageRatherThanOverIt(t *testing.T) {
	var out bytes.Buffer
	view := &statusView{w: &out, live: true}

	view.show("PROVISIONING", "")
	view.frame("⠋")
	fmt.Fprintf(view, "unavailable\nRetrying in 1s.\n")
	view.frame("⠙")

	want := "\r\x1b[2K  ⠋ PROVISIONING\n" +
		"\r\x1b[2K\x1b[1A\x1b[2Kunavailable\nRetrying in 1s.\n" +
		"\r\x1b[2K  ⠙ PROVISIONING\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

func TestTheStatusViewTruncatesTheSpinningLineToTheTerminal(t *testing.T) {
	var out bytes.Buffer
	view := &statusView{w: &out, live: true, width: 20}

	view.show("PROVISIONING", "Step 1 of 2.")
	view.frame("⠋")
	view.end()

	want := "\r\x1b[2K  ⠋ PROVISIONING: S…\n\r\x1b[2K\x1b[1A\x1b[2K  PROVISIONING: Step 1 of 2.\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

// liveWaitFixture is waitFixture with stderr a file the command takes for a terminal. The
// returned function reads what was written to it.
func liveWaitFixture(t *testing.T) (*cobra.Command, context.CancelFunc, func() string) {
	t.Helper()
	cmd, cancel, _, _ := waitFixture(t)
	prevTerminal, prevFrame := isTerminal, spinnerFrameRate
	isTerminal = func(*os.File) bool { return true }
	spinnerFrameRate = time.Millisecond
	t.Cleanup(func() { isTerminal, spinnerFrameRate = prevTerminal, prevFrame })
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	cmd.SetErr(f)
	return cmd, cancel, func() string {
		raw, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
}

func TestTheStatusViewDrawsFromTheFirstColumnAfterAnEcho(t *testing.T) {
	var out bytes.Buffer
	view := &statusView{w: &out, live: true}

	view.show("PROVISIONING", "")
	view.frame("⠋")
	out.WriteString("^C") // a terminal's echo of Ctrl-C, on the line below the frame
	view.end()
	fmt.Fprintln(&out, "montecarlo: stopped waiting")

	want := "\r\x1b[2K  ⠋ PROVISIONING\n^C\r\x1b[2K\x1b[1A\x1b[2K  PROVISIONING\nmontecarlo: stopped waiting\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
	if got := screen(out.String()); fmt.Sprint(got) != fmt.Sprint([]string{"  PROVISIONING", "montecarlo: stopped waiting"}) {
		t.Fatalf("screen = %q", got)
	}

	// An echo with nothing printed after it is erased all the same.
	out.Reset()
	view = &statusView{w: &out, live: true}
	view.show("PROVISIONING", "")
	view.frame("⠋")
	out.WriteString("^C")
	view.end()
	if got := screen(out.String()); fmt.Sprint(got) != fmt.Sprint([]string{"  PROVISIONING"}) {
		t.Fatalf("screen = %q", got)
	}
}

// screen is what a terminal shows after out: it plays the line feeds, carriage returns,
// cursor-up and erase-line sequences the views write, keeping the cursor's column as a
// terminal does, and returns the rows with trailing blanks trimmed. A line feed also returns
// to the first column, as a terminal's output processing makes it.
func screen(out string) []string {
	var rows [][]rune
	row, col := 0, 0
	at := func() {
		for len(rows) <= row {
			rows = append(rows, nil)
		}
	}
	for out != "" {
		switch {
		case strings.HasPrefix(out, "\x1b[2K"):
			at()
			rows[row] = nil
			out = out[len("\x1b[2K"):]
		case strings.HasPrefix(out, "\x1b["):
			end := strings.IndexByte(out, 'A')
			n, _ := strconv.Atoi(out[2:end])
			row -= n
			out = out[end+1:]
		case out[0] == '\r':
			col = 0
			out = out[1:]
		case out[0] == '\n':
			row, col = row+1, 0
			out = out[1:]
		default:
			r, size := utf8.DecodeRuneInString(out)
			at()
			for len(rows[row]) <= col {
				rows[row] = append(rows[row], ' ')
			}
			rows[row][col] = r
			col++
			out = out[size:]
		}
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, strings.TrimRight(string(r), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func TestWaitForStatusOnATerminalSpinsThenLeavesTheLinesClean(t *testing.T) {
	cmd, _, stderr := liveWaitFixture(t)
	read, _ := states(
		resource{"PROVISIONING", "Step 1 of 2."},
		resource{"PROVISIONING", "Step 1 of 2."},
		resource{"PROVISIONING", "Step 2 of 2."},
		resource{"READY", ""},
	)

	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get"); err != nil {
		t.Fatal(err)
	}

	out := stderr()
	if !strings.Contains(out, "\x1b[1A\x1b[2K  ") || !strings.Contains(out, "  ⠋ PROVISIONING") {
		t.Errorf("no redraw in place:\n%q", out)
	}
	want := []string{
		"Waiting for dep-1 to be READY:",
		"  PROVISIONING",
		"  PROVISIONING: Step 1 of 2.",
		"  PROVISIONING: Step 2 of 2.",
		"  READY",
		"dep-1 is READY.",
	}
	if got := screen(out); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("screen =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWaitForStatusOnATerminalEndsCleanWhenItFailsOrStops(t *testing.T) {
	cmd, _, stderr := liveWaitFixture(t)
	read, _ := states(resource{"PROVISIONING", ""}, resource{"FAILED", "Step 2 of 2 did not finish."})
	_, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get")
	if err == nil || err.Error() != "dep-1 is FAILED: Step 2 of 2 did not finish.\ncheck it with: montecarlo deployments get dep-1" {
		t.Fatalf("err = %v", err)
	}
	want := []string{"Waiting for dep-1 to be READY:", "  PROVISIONING", "  FAILED: Step 2 of 2 did not finish."}
	if got := screen(stderr()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("failed: screen =\n%s", strings.Join(got, "\n"))
	}

	cmd, _, stderr = liveWaitFixture(t)
	read, _ = states(resource{"PROVISIONING", ""})
	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 30, "dep-1", "deployments get"); err == nil || !strings.HasPrefix(err.Error(), "stopped waiting after 30s;") {
		t.Fatalf("err = %v", err)
	}
	want = []string{"Waiting for dep-1 to be READY:", "  PROVISIONING"}
	if got := screen(stderr()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("timed out: screen =\n%s", strings.Join(got, "\n"))
	}

	cmd, cancel, stderr := liveWaitFixture(t)
	interrupt := func() (*resource, *http.Response, error) {
		cancel()
		return &resource{status: "PROVISIONING"}, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}
	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, interrupt, ready, failed, 1800, "dep-1", "deployments get"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := screen(stderr()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("interrupted: screen =\n%s", strings.Join(got, "\n"))
	}
}

func TestWaitForStatusOnATerminalKeepsARetryMessageInView(t *testing.T) {
	cmd, _, stderr := liveWaitFixture(t)
	calls := 0
	read := func() (*resource, *http.Response, error) {
		calls++
		if calls == 1 {
			return nil, &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}, errors.New("unavailable")
		}
		return &resource{status: "READY"}, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}

	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"Waiting for dep-1 to be READY:",
		"unavailable",
		"Retrying in 1ms.",
		"  PROVISIONING",
		"  READY",
		"dep-1 is READY.",
	}
	if got := screen(stderr()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("screen =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWaitForStatusOnATerminalLeavesNoEchoOfCtrlC(t *testing.T) {
	cmd, cancel, stderr := liveWaitFixture(t)
	read := func() (*resource, *http.Response, error) {
		fmt.Fprint(cmd.ErrOrStderr(), "^C") // the terminal's echo, at the cursor below the frame
		cancel()
		return &resource{status: "PROVISIONING"}, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}

	if _, err := waitForStatus(cmd, &resource{status: "PROVISIONING"}, statusOf, read, ready, failed, 1800, "dep-1", "deployments get"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	want := []string{"Waiting for dep-1 to be READY:", "  PROVISIONING"}
	if got := screen(stderr()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("screen = %q\nraw %q", got, stderr())
	}
}
