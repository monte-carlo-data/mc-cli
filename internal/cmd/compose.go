package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// undoTimeout bounds the whole cleanup after a failed multi-step command. A variable so a test
// can shrink it; nothing writes it at runtime.
var undoTimeout = 30 * time.Second

// unwind records what a command that creates several resources in turn has created so far, so
// that a later failure can delete it again, newest first.
//
// The cleanup runs on a context detached from the command's, bounded by undoTimeout. Ctrl-C
// cancels the command's context, which is what usually ends such a command early, and the
// deletes must still be sent after it.
type unwind struct {
	cmd     *cobra.Command
	created []createdResource
}

type createdResource struct {
	kind      string
	id        string
	deleteCmd string
	del       func(ctx context.Context) (*http.Response, error)
}

func newUnwind(cmd *cobra.Command) *unwind {
	return &unwind{cmd: cmd}
}

// record notes a resource this run created. deleteCmd is the command, without the binary name,
// that deletes it by hand; del deletes it with the context it is given.
func (u *unwind) record(kind, id, deleteCmd string, del func(ctx context.Context) (*http.Response, error)) {
	u.created = append(u.created, createdResource{kind: kind, id: id, deleteCmd: deleteCmd, del: del})
}

// fail handles a failed step: it deletes everything recorded, newest first, and returns the
// step's error followed by one line per resource. uncertain, when not empty, is added when the
// response leaves the step's own outcome unknown: none arrived, or the API answered with a
// server error, so the resource may have been created with an id this run never saw.
//
// The returned error unwraps to the step's error. Nothing from a request body goes into it.
func (u *unwind) fail(resp *http.Response, err error, uncertain string) error {
	stepErr := apiErr(resp, err)
	var report strings.Builder
	if uncertain != "" && outcomeUnknown(resp) {
		fmt.Fprintf(&report, "\n%s", uncertain)
	}
	if len(u.created) > 0 {
		report.WriteString("\nUndoing what this command created:")
		u.undo(&report)
	}
	if report.Len() == 0 {
		return stepErr
	}
	return &unwoundError{err: stepErr, report: report.String()}
}

// undo deletes the recorded resources, newest first, writing one line per resource to report.
// A resource that is already gone counts as deleted.
func (u *unwind) undo(report *strings.Builder) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(u.cmd.Context()), undoTimeout)
	defer cancel()
	for i := len(u.created) - 1; i >= 0; i-- {
		r := u.created[i]
		_, resp, err := retryWithin(ctx, u.cmd.ErrOrStderr(), func() (struct{}, *http.Response, error) {
			resp, err := r.del(ctx)
			return struct{}{}, resp, err
		})
		if err == nil || (resp != nil && resp.StatusCode == http.StatusNotFound) {
			fmt.Fprintf(report, "\n  deleted the %s %s", r.kind, r.id)
			continue
		}
		fmt.Fprintf(report, "\n  could not delete the %s %s: %s\n    delete it with: %s %s",
			r.kind, r.id, firstLine(apiErr(resp, err)), binaryName, r.deleteCmd)
	}
}

// outcomeUnknown is true when a response does not say whether the request was carried out. A
// 503 or 429 says it was not.
func outcomeUnknown(resp *http.Response) bool {
	return resp == nil || (resp.StatusCode >= http.StatusInternalServerError && !retryableStatus(resp.StatusCode))
}

type unwoundError struct {
	err    error
	report string
}

func (e *unwoundError) Error() string { return e.err.Error() + e.report }

func (e *unwoundError) Unwrap() error { return e.err }
