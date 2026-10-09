// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	return map[string]any{"id": "run-1", "status": status, "validations": rows, "validations_total": len(rows)}
}

func problem(message, resolution string) []any {
	return []any{map[string]any{"friendly_message": message, "resolution": resolution, "cause": nil, "stack_trace": nil}}
}

// polls answers each fetch with the next state, then keeps answering the last.
func polls(states ...map[string]any) (runFetch, *int) {
	calls := 0
	return func(*int64, string) (any, *http.Response, error) {
		s := states[min(calls, len(states)-1)]
		calls++
		return s, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}, &calls
}

func TestFollowValidationRunFollowsTheRunAndPrintsEachValidationAsItFinishes(t *testing.T) {
	cmd, _, stdout, stderr := validationFixture(t)
	fetch, calls := polls(
		run("running", row("connect", "completed", true), row("tables", "running", nil)),
		run("completed", row("connect", "completed", true), row("tables", "completed", true, map[string]any{"warnings": problem("Two tables were not readable.", "Grant SELECT on them.")})),
	)

	passed, err := followValidationRun(cmd, run("running", row("connect", "pending", nil), row("tables", "pending", nil)), fetch, "validations get run")

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if *calls != 2 {
		t.Errorf("fetched %d times", *calls)
	}
	want := "Running 2 validations:\n  ✓ Check connect\n  ⚠ Check tables\n\nCheck tables:\n  Two tables were not readable.\n    Grant SELECT on them.\n2 of 2 validations passed (run run-1).\n"
	if stderr.String() != want {
		t.Fatalf("stderr =\n%s\nwant\n%s", stderr, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout)
	}
}

// answer is one response a scripted read gives: a run with an optional ETag, an error response
// with status, or, when both state and status are zero, a 304.
type answer struct {
	state  map[string]any
	etag   string
	status int
}

// reads answers each fetch with the next answer, then keeps answering the last, and records
// what each fetch was given: the since, "-" for none, then the ETag when there is one.
func reads(answers ...answer) (func(*int64, string) (any, *http.Response, error), *[]string) {
	var got []string
	return func(since *int64, etag string) (any, *http.Response, error) {
		req := "-"
		if since != nil {
			req = fmt.Sprint(*since)
		}
		if etag != "" {
			req += " " + etag
		}
		got = append(got, req)
		a := answers[min(len(got)-1, len(answers)-1)]
		if a.status != 0 {
			return nil, &http.Response{StatusCode: a.status, Header: http.Header{}}, fmt.Errorf("status %d", a.status)
		}
		if a.state == nil {
			return nil, &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{}}, errors.New("304 Not Modified")
		}
		return a.state, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {a.etag}}}, nil
	}, &got
}

func at(revision int64, r map[string]any) map[string]any {
	r["revision"] = revision
	return r
}

// delta is a since read's answer: only what changed, with validations_total still the whole
// run's count.
func delta(revision int64, status string, total int, rows ...map[string]any) map[string]any {
	r := at(revision, run(status, rows...))
	r["validations_total"] = total
	return r
}

func TestFollowValidationRunPollsForChangesAndMergesThemByName(t *testing.T) {
	cmd, _, _, stderr := validationFixture(t)
	fetch, got := reads(
		answer{state: delta(5, "running", 2, row("connect", "completed", true))},
		answer{state: delta(7, "completed", 2, row("tables", "completed", true))},
		answer{state: at(7, run("completed", row("connect", "completed", true), row("tables", "completed", true)))},
	)

	passed, err := followValidationRun(cmd, at(3, run("running", row("connect", "pending", nil), row("tables", "pending", nil))), fetch, "")

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v\n%s", passed, err, stderr)
	}
	// The last read takes the whole run, which is what fetch's caller is left holding.
	if fmt.Sprint(*got) != "[3 5 -]" {
		t.Errorf("reads = %v", *got)
	}
	if !strings.Contains(stderr.String(), "  ✓ Check connect\n  ✓ Check tables\n") || !strings.HasSuffix(stderr.String(), "2 of 2 validations passed (run run-1).\n") {
		t.Fatalf("stderr =\n%s", stderr)
	}
}

func TestFollowValidationRunReadsNoMoreWhenTheLastReadListedTheWholeRun(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	fetch, got := reads(answer{state: at(5, run("completed", row("connect", "completed", true), row("tables", "completed", true)))})

	passed, err := followValidationRun(cmd, at(3, run("running", row("connect", "pending", nil), row("tables", "pending", nil))), fetch, "")

	if err != nil || !passed || fmt.Sprint(*got) != "[3]" {
		t.Fatalf("passed %v, err %v, reads %v", passed, err, *got)
	}
}

