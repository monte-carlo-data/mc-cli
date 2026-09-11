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
			return render(cmd, out)
		},
	})
}
