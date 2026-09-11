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
	cmd.Flags().Bool("verbose", false, "")
	cmd.Flags().StringSlice("tags", nil, "")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestFlagStringIsLiteralWhileSecretAndMapExpandFiles(t *testing.T) {
	textPath := filepath.Join(t.TempDir(), "value.txt")
	if err := os.WriteFile(textPath, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(jsonPath, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	plain := flagsCmd(t, "--name", "literal")
	if got, err := flagString(plain, "name"); err != nil || got != "literal" {
		t.Fatalf("flagString got %q, %v", got, err)
	}

	nameCmd := flagsCmd(t, "--name", "@"+textPath)
	if got, err := flagString(nameCmd, "name"); err != nil || got != "@"+textPath {
		t.Fatalf("flagString did not keep @%s literal: got %q, %v", textPath, got, err)
	}

	secretCmd := flagsCmd(t, "--secret", "@"+textPath)
	if got, err := flagSecret(secretCmd, "secret"); err != nil || got != "from file" {
		t.Fatalf("flagSecret got %q, %v", got, err)
	}
	missingSecret := flagsCmd(t, "--secret", "@"+filepath.Join(t.TempDir(), "nope"))
	if _, err := flagSecret(missingSecret, "secret"); err == nil {
		t.Fatal("a missing file was not an error for flagSecret")
	}

	mapCmd := flagsCmd(t, "--headers", "@"+jsonPath)
	got, err := flagStringMap(mapCmd, "headers")
	if err != nil || got["k"] != "v" || len(got) != 1 {
		t.Fatalf("flagStringMap got %v, %v", got, err)
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

func TestFlagStringMapErrorsOnAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	cmd := flagsCmd(t, "--headers", "@"+path)
	_, err := flagStringMap(cmd, "headers")
	if err == nil || !strings.Contains(err.Error(), "--headers") || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}

func TestFlagStringMapErrorsOnANonObjectJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list.json")
	if err := os.WriteFile(path, []byte(`[1,2]`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := flagsCmd(t, "--headers", "@"+path)
	_, err := flagStringMap(cmd, "headers")
	if err == nil || !strings.Contains(err.Error(), "JSON object of strings") {
		t.Fatalf("err = %v", err)
	}
}

func TestFlagStringMapRejectsMixingAFileWithPairs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(path, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := flagsCmd(t, "--headers", "@"+path, "--headers", "b=1")
	_, err := flagStringMap(cmd, "headers")
	if err == nil || !strings.Contains(err.Error(), "not key=value") {
		t.Fatalf("err = %v", err)
	}
}

func TestFlagSecretPromptRequiresATerminal(t *testing.T) {
	t.Run("prompt without a terminal errors", func(t *testing.T) {
		cmd := flagsCmd(t, "--secret-prompt")
		_, err := flagSecret(cmd, "secret")
		if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("prompt and value together error", func(t *testing.T) {
		both := flagsCmd(t, "--secret-prompt", "--secret", "x")
		if _, err := flagSecret(both, "secret"); err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("value alone passes through", func(t *testing.T) {
		plain := flagsCmd(t, "--secret", "x")
		if got, err := flagSecret(plain, "secret"); err != nil || got != "x" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}

func TestFlagSecretPromptFalseIsIgnored(t *testing.T) {
	t.Run("value used when prompt explicitly false", func(t *testing.T) {
		cmd := flagsCmd(t, "--secret-prompt=false", "--secret", "value")
		got, err := flagSecret(cmd, "secret")
		if err != nil || got != "value" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("missing value reports the missing flag, not a terminal error", func(t *testing.T) {
		cmd := flagsCmd(t, "--secret-prompt=false")
		got, err := flagSecret(cmd, "secret")
		if err != nil || got != "" {
			t.Fatalf("flagSecret got %q, %v", got, err)
		}
		if err := requireAny(cmd, "secret"); err == nil || err.Error() != "--secret is required" {
			t.Fatalf("requireAny err = %v", err)
		}
	})
}

func TestReadSecretUsesTheSeamWhenStdinIsATerminal(t *testing.T) {
	prev := isTerminal
	isTerminal = func(f *os.File) bool { return true }
	t.Cleanup(func() { isTerminal = prev })

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })

	cmd := flagsCmd(t, "--secret-prompt")
	cmd.SetIn(f)

	_, err = flagSecret(cmd, "secret")
	if err == nil {
		t.Fatal("expected an error reading a password from a non-tty file")
	}
	if strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("seam was not exercised: %v", err)
	}
}

func TestFlagBoolReturnsThePassedValue(t *testing.T) {
	cmd := flagsCmd(t, "--verbose")
	if got, err := flagBool(cmd, "verbose"); err != nil || !got {
		t.Fatalf("got %v, %v", got, err)
	}
	cmd = flagsCmd(t, "--verbose=false")
	if got, err := flagBool(cmd, "verbose"); err != nil || got {
		t.Fatalf("got %v, %v", got, err)
	}
	cmd = flagsCmd(t)
	if got, err := flagBool(cmd, "verbose"); err != nil || got {
		t.Fatalf("default was not false: got %v, %v", got, err)
	}
}

func TestFlagStringSliceParsesRepeatedFlagsAndCommaSeparatedValues(t *testing.T) {
	repeated := flagsCmd(t, "--tags", "a", "--tags", "b")
	got, err := flagStringSlice(repeated, "tags")
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v, %v", got, err)
	}

	comma := flagsCmd(t, "--tags", "a,b")
	got, err = flagStringSlice(comma, "tags")
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestExpandHomeExpandsOnlyALeadingTilde(t *testing.T) {
	dir := t.TempDir()
	// os.UserHomeDir reads HOME on Unix and USERPROFILE on Windows.
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	if got, err := expandHome("~"); err != nil || got != dir {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := expandHome("~/x"); err != nil || got != filepath.Join(dir, "x") {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := expandHome("~notme/x"); err != nil || got != "~notme/x" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := expandHome("a/~/b"); err != nil || got != "a/~/b" {
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
