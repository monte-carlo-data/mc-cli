// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// newTestCommand builds a minimal fixture command wired for confirm's use directly — it is
// never run through cmd.Execute(), so Run is never invoked.
func newTestCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "t", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().Bool("yes", false, "")
	return cmd
}

func TestConfirmSkipsTheQuestionWithYes(t *testing.T) {
	cmd := newTestCommand(t)
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := confirm(cmd, "Delete deployment x"); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmRefusesWithoutATerminalOrYes(t *testing.T) {
	// go test runs without a terminal on stdin, and the fixture command's input defaults to
	// the real os.Stdin, so this exercises the non-interactive refusal path.
	cmd := newTestCommand(t)
	err := confirm(cmd, "Delete deployment x")
	if err == nil || err.Error() != "Delete deployment x: pass --yes to run without a prompt" {
		t.Fatalf("err = %v", err)
	}
}

func TestConfirmParsesTheAnswer(t *testing.T) {
	original := isTerminal
	isTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { isTerminal = original })

	tests := []struct {
		name   string
		answer string
		accept bool
	}{
		{"lowercase y", "y\n", true},
		{"yes", "yes\n", true},
		{"uppercase Y", "Y\n", true},
		{"padded", " y \n", true},
		{"n", "n\n", false},
		{"no", "no\n", false},
		{"empty line", "\n", false},
		{"garbage", "maybe\n", false},
		{"EOF", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestCommand(t)
			cmd.SetIn(strings.NewReader(tt.answer))
			var stderr bytes.Buffer
			cmd.SetErr(&stderr)

			err := confirm(cmd, "Delete deployment x")

			if tt.accept {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else if err == nil || err.Error() != "aborted" {
				t.Fatalf("err = %v, want aborted", err)
			}

			if got, want := stderr.String(), "Delete deployment x? [y/N] "; got != want {
				t.Fatalf("stderr = %q, want %q", got, want)
			}
		})
	}
}

// one smoke test over a generated command; the shape is the generator's.
func TestGeneratedDeleteRefusesWithoutYesOffATerminal(t *testing.T) {
	_, _, err := executeStreams(t, "deployments", "delete", "some-id", "--config-dir", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "Delete deployment some-id: pass --yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfirmEndsWhenTheCommandIsInterrupted(t *testing.T) {
	cmd := newTestCommand(t)
	answer, _ := io.Pipe() // nobody answers
	cmd.SetIn(answer)
	cmd.SetErr(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- confirm(cmd, "Delete deployment x") }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if exitCode(ctx, err) != exitInterrupted {
			t.Fatalf("exit code = %d", exitCode(ctx, err))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the prompt kept waiting after the command was interrupted")
	}
}

func TestConfirmExitCodes(t *testing.T) {
	cmd := newTestCommand(t)
	if got := exitCode(context.Background(), confirm(cmd, "Delete deployment x")); got != exitUsage {
		t.Fatalf("without a terminal or --yes: exit %d", got)
	}
	original := isTerminal
	isTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { isTerminal = original })
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetErr(io.Discard)
	if got := exitCode(context.Background(), confirm(cmd, "Delete deployment x")); got != exitInterrupted {
		t.Fatalf("declined: exit %d", got)
	}
}
