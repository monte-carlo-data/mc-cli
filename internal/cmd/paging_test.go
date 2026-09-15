package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// pagedWidgets answers three pages of one widget each, recording the cursors asked for.
func pagedWidgets(asked *[]string) func(string) (any, *http.Response, error) {
	pages := map[string]widgetPage{
		"":   {Items: []widget{{ID: "1", Count: 1000000}}, NextCursor: cursor("c2"), HasMore: true},
		"c2": {Items: []widget{{ID: "2"}}, NextCursor: cursor("c3"), HasMore: true},
		"c3": {Items: []widget{{ID: "3"}}, HasMore: false},
	}
	return func(c string) (any, *http.Response, error) {
		*asked = append(*asked, c)
		return pages[c], nil, nil
	}
}

func TestAllPagesErrorsWhenMoreIsReportedWithoutACursor(t *testing.T) {
	fetch := func(string) (any, *http.Response, error) {
		return widgetPage{Items: []widget{{ID: "1"}}, HasMore: true}, nil, nil
	}
	_, err := allPages(fetch)
	if err == nil || !strings.Contains(err.Error(), "returned no cursor") {
		t.Fatalf("err %v", err)
	}
}

func TestAllPagesErrorsWhenTheCursorRepeats(t *testing.T) {
	calls := 0
	fetch := func(string) (any, *http.Response, error) {
		calls++
		return widgetPage{Items: []widget{{ID: "1"}}, NextCursor: cursor("c"), HasMore: true}, nil, nil
	}
	_, err := allPages(fetch)
	if err == nil || !strings.Contains(err.Error(), `cursor "c" twice`) {
		t.Fatalf("err %v", err)
	}
	if calls > 2 {
		t.Fatalf("fetch was called %d times", calls)
	}
}

func TestAsPageReadsANullItemsAsAnEmptyPage(t *testing.T) {
	p, err := asPage(widgetPage{HasMore: false})
	if err != nil {
		t.Fatal(err)
	}
	if p.Items == nil || len(p.Items) != 0 {
		t.Fatalf("items = %#v", p.Items)
	}
}
