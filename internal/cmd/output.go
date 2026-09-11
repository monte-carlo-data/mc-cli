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
	return "", fmt.Errorf("--output must be table, wide or json, not %q", format)
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

// apiErr renders an API failure. With a problem document: its detail, code and request id, then
// one line per invalid field. Without one, the request, the status and the body's message, which
// is what the API gateway answers when a credential or a URL is wrong.
func apiErr(resp *http.Response, err error) error {
	var apiError *sdk.GenericOpenAPIError
	if !errors.As(err, &apiError) {
		return err
	}
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
		msg += "\nCheck the credentials and --endpoint, or the profile they come from."
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
