package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set at build time with -ldflags "-X github.com/monte-carlo-data/mc-cli/internal/cmd.version=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
					"version": version, "commit": commit, "date": date,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s, %s)\n", binaryName, version, commit, date)
			return nil
		},
	})
}
