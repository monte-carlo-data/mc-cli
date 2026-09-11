package cmd

import (
	"os"
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

func TestProfileUseAndList(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)
	if _, err := execute(t, "profile", "use", "nope", "--config-dir", dir); err == nil || !strings.Contains(err.Error(), "default, staging") {
		t.Fatalf("err = %v", err)
	}
	if _, err := execute(t, "profile", "use", "staging", "--config-dir", dir); err != nil {
		t.Fatal(err)
	}
	out, err := execute(t, "profile", "list", "--config-dir", dir, "--output", "table")
	if err != nil {
		t.Fatal(err)
	}
	want := "NAME     AUTH   INSTANCE  ID          ACTIVE\ndefault  token            legacy-id   false\nstaging  token            staging-id  true\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	out, err = execute(t, "profile", "list", "--config-dir", dir, "--output", "json")
	if err != nil || !strings.Contains(out, `"name": "staging"`) || strings.Contains(out, "legacy-token") {
		t.Fatalf("json: %v\n%s", err, out)
	}
}
