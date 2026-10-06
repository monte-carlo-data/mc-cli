package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
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
	code, stderr := runExit(t, context.Background(), "whoami", "--endpoint", "http://127.0.0.1:1", "--api-id", "i", "--api-token", "s")
	if code != exitFailure || !strings.HasPrefix(stderr, binaryName+": ") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestRunReturnsZeroOnSuccess(t *testing.T) {
	if code, stderr := runExit(t, context.Background(), "version"); code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestRunReturns130WhenInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _ := runExit(t, ctx, "whoami", "--endpoint", "http://127.0.0.1:1", "--api-id", "i", "--api-token", "s")
	if code != exitInterrupted {
		t.Fatalf("exit %d", code)
	}
}
