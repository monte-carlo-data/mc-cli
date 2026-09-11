package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

func outputCmd(t *testing.T, format string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{Use: "t", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("output", "", "")
	cmd.SetArgs([]string{"--output", format})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	return cmd, &out
}

type widget struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Count   int      `json:"count"`
	Tags    []string `json:"tags"`
	Owner   *string  `json:"owner"`
}

func TestRenderTableSortsFieldsAndFormatsValues(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	if err := render(cmd, widget{ID: "1", Name: "w", Enabled: true, Count: 2, Tags: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	want := "count    2\nenabled  true\nid       1\nname     w\nowner    \ntags     [\"a\"]\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRenderTableShowsTheGivenFieldsInOrder(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	if err := render(cmd, widget{ID: "1", Name: "w", Enabled: true}, "name", "id"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "name  w\nid    1\n" {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderWideAddsTheRemainingFieldsAfterTheGivenOnes(t *testing.T) {
	cmd, out := outputCmd(t, "wide")
	if err := render(cmd, widget{ID: "1", Name: "w", Enabled: true}, "name", "id"); err != nil {
		t.Fatal(err)
	}
	want := "name     w\nid       1\ncount    0\nenabled  true\nowner    \ntags     \n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRenderListWideAddsColumnsTheRowsCarry(t *testing.T) {
	cmd, out := outputCmd(t, "wide")
	rows := []widget{{ID: "1", Name: "a"}}
	if err := renderList(cmd, rows, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "ID  COUNT  ENABLED  NAME  OWNER  TAGS\n") {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderJSONIgnoresTheFieldList(t *testing.T) {
	cmd, out := outputCmd(t, "json")
	if err := render(cmd, widget{ID: "1", Enabled: true}, "name"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"enabled": true`) {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderJSON(t *testing.T) {
	cmd, out := outputCmd(t, "json")
	if err := render(cmd, widget{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "{\n  \"id\": \"1\",") {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderListTableUsesTheColumns(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	rows := []widget{{ID: "1", Name: "a", Count: 10}, {ID: "2", Name: "b"}}
	if err := renderList(cmd, rows, []string{"id", "name", "count"}); err != nil {
		t.Fatal(err)
	}
	want := "ID  NAME  COUNT\n1   a     10\n2   b     0\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestOutputFormatRejectsOtherValues(t *testing.T) {
	cmd, _ := outputCmd(t, "yaml")
	if _, err := outputFormat(cmd); err == nil {
		t.Fatal("yaml was accepted")
	}
}

func problemServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func callWhoami(t *testing.T, srv *httptest.Server) (*http.Response, error) {
	t.Helper()
	api, err := sdk.NewClient(context.Background(), sdk.Options{
		Endpoint: srv.URL, TokenID: "id", TokenSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, resp, err := api.UsersAPI.GetCurrentUser(context.Background()).Execute()
	if err == nil {
		t.Fatal("the server's error was swallowed")
	}
	return resp, err
}

func TestApiErrRendersTheProblem(t *testing.T) {
	srv := problemServer(t, http.StatusForbidden, "application/problem+json", `{
		"code": "account_frozen", "detail": "The account is paused.", "request_id": "req-1",
		"status": 403, "title": "Forbidden", "type": "urn:montecarloai:error:account_frozen",
		"errors": [{"code": "invalid", "field": ["settings", "timeout"], "message": "must be positive"}]
	}`)
	got := apiErr(callWhoami(t, srv)).Error()
	want := "The account is paused. (account_frozen, request req-1)\n  settings.timeout: must be positive"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestApiErrRendersAGatewayDenial(t *testing.T) {
	// A declared status with the API gateway's body, not a problem document.
	srv := problemServer(t, http.StatusForbidden, "application/json",
		`{"Message":"User is not authorized to access this resource with an explicit deny in an identity-based policy"}`)
	got := apiErr(callWhoami(t, srv)).Error()
	want := "GET " + srv.URL + "/api/v2/users/me: 403 Forbidden: User is not authorized to access this resource with an explicit deny in an identity-based policy\nCheck the credentials and --endpoint, or the profile they come from."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "required property") {
		t.Fatal("the SDK's decode error leaked into the message")
	}
}

func TestApiErrFallsBackToTheBody(t *testing.T) {
	srv := problemServer(t, http.StatusTeapot, "text/plain", "short and stout")
	got := apiErr(callWhoami(t, srv)).Error()
	want := "GET " + srv.URL + "/api/v2/users/me: 418 I'm a teapot: short and stout"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestApiErrPassesOtherErrorsThrough(t *testing.T) {
	err := context.Canceled
	if apiErr(nil, err) != err {
		t.Fatal("a non-API error was rewrapped")
	}
}

func TestBodyMessage(t *testing.T) {
	cases := map[string]string{
		`{"message":"Unauthorized"}`: "Unauthorized",
		`{"Message":"Denied"}`:       "Denied",
		`{"detail":"x"}`:             "x",
		`{"other":"x"}`:              `{"other":"x"}`,
		"plain text\n":               "plain text",
		"":                           "",
	}
	for body, want := range cases {
		if got := bodyMessage([]byte(body)); got != want {
			t.Errorf("%q: got %q, want %q", body, got, want)
		}
	}
}
