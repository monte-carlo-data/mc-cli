package cmd

import (
	"context"
	"errors"
	"fmt"
)

// The process exit codes. Scripts depend on them, so a change here is a breaking change; the
// README lists them.
const (
	exitOK          = 0
	exitFailure     = 1   // an API or runtime failure, and anything not classified below
	exitUsage       = 2   // the command line is wrong: an unknown command or flag, a bad value
	exitNotFound    = 3   // the API answered 404
	exitAuth        = 4   // the credentials were rejected: 401 or 403
	exitTransient   = 5   // the API was still answering 503 or 429 when the command ended
	exitValidation  = 6   // the command ran, but the validations it ran did not pass
	exitInterrupted = 130 // declined at a confirmation, or interrupted
)

// exitError carries the exit code for err. Its message is err's, unchanged.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }

func (e *exitError) Unwrap() error { return e.err }

// withExitCode attaches code to err; a nil err stays nil.
func withExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitError{code: code, err: err}
}

// usageError is fmt.Errorf for a mistake on the command line: running it again unchanged fails
// the same way.
func usageError(format string, args ...any) error {
	return withExitCode(exitUsage, fmt.Errorf(format, args...))
}

// validationsFailed is the error for a command whose validations ran and did not all pass.
func validationsFailed(format string, args ...any) error {
	return withExitCode(exitValidation, fmt.Errorf(format, args...))
}

// exitCode is the exit code for the error a command returned under ctx. An interrupt wins over
// whatever error it caused, because some errors arrive here flattened into text.
func exitCode(ctx context.Context, err error) int {
	if err == nil {
		return exitOK
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return exitInterrupted
	}
	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return exitFailure
}
