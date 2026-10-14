// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import "github.com/spf13/cobra"

// DocsRoot returns the command tree as the command line sees it, for the reference generator.
// Cobra adds the help and completion commands and each command's -h flag only when a command
// line runs, and executeArgs groups the generated commands then, so this does all of it first.
// It is safe to call more than once.
func DocsRoot() *cobra.Command {
	groupResourceCommands(rootCmd)
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	initHelpFlags(rootCmd)
	return rootCmd
}

func initHelpFlags(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	for _, sub := range cmd.Commands() {
		initHelpFlags(sub)
	}
}
