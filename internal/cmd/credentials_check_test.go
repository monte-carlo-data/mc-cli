// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
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

// TestProfileSetValidatesOnlyWhatItWrites checks that neither the environment nor the profile
// being replaced lends validation an endpoint or credentials.
func TestProfileSetValidatesOnlyWhatItWrites(t *testing.T) {
	srv, calls := credentialServer(t, http.StatusOK, http.StatusOK)
	dir := writeProfiles(t, "[dev]\nmcd_api_endpoint = "+srv.URL+"/graphql\n")
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	code, _, stderr := runExit(t, context.Background(), tokenSetArgs(dir, closed.URL)...)
	if code != exitFailure || calls.Load() != 0 {
		t.Fatalf("exit %d, %d calls to the profile's endpoint; stderr:\n%s", code, calls.Load(), stderr)
	}
}
