// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
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
		return usageError("a profile name is required")
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
	return usageError("%s holds %s; %s", flag, what, invariant)
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
			creds, err := credentialsFromFlags(cmd)
			if err != nil {
				return err
			}
			var user *sdk.CurrentUserOut
			if skip, _ := flagBool(cmd, "no-validate"); !skip {
				if user, err = validateCredentials(cmd, creds); err != nil {
					return err
				}
			}
			path, madeActive, err := writeProfile(dir, name, creds)
			if err != nil {
				return err
			}
			return reportProfileWritten(cmd, dir, name, path, madeActive, user)
		},
	}
	cmd.Flags().Bool("no-validate", false, "Write the profile without first checking the credentials with Monte Carlo.")
	return cmd
}

// profileCredentials is the one credential mechanism a profile holds: an OAuth client with the
// instance it belongs to, or an API token, which may carry an instance too.
type profileCredentials struct {
	ClientID     string
	ClientSecret string
	Instance     string
	APIID        string
	APIToken     string
}

func (c profileCredentials) oauth() bool { return c.ClientID != "" }

// apiTokenLength is the length of every API token secret Monte Carlo issues.
const apiTokenLength = 56

// instancePattern is the form of an instance id, such as us1.
var instancePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,63}$`)

// credentialsFromFlags reads the credential flags into one complete mechanism. Values are
// trimmed, since a pasted value often carries a stray space or newline.
func credentialsFromFlags(cmd *cobra.Command) (profileCredentials, error) {
	var c profileCredentials
	var err error
	if c.ClientID, err = flagString(cmd, "client-id"); err != nil {
		return c, err
	}
	if c.ClientSecret, err = flagSecret(cmd, "client-secret"); err != nil {
		return c, err
	}
	if c.Instance, err = flagString(cmd, "instance"); err != nil {
		return c, err
	}
	if c.APIID, err = flagString(cmd, "api-id"); err != nil {
		return c, err
	}
	if c.APIToken, err = flagSecret(cmd, "api-token"); err != nil {
		return c, err
	}
	return c, c.check()
}

// check trims c and rejects it unless it is exactly one complete, well-formed mechanism.
func (c *profileCredentials) check() error {
	for _, v := range []struct {
		flag  string
		value *string
	}{
		{"--client-id", &c.ClientID},
		{"--client-secret", &c.ClientSecret},
		{"--instance", &c.Instance},
		{"--api-id", &c.APIID},
		{"--api-token", &c.APIToken},
	} {
		*v.value = strings.TrimSpace(*v.value)
		if err := validateProfileValue(v.flag, *v.value); err != nil {
			return err
		}
	}

	oauth := c.ClientID != "" || c.ClientSecret != ""
	token := c.APIID != "" || c.APIToken != ""
	switch {
	case oauth && token:
		return usageError("pass OAuth client credentials or an API token, not both")
	case oauth && (c.ClientID == "" || c.ClientSecret == ""):
		return usageError("--client-id and --client-secret go together")
	case oauth && c.Instance == "":
		return usageError("--instance is required with OAuth client credentials")
	case token && (c.APIID == "" || c.APIToken == ""):
		return usageError("--api-id and --api-token go together")
	case !oauth && !token:
		return usageError("pass --client-id, --client-secret and --instance, or --api-id and --api-token")
	}
	if c.Instance != "" && !instancePattern.MatchString(c.Instance) {
		return usageError("--instance %q is not an instance id; it is letters, digits and hyphens, for example us1", c.Instance)
	}
	if token && len(c.APIToken) != apiTokenLength {
		return usageError("--api-token is %d characters, but an API token is %d; check it was copied whole", len(c.APIToken), apiTokenLength)
	}
	return nil
}

// writeProfile writes c to the profile name, removing the other mechanism's keys, and makes it
// the active profile when none is active yet. It returns the file written and whether the
// profile became active.
func writeProfile(dir, name string, c profileCredentials) (path string, madeActive bool, err error) {
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		return "", false, err
	}
	if c.oauth() {
		f.set(name, keyClientID, c.ClientID)
		f.set(name, keySecret, c.ClientSecret)
		f.set(name, keyInstance, c.Instance)
		f.unset(name, keyID)
		f.unset(name, keyToken)
	} else {
		f.set(name, keyID, c.APIID)
		f.set(name, keyToken, c.APIToken)
		f.unset(name, keyClientID)
		f.unset(name, keySecret)
		if c.Instance != "" {
			f.set(name, keyInstance, c.Instance)
		}
	}
	if err := f.save(credentialsMode); err != nil {
		return "", false, err
	}

	active, err := activeProfile(dir)
	if err != nil {
		return f.path, false, err
	}
	if active != "" {
		return f.path, false, nil
	}
	if err := setActiveProfile(dir, name); err != nil {
		return f.path, false, err
	}
	return f.path, true, nil
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
