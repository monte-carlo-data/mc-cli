// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// currentUserBody is a CurrentUserOut JSON body carrying every field, including account_frozen,
// which whoami's table view deliberately leaves out.
const currentUserBody = `{
	"user_id": "u1",
	"email": "ada@example.com",
	"first_name": "Ada",
	"last_name": "Lovelace",
	"identity_type": "user",
	"account_id": "a1",
	"account_name": "Analytical Engines",
	"account_frozen": true,
	"auth_groups": ["admin", "viewer"]
}`

func whoamiServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/users/me" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(currentUserBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWhoamiTable is F7: the table view shows the eight fields whoami.go names, in that order,
// and leaves account_frozen out entirely.
func TestWhoamiTable(t *testing.T) {
	srv := whoamiServer(t)

	out, err := execute(t, "whoami",
		"--endpoint", srv.URL,
		"--api-id", "i", "--api-token", "s",
		"--config-dir", t.TempDir(),
		"--output", "table")
	if err != nil {
		t.Fatal(err)
	}

	want := []struct{ key, value string }{
		{"email", "ada@example.com"},
		{"first_name", "Ada"},
		{"last_name", "Lovelace"},
		{"identity_type", "user"},
		{"auth_groups", `["admin","viewer"]`},
		{"account_name", "Analytical Engines"},
		{"account_id", "a1"},
		{"user_id", "u1"},
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%s", len(lines), len(want), out)
	}
	splitCols := regexp.MustCompile(`\s{2,}`)
	for i, row := range want {
		parts := splitCols.Split(lines[i], 2)
		if len(parts) != 2 || parts[0] != row.key || parts[1] != row.value {
			t.Fatalf("row %d: got %q, want %s = %s", i, lines[i], row.key, row.value)
		}
	}

	if strings.Contains(out, "account_frozen") {
		t.Fatalf("account_frozen leaked into the table:\n%s", out)
	}
}

// TestWhoamiJSON is F7: --output json carries every field, including account_frozen.
func TestWhoamiJSON(t *testing.T) {
	srv := whoamiServer(t)

	out, err := execute(t, "whoami",
		"--endpoint", srv.URL,
		"--api-id", "i", "--api-token", "s",
		"--config-dir", t.TempDir(),
		"--output", "json")
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	frozen, ok := got["account_frozen"]
	if !ok {
		t.Fatalf("account_frozen missing from JSON output: %v", got)
	}
	if frozen != true {
		t.Fatalf("account_frozen = %v, want true", frozen)
	}
	if got["email"] != "ada@example.com" {
		t.Fatalf("email = %v", got["email"])
	}
}