func TestFollowValidationRunSendsTheETagBackAndTreatsA304AsNoChange(t *testing.T) {
	cmd, _, _, stderr := validationFixture(t)
	fetch, got := reads(
		answer{state: delta(5, "running", 2, row("connect", "completed", true)), etag: `W/"5"`},
		answer{},
		answer{state: delta(7, "completed", 2, row("tables", "completed", true)), etag: `W/"7"`},
		answer{state: at(7, run("completed", row("connect", "completed", true), row("tables", "completed", true)))},
	)

	passed, err := followValidationRun(cmd, at(3, run("running", row("connect", "pending", nil), row("tables", "pending", nil))), fetch, "")

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v\n%s", passed, err, stderr)
	}
	if fmt.Sprint(*got) != `[3 5 W/"5" 5 W/"5" -]` {
		t.Errorf("reads = %v", *got)
	}
}

// A 404 on the final whole read is an error, and passed is false.
func TestFollowValidationRunFailsWhenTheFinalWholeReadErrors(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	fetch, got := reads(
		answer{state: delta(5, "completed", 2, row("connect", "completed", true))},
		answer{status: http.StatusNotFound},
	)

	passed, err := followValidationRun(cmd, at(3, run("running", row("connect", "pending", nil), row("tables", "pending", nil))), fetch, "")

	if err == nil || passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if fmt.Sprint(*got) != "[3 -]" {
		t.Errorf("reads = %v", *got)
	}
}

// A 503 on the final whole read is retried like any other transient failure.
func TestFollowValidationRunRetriesA503OnTheFinalWholeRead(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	fetch, got := reads(
		answer{state: delta(5, "completed", 2, row("connect", "completed", true))},
		answer{status: http.StatusServiceUnavailable},
		answer{state: at(7, run("completed", row("connect", "completed", true), row("tables", "completed", true)))},
	)

	passed, err := followValidationRun(cmd, at(3, run("running", row("connect", "pending", nil), row("tables", "pending", nil))), fetch, "")

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if fmt.Sprint(*got) != "[3 - -]" {
		t.Errorf("reads = %v", *got)
	}
}

func TestFollowValidationRunReadsTheWholeRunWhenItCarriesNoRevision(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	fetch, got := reads(answer{state: run("completed", row("connect", "completed", true), row("tables", "completed", true)), etag: `W/"2"`})

	passed, err := followValidationRun(cmd, run("running", row("connect", "pending", nil), row("tables", "pending", nil)), fetch, "")

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if fmt.Sprint(*got) != "[-]" {
		t.Errorf("reads = %v", *got)
	}
}

// A partial completed read triggers the extra whole read.
func TestFollowValidationRunReadsAgainWhenTheCompletedReadIsPartial(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	partial := run("completed", row("connect", "completed", true))
	partial["validations_total"] = 5
	fetch, calls := polls(partial)

	_, err := followValidationRun(cmd, run("running", row("connect", "pending", nil)), fetch, "")

	if err == nil || !strings.Contains(err.Error(), "lists 1 of 5 validations") {
		t.Fatalf("err = %v", err)
	}
	if *calls != 2 {
		t.Errorf("fetched %d times", *calls)
	}
}

func TestFollowValidationRunFailsUnlessEveryValidationPassed(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  map[string]any
		says string
	}{
		{"a blocking problem", row("connect", "completed", false, map[string]any{"errors": problem("The key was rejected.", "Check the user's public key.")}), "  ✗ Check connect\n"},
		{"skipped", row("connect", "skipped", nil), "(skipped: a prerequisite did not pass)"},
		{"timed out", row("connect", "timed_out", false), "(timed out)"},
		{"timed out despite a stray passed flag", row("connect", "timed_out", true), "(timed out)"},
		{"could not run", row("connect", "failed", false), "(could not run)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, _, stderr := validationFixture(t)
			passed, err := followValidationRun(cmd, run("completed", row("ok", "completed", true), tc.row), nil, "validations get run")
			if err != nil || passed {
				t.Fatalf("passed %v, err %v", passed, err)
			}
			if !strings.Contains(stderr.String(), tc.says) || !strings.Contains(stderr.String(), "1 of 2 validations passed (run run-1).") {
				t.Fatalf("stderr =\n%s", stderr)
			}
		})
	}
}

func TestFollowValidationRunRetriesATransientPollAndReportsAnyOtherFailure(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	calls := 0
	fetch := func(*int64, string) (any, *http.Response, error) {
		calls++
		if calls == 1 {
			return nil, &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}, errors.New("unavailable")
		}
		return run("completed", row("connect", "completed", true)), &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}
	if passed, err := followValidationRun(cmd, run("running", row("connect", "running", nil)), fetch, "validations get run"); err != nil || !passed || calls != 2 {
		t.Fatalf("passed %v, err %v, after %d calls", passed, err, calls)
	}

	cmd, _, _, _ = validationFixture(t)
	gone := func(*int64, string) (any, *http.Response, error) {
		return nil, &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}}, errors.New("no validation run with that id")
	}
	if _, err := followValidationRun(cmd, run("running", row("connect", "running", nil)), gone, "validations get run"); err == nil || !strings.Contains(err.Error(), "no validation run") {
		t.Fatalf("err = %v", err)
	}
}

