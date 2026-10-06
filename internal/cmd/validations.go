// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// The polling budget. Variables so a test can shrink them; nothing writes them at runtime.
var (
	validationPollInterval = 3 * time.Second
	validationPollTimeout  = 30 * time.Minute
	validationFrameRate    = 120 * time.Millisecond
)

// validationRun is the part of a validation run this CLI reads. It is decoded from the API's
// JSON rather than taken from the SDK's type, the way render reads any response.
type validationRun struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	Validations []validationRow `json:"validations"`
	Total       int             `json:"validations_total"`
	Revision    *int64          `json:"revision"`
}

type validationRow struct {
	Name        string              `json:"name"`
	Description *string             `json:"description"`
	Status      string              `json:"status"`
	Passed      *bool               `json:"passed"`
	Errors      []validationProblem `json:"errors"`
	Warnings    []validationProblem `json:"warnings"`
}

type validationProblem struct {
	FriendlyMessage *string `json:"friendly_message"`
	Resolution      *string `json:"resolution"`
}

// runFetch reads a validation run. With since set, the read lists only the validations
// changed after that revision; with etag set, it answers 304 while the run is unchanged.
type runFetch func(since *int64, etag string) (any, *http.Response, error)

// followValidationRun follows a validation run to its end and says whether every validation in
// it passed: reached a verdict, and found no blocking problem. Warnings do not fail it.
//
// first is the run as the validate call returned it; fetch reads its current state, and is
// retried like retryOnTransient. fetch gets the revision of the run held so far as since, nil
// when the run carries none, and the ETag of the last read, "" before there is one. The
// validations it returns are merged into the held run by name, so a read listing only those
// changed since that revision is enough, and a 304 leaves the held run as it is. When the
// last read listed fewer validations than the run has, the run is read once more whole, with
// neither: the caller keeps what fetch last returned, so that must be the complete run.
//
// runCmd is the command, without the binary name, that reads a run by id; the id is appended
// to it, mirroring how the undo helper's record takes deleteCmd from the caller. "" means no
// command reads a run, and the timeout error names none.
//
// Progress goes to stderr: a table redrawn in place on a terminal, else one line per
// validation as it finishes. The problems behind each verdict are printed at the end, then a
// summary naming the run. Ctrl-C stops the wait.
func followValidationRun(cmd *cobra.Command, first any, fetch runFetch, runCmd string) (bool, error) {
	run, err := asValidationRun(first)
	if err != nil {
		return false, err
	}
	w := cmd.ErrOrStderr()
	live := stderrIsTerminal(cmd)
	fmt.Fprintf(w, "Running %d validations:\n", len(run.Validations))
	view := &validationView{w: w, live: live, color: live && os.Getenv("NO_COLOR") == "", width: stderrWidth(cmd)}
	deadline := time.Now().Add(validationPollTimeout)
	wait := validationPollInterval
	whole := true
	etag := ""
	for run.Status != "completed" {
		if time.Now().Add(wait).After(deadline) {
			view.draw(run, "")
			if runCmd == "" {
				return false, fmt.Errorf("stopped waiting after %s; the run is %s", validationPollTimeout, run.ID)
			}
			return false, fmt.Errorf("stopped waiting after %s; the run is %s, readable with %s %s %s", validationPollTimeout, run.ID, binaryName, runCmd, run.ID)
		}
		if err := view.animate(cmd, run, wait); err != nil {
			return false, err
		}
		next, resp, changed, err := readValidationRun(cmd, view, fetch, run.Revision, etag)
		if err != nil {
			return false, err
		}
		wait = pollAfter(resp)
		if !changed {
			continue
		}
		etag = resp.Header.Get("ETag")
		whole = len(next.Validations) == next.Total
		next.Validations = mergeValidations(run.Validations, next.Validations)
		run = next
	}
	if !whole {
		if run, _, _, err = readValidationRun(cmd, view, fetch, nil, ""); err != nil {
			return false, err
		}
	}
	view.draw(run, "")
	if len(run.Validations) != run.Total {
		return false, fmt.Errorf("cannot read the validation run: it lists %d of %d validations", len(run.Validations), run.Total)
	}
	if run.Total == 0 {
		return false, fmt.Errorf("the validation run has no validations")
	}
	view.problems(run)
	passed := 0
	for _, v := range run.Validations {
		if v.passed() {
			passed++
		}
	}
	fmt.Fprintf(w, "%d of %d validations passed (run %s).\n", passed, len(run.Validations), run.ID)
	return passed == len(run.Validations), nil
}

// readValidationRun calls fetch, retried like retryOnTransient, and decodes the run. changed is
// false for a 304, which carries no run.
func readValidationRun(cmd *cobra.Command, view io.Writer, fetch runFetch, since *int64, etag string) (run validationRun, resp *http.Response, changed bool, err error) {
	out, resp, err := retryWithin(cmd.Context(), view, func() (any, *http.Response, error) { return fetch(since, etag) })
	if resp != nil && resp.StatusCode == http.StatusNotModified {
		return run, resp, false, nil
	}
	if err != nil {
		return run, resp, false, apiErr(resp, err)
	}
	run, err = asValidationRun(out)
	return run, resp, true, err
}

