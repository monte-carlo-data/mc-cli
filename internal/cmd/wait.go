// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The status wait's timing. Variables so a test can shrink them; nothing writes them at runtime.
// statusTimeoutUnit is the length of one of waitForStatus's timeoutSeconds.
var (
	statusPollInterval = 5 * time.Second
	statusTimeoutUnit  = time.Second
)

// waitForStatus waits for a resource to settle and returns it as last read.
//
// first is the resource as the call that created or changed it returned it, and status gives
// a value's status and a detail that says more about it. When first's status is in ready,
// first is returned as it is, with nothing read and nothing printed. Otherwise read is called
// every statusPollInterval, retried like retryOnTransient, until the status is in ready or in
// failed. A status in neither keeps the wait going.
//
// Progress goes to stderr: a line naming the resource and the status it waits for, then a
// line each time the status or its detail changes, the latest redrawn in place with a spinner
// on a terminal, then a summary. Stdout stays clean for the caller to print the result.
//
// A failed status is an error carrying the detail. The wait running past timeoutSeconds is an
// error naming the last status, as is Ctrl-C, which stops the wait through the command's
// context. So is a 503 or a 429 whose retry would outlast timeoutSeconds, which stops the wait
// early, says so, and adds that read's failure as text. A 503 or a 429 still answering when the
// retries' own budget runs out ends the wait with that failure, its exit code kept, followed by
// the id and the last status. Each of these errors names id and, on a line of its own, readCmd,
// the command without the binary name that reads the resource; the id is appended to it. An
// empty readCmd adds no such line. A read failing with anything other than a 503 or a 429 ends
// the wait with that failure alone.
// With every error, the latest value held is returned too.
func waitForStatus[T any](
	cmd *cobra.Command,
	first *T,
	status func(*T) (value, detail string),
	read func() (*T, *http.Response, error),
	ready, failed []string,
	timeoutSeconds int,
	id, readCmd string,
) (*T, error) {
	// id comes from an API response, so it is made safe to print like the status and detail.
	safeID := oneLine(id)
	held := first
	value, detail := "", ""
	if held != nil {
		value, detail = status(held)
	}
	if slices.Contains(ready, value) {
		return held, nil
	}
	recheck := ""
	if readCmd != "" {
		recheck = fmt.Sprintf("\ncheck it with: %s %s %s", binaryName, readCmd, safeID)
	}
	if slices.Contains(failed, value) {
		return held, statusFailed(safeID, value, detail, recheck)
	}

	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "Waiting for %s to be %s:\n", safeID, strings.Join(ready, " or "))
	view := &statusView{w: w, live: stderrIsTerminal(cmd), width: stderrWidth(cmd)}
	view.show(value, detail)

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(timeoutSeconds)*statusTimeoutUnit)
	defer cancel()
	still := func() string {
		if value == "" {
			return ""
		}
		return " is still " + oneLine(value)
	}
	// stopped is the error for a wait that ran out or was interrupted.
	stopped := func() error {
		if err := cmd.Context().Err(); err != nil {
			return fmt.Errorf("stopped waiting: %w; %s%s%s", err, safeID, still(), recheck)
		}
		return fmt.Errorf("stopped waiting after %s; %s%s%s", timeout, safeID, still(), recheck)
	}
	poll := func() error {
		for {
			if !view.sleep(ctx, statusPollInterval) {
				return stopped()
			}
			start := time.Now()
			next, resp, err := retryWithin(ctx, view, read)
			if err != nil {
				if ctx.Err() != nil {
					return stopped()
				}
				if resp == nil || !retryableStatus(resp.StatusCode) {
					return apiErr(resp, err)
				}
				if _, boundByCtx := retryDeadline(ctx, start); boundByCtx {
					// The wait's own deadline cut the retries short, with time still left. The
					// failure is text, so the exit code stays that of a wait that ran out.
					return fmt.Errorf("stopped waiting: retrying the read would outlast the %s timeout; %s%s\nthe last read failed: %s%s",
						timeout, safeID, still(), apiErr(resp, err).Error(), recheck)
				}
				// The retry budget ran out first. The failure is kept, with its exit code.
				return fmt.Errorf("%w\n%s%s%s", apiErr(resp, err), safeID, still(), recheck)
			}
			if next == nil {
				return fmt.Errorf("cannot read %s: the response held nothing", safeID)
			}
			held = next
			value, detail = status(held)
			view.show(value, detail)
			switch {
			case slices.Contains(ready, value):
				return nil
			case slices.Contains(failed, value):
				return statusFailed(safeID, value, detail, recheck)
			}
		}
	}
	err := poll()
	view.end()
	if err == nil {
		fmt.Fprintf(w, "%s is %s.\n", safeID, oneLine(value))
	}
	return held, err
}

