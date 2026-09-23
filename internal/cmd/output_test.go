package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestOutputFormatDefaultsToJSONWhenStdoutIsNotATerminal(t *testing.T) {
	cmd, out := outputCmd(t, "")
	if err := render(cmd, widget{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "{\n") {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestOutputFormatDefaultsToTableOnATerminal(t *testing.T) {
	cmd, _ := outputCmd(t, "")
	prev := isTerminal
	isTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { isTerminal = prev })

	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	cmd.SetOut(f)

	if err := render(cmd, widget{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(data), "{") || !strings.Contains(string(data), "id") {
		t.Fatalf("got:\n%s", data)
	}
}

func TestRenderErrorsOnAnUnknownField(t *testing.T) {
	cmd, _ := outputCmd(t, "table")
	err := render(cmd, widget{ID: "1"}, "bogus")
	if err == nil || err.Error() != `unknown field "bogus" in the response` {
		t.Fatalf("err %v", err)
	}
}

func TestRenderWideIgnoresAnUnknownField(t *testing.T) {
	cmd, out := outputCmd(t, "wide")
	if err := render(cmd, widget{ID: "1"}, "bogus"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bogus") {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderListErrorsOnAnUnknownColumn(t *testing.T) {
	cmd, _ := outputCmd(t, "table")
	err := renderList(cmd, []widget{{ID: "1"}}, []string{"bogus"})
	if err == nil || err.Error() != `unknown field "bogus" in the response` {
		t.Fatalf("err %v", err)
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

func TestRenderJSONIndentsWithTwoSpaces(t *testing.T) {
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

type widgetPage struct {
	Items      []widget `json:"items"`
	NextCursor *string  `json:"next_cursor"`
	HasMore    bool     `json:"has_more"`
	Count      *int     `json:"count"`
}

func cursor(s string) *string { return &s }

func TestRenderPageTableEndsWithTheNextCursorWhenThereIsMore(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	p := widgetPage{Items: []widget{{ID: "1", Name: "a"}}, NextCursor: cursor("c2"), HasMore: true}
	if err := renderPage(cmd, p, []string{"id", "name"}); err != nil {
		t.Fatal(err)
	}
	want := "ID  NAME\n1   a\nNext page: --cursor \"c2\"\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRenderPageTableOnTheLastPageHasNoCursorLine(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	p := widgetPage{Items: []widget{{ID: "1", Name: "a"}}, HasMore: false}
	if err := renderPage(cmd, p, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ID\n1\n" {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderPageTableEndsWithTheTotalWhenCounted(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	total := 3
	p := widgetPage{Items: []widget{{ID: "1"}}, HasMore: false, Count: &total}
	if err := renderPage(cmd, p, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ID\n1\nTotal: 3\n" {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderPageJSONIsTheWholeEnvelope(t *testing.T) {
	cmd, out := outputCmd(t, "json")
	p := widgetPage{Items: []widget{{ID: "1"}}, NextCursor: cursor("c2"), HasMore: true}
	if err := renderPage(cmd, p, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"items": [`, `"next_cursor": "c2"`, `"has_more": true`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in:\n%s", want, out.String())
		}
	}
}

func TestRenderPageRefusesAValueThatIsNotAPage(t *testing.T) {
	cmd, _ := outputCmd(t, "table")
	err := renderPage(cmd, widget{ID: "1"}, []string{"id"})
	if err == nil || !strings.HasPrefix(err.Error(), "cannot render cmd.widget as a page of results") {
		t.Fatalf("err %v", err)
	}
}

func TestRenderPageWarnsWhenMoreIsReportedWithoutACursor(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	p := widgetPage{Items: []widget{{ID: "1"}}, HasMore: true}
	if err := renderPage(cmd, p, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ID\n1\n" {
		t.Fatalf("got:\n%s", out.String())
	}
	if !strings.Contains(errBuf.String(), "returned no cursor") {
		t.Fatalf("stderr:\n%s", errBuf.String())
	}
}

func TestRenderPagesRejectsAnUnknownOutputBeforeFetching(t *testing.T) {
	cmd, _ := outputCmd(t, "yaml")
	calls := 0
	fetch := func(string) (any, *http.Response, error) {
		calls++
		return widgetPage{}, nil, nil
	}
	if err := renderPages(cmd, []string{"id"}, fetch); err == nil {
		t.Fatal("yaml was accepted")
	}
	if calls != 0 {
		t.Fatalf("fetch was called %d times", calls)
	}
}

func TestRenderPagesFollowsTheCursorToTheEnd(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	var asked []string
	if err := renderPages(cmd, []string{"id", "count"}, pagedWidgets(&asked)); err != nil {
		t.Fatal(err)
	}
	want := "ID  COUNT\n1   1000000\n2   0\n3   0\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	if strings.Join(asked, ",") != ",c2,c3" {
		t.Fatalf("asked for cursors %q", asked)
	}
}

func TestRenderPagesJSONIsTheArrayOfEveryItem(t *testing.T) {
	cmd, out := outputCmd(t, "json")
	var asked []string
	if err := renderPages(cmd, []string{"id"}, pagedWidgets(&asked)); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "[\n") || strings.Count(got, `"id": `) != 3 || strings.Contains(got, "has_more") {
		t.Fatalf("got:\n%s", got)
	}
	if !strings.Contains(got, `"count": 1000000`) {
		t.Fatalf("the number was not kept as written:\n%s", got)
	}
}

func TestRenderPagesOnAnEmptyListPrintsAnEmptyArray(t *testing.T) {
	cmd, out := outputCmd(t, "json")
	fetch := func(string) (any, *http.Response, error) {
		return widgetPage{Items: []widget{}}, nil, nil
	}
	if err := renderPages(cmd, []string{"id"}, fetch); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[]\n" {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestRenderPagesReturnsTheErrorOfTheFailingPage(t *testing.T) {
	cmd, out := outputCmd(t, "table")
	fetch := func(c string) (any, *http.Response, error) {
		if c == "" {
			return widgetPage{Items: []widget{{ID: "1"}}, NextCursor: cursor("c2"), HasMore: true}, nil, nil
		}
		return nil, nil, context.Canceled
	}
	err := renderPages(cmd, []string{"id"}, fetch)
	if err != context.Canceled {
		t.Fatalf("err %v", err)
	}
	if out.String() != "" {
		t.Fatalf("a partial list was printed:\n%s", out.String())
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

func TestBodyMessagePrefersTheMessageKeyElseTheRawBody(t *testing.T) {
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

func TestRenderIfJSONPrintsOnlyForJSON(t *testing.T) {
	for format, want := range map[string]string{"json": "{\n  \"id\": \"w1\"\n}\n", "table": "", "wide": ""} {
		cmd, out := outputCmd(t, format)
		if err := renderIfJSON(cmd, map[string]string{"id": "w1"}); err != nil || out.String() != want {
			t.Errorf("%s: out %q, err %v", format, out, err)
		}
	}
	cmd, _ := outputCmd(t, "yaml")
	if err := renderIfJSON(cmd, map[string]string{}); err == nil {
		t.Error("an unknown format was accepted")
	}
}
