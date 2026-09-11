package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func confirmCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "t", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().Bool("yes", false, "")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestConfirmSkipsTheQuestionWithYes(t *testing.T) {
	if err := confirm(confirmCmd(t, "--yes"), "Delete deployment x"); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmRefusesWithoutATerminalOrYes(t *testing.T) {
	// go test runs without a terminal on stdin, so this is the non-interactive path.
	err := confirm(confirmCmd(t), "Delete deployment x")
	if err == nil || err.Error() != "Delete deployment x: pass --yes to run without a prompt" {
		t.Fatalf("err = %v", err)
	}
}

func TestGeneratedDeleteRefusesWithoutYesOffATerminal(t *testing.T) {
	_, err := execute(t, "deployments", "delete", "some-id", "--config-dir", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "Delete deployment some-id: pass --yes") {
		t.Fatalf("err = %v", err)
	}
}
