package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func flagsCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "t", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("secret", "", "")
	cmd.Flags().Bool("secret-prompt", false, "")
	cmd.Flags().StringArray("headers", nil, "")
	cmd.Flags().Int32("count", 0, "")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestFlagStringReadsAFileForAnAtValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value.txt")
	if err := os.WriteFile(path, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := flagsCmd(t, "--name", "@"+path)
	got, err := flagString(cmd, "name")
	if err != nil || got != "from file" {
		t.Fatalf("got %q, %v", got, err)
	}
	plain := flagsCmd(t, "--name", "literal")
	if got, _ := flagString(plain, "name"); got != "literal" {
		t.Fatalf("got %q", got)
	}
	missing := flagsCmd(t, "--name", "@"+filepath.Join(t.TempDir(), "nope"))
	if _, err := flagString(missing, "name"); err == nil {
		t.Fatal("a missing file was not an error")
	}
}

func TestFlagStringMapTakesPairsOrAJSONFile(t *testing.T) {
	cmd := flagsCmd(t, "--headers", "a=1", "--headers", "b=x=y")
	got, err := flagStringMap(cmd, "headers")
	if err != nil || got["a"] != "1" || got["b"] != "x=y" || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}

	path := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(path, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile := flagsCmd(t, "--headers", "@"+path)
	got, err = flagStringMap(fromFile, "headers")
	if err != nil || got["k"] != "v" || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}

	bad := flagsCmd(t, "--headers", "novalue")
	if _, err := flagStringMap(bad, "headers"); err == nil || !strings.Contains(err.Error(), "key=value") {
		t.Fatalf("err = %v", err)
	}
}

func TestFlagSecretPromptNeedsATerminal(t *testing.T) {
	cmd := flagsCmd(t, "--secret-prompt")
	_, err := flagSecret(cmd, "secret")
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("err = %v", err)
	}
	both := flagsCmd(t, "--secret-prompt", "--secret", "x")
	if _, err := flagSecret(both, "secret"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("err = %v", err)
	}
	plain := flagsCmd(t, "--secret", "x")
	if got, err := flagSecret(plain, "secret"); err != nil || got != "x" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRequireAnyNamesTheFlags(t *testing.T) {
	cmd := flagsCmd(t)
	if err := requireAny(cmd, "name"); err == nil || err.Error() != "--name is required" {
		t.Fatalf("err = %v", err)
	}
	err := requireAny(cmd, "secret", "secret-prompt")
	if err == nil || err.Error() != "one of --secret or --secret-prompt is required" {
		t.Fatalf("err = %v", err)
	}
	given := flagsCmd(t, "--count", "3")
	if err := requireAny(given, "name", "count"); err != nil {
		t.Fatal(err)
	}
	if n, _ := flagInt(given, "count"); n != 3 {
		t.Fatalf("count = %d", n)
	}
}
