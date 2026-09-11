package cmd

import (
	"bytes"
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
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, sub := range cmd.Commands() {
		resetFlags(sub)
	}
}

// execute runs the root command with args and returns what it wrote to stdout.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	isolateEnv(t)
	resetFlags(rootCmd)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}
