// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileSetWritesOAuthAndBecomesActive(t *testing.T) {
	dir := t.TempDir()
	out, err := execute(t, "profile", "set", "dev", "--config-dir", dir,
		"--client-id", "cid", "--client-secret", "sec", "--instance", "us1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `Wrote profile "dev"`) || !strings.Contains(out, "now the active profile") {
		t.Fatalf("out:\n%s", out)
	}
	got, _ := os.ReadFile(profilesPath(dir))
	want := "[dev]\nmcd_oauth_client_id = cid\nmcd_oauth_client_secret = sec\nmcd_instance_id = us1\n"
	if string(got) != want {
		t.Fatalf("profiles.ini:\n%s", got)
	}
	if active, _ := activeProfile(dir); active != "dev" {
		t.Fatalf("active = %q", active)
	}
}

func TestProfileSetSwitchesMechanismAndKeepsForeignKeys(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)
	if err := setActiveProfile(dir, "staging"); err != nil {
		t.Fatal(err)
	}
	out, err := execute(t, "profile", "set", "default", "--config-dir", dir,
		"--client-id", "cid", "--client-secret", "sec", "--instance", "eu1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "now the active profile") {
		t.Fatal("an existing active profile was replaced")
	}
	got, _ := os.ReadFile(profilesPath(dir))
	text := string(got)
	for _, want := range []string{
		"# written by the Monte Carlo CLI\n",
		"mcd_agent_image_host = docker.io\n",
		"mcd_oauth_client_id = cid\n",
		"mcd_instance_id = eu1\n",
		"[staging]\nmcd_id: staging-id\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "legacy-id") || strings.Contains(text, "legacy-token") {
		t.Fatalf("the token pair survived an OAuth set:\n%s", text)
	}
}

func TestProfileSetRefusesMixedOrPartialCredentials(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]string{
		"not both":               {"--client-id", "c", "--client-secret", "s", "--instance", "us1", "--api-id", "i"},
		"go together":            {"--client-id", "c"},
		"--instance is required": {"--client-id", "c", "--client-secret", "s"},
		"or --api-id":            {},
	}
	for want, flags := range cases {
		_, err := execute(t, append([]string{"profile", "set", "p", "--config-dir", dir}, flags...)...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: err = %v, want %q", flags, err, want)
		}
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a refused set wrote the file")
	}
}

// testAPIToken has the length of a real API token secret.
var testAPIToken = strings.Repeat("t", apiTokenLength)

func TestProfileSetTrimsPastedValues(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("  "+testAPIToken+" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, "profile", "set", "dev", "--config-dir", dir,
		"--api-id", " i\t", "--api-token", "@"+tokenFile); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(profilesPath(dir))
	want := "[dev]\nmcd_id = i\nmcd_token = " + testAPIToken + "\n"
	if string(got) != want {
		t.Fatalf("profiles.ini:\n%s", got)
	}
}

func TestProfileSetRejectsMalformedValues(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]string{
		"not an instance id": {"--client-id", "c", "--client-secret", "s", "--instance", "us1.eu"},
		"is 5 characters":    {"--api-id", "i", "--api-token", "short"},
	}
	for want, flags := range cases {
		code, _, stderr := runExit(t, context.Background(), append([]string{"profile", "set", "p", "--config-dir", dir}, flags...)...)
		if code != exitUsage || !strings.Contains(stderr, want) {
			t.Errorf("%v: exit %d, stderr %q, want %q", flags, code, stderr, want)
		}
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a rejected set wrote the file")
	}
}

func TestProfileUseChoosesTheActiveProfileThatListReflects(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)

	t.Run("use rejects an unknown profile", func(t *testing.T) {
		_, err := execute(t, "profile", "use", "nope", "--config-dir", dir)
		if err == nil || !strings.Contains(err.Error(), "default, staging") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("use selects the active profile", func(t *testing.T) {
		if _, err := execute(t, "profile", "use", "staging", "--config-dir", dir); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("list marks it active in a table", func(t *testing.T) {
		out, err := execute(t, "profile", "list", "--config-dir", dir, "--output", "table")
		if err != nil {
			t.Fatal(err)
		}
		want := "NAME     AUTH   INSTANCE  ID          ACTIVE\ndefault  token            legacy-id   false\nstaging  token            staging-id  true\n"
		if out != want {
			t.Fatalf("got:\n%s\nwant:\n%s", out, want)
		}
	})

	t.Run("list omits secrets as json", func(t *testing.T) {
		out, err := execute(t, "profile", "list", "--config-dir", dir, "--output", "json")
		if err != nil || !strings.Contains(out, `"name": "staging"`) || strings.Contains(out, "legacy-token") {
			t.Fatalf("json: %v\n%s", err, out)
		}
	})
}

func TestProfileSetRejectsInputThatWouldCorruptProfilesINI(t *testing.T) {
	t.Run("newline in the name", func(t *testing.T) {
		dir := t.TempDir()
		_, err := execute(t, "profile", "set", "dev\nline", "--config-dir", dir,
			"--api-id", "i", "--api-token", "t")
		if err == nil || !strings.Contains(err.Error(), "profile name") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
			t.Fatal("a rejected set wrote the file")
		}
	})

	t.Run("empty name", func(t *testing.T) {
		dir := t.TempDir()
		_, err := execute(t, "profile", "set", "", "--config-dir", dir,
			"--api-id", "i", "--api-token", "t")
		if err == nil || !strings.Contains(err.Error(), "a profile name is required") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
			t.Fatal("a rejected set wrote the file")
		}
	})

	t.Run("interior newline in an @file value", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokenFile, []byte("first-line\nsecond-line\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := execute(t, "profile", "set", "dev", "--config-dir", dir,
			"--api-id", "i", "--api-token", "@"+tokenFile)
		if err == nil || !strings.Contains(err.Error(), "--api-token") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
			t.Fatal("a rejected set wrote the file")
		}
	})
}

func TestResolvedProfileNameFollowsPrecedence(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, "[default]\nmcd_id = i\n")
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	if got, err := resolvedProfileName(dir, f); err != nil || got != "default" {
		t.Fatalf("default section only: %q, %v", got, err)
	}

	t.Setenv("MCD_DEFAULT_PROFILE", "envprofile")
	if got, err := resolvedProfileName(dir, f); err != nil || got != "envprofile" {
		t.Fatalf("env set: %q, %v", got, err)
	}

	if err := setActiveProfile(dir, "cliprofile"); err != nil {
		t.Fatal(err)
	}
	if got, err := resolvedProfileName(dir, f); err != nil || got != "cliprofile" {
		t.Fatalf("cli.ini set: %q, %v", got, err)
	}
}

func TestResolvedProfileNameIsEmptyWithNoSignal(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolvedProfileName(dir, f); err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}
