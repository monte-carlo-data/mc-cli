package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newProfileCmd())
}

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage the profiles in profiles.ini",
		Long: `Manage the profiles in ~/.mcd/profiles.ini, the credentials file every Monte Carlo tool shares.

A profile holds either OAuth client credentials with the instance they belong to, or an API
token. "profile use" picks the profile commands run with when --profile is not passed.`,
	}
	cmd.AddCommand(newProfileSetCmd(), newProfileUseCmd(), newProfileListCmd())
	return cmd
}

func newProfileSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Write a profile's credentials",
		Long: `Write a profile's credentials to profiles.ini, creating the profile when it is new.

Pass --client-id, --client-secret and --instance for OAuth client credentials, or --api-id and
--api-token for an API token. Writing one kind removes the other from the profile. Keys this
command does not know are left as they are. The first profile written becomes the active one.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir, err := configDir(cmd)
			if err != nil {
				return err
			}
			clientID, err := flagString(cmd, "client-id")
			if err != nil {
				return err
			}
			clientSecret, err := flagSecret(cmd, "client-secret")
			if err != nil {
				return err
			}
			instance, err := flagString(cmd, "instance")
			if err != nil {
				return err
			}
			apiID, err := flagString(cmd, "api-id")
			if err != nil {
				return err
			}
			apiToken, err := flagSecret(cmd, "api-token")
			if err != nil {
				return err
			}

			oauth := clientID != "" || clientSecret != ""
			token := apiID != "" || apiToken != ""
			switch {
			case oauth && token:
				return errors.New("pass OAuth client credentials or an API token, not both")
			case oauth && (clientID == "" || clientSecret == ""):
				return errors.New("--client-id and --client-secret go together")
			case oauth && instance == "":
				return errors.New("--instance is required with OAuth client credentials")
			case token && (apiID == "" || apiToken == ""):
				return errors.New("--api-id and --api-token go together")
			case !oauth && !token:
				return errors.New("pass --client-id, --client-secret and --instance, or --api-id and --api-token")
			}

			f, err := loadINI(profilesPath(dir))
			if err != nil {
				return err
			}
			if oauth {
				f.set(name, keyClientID, clientID)
				f.set(name, keySecret, clientSecret)
				f.set(name, keyInstance, instance)
				f.unset(name, keyID)
				f.unset(name, keyToken)
			} else {
				f.set(name, keyID, apiID)
				f.set(name, keyToken, apiToken)
				f.unset(name, keyClientID)
				f.unset(name, keySecret)
				if instance != "" {
					f.set(name, keyInstance, instance)
				}
			}
			if err := f.save(credentialsMode); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote profile %q to %s\n", name, f.path)

			active, err := activeProfile(dir)
			if err != nil {
				return err
			}
			if active == "" {
				if err := setActiveProfile(dir, name); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Profile %q is now the active profile\n", name)
			}
			return nil
		},
	}
	cmd.Flags().Bool("client-secret-prompt", false, "Read --client-secret from a hidden prompt instead of the command line.")
	cmd.Flags().Bool("api-token-prompt", false, "Read --api-token from a hidden prompt instead of the command line.")
	return cmd
}

func newProfileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Choose the profile commands run with",
		Long: `Choose the profile commands run with when --profile is not passed and MCD_DEFAULT_PROFILE
is not set. The choice is stored in cli.ini beside profiles.ini.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir, err := configDir(cmd)
			if err != nil {
				return err
			}
			f, err := loadINI(profilesPath(dir))
			if err != nil {
				return err
			}
			if !f.hasSection(name) {
				return profileNotFound(f, name)
			}
			if err := setActiveProfile(dir, name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Profile %q is now the active profile\n", name)
			return nil
		},
	}
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the profiles and which one is active",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := configDir(cmd)
			if err != nil {
				return err
			}
			f, err := loadINI(profilesPath(dir))
			if err != nil {
				return err
			}
			active, err := activeProfile(dir)
			if err != nil {
				return err
			}
			rows := []profileSummary{}
			for _, name := range f.sections() {
				rows = append(rows, summarize(f, name, active))
			}
			return renderList(cmd, rows, []string{"name", "auth", "instance", "id", "active"})
		},
	}
}
