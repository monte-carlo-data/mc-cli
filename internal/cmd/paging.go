// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// page is the envelope a paged list answers with, read through JSON so every SDK page type
// fits.
type page struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
	Count      *json.Number     `json:"count"`
}

// next is the cursor that fetches the next page, and whether there is one.
func (p page) next() (string, bool) {
	if !p.HasMore || p.NextCursor == nil || *p.NextCursor == "" {
		return "", false
	}
	return *p.NextCursor, true
}

// allPages follows fetch's cursor from the first page to the last, collecting every item.
// fetch answers the page at a cursor, the empty cursor being the first. It errors if the API
// says there is more but gives no cursor to fetch it, and if a cursor repeats.
func allPages(fetch func(cursor string) (any, *http.Response, error)) ([]map[string]any, error) {
	items := []map[string]any{}
	cursor := ""
	seen := map[string]bool{cursor: true}
	for {
		out, resp, err := fetch(cursor)
		if err != nil {
			return nil, apiErr(resp, err)
		}
		p, err := asPage(out)
		if err != nil {
			return nil, err
		}
		items = append(items, p.Items...)
		next, more := p.next()
		if !more {
			if p.HasMore {
				return nil, errors.New("the API reported more items but returned no cursor")
			}
			return items, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("the API returned cursor %q twice; stopping", next)
		}
		seen[next] = true
		cursor = next
	}
}

// asPage reads v as a page envelope. A missing items or has_more key, or a value that is not
// an object, is refused; a null items decodes to an empty page.
func asPage(v any) (page, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return page{}, err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return page{}, fmt.Errorf("cannot render %T as a page of results", v)
	}
	if _, ok := probe["items"]; !ok {
		return page{}, fmt.Errorf("cannot render %T as a page of results", v)
	}
	if _, ok := probe["has_more"]; !ok {
		return page{}, fmt.Errorf("cannot render %T as a page of results", v)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Numbers stay as written so the items re-encode exactly.
	dec.UseNumber()
	var p page
	if err := dec.Decode(&p); err != nil {
		return page{}, fmt.Errorf("cannot read the page: %w", err)
	}
	if p.Items == nil {
		p.Items = []map[string]any{}
	}
	return p, nil
}
