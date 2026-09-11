package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// isTerminal reports whether f is a terminal. Tests replace it.
var isTerminal = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

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
