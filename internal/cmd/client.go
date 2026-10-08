// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
// --profile nor MCD_DEFAULT_PROFILE names one and no credential mechanism is otherwise supplied,
// and the default endpoint.
func clientOptions(cmd *cobra.Command) (sdk.Options, error) {
	dir, err := configDir(cmd)
	if err != nil {
		return sdk.Options{}, err
	}
	str := func(name string) string {
		v, _ := cmd.Flags().GetString(name)
		return v
	}
	clientSecret, err := flagSecret(cmd, "client-secret")
	if err != nil {
		return sdk.Options{}, err
	}
	apiToken, err := flagSecret(cmd, "api-token")
	if err != nil {
		return sdk.Options{}, err
	}
	endpoint, err := endpointFlag(cmd)
	if err != nil {
		return sdk.Options{}, err
	}
	opts := cliOptions(cmd)
	opts.Endpoint = endpoint
	opts.ClientID = str("client-id")
	opts.ClientSecret = clientSecret
	opts.Instance = str("instance")
	opts.TokenID = str("api-id")
	opts.TokenSecret = apiToken
	opts.Profile = str("profile")
	opts.ConfigDir = dir

	// The active profile set with "profile use" is a CLI-only fallback, layered in only when
	// nothing else names a profile or already supplies a complete credential mechanism. Once
	// adopted, a failure to resolve it needs to say where the profile came from: it did not
	// come from a flag or the environment, so a bare SDK error would leave the caller looking
	// in the wrong place.
	adoptedActive := false
	if opts.Profile == "" && os.Getenv("MCD_DEFAULT_PROFILE") == "" && !hasCredentialMechanism(opts) {
		active, err := activeProfile(dir)
		if err != nil {
			return sdk.Options{}, err
		}
		if active != "" {
			opts.Profile = active
			adoptedActive = true
		}
	}

	resolved, err := opts.Resolve()
	if err != nil {
		if adoptedActive {
			return sdk.Options{}, fmt.Errorf("active profile %q (from %s): %w; run %q", opts.Profile, cliPath(dir), err, binaryName+" profile use")
		}
		return sdk.Options{}, err
	}
	if resolved.Endpoint == "" {
		resolved.Endpoint = defaultEndpoint
	}
	return resolved, nil
}

// endpointFlag is --endpoint as an API base URL, or empty when it was not passed.
func endpointFlag(cmd *cobra.Command) (string, error) {
	v, err := flagString(cmd, "endpoint")
	if err != nil {
		return "", err
	}
	endpoint, err := normalizeEndpoint(v)
	if err != nil {
		return "", usageError("--endpoint %q is not an https URL", endpoint)
	}
	return endpoint, nil
}

// storedEndpoint is the endpoint stored in the profile name, or empty when it has none. It reads
// that one key and nothing else.
func storedEndpoint(dir, name string) (string, error) {
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		return "", err
	}
	value, ok := f.get(name, keyEndpoint)
	if !ok {
		return "", nil
	}
	endpoint, err := normalizeEndpoint(value)
	if err != nil {
		return "", fmt.Errorf("profile %q: %s %q is not an https URL; pass --endpoint", name, keyEndpoint, value)
	}
	return endpoint, nil
}

// normalizeEndpoint returns v as an API base URL, empty for empty input. A trailing /graphql,
// which a URL copied from another Monte Carlo tool carries, is dropped. Only https is accepted,
// and http to a loopback host.
func normalizeEndpoint(v string) (string, error) {
	v = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(v), "/"), "/graphql")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	loopback := err == nil && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback)) {
		return v, errors.New("not an https URL")
	}
	return v, nil
}

// cliOptions is the Options every client the CLI builds starts from: how it identifies itself.
func cliOptions(cmd *cobra.Command) sdk.Options {
	v, _, _ := resolvedVersion()
	return sdk.Options{
		UserAgent: binaryName + "/" + v,
		// The gateway drops User-Agent, so these are what identify the CLI to usage telemetry.
		TelemetryReason:  "cli",
		TelemetryService: "mc-cli",
		TelemetryCommand: telemetryCommand(cmd),
	}
}

// telemetryCommand names the command without the binary, e.g. "connections add snowflake".
// CommandPath joins command names only, so positional arguments never reach it.
func telemetryCommand(cmd *cobra.Command) string {
	return strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), binaryName))
}

// hasCredentialMechanism reports whether opts, or the environment variables the SDK falls back
// to, already supply a complete credential mechanism: an OAuth client id and secret, or an API
// token id and secret. It mirrors sdk.Options.Resolve's own notion of "complete" so the CLI's
// active-profile fallback only ever fills a gap Resolve would otherwise fill from a profile.
func hasCredentialMechanism(opts sdk.Options) bool {
	if opts.ClientID != "" && opts.ClientSecret != "" {
		return true
	}
	if opts.TokenID != "" && opts.TokenSecret != "" {
		return true
	}
	if os.Getenv("MCD_DEFAULT_OAUTH_CLIENT_ID") != "" && os.Getenv("MCD_DEFAULT_OAUTH_CLIENT_SECRET") != "" {
		return true
	}
	if os.Getenv("MCD_DEFAULT_API_ID") != "" && os.Getenv("MCD_DEFAULT_API_TOKEN") != "" {
		return true
	}
	return false
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
