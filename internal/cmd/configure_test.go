// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// runConfigure runs configure with input typed at a terminal, hidden answers included.
func runConfigure(t *testing.T, input string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return runConfigureIn(t, context.Background(), input, false, args...)
}

// runConfigureIn is runConfigure with a context; keepOpen leaves the input open after input.
func runConfigureIn(t *testing.T, ctx context.Context, input string, keepOpen bool, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	prevTerminal, prevPassword := isTerminal, readPassword
	isTerminal = func(*os.File) bool { return true }
	readPassword = func(f *os.File) ([]byte, error) {
		line, err := readLine(f)
		return []byte(strings.TrimSuffix(line, "\n")), err
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if keepOpen {
		t.Cleanup(func() { w.Close() })
	} else {
		w.Close()
	}
	rootCmd.SetIn(r)
	t.Cleanup(func() {
		isTerminal, readPassword = prevTerminal, prevPassword
		rootCmd.SetIn(nil)
		r.Close()
	})
	return runExit(t, ctx, append([]string{"configure"}, args...)...)
}

func TestConfigureSetsUpAnAPIToken(t *testing.T) {
	srv, _ := credentialServer(t, http.StatusOK, http.StatusOK)
	dir := t.TempDir()
	code, stdout, stderr := runConfigure(t, "2\ni\n"+testAPIToken+"\n", "--config-dir", dir, "--endpoint", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	want := "Validated: ada@example.com (Ada Lovelace), account Analytical Engines\n" +
		"  identity: user   groups: admin, viewer\n" +
		`Wrote profile "default" to ` + profilesPath(dir) + "\n" +
		`Profile "default" is now the active profile` + "\n"
	if stdout != want {
		t.Fatalf("got:\n%s\nwant:\n%s", stdout, want)
	}
	if strings.Contains(stderr, testAPIToken) {
		t.Fatal("the secret was echoed")
	}
	got, _ := os.ReadFile(profilesPath(dir))
	if string(got) != "[default]\nmcd_id = i\nmcd_token = "+testAPIToken+"\nmcd_api_endpoint = "+srv.URL+"/graphql\n" {
		t.Fatalf("profiles.ini:\n%s", got)
	}
}

func TestConfigureSetsUpAnOAuthClientInTheNamedProfile(t *testing.T) {
	srv, _ := credentialServer(t, http.StatusOK, http.StatusOK)
	dir := t.TempDir()
	code, _, stderr := runConfigure(t, "1\ncid\nsec\nus1\n", "--config-dir", dir, "--endpoint", srv.URL, "--profile", "dev")
	if code != exitOK {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	got, _ := os.ReadFile(profilesPath(dir))
	if string(got) != "[dev]\nmcd_oauth_client_id = cid\nmcd_oauth_client_secret = sec\nmcd_instance_id = us1\nmcd_api_endpoint = "+srv.URL+"/graphql\n" {
		t.Fatalf("profiles.ini:\n%s", got)
	}
}

func TestConfigureAsksAgainUntilAnAnswerIsUsable(t *testing.T) {
	dir := t.TempDir()
	input := "3\n\n2\ni\nshort\n" + testAPIToken + "\n"
	code, _, stderr := runConfigure(t, input, "--config-dir", dir, "--no-validate")
	if code != exitOK {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"enter 1 or 2", "A value is required.", "the secret is 5 characters"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	code, _, stderr = runConfigure(t, "1\ncid\nsec\nus1.eu\nus1\n", "--config-dir", dir, "--no-validate")
	if code != exitOK || !strings.Contains(stderr, "is not an instance id") {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
}

func TestConfigureWritesNothingWhenTheCredentialsAreRejected(t *testing.T) {
	srv, _ := credentialServer(t, http.StatusOK, http.StatusUnauthorized)
	dir := t.TempDir()
	code, stdout, stderr := runConfigure(t, "2\ni\n"+testAPIToken+"\n", "--config-dir", dir, "--endpoint", srv.URL)
	if code != exitAuth || !strings.Contains(stderr, "were rejected") || stdout != "" {
		t.Fatalf("exit %d; stdout %q; stderr:\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("rejected credentials were written")
	}
}

func TestConfigureOffersToReplaceTheActiveProfile(t *testing.T) {
	for answer, want := range map[string]string{"y": "dev", "n": "staging"} {
		t.Run(answer, func(t *testing.T) {
			dir := writeProfiles(t, legacyProfiles)
			if err := setActiveProfile(dir, "staging"); err != nil {
				t.Fatal(err)
			}
			code, _, stderr := runConfigure(t, "2\ni\n"+testAPIToken+"\n"+answer+"\n",
				"--config-dir", dir, "--profile", "dev", "--no-validate")
			if code != exitOK || !strings.Contains(stderr, `Make "dev" the active profile instead of "staging"?`) {
				t.Fatalf("exit %d; stderr:\n%s", code, stderr)
			}
			if active, _ := activeProfile(dir); active != want {
				t.Fatalf("active = %q, want %q", active, want)
			}
		})
	}
}

func TestConfigureWithoutATerminalPointsAtProfileSet(t *testing.T) {
	prev := isTerminal
	isTerminal = func(*os.File) bool { return false }
	t.Cleanup(func() { isTerminal = prev })

	dir := t.TempDir()
	code, _, stderr := runExit(t, context.Background(), "configure", "--config-dir", dir, "--profile", "dev")
	if code != exitUsage || !strings.Contains(stderr, "profile set dev --api-id <id> --api-token @<path>") {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("the file was written")
	}
}

func TestConfigureFailsWhenInputEndsEarly(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runConfigure(t, "2\n", "--config-dir", dir)
	if code != exitFailure || !strings.Contains(stderr, "input ended before setup finished") {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("the file was written")
	}
}

func TestConfigureSucceedsWhenInputEndsAtTheActiveProfileOffer(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)
	if err := setActiveProfile(dir, "staging"); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runConfigure(t, "2\ni\n"+testAPIToken+"\n",
		"--config-dir", dir, "--profile", "dev", "--no-validate")
	if code != exitOK || !strings.Contains(stdout, `Wrote profile "dev"`) {
		t.Fatalf("exit %d; stdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if active, _ := activeProfile(dir); active != "staging" {
		t.Fatalf("active = %q, want staging", active)
	}
}

func TestConfigureRejectsCredentialFlags(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runConfigure(t, "", "--config-dir", dir, "--api-id", "i")
	if code != exitUsage || !strings.Contains(stderr, "profile set") {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("the file was written")
	}
}

func TestConfigureStopsWhenCancelledAtAPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	code, _, stderr := runConfigureIn(t, ctx, "", true, "--config-dir", dir)
	if code != exitInterrupted {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("the file was written")
	}
}
