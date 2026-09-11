package cmd

import (
	"fmt"
	"runtime/debug"

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
			v, c, d := version, commit, date
			if v == "dev" {
				info, ok := debug.ReadBuildInfo()
				v, c, d = versionFromBuildInfo(info, ok)
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
					"version": v, "commit": c, "date": d,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s, %s)\n", binaryName, v, c, d)
			return nil
		},
	})
}

// versionFromBuildInfo derives version, commit and date from a binary's build info, for a build
// that did not go through the release ldflags, for example "go install". ok is ReadBuildInfo's
// own result; it is false only for a binary built without module support, in which case the "dev"
// defaults stand as they are. The commit is trimmed to a short 12-character form, matching how
// the release process names one.
func versionFromBuildInfo(info *debug.BuildInfo, ok bool) (v, c, d string) {
	v, c, d = version, commit, date
	if !ok || info == nil {
		return v, c, d
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			c = s.Value
			if len(c) > 12 {
				c = c[:12]
			}
		case "vcs.time":
			d = s.Value
		}
	}
	return v, c, d
}
