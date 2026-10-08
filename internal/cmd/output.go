// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

// outputFormat is --output, else table on a terminal and json otherwise.
func outputFormat(cmd *cobra.Command) (string, error) {
	format, _ := cmd.Flags().GetString("output")
	switch format {
	case "":
		if stdoutIsTerminal(cmd) {
			return "table", nil
		}
		return "json", nil
	case "table", "wide", "json":
		return format, nil
	}
	return "", usageError("--output must be table, wide or json, not %q", format)
}

// render prints one object: as JSON, or as a two-column table of its fields. The table shows
// `fields` in that order; wide, or no fields, shows every field. JSON is always the whole
// object.
func render(cmd *cobra.Command, v any, fields ...string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	values, err := asFields(v)
	if err != nil {
		return err
	}
	keys := fields
	if format == "wide" || len(keys) == 0 {
		keys = withRemaining(fields, []map[string]any{values})
	} else if err := checkKnownFields(keys, []map[string]any{values}); err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%s\n", k, cell(values[k]))
	}
	return w.Flush()
}

// renderIfJSON prints v as JSON when that is the output format, and nothing otherwise: for a
// result whose progress stderr has already shown in full.
func renderIfJSON(cmd *cobra.Command, v any) error {
	format, err := outputFormat(cmd)
	if err != nil || format != "json" {
		return err
	}
	return writeJSON(cmd.OutOrStdout(), v)
}

// renderList prints a list: as JSON, or as a table with one column per named field. Wide adds
// every other field the rows carry.
func renderList(cmd *cobra.Command, v any, columns []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	rows, err := asRows(v)
	if err != nil {
		return err
	}
	if format == "wide" {
		columns = withRemaining(columns, rows)
	} else if err := checkKnownFields(columns, rows); err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	header := make([]string, len(columns))
	for i, c := range columns {
		header[i] = strings.ToUpper(c)
	}
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = cell(row[c])
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	return w.Flush()
}

// renderPage prints one page of a list: as JSON, the whole envelope; as a table, its items,
// the total when the API counted one, then the cursor that fetches the next page when there
// is one.
func renderPage(cmd *cobra.Command, v any, columns []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	p, err := asPage(v)
	if err != nil {
		return err
	}
	if err := renderList(cmd, p.Items, columns); err != nil {
		return err
	}
	if p.Count != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Total: %s\n", p.Count)
	}
	if cursor, more := p.next(); more {
		fmt.Fprintf(cmd.OutOrStdout(), "Next page: --cursor %q\n", cursor)
	} else if p.HasMore {
		fmt.Fprintln(cmd.ErrOrStderr(), "the API reported more items but returned no cursor")
	}
	return nil
}

// renderPages prints a whole list by following its cursor. fetch answers the page at a cursor,
// the empty cursor being the first. The pages' items render as one list, as JSON an array.
// A page that fails returns its API error, and nothing is printed.
func renderPages(cmd *cobra.Command, columns []string, fetch func(cursor string) (any, *http.Response, error)) error {
	if _, err := outputFormat(cmd); err != nil {
		return err
	}
	items, err := allPages(fetch)
	if err != nil {
		return err
	}
	return renderList(cmd, items, columns)
}

// withRemaining is `first`, then every other key the rows carry, sorted.
func withRemaining(first []string, rows []map[string]any) []string {
	seen := make(map[string]bool, len(first))
	out := append([]string{}, first...)
	for _, k := range first {
		seen[k] = true
	}
	var rest []string
	for _, row := range rows {
		for k := range row {
			if !seen[k] {
				seen[k] = true
				rest = append(rest, k)
			}
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// checkKnownFields errors naming the first of fields that is not a key of any row. An empty
// result has no keys to check against.
func checkKnownFields(fields []string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	known := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			known[k] = true
		}
	}
	for _, f := range fields {
		if !known[f] {
			return fmt.Errorf("unknown field %q in the response", f)
		}
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// asFields and asRows go through JSON so the table shows the API's field names, the ones the
// JSON output shows, rather than Go's.
func asFields(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("cannot render %T as a table; use --output json", v)
	}
	return fields, nil
}

func asRows(v any) ([]map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("cannot render %T as a table; use --output json", v)
	}
	return rows, nil
}

// cell formats one table value. Nested objects and lists stay compact JSON.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return fmt.Sprint(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// authHint follows a 401 or 403 the gateway answered without a problem document.
const authHint = "Check the credentials and --endpoint, or the profile they come from."

// apiErr renders an API failure, carrying the exit code its status maps to. Any other error is
// returned as it is.
func apiErr(resp *http.Response, err error) error {
	return apiErrWithHint(resp, err, authHint)
}

// apiErrWithHint is apiErr with its own hint for a refused credential.
func apiErrWithHint(resp *http.Response, err error, hint string) error {
	var apiError *sdk.GenericOpenAPIError
	if !errors.As(err, &apiError) {
		return err
	}
	return withExitCode(statusExitCode(resp, apiError), renderAPIErr(resp, err, apiError, hint))
}

// statusExitCode is the exit code for a failed call's status, taken from the response, else
// from the problem document. A decode failure on a success status is a plain failure.
func statusExitCode(resp *http.Response, apiError *sdk.GenericOpenAPIError) int {
	status := 0
	if resp != nil {
		status = resp.StatusCode
	} else if problem, ok := apiError.Model().(sdk.ProblemOut); ok {
		status = int(problem.GetStatus())
	}
	switch {
	case status == http.StatusNotFound:
		return exitNotFound
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return exitAuth
	case status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests:
		return exitTransient
	}
	return exitFailure
}

// renderAPIErr builds the message. With a problem document: its detail, code and request id,
// then one line per invalid field. Without one, the request, the status and the body's message,
// which is what the API gateway answers when a credential or a URL is wrong, and hint when the
// status is 401 or 403.
func renderAPIErr(resp *http.Response, err error, apiError *sdk.GenericOpenAPIError, hint string) error {
	if problem, ok := apiError.Model().(sdk.ProblemOut); ok {
		var b strings.Builder
		fmt.Fprintf(&b, "%s (%s, request %s)", problem.GetDetail(), problem.GetCode(), problem.GetRequestId())
		for _, fieldErr := range problem.GetErrors() {
			fmt.Fprintf(&b, "\n  %s: %s", strings.Join(fieldErr.GetField(), "."), fieldErr.GetMessage())
		}
		return errors.New(b.String())
	}
	body := bodyMessage(apiError.Body())
	if resp == nil || resp.Request == nil {
		if body == "" {
			return err
		}
		return errors.New(body)
	}
	msg := fmt.Sprintf("%s %s: %s", resp.Request.Method, resp.Request.URL.Redacted(), resp.Status)
	if body != "" {
		msg += ": " + body
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		msg += "\n" + hint
	}
	return errors.New(msg)
}

// bodyMessage is the body's `message` when it is a JSON object with one, else the body itself.
func bodyMessage(body []byte) string {
	text := strings.TrimSpace(string(body))
	var object map[string]any
	if json.Unmarshal(body, &object) == nil {
		for _, key := range []string{"message", "Message", "detail", "error"} {
			if v, ok := object[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return text
}