func TestFollowValidationRunStopsOnCtrlCAndAfterItsBudget(t *testing.T) {
	cmd, cancel, _, _ := validationFixture(t)
	fetch, _ := polls(run("running", row("connect", "running", nil)))
	cancel()
	if _, err := followValidationRun(cmd, run("running", row("connect", "running", nil)), fetch, "validations get run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	cmd, _, _, _ = validationFixture(t)
	validationPollTimeout = 30 * time.Millisecond
	start := time.Now()
	_, err := followValidationRun(cmd, run("running", row("connect", "running", nil)), fetch, "validations get run")
	if err == nil || !strings.Contains(err.Error(), "validations get run run-1") || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}

	// With no command that reads a run, the error names the run and no command.
	cmd, _, _, _ = validationFixture(t)
	validationPollTimeout = 30 * time.Millisecond
	_, err = followValidationRun(cmd, run("running", row("connect", "running", nil)), fetch, "")
	if err == nil || !strings.HasSuffix(err.Error(), "the run is run-1") {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeValidationsReplacesByNameAndAppendsTheUnknown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		held    []validationRow
		changed []validationRow
		want    []validationRow
	}{
		{
			"a known name is replaced in place",
			[]validationRow{{Name: "a", Status: "pending"}, {Name: "b", Status: "pending"}},
			[]validationRow{{Name: "a", Status: "completed"}},
			[]validationRow{{Name: "a", Status: "completed"}, {Name: "b", Status: "pending"}},
		},
		{
			"held's order is kept",
			[]validationRow{{Name: "b", Status: "pending"}, {Name: "a", Status: "pending"}},
			[]validationRow{{Name: "a", Status: "completed"}, {Name: "b", Status: "completed"}},
			[]validationRow{{Name: "b", Status: "completed"}, {Name: "a", Status: "completed"}},
		},
		{
			"an unknown name is appended last",
			[]validationRow{{Name: "a", Status: "pending"}},
			[]validationRow{{Name: "b", Status: "completed"}},
			[]validationRow{{Name: "a", Status: "pending"}, {Name: "b", Status: "completed"}},
		},
		{
			"an empty changed returns held's content",
			[]validationRow{{Name: "a", Status: "pending"}},
			nil,
			[]validationRow{{Name: "a", Status: "pending"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeValidations(tc.held, tc.changed)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeValidationsDoesNotModifyHeld(t *testing.T) {
	held := []validationRow{{Name: "a", Status: "pending"}}
	mergeValidations(held, []validationRow{{Name: "a", Status: "completed"}})

	if held[0].Status != "pending" {
		t.Errorf("held[0] = %+v", held[0])
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

func TestViewWriteResetsTheRedrawSoARetryMessageIsNotOverwritten(t *testing.T) {
	var out bytes.Buffer
	view := &validationView{w: &out, live: true}
	r, _ := asValidationRun(run("running", row("connect", "running", nil)))

	view.draw(r, "⠋")
	fmt.Fprintf(view, "unavailable\nRetrying in 1s.\n")
	view.draw(r, "⠙")

	want := "\x1b[2K  ⠋ Check connect\n" +
		"unavailable\nRetrying in 1s.\n" +
		"\x1b[2K  ⠙ Check connect\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

func TestFollowValidationRunHonoursRetryAfterBetweenPolls(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	var times []time.Time
	calls := 0
	fetch := func(*int64, string) (any, *http.Response, error) {
		times = append(times, time.Now())
		calls++
		if calls == 1 {
			return run("running", row("connect", "running", nil)), &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Retry-After": {"1"}}}, nil
		}
		return run("completed", row("connect", "completed", true)), &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, nil
	}

	passed, err := followValidationRun(cmd, run("running", row("connect", "pending", nil)), fetch, "validations get run")

	if err != nil || !passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if len(times) != 2 {
		t.Fatalf("fetched %d times", len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 900*time.Millisecond || gap > 5*time.Second {
		t.Fatalf("gap between polls = %s", gap)
	}
}

func TestFollowValidationRunRejectsARunWhoseValidationCountDoesNotMatchItsTotal(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	r := run("completed", row("connect", "completed", true))
	r["validations_total"] = 5

	if _, err := followValidationRun(cmd, r, nil, "validations get run"); err == nil || !strings.Contains(err.Error(), "it lists 1 of 5 validations") {
		t.Fatalf("err = %v", err)
	}
}

func TestFollowValidationRunRejectsACompletedRunWithNoValidations(t *testing.T) {
	cmd, _, _, _ := validationFixture(t)
	r := run("completed")

	if _, err := followValidationRun(cmd, r, nil, "validations get run"); err == nil || !strings.Contains(err.Error(), "no validations") {
		t.Fatalf("err = %v", err)
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
