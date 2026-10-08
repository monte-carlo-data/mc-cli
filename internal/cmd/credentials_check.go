// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// validationTimeout bounds the call that checks new credentials; the API client has no timeout
// of its own.
var validationTimeout = 30 * time.Second

// validateCredentials asks the API whom c belongs to. Only c and --endpoint are used, never the
// environment or an existing profile, so the credentials are checked exactly as they will be
// written.
func validateCredentials(cmd *cobra.Command, c profileCredentials) (*sdk.CurrentUserOut, error) {
	endpoint, err := flagString(cmd, "endpoint")
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	opts := cliOptions(cmd)
	opts.Endpoint = endpoint
	opts.ClientID, opts.ClientSecret, opts.Instance = c.ClientID, c.ClientSecret, c.Instance
	opts.TokenID, opts.TokenSecret = c.APIID, c.APIToken
	api, err := sdk.NewClient(cmd.Context(), opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), validationTimeout)
	defer cancel()
	user, resp, err := api.UsersAPI.GetCurrentUser(ctx).Execute()
	if err != nil {
		return nil, validationErr(resp, err)
	}
	return user, nil
}

// validationErr says whether the credentials were rejected, or could not be checked at all.
// A rejected OAuth client fails at the token exchange, before any API response exists.
func validationErr(resp *http.Response, err error) error {
	err = apiErrWithHint(resp, err, "Check the credentials, and --endpoint if you passed one.")
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) && retrieve.Response != nil && retrieve.Response.StatusCode < http.StatusInternalServerError {
		err = withExitCode(exitAuth, err)
	}
	code := exitFailure
	var coded *exitError
	if errors.As(err, &coded) {
		code = coded.code
	}
	if code == exitAuth {
		return withExitCode(code, fmt.Errorf("the credentials were rejected, so the profile was not written: %w", err))
	}
	return withExitCode(code, fmt.Errorf("could not check the credentials, so the profile was not written: %w\nPass --no-validate to write it without checking.", err))
}

// profileWritten is the JSON result of writing a profile. User is null when the credentials
// were not validated.
type profileWritten struct {
	Profile   string              `json:"profile"`
	Path      string              `json:"path"`
	Active    bool                `json:"active"`
	Validated bool                `json:"validated"`
	User      *sdk.CurrentUserOut `json:"user"`
}

// reportProfileWritten tells the user who the credentials belong to, when they were validated,
// and where the profile was written.
func reportProfileWritten(cmd *cobra.Command, dir, name, path string, madeActive bool, user *sdk.CurrentUserOut) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if format == "json" {
		active, err := activeProfile(dir)
		if err != nil {
			return err
		}
		return writeJSON(out, profileWritten{
			Profile:   name,
			Path:      path,
			Active:    active == name,
			Validated: user != nil,
			User:      user,
		})
	}
	if user != nil {
		printIdentity(out, user)
	}
	fmt.Fprintf(out, "Wrote profile %q to %s\n", name, path)
	if madeActive {
		fmt.Fprintf(out, "Profile %q is now the active profile\n", name)
	}
	return nil
}

// printIdentity is the part of whoami that shows whether these are the credentials meant: who,
// in which account, and with what access.
func printIdentity(w io.Writer, u *sdk.CurrentUserOut) {
	who := u.GetEmail()
	if name := strings.TrimSpace(u.GetFirstName() + " " + u.GetLastName()); name != "" {
		who += " (" + name + ")"
	}
	account := u.GetAccountName()
	if account == "" {
		account = u.GetAccountId()
	}
	groups := strings.Join(u.GetAuthGroups(), ", ")
	if groups == "" {
		groups = "none"
	}
	fmt.Fprintf(w, "Validated: %s, account %s\n  identity: %s   groups: %s\n", who, account, u.GetIdentityType(), groups)
}
