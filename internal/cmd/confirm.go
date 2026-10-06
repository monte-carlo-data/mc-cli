// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// confirm asks `question` on the terminal and returns an error unless the answer is yes.
// --yes skips the question; without a terminal it is required.
//
// Input is treated as interactive when stdin is a real terminal (stdinIsTerminal), or when
// the command's input has been explicitly redirected away from os.Stdin — as tests do with
// cmd.SetIn. Injected input is read as a terminal would be; only the real, non-tty os.Stdin
// case is refused.
func confirm(cmd *cobra.Command, question string) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	interactive := stdinIsTerminal(cmd) || cmd.InOrStdin() != os.Stdin
	if !interactive {
		return usageError("%s: pass --yes to run without a prompt", question)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s? [y/N] ", question)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// Ctrl-C cancels the context rather than ending the process, so the command waits on the
	// context, not on the read. An unanswered read is left behind; the process is about to exit.
	answered := make(chan string, 1)
	go func() {
		answer, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		answered <- answer
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(cmd.ErrOrStderr())
		return ctx.Err()
	case answer := <-answered:
		if confirmed(answer) {
			return nil
		}
		return withExitCode(exitInterrupted, errors.New("aborted"))
	}
}

// confirmed reports whether answer — as read from the prompt, trailing newline included —
// is an affirmative response: "y" or "yes", case-insensitive, ignoring surrounding whitespace.
func confirmed(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}
