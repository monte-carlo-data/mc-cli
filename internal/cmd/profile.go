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
		Use:     "profile",
		GroupID: groupGettingStarted,
		Short:   "Set up credentials non-interactively, and manage profiles",
		Long: `Set up credentials without prompts, for scripts and CI, and manage the profiles in
~/.mcd/profiles.ini, the credentials file every Monte Carlo tool shares. "` + binaryName + ` configure"
sets them up interactively instead.

A profile holds either OAuth client credentials with the instance they belong to, or an API
token. "profile set" writes one, "profile use" picks the profile commands run with when
--profile is not passed, and "profile list" shows them.`,
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

The credentials are checked with Monte Carlo first, and nothing is written unless they are
accepted; --no-validate skips the check. They are checked against --endpoint, else the endpoint
already stored in the profile, else the default. A non-default --endpoint is stored in the
profile for the commands that use it; passing the default removes a stored one. Only the flags
passed are written: the MCD_DEFAULT_* environment variables do not apply to this command.

As JSON, the result is one object: profile, path, active, validated, and user, the user the
credentials belong to, which is null when they were not checked.`,
		Example: `  # An OAuth client, with the secret read from a hidden prompt
  ` + binaryName + ` profile set prod --client-id <id> --client-secret-prompt --instance us1

  # An API token, with the secret read from a file
  ` + binaryName + ` profile set prod --api-id <id> --api-token @token.txt`,
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
			endpoint, err := endpointFlag(cmd)
			if err != nil {
				return err
			}
			var user *sdk.CurrentUserOut
			if skip, _ := flagBool(cmd, "no-validate"); !skip {
				at, err := checkEndpoint(dir, name, endpoint)
				if err != nil {
					return err
				}
				if user, err = validateCredentials(cmd, creds, at); err != nil {
					return err
				}
			}
			path, madeActive, err := writeProfile(dir, name, creds, endpoint)
			if err != nil {
				return err
			}
			return reportProfileWritten(cmd, dir, name, path, madeActive, user)
		},
	}
	// These shadow the root's credential flags, whose help describes reading credentials, so
	// that this command's help lists them as the values it writes.
	f := cmd.Flags()
	f.String("client-id", "", "OAuth client id to write.")
	f.String("client-secret", "", "OAuth client secret to write. Visible in the process list; --client-secret-prompt asks for it instead, and @<path> reads it from a file.")
	f.Bool("client-secret-prompt", false, "Read --client-secret from a hidden prompt instead of the command line.")
	f.String("instance", "", "Instance the OAuth client belongs to, for example us1. Required with an OAuth client.")
	f.String("api-id", "", "API token id to write.")
	f.String("api-token", "", "API token secret to write. Visible in the process list; --api-token-prompt asks for it instead, and @<path> reads it from a file.")
	f.Bool("api-token-prompt", false, "Read --api-token from a hidden prompt instead of the command line.")
	f.Bool("no-validate", false, "Write the profile without first checking the credentials with Monte Carlo.")
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
	if c.Instance != "" {
		if err := checkInstance(c.Instance); err != nil {
			return usageError("--instance %v", err)
		}
	}
	if token {
		if err := checkAPIToken(c.APIToken); err != nil {
			return usageError("--api-token %v", err)
		}
	}
	return nil
}

// checkInstance rejects a value that cannot be an instance id.
func checkInstance(v string) error {
	if !instancePattern.MatchString(v) {
		return fmt.Errorf("%q is not an instance id; it is letters, digits and hyphens, for example us1", v)
	}
	return nil
}

// checkAPIToken rejects an API token secret of the wrong length, which is what a partial paste
// leaves.
func checkAPIToken(v string) error {
	if len(v) != apiTokenLength {
		return fmt.Errorf("is %d characters, but an API token is %d; check it was copied whole", len(v), apiTokenLength)
	}
	return nil
}

// writeProfile writes c to the profile name, removing the other mechanism's keys, and makes it
// the active profile when none is active yet. A non-default endpoint is stored, in the GraphQL
// form the other tools read; the default removes a stored one, and none leaves it as it is. It
// returns the file written and whether the profile became active.
func writeProfile(dir, name string, c profileCredentials, endpoint string) (path string, madeActive bool, err error) {
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
	switch endpoint {
	case "":
	case defaultEndpoint:
		f.unset(name, keyEndpoint)
	default:
		f.set(name, keyEndpoint, endpoint+"/graphql")
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
