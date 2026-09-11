package cmd

import (
	"github.com/spf13/cobra"
)

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

// registerRootCompletions runs from root.go's init, after the persistent flags exist. Go runs
// init functions in file-name order, so an init here would see no flags to register on.
func registerRootCompletions() {
	if err := rootCmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions([]cobra.Completion{"table", "wide", "json"}, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		panic(err)
	}
	if err := rootCmd.RegisterFlagCompletionFunc("profile", profileCompletion); err != nil {
		panic(err)
	}
}
