package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"
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

// waitForValidations follows a validation run to its end and says whether every validation in
// it passed: reached a verdict, and found no blocking problem. Warnings do not fail it.
//
// first is the run as the validate call returned it; fetch reads its current state, and is
// retried like retryOnTransient. Progress goes to stderr: a table redrawn in place on a
// terminal, else one line per validation as it finishes. The problems behind each verdict are
// printed at the end. Ctrl-C stops the wait.
func waitForValidations(cmd *cobra.Command, first any, fetch func() (any, *http.Response, error)) (bool, error) {
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
	for run.Status != "completed" {
		if time.Now().Add(wait).After(deadline) {
			view.draw(run, "")
			return false, fmt.Errorf("stopped waiting after %s; the run is %s, readable with %s validations get run %s", validationPollTimeout, run.ID, binaryName, run.ID)
		}
		if err := view.animate(cmd, run, wait); err != nil {
			return false, err
		}
		out, resp, err := retryWithin(cmd.Context(), w, fetch)
		if err != nil {
			return false, apiErr(resp, err)
		}
		if run, err = asValidationRun(out); err != nil {
			return false, err
		}
		wait = pollAfter(resp)
	}
	view.draw(run, "")
	view.problems(run)
	passed := 0
	for _, v := range run.Validations {
		if v.passed() {
			passed++
		}
	}
	fmt.Fprintf(w, "%d of %d validations passed.\n", passed, len(run.Validations))
	return passed == len(run.Validations), nil
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

// stderrIsTerminal is stdoutIsTerminal for the command's error stream.
func stderrIsTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.ErrOrStderr().(*os.File)
	return ok && isTerminal(f)
}

// stderrWidth is the terminal's width, or 0 when stderr is not one.
func stderrWidth(cmd *cobra.Command) int {
	f, ok := cmd.ErrOrStderr().(*os.File)
	if !ok || !isTerminal(f) {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}
