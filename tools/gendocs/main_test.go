// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/monte-carlo-data/mc-cli/internal/cmd"
)

func generate(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := run(cmd.DocsRoot(), dir, version); err != nil {
		t.Fatal(err)
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var attrURL = regexp.MustCompile(`(?:href|src)="([^"]*)"`)

// TestEveryCommandHasOnePage checks the site holds exactly a page per command, the intro and
// the stylesheet, and that every link names one of its files.
func TestEveryCommandHasOnePage(t *testing.T) {
	dir := generate(t, "v1.2.3")
	want := map[string]bool{"index.html": true, "style.css": true}
	for _, c := range commands(cmd.DocsRoot()) {
		if want[fileName(c)] {
			t.Errorf("%s: two commands share %s", c.CommandPath(), fileName(c))
		}
		want[fileName(c)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("unexpected file %s", e.Name())
		}
		delete(want, e.Name())
	}
	for name := range want {
		t.Errorf("missing %s", name)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		for _, m := range attrURL.FindAllStringSubmatch(read(t, filepath.Join(dir, e.Name())), -1) {
			if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
				t.Errorf("%s: broken link %q", e.Name(), m[1])
			}
		}
	}
}

// TestPagesMatchHelp checks each page carries its command's usage line and flags as --help
// prints them.
func TestPagesMatchHelp(t *testing.T) {
	dir := generate(t, "v1.2.3")
	for _, c := range commands(cmd.DocsRoot()) {
		page := html.UnescapeString(read(t, filepath.Join(dir, fileName(c))))
		want := []string{strings.TrimRight(c.LocalFlags().FlagUsages(), " \n"), strings.TrimRight(c.InheritedFlags().FlagUsages(), " \n")}
		if c.Runnable() {
			want = append(want, c.UseLine())
		}
		for _, w := range want {
			if !strings.Contains(page, w) {
				t.Errorf("%s: page lacks\n%s", c.CommandPath(), w)
			}
		}
	}
}

// TestPlaceholdersAreText checks a <placeholder> in help text is escaped, not dropped as a tag.
func TestPlaceholdersAreText(t *testing.T) {
	dir := generate(t, "v1.2.3")
	page := read(t, filepath.Join(dir, "montecarlo_bi-containers_get.html"))
	if !strings.Contains(page, "&lt;bi_container_id&gt;") {
		t.Errorf("<bi_container_id> is not on the page as text:\n%s", page)
	}
}

// TestPagesLoadUnderCSP checks for what a CSP of default-src 'none' with same-origin styles
// would block, and for links to a directory, which the host answers with 403.
func TestPagesLoadUnderCSP(t *testing.T) {
	dir := generate(t, "v1.2.3")
	files, _ := filepath.Glob(filepath.Join(dir, "*.html"))
	for _, f := range files {
		page := read(t, f)
		for _, bad := range []string{"<script", "style=", "<style", "<img", "<iframe", "<object"} {
			if strings.Contains(page, bad) {
				t.Errorf("%s contains %s", filepath.Base(f), bad)
			}
		}
		for _, m := range attrURL.FindAllStringSubmatch(page, -1) {
			if strings.Contains(m[1], ":") || strings.HasSuffix(m[1], "/") || m[1] == "" {
				t.Errorf("%s: link %q is not a file of the site", filepath.Base(f), m[1])
			}
		}
	}
}

// TestOutputIsReproducible checks two runs write the same bytes.
func TestOutputIsReproducible(t *testing.T) {
	a, b := generate(t, "preview of abc1234"), generate(t, "preview of abc1234")
	files, _ := filepath.Glob(filepath.Join(a, "*"))
	for _, f := range files {
		if read(t, f) != read(t, filepath.Join(b, filepath.Base(f))) {
			t.Errorf("%s differs between runs", filepath.Base(f))
		}
	}
}

// TestInstallCommandsMatchReadme checks the intro's install commands are the README's, and that
// the pinned one names the release the site was built from.
func TestInstallCommandsMatchReadme(t *testing.T) {
	readme := read(t, filepath.Join("..", "..", "README.md"))
	for _, c := range []string{installSh, installPs1, installPinned} {
		if !strings.Contains(readme, "\n"+c+"\n") {
			t.Errorf("README.md has no line\n%s", c)
		}
	}

	intro := html.UnescapeString(read(t, filepath.Join(generate(t, "v1.2.3"), "index.html")))
	pinned := "releases/download/v1.2.3/install.sh | MONTECARLO_VERSION=v1.2.3 sh"
	for _, want := range []string{installSh, installPs1, pinned, "Version: v1.2.3"} {
		if !strings.Contains(intro, want) {
			t.Errorf("intro lacks %s", want)
		}
	}

	preview := html.UnescapeString(read(t, filepath.Join(generate(t, "preview of abc1234"), "index.html")))
	for _, want := range []string{installPinned, "Version: preview of abc1234"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview intro lacks %s", want)
		}
	}
}

func TestVersionIsRequired(t *testing.T) {
	if err := run(cmd.DocsRoot(), t.TempDir(), ""); err == nil {
		t.Error("no error without -version")
	}
}
