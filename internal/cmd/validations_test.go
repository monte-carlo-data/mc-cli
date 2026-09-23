package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// validationFixture is a command with captured streams, and the polling budget shrunk for the
// duration of the test.
func validationFixture(t *testing.T) (*cobra.Command, context.CancelFunc, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	prev := []time.Duration{validationPollInterval, validationPollTimeout, validationFrameRate, transientRetryInterval, retryAfterFloor}
	validationPollInterval, validationPollTimeout, validationFrameRate = 5*time.Millisecond, 2*time.Second, time.Millisecond
	transientRetryInterval, retryAfterFloor = 5*time.Millisecond, time.Millisecond
	t.Cleanup(func() {
		validationPollInterval, validationPollTimeout, validationFrameRate = prev[0], prev[1], prev[2]
		transientRetryInterval, retryAfterFloor = prev[3], prev[4]
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

func row(name, status string, passed any, extra ...map[string]any) map[string]any {
	r := map[string]any{"name": name, "description": "Check " + name, "status": status, "passed": passed, "errors": []any{}, "warnings": []any{}}
	for _, e := range extra {
		for k, v := range e {
			r[k] = v
		}
	}
	return r
}

func run(status string, rows ...map[string]any) map[string]any {
	return map[string]any{"id": "run-1", "status": status, "validations": rows}
}

func problem(message, resolution string) []any {
	return []any{map[string]any{"friendly_message": message, "resolution": resolution}}
}

// polls answers each fetch with the next state, then keeps answering the last.
func polls(states ...map[string]any) (func() (any, *http.Response, error), *int) {
	calls := 0
	return func() (any, *http.Response, error) {
		s := states[min(calls, len(states)-1)]
		calls++
		return s, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}, &calls
}

func TestWaitForValidationsFollowsTheRunAndPrintsEachValidationAsItFinishes(t *testing.T) {
	cmd, _, stdout, stderr := validationFixture(t)
	fetch, calls := polls(
		run("running", row("connect", "completed", true), row("tables", "running", nil)),
		run("completed", row("connect", "completed", true), row("tables", "completed", true, map[string]any{"warnings": problem("Two tables were not readable.", "Grant SELECT on them.")})),
	)

	passed, err := waitForValidations(cmd, run("running", row("connect", "pending", nil), row("tables", "pending", nil)), fetch)

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if *calls != 2 {
		t.Errorf("fetched %d times", *calls)
	}
	want := "Running 2 validations:\n  ✓ Check connect\n  ⚠ Check tables\n\nCheck tables:\n  Two tables were not readable.\n    Grant SELECT on them.\n2 of 2 validations passed.\n"
	if stderr.String() != want {
		t.Fatalf("stderr =\n%s\nwant\n%s", stderr, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestWaitForValidationsFailsUnlessEveryValidationPassed(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  map[string]any
		says string
	}{
		{"a blocking problem", row("connect", "completed", false, map[string]any{"errors": problem("The key was rejected.", "Check the user's public key.")}), "  ✗ Check connect\n"},
		{"skipped", row("connect", "skipped", nil), "(skipped: a prerequisite did not pass)"},
		{"timed out", row("connect", "timed_out", nil), "(timed out)"},
		{"could not run", row("connect", "failed", nil), "(could not run)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, _, stderr := validationFixture(t)
			passed, err := waitForValidations(cmd, run("completed", row("ok", "completed", true), tc.row), nil)
			if err != nil || passed {
				t.Fatalf("passed %v, err %v", passed, err)
			}
			if !strings.Contains(stderr.String(), tc.says) || !strings.Contains(stderr.String(), "1 of 2 validations passed.") {
				t.Fatalf("stderr =\n%s", stderr)
			}
		})
	}
}

func TestWaitForValidationsRetriesATransientPollAndReportsAnyOtherFailure(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	calls := 0
	fetch := func() (any, *http.Response, error) {
		calls++
		if calls == 1 {
			return nil, &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}, errors.New("unavailable")
		}
		return run("completed", row("connect", "completed", true)), &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}
	if passed, err := waitForValidations(cmd, run("running", row("connect", "running", nil)), fetch); err != nil || !passed || calls != 2 {
		t.Fatalf("passed %v, err %v, after %d calls", passed, err, calls)
	}

	cmd, _, _, _ = validationFixture(t)
	gone := func() (any, *http.Response, error) {
		return nil, &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}}, errors.New("no validation run with that id")
	}
	if _, err := waitForValidations(cmd, run("running", row("connect", "running", nil)), gone); err == nil || !strings.Contains(err.Error(), "no validation run") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForValidationsStopsOnCtrlCAndAfterItsBudget(t *testing.T) {
	cmd, cancel, _, _ := validationFixture(t)
	fetch, _ := polls(run("running", row("connect", "running", nil)))
	cancel()
	if _, err := waitForValidations(cmd, run("running", row("connect", "running", nil)), fetch); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	cmd, _, _, _ = validationFixture(t)
	validationPollTimeout = 30 * time.Millisecond
	start := time.Now()
	_, err := waitForValidations(cmd, run("running", row("connect", "running", nil)), fetch)
	if err == nil || !strings.Contains(err.Error(), "validations get run run-1") || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

func TestPollAfterHonoursRetryAfter(t *testing.T) {
	_, _, _, _ = validationFixture(t)
	with := &http.Response{Header: http.Header{"Retry-After": []string{"4"}}}
	if got := pollAfter(with); got != 4*time.Second {
		t.Errorf("got %s", got)
	}
	if got := pollAfter(&http.Response{Header: http.Header{}}); got != validationPollInterval {
		t.Errorf("got %s", got)
	}
}

func TestTheLiveViewRedrawsInPlaceAndMarksUnfinishedRows(t *testing.T) {
	var out bytes.Buffer
	view := &validationView{w: &out, live: true}
	r, _ := asValidationRun(run("running", row("connect", "completed", true), row("tables", "running", nil), row("views", "pending", nil)))

	view.draw(r, "⠋")
	view.draw(r, "⠙")

	// Only the running validation spins; the one waiting for its turn is a dot.
	want := "\x1b[2K  ✓ Check connect\n\x1b[2K  ⠋ Check tables\n\x1b[2K  · Check views\n" +
		"\x1b[3A\x1b[2K  ✓ Check connect\n\x1b[2K  ⠙ Check tables\n\x1b[2K  · Check views\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

func TestTheLiveViewTruncatesToTheTerminalAndColoursOnlyWhenAsked(t *testing.T) {
	long := row("connect", "completed", false, map[string]any{"description": strings.Repeat("x", 40)})
	r, _ := asValidationRun(run("completed", long))
	plain := (&validationView{width: 20}).line(r.Validations[0], "")
	if plain != "  ✗ "+strings.Repeat("x", 15)+"…" {
		t.Errorf("plain = %q", plain)
	}
	coloured := (&validationView{color: true}).line(r.Validations[0], "")
	if !strings.HasPrefix(coloured, "  \x1b[31m✗\x1b[0m ") {
		t.Errorf("coloured = %q", coloured)
	}
}
