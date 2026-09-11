package cmd

import (
	"context"
	"os"
	"path/filepath"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

// apiClient builds the SDK client from the command's flags and returns the context to call it
// with.
func apiClient(cmd *cobra.Command) (*sdk.APIClient, context.Context, error) {
	opts, err := clientOptions(cmd)
	if err != nil {
		return nil, nil, err
	}
	api, err := sdk.NewClient(cmd.Context(), opts)
	if err != nil {
		return nil, nil, err
	}
	return api, cmd.Context(), nil
}

// clientOptions maps the persistent flags onto the SDK's Options and resolves them.
//
// The SDK owns the precedence between flags, environment and profile. The CLI adds two things
// the SDK does not know: the profile chosen with "profile use", consulted only when neither
// --profile nor MCD_DEFAULT_PROFILE names one, and the default endpoint.
func clientOptions(cmd *cobra.Command) (sdk.Options, error) {
	dir, err := configDir(cmd)
	if err != nil {
		return sdk.Options{}, err
	}
	str := func(name string) string {
		v, _ := cmd.Flags().GetString(name)
		return v
	}
	opts := sdk.Options{
		Endpoint:     str("endpoint"),
		ClientID:     str("client-id"),
		ClientSecret: str("client-secret"),
		Instance:     str("instance"),
		TokenID:      str("api-id"),
		TokenSecret:  str("api-token"),
		Profile:      str("profile"),
		ConfigDir:    dir,
		UserAgent:    binaryName + "/" + version,
	}
	if opts.Profile == "" && os.Getenv("MCD_DEFAULT_PROFILE") == "" {
		active, err := activeProfile(dir)
		if err != nil {
			return sdk.Options{}, err
		}
		opts.Profile = active
	}
	resolved, err := opts.Resolve()
	if err != nil {
		return sdk.Options{}, err
	}
	if resolved.Endpoint == "" {
		resolved.Endpoint = defaultEndpoint
	}
	return resolved, nil
}

// configDir is --config-dir, else ~/.mcd.
func configDir(cmd *cobra.Command) (string, error) {
	if dir, _ := cmd.Flags().GetString("config-dir"); dir != "" {
		return expandHome(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mcd"), nil
}
