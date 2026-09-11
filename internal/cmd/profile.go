package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

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

// validateProfileValue rejects a profile name or value that would corrupt profiles.ini: one
// spanning multiple lines, or, for the profile name, one holding a section delimiter. flag
// names the command-line flag the value came from, or "profile name" for the name itself.
func validateProfileValue(flag, value string) error {
	if flag == "profile name" && value == "" {
		return errors.New("a profile name is required")
	}
	bad := "\r\n"
	invariant := "a profile value is one line"
	if flag == "profile name" {
		bad += sectionOpen + sectionClose
		invariant = "a profile name is one line and holds no " + sectionOpen + " or " + sectionClose
	}
	idx := strings.IndexAny(value, bad)
	if idx < 0 {
		return nil
	}
	what := "a newline"
	if value[idx] == sectionOpen[0] || value[idx] == sectionClose[0] {
		what = fmt.Sprintf("%q", string(value[idx]))
	}
	return fmt.Errorf("%s holds %s; %s", flag, what, invariant)
}

func newProfileSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Write a profile's credentials",
		Long: `Write a profile's credentials to profiles.ini, creating the profile when it is new.

Pass --client-id, --client-secret and --instance for OAuth client credentials, or --api-id and
--api-token for an API token. Writing one kind removes the other from the profile. Keys this
command does not know are left as they are. The first profile written becomes the active one.

These flags are the values written here. The environment defaults listed under the global flags
do not apply to this command.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validateProfileValue("profile name", name); err != nil {
				return err
			}
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
			if err := validateProfileValue("--client-id", clientID); err != nil {
				return err
			}
			if err := validateProfileValue("--client-secret", clientSecret); err != nil {
				return err
			}
			if err := validateProfileValue("--instance", instance); err != nil {
				return err
			}
			if err := validateProfileValue("--api-id", apiID); err != nil {
				return err
			}
			if err := validateProfileValue("--api-token", apiToken); err != nil {
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

// profileNotFound reports that name is not a section in f, listing the profiles that do exist.
func profileNotFound(f *iniFile, name string) error {
	names := f.sections()
	if len(names) == 0 {
		return fmt.Errorf("profile %q not found; %s has no profiles yet", name, f.path)
	}
	return fmt.Errorf("profile %q not found in %s; profiles: %s", name, f.path, strings.Join(names, ", "))
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
			active, err := resolvedProfileName(dir, f)
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

// profileSummary is one row of "profile list".
type profileSummary struct {
	Name     string `json:"name"`
	Auth     string `json:"auth"`
	Instance string `json:"instance"`
	ID       string `json:"id"`
	Active   bool   `json:"active"`
}

func summarize(f *iniFile, name, active string) profileSummary {
	s := profileSummary{Name: name, Active: name == active}
	s.Instance, _ = f.get(name, keyInstance)
	if id, ok := f.get(name, keyClientID); ok && id != "" {
		s.Auth, s.ID = "oauth", id
	} else if id, ok := f.get(name, keyID); ok && id != "" {
		s.Auth, s.ID = "token", id
	} else {
		s.Auth = "none"
	}
	return s
}

// resolvedProfileName is the profile "profile list" marks active: the profile chosen with
// "profile use", else MCD_DEFAULT_PROFILE, else "default" when that section exists in f. This
// mirrors clientOptions' own precedence, but purely for display; it does not decide which
// credentials a command actually runs with.
func resolvedProfileName(dir string, f *iniFile) (string, error) {
	active, err := activeProfile(dir)
	if err != nil {
		return "", err
	}
	if active != "" {
		return active, nil
	}
	if env := os.Getenv("MCD_DEFAULT_PROFILE"); env != "" {
		return env, nil
	}
	if f.hasSection("default") {
		return "default", nil
	}
	return "", nil
}