// statusFailed is the error for a resource that reached a failed status.
func statusFailed(id, value, detail, recheck string) error {
	if d := oneLine(detail); d != "" {
		return errors.New(id + " is " + oneLine(value) + ": " + d + recheck)
	}
	return errors.New(id + " is " + oneLine(value) + recheck)
}

// statusView prints a status wait's progress, one line per status and detail. Off a terminal
// each line is printed once, when it is shown. On a terminal the latest line is redrawn in
// place with a spinner while the wait sleeps, and drawn without one when the next line
// replaces it or the wait ends, so the screen then holds what is printed off a terminal.
type statusView struct {
	w     io.Writer
	live  bool
	width int
	// line is the latest line's text, without its indent; "" before there is one.
	line string
	// drawn is true while line is on screen with a spinner, the cursor on the line below it.
	drawn bool
}

// show makes the status and detail the latest line, unless they are already.
func (v *statusView) show(value, detail string) {
	if value == "" {
		return
	}
	line := oneLine(value)
	if d := oneLine(detail); d != "" {
		line += ": " + d
	}
	if line == v.line {
		return
	}
	if v.live {
		v.settle()
	} else {
		fmt.Fprintln(v.w, "  "+line)
	}
	v.line = line
}

// settle draws the latest line without a spinner, over its spinning frame when one is on
// screen. Off a terminal it does nothing, since show printed the line already.
func (v *statusView) settle() {
	if !v.live || v.line == "" {
		return
	}
	v.home()
	fmt.Fprintf(v.w, "  %s\n", v.line)
}

// frame draws the latest line with spin before it, over the previous frame when one is on
// screen.
func (v *statusView) frame(spin string) {
	if v.line == "" {
		return
	}
	v.home()
	fmt.Fprintf(v.w, "  %s %s\n", spin, fitWidth(v.line, v.width))
	v.drawn = true
}

// home clears the cursor's line and puts the cursor at its start, then, when a frame is on
// screen, moves up onto the frame and clears that too. Whatever the terminal echoed below the
// frame, such as ^C, goes with it, and what is drawn next starts at the first column.
func (v *statusView) home() {
	fmt.Fprint(v.w, "\r\x1b[2K")
	if v.drawn {
		fmt.Fprint(v.w, "\x1b[1A\x1b[2K")
	}
	v.drawn = false
}

// end leaves the latest line on screen without a spinner, for the summary or the error to
// follow on a line of its own.
func (v *statusView) end() {
	v.settle()
	v.line = ""
}

// sleep waits for wait to pass, animating the latest line on a terminal. It is false when ctx
// ends first.
func (v *statusView) sleep(ctx context.Context, wait time.Duration) bool {
	done := time.After(wait)
	if !v.live {
		select {
		case <-ctx.Done():
			return false
		case <-done:
			return true
		}
	}
	frame := time.NewTicker(spinnerFrameRate)
	defer frame.Stop()
	for i := 0; ; i++ {
		v.frame(spinner[i%len(spinner)])
		select {
		case <-ctx.Done():
			return false
		case <-done:
			return true
		case <-frame.C:
		}
	}
}

// Write passes p through to the underlying stream, for a message written mid-wait such as a
// retry notice. On a terminal it first erases the spinning frame and returns to the first
// column, so no stale frame is left above the message; the next frame draws the line again
// below it.
func (v *statusView) Write(p []byte) (int, error) {
	if v.live {
		v.home()
	}
	return v.w.Write(p)
}
