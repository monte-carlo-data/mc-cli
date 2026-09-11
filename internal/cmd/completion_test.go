package cmd

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

// fixtureCmd adds a throwaway command to rootCmd with a plain flag, --name, and an
// enum-completed one, --type, removed once the test ends.
func fixtureCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "fixture-create", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("name", "", "Display name.")
	cmd.Flags().String("type", "", "Kind of thing.")
	if err := cmd.RegisterFlagCompletionFunc("type", enumCompletion([]string{"AWS", "GCP"})); err != nil {
		t.Fatal(err)
	}
	rootCmd.AddCommand(cmd)
	t.Cleanup(func() { rootCmd.RemoveCommand(cmd) })
	return cmd
}

func TestEnumCompletionListsTheSDKValues(t *testing.T) {
	values, directive := enumCompletion(sdk.AllowedDeploymentTypeEnumValues)(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive %v", directive)
	}
	joined := strings.Join(values, ",")
	if !strings.Contains(joined, "COLLECTION_AGENT") || !strings.Contains(joined, "COLLECTION_DATA_STORE") {
		t.Fatalf("values %v", values)
	}
}

func TestProfileCompletionListsTheSections(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)
	resetFlags(rootCmd)
	if err := rootCmd.ParseFlags([]string{"--config-dir", dir}); err != nil {
		t.Fatal(err)
	}
	values, directive := profileCompletion(rootCmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp || strings.Join(values, ",") != "default,staging" {
		t.Fatalf("values %v, directive %v", values, directive)
	}
}

func TestReorderCompletionsPutsOwnFlagsFirstAndKeepsOrder(t *testing.T) {
	globals := map[string]bool{"--profile": true, "--output": true, "--help": true}
	in := "--output\tformat\n--help\thelp\n--name\tdisplay name\n--profile\tprofile\n:4\n"
	want := "--name\tdisplay name\n--output\tformat\n--help\thelp\n--profile\tprofile\n:36\n"
	if got := reorderCompletions(in, globals); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// Enum values, and a list of globals alone, pass through untouched.
	for _, unchanged := range []string{"AWS\nGCP\n:4\n", "--profile\tp\n--output\to\n:4\n", ":0\n"} {
		if got := reorderCompletions(unchanged, globals); got != unchanged {
			t.Fatalf("changed %q into %q", unchanged, got)
		}
	}
}

func TestExecuteCompletionListsTheCommandsFlagsFirst(t *testing.T) {
	isolateEnv(t)
	fixtureCmd(t)
	resetFlags(rootCmd)
	rootCmd.SetArgs([]string{cobra.ShellCompRequestCmd, "fixture-create", "--type", "AWS", "--"})
	var out bytes.Buffer
	if code := executeCompletion(context.Background(), &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	wantDirective := ":" + strconv.Itoa(int(cobra.ShellCompDirectiveNoFileComp|cobra.ShellCompDirectiveKeepOrder))
	if !strings.HasPrefix(lines[0], "--name\t") || lines[len(lines)-1] != wantDirective {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestCompletionCommandOffersEnumValues(t *testing.T) {
	fixtureCmd(t)
	out, err := execute(t, cobra.ShellCompRequestCmd, "fixture-create", "--type", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AWS", "GCP"} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("missing %s in:\n%s", want, out)
		}
	}
}

// TestSmokeCompletionOffersEnumValuesOnAGeneratedCommand is a labelled smoke test over the
// real generated command tree; the mechanism itself is covered by the fixture command above.
func TestSmokeCompletionOffersEnumValuesOnAGeneratedCommand(t *testing.T) {
	out, err := execute(t, cobra.ShellCompRequestCmd, "deployments", "create", "--runtime-platform", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AWS", "AZURE", "GCP", "GENERIC"} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("missing %s in:\n%s", want, out)
		}
	}
	out, err = execute(t, cobra.ShellCompRequestCmd, "whoami", "--output", "")
	if err != nil || !strings.Contains(out, "wide\n") {
		t.Fatalf("output completion: %v\n%s", err, out)
	}
}