// mergeValidations replaces each validation in held that changed carries by name, keeping
// held's order. A validation held does not know yet goes last.
func mergeValidations(held, changed []validationRow) []validationRow {
	out := slices.Clone(held)
	index := make(map[string]int, len(out))
	for i, v := range out {
		index[v.Name] = i
	}
	for _, v := range changed {
		if i, ok := index[v.Name]; ok {
			out[i] = v
			continue
		}
		index[v.Name] = len(out)
		out = append(out, v)
	}
	return out
}

func asValidationRun(v any) (validationRun, error) {
	var run validationRun
	raw, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(raw, &run)
	}
	if err != nil {
		return run, fmt.Errorf("cannot read the validation run: %w", err)
	}
	return run, nil
}

// pollAfter is the wait the response's Retry-After asks for, in seconds, else the default.
func pollAfter(resp *http.Response) time.Duration {
	if resp != nil {
		if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return validationPollInterval
}

func (v validationRow) passed() bool {
	return v.Status == "completed" && v.Passed != nil && *v.Passed
}

func (v validationRow) finished() bool {
	return v.Status != "pending" && v.Status != "running"
}

func (v validationRow) label() string {
	if v.Description != nil && *v.Description != "" {
		return *v.Description
	}
	return v.Name
}

// mark is the row's symbol and the note after its label. spin stands in for a validation that
// is running; one still waiting for its turn is a dot.
func (v validationRow) mark(spin string) (symbol, note string) {
	switch {
	case v.Status == "pending":
		return "·", ""
	case !v.finished():
		return spin, ""
	case v.passed() && len(v.Warnings) > 0:
		return "⚠", ""
	case v.passed():
		return "✓", ""
	case v.Status == "completed":
		return "✗", ""
	case v.Status == "skipped":
		return "-", " (skipped: a prerequisite did not pass)"
	case v.Status == "timed_out":
		return "✗", " (timed out)"
	}
	return "✗", " (could not run)"
}

// validationView prints a run's progress: redrawn in place on a terminal, else each row once,
// when it finishes.
type validationView struct {
	w       io.Writer
	live    bool
	color   bool
	width   int
	drawn   int
	printed map[string]bool
}

// Write passes p through to the underlying stream. A message written mid-poll — a retry
// notice — lands below the last drawn frame, so the redraw must not try to move back over it:
// resetting drawn makes the next frame draw fresh, below the message, rather than a cursor-up
// that would overwrite and duplicate rows.
func (v *validationView) Write(p []byte) (int, error) {
	n, err := v.w.Write(p)
	if v.live {
		v.drawn = 0
	}
	return n, err
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// animate redraws the run until wait has passed, or returns the context's error on Ctrl-C.
func (v *validationView) animate(cmd *cobra.Command, run validationRun, wait time.Duration) error {
	done := time.After(wait)
	frame := time.NewTicker(validationFrameRate)
	defer frame.Stop()
	for i := 0; ; i++ {
		v.draw(run, spinner[i%len(spinner)])
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-done:
			return nil
		case <-frame.C:
		}
	}
}

func (v *validationView) draw(run validationRun, spin string) {
	if !v.live {
		if v.printed == nil {
			v.printed = map[string]bool{}
		}
		for _, row := range run.Validations {
			if row.finished() && !v.printed[row.Name] {
				v.printed[row.Name] = true
				fmt.Fprintln(v.w, v.line(row, ""))
			}
		}
		return
	}
	if spin == "" {
		spin = "·"
	}
	if v.drawn > 0 {
		fmt.Fprintf(v.w, "\x1b[%dA", v.drawn)
	}
	for _, row := range run.Validations {
		fmt.Fprintf(v.w, "\x1b[2K%s\n", v.line(row, spin))
	}
	v.drawn = len(run.Validations)
}

func (v *validationView) line(row validationRow, spin string) string {
	symbol, note := row.mark(spin)
	text := row.label() + note
	if v.width > 4 && utf8.RuneCountInString(text) > v.width-4 {
		text = string([]rune(text)[:v.width-5]) + "…"
	}
	return "  " + v.paint(symbol) + " " + text
}

func (v *validationView) paint(symbol string) string {
	if !v.color {
		return symbol
	}
	code := map[string]string{"✓": "32", "✗": "31", "⚠": "33", "-": "2", "·": "2"}[symbol]
	if code == "" {
		return symbol
	}
	return "\x1b[" + code + "m" + symbol + "\x1b[0m"
}

// problems prints what each validation reported: its errors, then its warnings.
func (v *validationView) problems(run validationRun) {
	for _, row := range run.Validations {
		if len(row.Errors)+len(row.Warnings) == 0 {
			continue
		}
		fmt.Fprintf(v.w, "\n%s:\n", row.label())
		for _, p := range append(append([]validationProblem{}, row.Errors...), row.Warnings...) {
			if p.FriendlyMessage != nil && *p.FriendlyMessage != "" {
				fmt.Fprintf(v.w, "  %s\n", *p.FriendlyMessage)
			}
			if p.Resolution != nil && *p.Resolution != "" {
				fmt.Fprintf(v.w, "    %s\n", *p.Resolution)
			}
		}
	}
}
