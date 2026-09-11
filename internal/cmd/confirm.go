package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// confirm asks `question` on the terminal and returns an error unless the answer is yes.
// --yes skips the question; without a terminal it is required.
func confirm(cmd *cobra.Command, question string) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("%s: pass --yes to run without a prompt", question)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s? [y/N] ", question)
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && answer == "" {
		return errors.New("aborted")
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return errors.New("aborted")
}
