// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// completionRequest reports whether args invoke cobra's hidden completion entry point.
func completionRequest(args []string) bool {
	return len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd)
}

// executeCompletion runs cobra's completion and rewrites its answer so a command's own flags
// come before the global ones, then asks the shell to keep that order. Cobra lists inherited
// flags first and lets the shell sort, which buries `--name` among the credential flags.
func executeCompletion(ctx context.Context, out io.Writer) int {
	// Only stdout is captured: cobra prints a debug line about the directive on stderr, which
	// the shell ignores and which would otherwise land after the directive here.
	prevOut := rootCmd.OutOrStdout()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	err := rootCmd.ExecuteContext(ctx)
	rootCmd.SetOut(prevOut)
	if err != nil {
		fmt.Fprint(out, buf.String())
		return exitCode(ctx, err)
	}
	fmt.Fprint(out, reorderCompletions(buf.String(), globalFlagNames()))
	return exitOK
}

// globalFlagNames is every spelling of the root command's persistent flags, plus help.
func globalFlagNames() map[string]bool {
	names := map[string]bool{"--help": true, "-h": true}
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		names["--"+f.Name] = true
		if f.Shorthand != "" {
			names["-"+f.Shorthand] = true
		}
	})
	return names
}

// reorderCompletions moves the global flags after the others and sets the keep-order bit on
// the trailing directive line. Anything that is not a flag list passes through unchanged.
func reorderCompletions(output string, globals map[string]bool) string {
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], ":") {
		return output
	}
	directive, err := strconv.Atoi(lines[len(lines)-1][1:])
	if err != nil {
		return output
	}
	var own, global []string
	for _, line := range lines[:len(lines)-1] {
		name, _, _ := strings.Cut(line, "\t")
		if globals[strings.TrimSuffix(name, "=")] {
			global = append(global, line)
		} else {
			own = append(own, line)
		}
	}
	if len(own) == 0 || len(global) == 0 {
		return output
	}
	ordered := append(own, global...)
	ordered = append(ordered, ":"+strconv.Itoa(directive|int(cobra.ShellCompDirectiveKeepOrder)))
	return strings.Join(ordered, "\n") + "\n"
}

// enumCompletion completes a flag with an enum's values, as the SDK exports them.
func enumCompletion[T ~string](values []T) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		out := make([]cobra.Completion, len(values))
		for i, v := range values {
			out[i] = string(v)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// profileCompletion completes --profile with the sections of profiles.ini.
func profileCompletion(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	dir, err := configDir(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return f.sections(), cobra.ShellCompDirectiveNoFileComp
}

// registerRootCompletions runs from root.go's init. The persistent flags exist by then, since
// rootCmd's var initializer registers them before any init runs. Go runs
// init functions in file-name order, so an init here would see no flags to register on.
func registerRootCompletions() {
	if err := rootCmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions([]cobra.Completion{"table", "wide", "json"}, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		panic(err)
	}
	if err := rootCmd.RegisterFlagCompletionFunc("profile", profileCompletion); err != nil {
		panic(err)
	}
}
