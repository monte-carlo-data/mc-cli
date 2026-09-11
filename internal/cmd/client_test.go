package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const precedenceProfiles = `[default]
mcd_id = default-id
mcd_token = default-token
mcd_api_endpoint = https://dev.example.com/graphql

[dev]
mcd_id = dev-id
mcd_token = dev-token

[env]
mcd_id = env-id
mcd_token = env-token

[active]
mcd_id = active-id
mcd_token = active-token
`

func resolvedFor(t *testing.T, dir string, args ...string) (string, string) {
	t.Helper()
	resetFlags(rootCmd)
	if err := rootCmd.ParseFlags(append([]string{"--config-dir", dir}, args...)); err != nil {
		t.Fatal(err)
	}
	opts, err := clientOptions(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	return opts.TokenID, opts.Endpoint
}

func TestProfilePrecedence(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, precedenceProfiles)
	if err := setActiveProfile(dir, "active"); err != nil {
		t.Fatal(err)
	}

	if id, _ := resolvedFor(t, dir, "--profile", "dev"); id != "dev-id" {
		t.Fatalf("--profile: %s", id)
	}
	t.Setenv("MCD_DEFAULT_PROFILE", "env")
	if id, _ := resolvedFor(t, dir); id != "env-id" {
		t.Fatalf("MCD_DEFAULT_PROFILE: %s", id)
	}
	t.Setenv("MCD_DEFAULT_PROFILE", "")
	if id, _ := resolvedFor(t, dir); id != "active-id" {
		t.Fatalf("active profile: %s", id)
	}
	if err := setActiveProfile(dir, ""); err != nil {
		t.Fatal(err)
	}
	if id, _ := resolvedFor(t, dir); id != "default-id" {
		t.Fatalf("default: %s", id)
	}
}

func TestFlagsBeatTheProfile(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, precedenceProfiles)
	if id, _ := resolvedFor(t, dir, "--api-id", "flag-id", "--api-token", "flag-token"); id != "flag-id" {
		t.Fatalf("flags: %s", id)
	}
}

func TestEndpointComesFromTheProfileElseTheDefault(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, precedenceProfiles)
	if _, endpoint := resolvedFor(t, dir); endpoint != "https://dev.example.com" {
		t.Fatalf("profile endpoint: %s", endpoint)
	}
	if _, endpoint := resolvedFor(t, dir, "--profile", "dev"); endpoint != defaultEndpoint {
		t.Fatalf("default endpoint: %s", endpoint)
	}
	if _, endpoint := resolvedFor(t, dir, "--endpoint", "https://x.example.com"); endpoint != "https://x.example.com" {
		t.Fatalf("flag endpoint: %s", endpoint)
	}
}

func TestAnActiveProfileThatNoLongerExistsIsAnError(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, precedenceProfiles)
	if err := setActiveProfile(dir, "gone"); err != nil {
		t.Fatal(err)
	}
	resetFlags(rootCmd)
	if err := rootCmd.ParseFlags([]string{"--config-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := clientOptions(rootCmd); err == nil {
		t.Fatal("a missing active profile resolved silently")
	}
}

// TestFullFlagCredentialsIgnoreAStaleActiveProfile is F4: a complete credential mechanism passed
// on the command line must never trigger a lookup of the "profile use" active profile, so a
// stale or deleted active profile cannot break a caller who already supplied everything needed.
func TestFullFlagCredentialsIgnoreAStaleActiveProfile(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, precedenceProfiles)
	if err := setActiveProfile(dir, "gone"); err != nil {
		t.Fatal(err)
	}

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user_id":"u1","email":"a@example.com","identity_type":"user","account_id":"a1","account_frozen":false}`)
	}))
	defer srv.Close()

	resetFlags(rootCmd)
	if err := rootCmd.ParseFlags([]string{
		"--config-dir", dir,
		"--endpoint", srv.URL,
		"--api-id", "flag-id",
		"--api-token", "flag-token",
	}); err != nil {
		t.Fatal(err)
	}
	api, ctx, err := apiClient(rootCmd)
	if err != nil {
		t.Fatalf("a stale active profile should not have been consulted: %v", err)
	}
	if _, _, err := api.UsersAPI.GetCurrentUser(ctx).Execute(); err != nil {
		t.Fatalf("request did not reach the server: %v", err)
	}
	if gotPath != "/api/v2/users/me" {
		t.Fatalf("unexpected request path: %s", gotPath)
	}
}

// TestAnAdoptedActiveProfileNamesWhereItCameFrom is F4: when no flag, environment variable, or
// profile name resolves a mechanism on its own, and the CLI falls back to the "profile use"
// active profile, a resolution failure has to say the failure came from that profile and from
// cli.ini specifically, not from a flag or MCD_DEFAULT_PROFILE the caller might go looking at.
func TestAnAdoptedActiveProfileNamesWhereItCameFrom(t *testing.T) {
	isolateEnv(t)
	dir := writeProfiles(t, precedenceProfiles)
	if err := setActiveProfile(dir, "gone"); err != nil {
		t.Fatal(err)
	}
	resetFlags(rootCmd)
	if err := rootCmd.ParseFlags([]string{"--config-dir", dir}); err != nil {
		t.Fatal(err)
	}
	_, err := clientOptions(rootCmd)
	if err == nil {
		t.Fatal("a missing active profile resolved silently")
	}
	if !strings.Contains(err.Error(), "cli.ini") {
		t.Fatalf("error does not name cli.ini: %v", err)
	}
	if !strings.Contains(err.Error(), "profile use") {
		t.Fatalf("error does not hint at %q: %v", "profile use", err)
	}
}
