// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "whoami",
		Short: "Show the user and account the credentials belong to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			api, ctx, err := apiClient(cmd)
			if err != nil {
				return err
			}
			out, resp, err := api.UsersAPI.GetCurrentUser(ctx).Execute()
			if err != nil {
				return apiErr(resp, err)
			}
			// account_frozen is deliberately off the field list; JSON and --output wide still show it.
			return render(cmd, out,
				"email", "first_name", "last_name", "identity_type", "auth_groups",
				"account_name", "account_id", "user_id")
		},
	})
}
