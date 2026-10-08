// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package cmd is the montecarlo command: the root, the hand-written commands, and the helpers
// the generated *_cmd.gen.go files call.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// binaryName is the command's name in help text. The main package lives in cmd/<binaryName>/.
const binaryName = "montecarlo"

// defaultEndpoint is the API base URL when neither a flag nor the profile names one.
const defaultEndpoint = "https://api.getmontecarlo.com"

// newRootCmd builds the root command and registers its persistent flags. It exists so rootCmd
// can be a var initializer: var initialization runs before any init(), which guarantees the
// persistent flags exist before registerRootCompletions, in this package's own init, looks for
// them.
func newRootCmd() *cobra.Command {
	// Run the root's persistent hook before any command's own, instead of replacing it.
	cobra.EnableTraverseRunHooks = true
	cmd := &cobra.Command{
		Use:   binaryName,
		Short: "Monte Carlo from the command line",
		Long: `Get started: run "` + binaryName + ` configure" to set up your credentials.

` + binaryName + ` talks to the Monte Carlo REST API.

Credentials come from flags, then the MCD_DEFAULT_* environment variables, then a profile in
~/.mcd/profiles.ini, the file every Monte Carlo tool shares. "` + binaryName + ` configure" writes one
interactively, and "` + binaryName + ` profile set" writes one from a script.

Credentials resolve as a set: pass a complete mechanism, either an API token pair or an OAuth
client, or none and let the environment or the profile supply one.

A secret flag accepts @<path> to read its value from a file, and has a --<name>-prompt companion.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// A bad --output is caught before the command runs, not when its result is printed,
		// after a write has already happened.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			_, err := outputFormat(cmd)
			return err
		},
	}

	cmd.AddGroup(
		&cobra.Group{ID: groupGettingStarted, Title: "Getting started:"},
		&cobra.Group{ID: groupResources, Title: "Resources:"},
	)

	// These names are reserved. The generator keeps the same list and refuses a body or query
	// flag that collides with one, so adding a flag here means adding it there.
	f := cmd.PersistentFlags()
	f.String("profile", "", `Profile in profiles.ini. Defaults to MCD_DEFAULT_PROFILE, then the profile chosen with "profile use", then "default".`)
	f.StringP("output", "o", "", "Output format: table, wide or json. Defaults to table on a terminal and json otherwise; wide is the table with every field.")
	f.String("config-dir", "", "Directory holding profiles.ini and cli.ini. Defaults to ~/.mcd.")
	f.String("endpoint", "", "API base URL. Defaults to the profile's, then "+defaultEndpoint+".")
	f.String("client-id", "", "OAuth client id. Defaults to MCD_DEFAULT_OAUTH_CLIENT_ID, then the profile's.")
	f.String("client-secret", "", "OAuth client secret. Defaults to MCD_DEFAULT_OAUTH_CLIENT_SECRET, then the profile's. Visible in the process list; --client-secret-prompt asks for it instead, and @<path> reads it from a file.")
	f.Bool("client-secret-prompt", false, "Read --client-secret from a hidden prompt instead of the command line.")
	f.String("instance", "", "Monte Carlo instance the OAuth client belongs to, for example us1. Defaults to MCD_DEFAULT_INSTANCE_ID, then the profile's.")
	f.String("api-id", "", "API token id. Defaults to MCD_DEFAULT_API_ID, then the profile's.")
	f.String("api-token", "", "API token secret. Defaults to MCD_DEFAULT_API_TOKEN, then the profile's. Visible in the process list; --api-token-prompt asks for it instead, and @<path> reads it from a file.")
	f.Bool("api-token-prompt", false, "Read --api-token from a hidden prompt instead of the command line.")
	f.BoolP("yes", "y", false, "Answer yes to every confirmation. Required without a terminal for any command that confirms.")

	// Marks an unknown flag, or a value its type rejects, as a usage error. Every command inherits it.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return withExitCode(exitUsage, err)
	})

	// Cobra answers a group given an unknown subcommand with the group's help. Print nothing
	// instead: executeArgs reports the unknown command.
	help := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if unknownSubcommand(c) == nil {
			help(c, args)
		}
	})
	return cmd
}

var rootCmd = newRootCmd()

// The root's command groups. Help lists them in this order, then any command in neither.
const (
	groupGettingStarted = "getting-started"
	groupResources      = "resources"
)

// utilityCommands stay out of the root's groups, under cobra's "Additional Commands".
var utilityCommands = map[string]bool{"help": true, "completion": true, "version": true}

// groupResourceCommands files every root command not already in a group, and not a utility,
// under Resources. The generated commands register themselves and carry no group, so this runs
// once every init has, before the command line is executed.
func groupResourceCommands(root *cobra.Command) {
	for _, c := range root.Commands() {
		if c.GroupID == "" && !utilityCommands[c.Name()] {
			c.GroupID = groupResources
		}
	}
}

// Execute runs the command and returns the process exit code. Ctrl-C or SIGTERM cancels the
// command's context, which ends a retry wait or a confirmation, and the exit code is then 130.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeArgs(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

// executeArgs is Execute with its inputs passed in, so a test can check the exit code.
func executeArgs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)
	if completionRequest(args) {
		return executeCompletion(ctx, stdout)
	}
	groupResourceCommands(rootCmd)
	executed, err := rootCmd.ExecuteContextC(ctx)
	err = commandLineErr(executed, err)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", binaryName, err)
	}
	return exitCode(ctx, err)
}

// commandLineErr marks err as a usage error when cobra rejected the command line before running
// cmd: an unknown command, the wrong arguments, a missing required flag, or a broken flag group.
// It repeats cobra's checks to find out: they return plain errors, and give the same answer each
// time.
func commandLineErr(cmd *cobra.Command, err error) error {
	if err == nil {
		return unknownSubcommand(cmd)
	}
	var coded *exitError
	if errors.As(err, &coded) {
		return err
	}
	unknownAtRoot := !cmd.HasParent() && !cmd.Runnable()
	if unknownAtRoot || cmd.ValidateArgs(cmd.Flags().Args()) != nil ||
		cmd.ValidateRequiredFlags() != nil || cmd.ValidateFlagGroups() != nil {
		return withExitCode(exitUsage, err)
	}
	return err
}

// unknownSubcommand is the usage error for a group command given an argument other than help,
// which can only be a subcommand it does not have. Cobra registers help only at the root.
func unknownSubcommand(cmd *cobra.Command) error {
	args := cmd.Flags().Args()
	if cmd.Runnable() || !cmd.HasSubCommands() || len(args) == 0 || args[0] == "help" {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2 // cobra's default, which it applies only at the root
	}
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(suggestions, "\n\t")
	}
	return withExitCode(exitUsage, errors.New(msg))
}

func init() {
	registerRootCompletions()
}
