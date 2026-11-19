// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// spinnerFrameRate is how often a spinner on stderr advances. A variable so a test can shrink it;
// nothing writes it at runtime.
var spinnerFrameRate = 120 * time.Millisecond

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// fitWidth cuts text to fit after a four-column prefix on a terminal width columns wide, ending
// it with an ellipsis. A width of 4 or less leaves text as it is.
func fitWidth(text string, width int) string {
	if width > 4 && utf8.RuneCountInString(text) > width-4 {
		return string([]rune(text)[:width-5]) + "…"
	}
	return text
}

// terminalLines splits API text into lines safe to print: line endings normalised, surrounding
// blank space trimmed, and control characters other than tab shown as '?'.
func terminalLines(s string) []string {
	s = strings.TrimSpace(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s))
	if s == "" {
		return nil
	}
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
	return strings.Split(s, "\n")
}

// oneLine is API text made safe to print on one line.
func oneLine(s string) string {
	return strings.Join(terminalLines(s), " ")
}

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
