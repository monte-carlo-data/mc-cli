// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

// credentialServer fakes the token exchange and the current-user call. A non-200 status makes
// that endpoint fail; calls counts the current-user requests.
func credentialServer(t *testing.T, tokenStatus, userStatus int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tokenStatus)
			if tokenStatus == http.StatusOK {
				_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600}`))
			} else {
				_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			}
		case "/api/v2/users/me":
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(userStatus)
			switch userStatus {
			case http.StatusOK:
				_, _ = w.Write([]byte(currentUserBody))
			case http.StatusServiceUnavailable:
				_, _ = w.Write([]byte(transientProblem))
			default:
				_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
			}
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func tokenSetArgs(dir, endpoint string, extra ...string) []string {
	return append([]string{"profile", "set", "dev", "--config-dir", dir, "--endpoint", endpoint,
		"--api-id", "i", "--api-token", testAPIToken}, extra...)
}

func oauthSetArgs(dir, endpoint string, extra ...string) []string {
	return append([]string{"profile", "set", "dev", "--config-dir", dir, "--endpoint", endpoint,
		"--client-id", "cid", "--client-secret", "sec", "--instance", "us1"}, extra...)
}

func TestProfileSetShowsWhomValidatedCredentialsBelongTo(t *testing.T) {
	srv, _ := credentialServer(t, http.StatusOK, http.StatusOK)
	dir := t.TempDir()
	out, err := execute(t, tokenSetArgs(dir, srv.URL, "--output", "table")...)
	if err != nil {
		t.Fatal(err)
	}
	want := "Validated: ada@example.com (Ada Lovelace), account Analytical Engines\n" +
		"  identity: user   groups: admin, viewer\n" +
		`Wrote profile "dev" to ` + profilesPath(dir) + "\n" +
		`Profile "dev" is now the active profile` + "\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestProfileSetValidatesOAuthAndReportsJSON(t *testing.T) {
	srv, _ := credentialServer(t, http.StatusOK, http.StatusOK)
	dir := t.TempDir()
	out, err := execute(t, oauthSetArgs(dir, srv.URL)...)
	if err != nil {
		t.Fatal(err)
	}
	var got profileWrittenJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Profile != "dev" || got.Path != profilesPath(dir) || !got.Active || !got.Validated ||
		got.User == nil || got.User["email"] != "ada@example.com" {
		t.Fatalf("got %+v", got)
	}
}

// profileWrittenJSON decodes profileWritten with the user left generic.
type profileWrittenJSON struct {
	Profile   string         `json:"profile"`
	Path      string         `json:"path"`
	Active    bool           `json:"active"`
	Validated bool           `json:"validated"`
	User      map[string]any `json:"user"`
}

func TestProfileSetNoValidateSkipsTheCheck(t *testing.T) {
	srv, calls := credentialServer(t, http.StatusOK, http.StatusUnauthorized)
	dir := t.TempDir()
	out, err := execute(t, tokenSetArgs(dir, srv.URL, "--no-validate")...)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("the API was called %d times", calls.Load())
	}
	if !strings.Contains(out, `"validated": false`) || !strings.Contains(out, `"user": null`) {
		t.Fatalf("out:\n%s", out)
	}
	if _, err := os.Stat(profilesPath(dir)); err != nil {
		t.Fatal("the profile was not written")
	}
}

func TestProfileSetWritesNothingWhenValidationFails(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	cases := []struct {
		name        string
		tokenStatus int
		userStatus  int
		oauth       bool
		endpoint    string
		code        int
		want        string
	}{
		{"rejected token", http.StatusOK, http.StatusUnauthorized, false, "", exitAuth, "were rejected"},
		{"forbidden token", http.StatusOK, http.StatusForbidden, false, "", exitAuth, "were rejected"},
		{"rejected OAuth client", http.StatusUnauthorized, http.StatusOK, true, "", exitAuth, "were rejected"},
		{"invalid OAuth request", http.StatusBadRequest, http.StatusOK, true, "", exitAuth, "were rejected"},
		{"forbidden OAuth client", http.StatusForbidden, http.StatusOK, true, "", exitAuth, "were rejected"},
		{"token endpoint not found", http.StatusNotFound, http.StatusOK, true, "", exitFailure, "--no-validate"},
		{"token endpoint rate limited", http.StatusTooManyRequests, http.StatusOK, true, "", exitTransient, "--no-validate"},
		{"token endpoint unavailable", http.StatusServiceUnavailable, http.StatusOK, true, "", exitTransient, "--no-validate"},
		{"token exchange down", http.StatusBadGateway, http.StatusOK, true, "", exitFailure, "--no-validate"},
		{"API unavailable", http.StatusOK, http.StatusServiceUnavailable, false, "", exitTransient, "--no-validate"},
		{"network failure", http.StatusOK, http.StatusOK, false, closed.URL, exitFailure, "--no-validate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			if endpoint == "" {
				srv, _ := credentialServer(t, tc.tokenStatus, tc.userStatus)
				endpoint = srv.URL
			}
			dir := t.TempDir()
			args := tokenSetArgs(dir, endpoint)
			if tc.oauth {
				args = oauthSetArgs(dir, endpoint)
			}
			code, _, stderr := runExit(t, context.Background(), args...)
			if code != tc.code || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, want %d; stderr:\n%s", code, tc.code, stderr)
			}
			// No profile is involved yet, so the usual hint pointing at one would mislead.
			if strings.Contains(stderr, "profile they come from") {
				t.Fatalf("stderr points at a profile:\n%s", stderr)
			}
			if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
				t.Fatal("a failed validation wrote the file")
			}
		})
	}
}

// TestValidationOptionsIgnoreEnvironmentAndProfile checks that neither the environment nor a
// profile lends validation an endpoint or credentials.
func TestValidationOptionsIgnoreEnvironmentAndProfile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".mcd"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := "[default]\nmcd_api_endpoint = http://elsewhere.example/graphql\n" +
		"mcd_id = env-id\nmcd_token = env-token\n"
	if err := os.WriteFile(filepath.Join(home, ".mcd", "profiles.ini"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("MCD_DEFAULT_PROFILE", "default")
	t.Setenv("MCD_DEFAULT_API_ID", "env-id")
	t.Setenv("MCD_DEFAULT_API_TOKEN", "env-token")

	cases := []struct{ name, endpoint, want string }{
		{"default endpoint", "", defaultEndpoint},
		{"endpoint flag", "https://custom.example", "https://custom.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "t"}
			cmd.Flags().String("endpoint", "", "")
			if tc.endpoint != "" {
				if err := cmd.Flags().Set("endpoint", tc.endpoint); err != nil {
					t.Fatal(err)
				}
			}
			opts, err := validationOptions(cmd, profileCredentials{APIID: "i", APIToken: testAPIToken})
			if err != nil {
				t.Fatal(err)
			}
			got, err := opts.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if got.Endpoint != tc.want || got.TokenID != "i" || got.TokenSecret != testAPIToken {
				t.Fatalf("got endpoint %q, token id %q, secret %q", got.Endpoint, got.TokenID, got.TokenSecret)
			}
		})
	}
}

func TestProfileSetTimesOutHangingValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/users/me" {
			http.NotFound(w, r)
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	orig := validationTimeout
	validationTimeout = 50 * time.Millisecond
	t.Cleanup(func() { validationTimeout = orig })

	dir := t.TempDir()
	code, _, stderr := runExit(t, context.Background(), tokenSetArgs(dir, srv.URL)...)
	if code != exitFailure || !strings.Contains(stderr, "--no-validate") {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitFailure, stderr)
	}
	if _, err := os.Stat(profilesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a timed-out validation wrote the file")
	}
}

func TestProfileSetReportsInactiveProfile(t *testing.T) {
	srv, _ := credentialServer(t, http.StatusOK, http.StatusOK)
	dir := writeProfiles(t, legacyProfiles)
	if err := setActiveProfile(dir, "staging"); err != nil {
		t.Fatal(err)
	}
	out, err := execute(t, tokenSetArgs(dir, srv.URL)...)
	if err != nil {
		t.Fatal(err)
	}
	var got profileWrittenJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Active || !got.Validated {
		t.Fatalf("got %+v", got)
	}
}

func TestPrintIdentityFallsBackWhenFieldsAreMissing(t *testing.T) {
	var u sdk.CurrentUserOut
	body := `{"user_id":"u","email":"e@example.com","identity_type":"service","account_id":"acct-1","account_frozen":false}`
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printIdentity(&buf, &u)
	want := "Validated: e@example.com, account acct-1\n  identity: service   groups: none\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}
