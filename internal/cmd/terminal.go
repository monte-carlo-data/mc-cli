// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// isTerminal reports whether f is a terminal. Tests replace it.
var isTerminal = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// readPassword reads one line from the terminal f with echo off. Tests replace it.
var readPassword = func(f *os.File) ([]byte, error) { return term.ReadPassword(int(f.Fd())) }

// stdinIsTerminal reports whether the command's input is a terminal. Input a test injects
// with cmd.SetIn is never one.
func stdinIsTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && isTerminal(f)
}

// stdoutIsTerminal is the same test for the command's output.
func stdoutIsTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.OutOrStdout().(*os.File)
	return ok && isTerminal(f)
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
