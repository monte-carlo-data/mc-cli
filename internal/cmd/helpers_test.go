// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The SDK reads these; a developer's own credentials must not leak into a test.
var environmentKeys = []string{
	"MCD_DEFAULT_PROFILE",
	"MCD_DEFAULT_API_ID",
	"MCD_DEFAULT_API_TOKEN",
	"MCD_DEFAULT_OAUTH_CLIENT_ID",
	"MCD_DEFAULT_OAUTH_CLIENT_SECRET",
	"MCD_DEFAULT_INSTANCE_ID",
}

func isolateEnv(t *testing.T) {
	t.Helper()
	for _, key := range environmentKeys {
		t.Setenv(key, "")
	}
}

// resetFlags returns every flag in the tree to its default, since pflag keeps values between
// executions of the same command objects.
func resetFlags(cmd *cobra.Command) {
	reset := func(f *pflag.Flag) {
		// A slice flag's Set appends once it has been set, and its default renders as "[]".
		if s, ok := f.Value.(pflag.SliceValue); ok {
			_ = s.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, sub := range cmd.Commands() {
		resetFlags(sub)
	}
}

// setContexts gives every command in the tree ctx. Cobra hands a subcommand a context only
// when it has none, so one a previous run left, possibly cancelled, would otherwise stay.
func setContexts(cmd *cobra.Command, ctx context.Context) {
	cmd.SetContext(ctx)
	for _, sub := range cmd.Commands() {
		setContexts(sub, ctx)
	}
}

// executeStreams runs the root command with args and returns what it wrote to stdout and to
// stderr separately.
func executeStreams(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	isolateEnv(t)
	resetFlags(rootCmd)
	setContexts(rootCmd, context.Background())
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// runExit runs args the way the binary does, under ctx, and returns the exit code and what was
// written to stdout and to stderr, including the error line.
func runExit(t *testing.T, ctx context.Context, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	isolateEnv(t)
	resetFlags(rootCmd)
	setContexts(rootCmd, ctx)
	var outBuf, errBuf bytes.Buffer
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	code = executeArgs(ctx, args, &outBuf, &errBuf)
	return code, outBuf.String(), errBuf.String()
}

// execute runs the root command with args and returns what it wrote to stdout and stderr
// merged into one string.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, err := executeStreams(t, args...)
	return stdout + stderr, err
}
