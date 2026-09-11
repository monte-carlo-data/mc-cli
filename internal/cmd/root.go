// Package cmd is the montecarlo command: the root, the hand-written commands, and the helpers
// the generated *_cmd.gen.go files call.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// binaryName is the command's name in help text. The main package lives in cmd/<binaryName>/.
const binaryName = "montecarlo"

// defaultEndpoint is the API base URL when neither a flag nor the profile names one.
const defaultEndpoint = "https://api.getmontecarlo.com"

var rootCmd = &cobra.Command{
	Use:   binaryName,
	Short: "Monte Carlo from the command line",
	Long: binaryName + ` talks to the Monte Carlo REST API.

Credentials come from flags, then the MCD_DEFAULT_* environment variables, then a profile in
~/.mcd/profiles.ini, the file every Monte Carlo tool shares. Run "` + binaryName + ` profile set"
to write one.

Every string flag accepts @<path> to read its value from a file.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the command and returns the process exit code. Ctrl-C cancels the command's
// context, which ends a retry wait.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	return 0
}

func init() {
	f := rootCmd.PersistentFlags()
	f.String("profile", "", `Profile in profiles.ini. Defaults to MCD_DEFAULT_PROFILE, then the profile chosen with "profile use", then "default".`)
	f.StringP("output", "o", "", "Output format: table, wide or json. Defaults to table on a terminal and json otherwise; wide is the table with every field.")
	f.String("config-dir", "", "Directory holding profiles.ini. Defaults to ~/.mcd.")
	f.String("endpoint", "", "API base URL. Defaults to the profile's, then "+defaultEndpoint+".")
	f.String("client-id", "", "OAuth client id. Defaults to MCD_DEFAULT_OAUTH_CLIENT_ID, then the profile's.")
	f.String("client-secret", "", "OAuth client secret. Defaults to MCD_DEFAULT_OAUTH_CLIENT_SECRET, then the profile's.")
	f.String("instance", "", "Monte Carlo instance the OAuth client belongs to, for example us1. Defaults to MCD_DEFAULT_INSTANCE_ID, then the profile's.")
	f.String("api-id", "", "API token id. Defaults to MCD_DEFAULT_API_ID, then the profile's.")
	f.String("api-token", "", "API token secret. Defaults to MCD_DEFAULT_API_TOKEN, then the profile's.")
	registerRootCompletions()
}
