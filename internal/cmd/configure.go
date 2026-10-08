// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newConfigureCmd())
}

func newConfigureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "configure",
		GroupID: groupGettingStarted,
		Short:   "Set up credentials interactively",
		Long: `Set up credentials by answering a few prompts, then check them with Monte Carlo and write
them to the profile --profile names, "default" when it is not passed.

Choose an OAuth client, with its client id, secret and instance, or an API token, with its id
and secret. Secrets are read with echo off. Nothing is written unless Monte Carlo accepts the
credentials; --no-validate writes them without checking.

The credential flags are not read here; every value is asked for. --endpoint only changes where
the credentials are checked, and is not written to the profile.

configure needs a terminal. To write a profile from a script, use "` + binaryName + ` profile set".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, err := flagString(cmd, "profile")
			if err != nil {
				return err
			}
			if name == "" {
				name = "default"
			}
			if err := validateProfileValue("profile name", name); err != nil {
				return err
			}
			if !stdinIsTerminal(cmd) {
				return usageError("configure needs a terminal to prompt on; without one, write the profile with\n"+
					"  %[1]s profile set %[2]s --client-id <id> --client-secret @<path> --instance <instance>\n"+
					"or\n"+
					"  %[1]s profile set %[2]s --api-id <id> --api-token @<path>", binaryName, name)
			}
			dir, err := configDir(cmd)
			if err != nil {
				return err
			}

			p := &prompter{cmd: cmd, out: cmd.ErrOrStderr()}
			creds, err := p.credentials()
			if err != nil {
				return err
			}
			if err := creds.check(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if skip, _ := flagBool(cmd, "no-validate"); !skip {
				var user *sdk.CurrentUserOut
				if user, err = validateCredentials(cmd, creds); err != nil {
					return err
				}
				printIdentity(out, user)
			}

			path, madeActive, err := writeProfile(dir, name, creds)
			if err != nil {
				return err
			}
			if !madeActive {
				if madeActive, err = p.offerActive(dir, name); err != nil {
					return err
				}
			}
			printWritten(out, name, path, madeActive)
			return nil
		},
	}
	cmd.Flags().Bool("no-validate", false, "Write the profile without first checking the credentials with Monte Carlo.")
	return cmd
}

// prompter asks configure's questions on the terminal, with the questions on stderr.
type prompter struct {
	cmd *cobra.Command
	out io.Writer
}

// credentials asks which mechanism to set up, then for its values.
func (p *prompter) credentials() (profileCredentials, error) {
	var c profileCredentials
	fmt.Fprint(p.out, "How do you want to authenticate?\n"+
		"  1) OAuth client: a client id, its secret and the instance it belongs to\n"+
		"  2) API token: a token id and its secret\n")
	choice, err := p.ask("Choice (1 or 2): ", false, func(v string) error {
		if v != "1" && v != "2" {
			return errors.New("enter 1 or 2")
		}
		return nil
	})
	if err != nil {
		return c, err
	}
	if choice == "1" {
		if c.ClientID, err = p.ask("Client id: ", false, nil); err != nil {
			return c, err
		}
		if c.ClientSecret, err = p.ask("Client secret: ", true, nil); err != nil {
			return c, err
		}
		c.Instance, err = p.ask("Instance (for example us1): ", false, checkInstance)
		return c, err
	}
	if c.APIID, err = p.ask("API token id: ", false, nil); err != nil {
		return c, err
	}
	c.APIToken, err = p.ask("API token secret: ", true, func(v string) error {
		if err := checkAPIToken(v); err != nil {
			return fmt.Errorf("the secret %w", err)
		}
		return nil
	})
	return c, err
}

// offerActive asks to make name the active profile when another one is.
func (p *prompter) offerActive(dir, name string) (bool, error) {
	active, err := activeProfile(dir)
	if err != nil || active == name {
		return false, err
	}
	answer, err := p.read(fmt.Sprintf("Make %q the active profile instead of %q? [y/N] ", name, active), false)
	if err != nil || !confirmed(answer) {
		return false, err
	}
	return true, setActiveProfile(dir, name)
}

// ask repeats the question until the answer is not empty and check, when given, accepts it.
func (p *prompter) ask(question string, hidden bool, check func(string) error) (string, error) {
	for {
		answer, err := p.read(question, hidden)
		if err != nil {
			return "", err
		}
		answer = strings.TrimSpace(answer)
		switch {
		case answer == "":
			fmt.Fprintln(p.out, "A value is required.")
		case check != nil && check(answer) != nil:
			fmt.Fprintf(p.out, "%v\n", check(answer))
		default:
			return answer, nil
		}
	}
}

// read asks question and returns the line answered, read with echo off when hidden. Ctrl-C ends
// a visible read; a hidden one waits for its line, since abandoning it would leave echo off.
func (p *prompter) read(question string, hidden bool) (string, error) {
	fmt.Fprint(p.out, question)
	in := p.cmd.InOrStdin().(*os.File)
	if hidden {
		secret, err := readPassword(in)
		fmt.Fprintln(p.out)
		return string(secret), err
	}
	ctx := p.cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	type result struct {
		line string
		err  error
	}
	answered := make(chan result, 1)
	go func() {
		line, err := readLine(in)
		answered <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return "", ctx.Err()
	case r := <-answered:
		return r.line, r.err
	}
}

// readLine reads up to and including a newline, one byte at a time so that nothing past the
// line is consumed: the next read may be a hidden one, straight from the terminal. Input that
// ends before the setup does is an error.
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			b.WriteByte(buf[0])
			if buf[0] == '\n' {
				return b.String(), nil
			}
		}
		if err == io.EOF {
			if b.Len() > 0 {
				return b.String(), nil
			}
			return "", errors.New("the input ended before setup finished")
		}
		if err != nil {
			return "", err
		}
	}
}
