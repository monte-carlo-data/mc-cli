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
	"github.com/spf13/cobra"
)

func generate(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := run(cmd.DocsRoot(), dir, version); err != nil {
		t.Fatal(err)
	}
	return dir
}

// read returns a file with LF line endings, which a Windows checkout replaces with CRLF.
func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// attrURL matches a URL attribute and the tag it is on.
var attrURL = regexp.MustCompile(`<(\w+)[^>]*?\s(?:href|src)="([^"]*)"`)

// external reports whether a URL is an <a> link off the site, the only kind allowed to leave it.
func external(tag, url string) bool {
	return tag == "a" && strings.HasPrefix(url, "https://")
}

// TestEveryCommandHasOnePage checks the site holds exactly a page per command, the intro, the
// guides and the static files, and that every link names one of its files.
func TestEveryCommandHasOnePage(t *testing.T) {
	dir := generate(t, "v1.2.3")
	want := map[string]bool{"index.html": true}
	for _, name := range staticFiles {
		want[name] = true
	}
	for _, g := range guides {
		want[g.File] = true
	}
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
			if external(m[1], m[2]) {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, m[2])); err != nil {
				t.Errorf("%s: broken link %q", e.Name(), m[2])
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

// TestCompletionPageRendersBlocks checks cobra's indented commands and "####" headings in the
// completion pages' text become code blocks and headings, and no page shows a literal "####".
func TestCompletionPageRendersBlocks(t *testing.T) {
	dir := generate(t, "v1.2.3")
	bash := read(t, filepath.Join(dir, "montecarlo_completion_bash.html"))
	for _, want := range []string{"<pre>source &lt;(montecarlo completion bash)</pre>", "<h3>Linux</h3>"} {
		if !strings.Contains(bash, want) {
			t.Errorf("completion bash page lacks %s", want)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.html"))
	for _, f := range files {
		if strings.Contains(read(t, f), "####") {
			t.Errorf("%s shows a literal ####", filepath.Base(f))
		}
	}
}

// usageSections lists the section titles and subcommand names in c's usage, in order, titles as
// the pages print them.
func usageSections(c *cobra.Command) []string {
	skip := map[string]bool{
		"Usage": true, "Aliases": true, "Examples": true, "Flags": true, "Global Flags": true,
		"Additional help topics": true,
	}
	titles := map[string]string{"Available Commands": "Commands", "Additional Commands": "Additional commands"}
	var out []string
	inCommands := false
	for _, line := range strings.Split(c.UsageString(), "\n") {
		switch {
		case line == "":
		case !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":"):
			title := strings.TrimSuffix(line, ":")
			inCommands = !skip[title]
			if inCommands {
				if t, ok := titles[title]; ok {
					title = t
				}
				out = append(out, "# "+title)
			}
		case inCommands && strings.HasPrefix(line, " "):
			out = append(out, strings.Fields(line)[0])
		}
	}
	return out
}

// pageSection matches a heading or the command link at the start of a commands table row.
var pageSection = regexp.MustCompile(`<h2>(.*?)</h2>|<tr><td><a href="[^"]*">(.*?)</a></td>`)

// pageSections lists the group titles and subcommand names on a command page, in order.
func pageSections(page string) []string {
	skip := map[string]bool{"Usage": true, "Aliases": true, "Examples": true, "Flags": true, "Global flags": true}
	var out []string
	for _, m := range pageSection.FindAllStringSubmatch(page, -1) {
		switch {
		case m[2] != "":
			out = append(out, html.UnescapeString(m[2]))
		case !skip[m[1]]:
			out = append(out, "# "+html.UnescapeString(m[1]))
		}
	}
	return out
}

// TestPagesGroupSubcommandsAsHelpDoes checks every page lists the same group titles and
// subcommands, in the same order, as the command's usage.
func TestPagesGroupSubcommandsAsHelpDoes(t *testing.T) {
	dir := generate(t, "v1.2.3")
	for _, c := range commands(cmd.DocsRoot()) {
		got := pageSections(read(t, filepath.Join(dir, fileName(c))))
		want := usageSections(c)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: page lists\n%q\nusage lists\n%q", c.CommandPath(), got, want)
		}
	}
}

// TestIntroListsGettingStarted checks the intro's Getting started section holds a table with
// configure and whoami.
func TestIntroListsGettingStarted(t *testing.T) {
	intro := read(t, filepath.Join(generate(t, "v1.2.3"), "index.html"))
	_, rest, ok := strings.Cut(intro, "<h2>Getting started</h2>")
	if !ok {
		t.Fatal("intro has no Getting started section")
	}
	section, _, _ := strings.Cut(rest, "<h2>")
	// The prose above the table links these commands too, so only the table counts.
	_, table, ok := strings.Cut(section, `<table class="commands">`)
	if !ok {
		t.Fatal("Getting started section has no command table")
	}
	for _, want := range []string{`href="montecarlo_configure.html"`, `href="montecarlo_whoami.html"`} {
		if !strings.Contains(table, want) {
			t.Errorf("Getting started table lacks %s", want)
		}
	}
}

// TestDocsRootOnFreshTree checks the site has the utility pages, the root's groups are found by
// ID, and only the utilities are left outside them. Nothing here executes the tree, so this
// checks what DocsRoot does alone.
func TestDocsRootOnFreshTree(t *testing.T) {
	dir := generate(t, "v1.2.3")
	for _, name := range []string{"montecarlo_help.html", "montecarlo_completion.html"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	found := map[string]bool{}
	for _, g := range subcommandGroups(cmd.DocsRoot()) {
		found[g.ID] = true
		if g.ID != "" {
			continue
		}
		for _, l := range g.Commands {
			if l.Name != "help" && l.Name != "completion" && l.Name != "version" {
				t.Errorf("%s is in no group", l.Name)
			}
		}
	}
	for _, id := range []string{gettingStartedGroup, "resources"} {
		if !found[id] {
			t.Errorf("no group %q", id)
		}
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
			if external(m[1], m[2]) {
				continue
			}
			if strings.Contains(m[2], ":") || strings.HasSuffix(m[2], "/") || m[2] == "" {
				t.Errorf("%s: <%s> URL %q is not a file of the site", filepath.Base(f), m[1], m[2])
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

var preBlock = regexp.MustCompile(`(?s)<pre>(.*?)</pre>`)

// TestGuidesQuoteRealCommands checks every command line a guide quotes names a command and
// flags it has, and every environment variable it names is one the root's flags read.
func TestGuidesQuoteRealCommands(t *testing.T) {
	root := cmd.DocsRoot()
	envName := regexp.MustCompile(`MCD_DEFAULT_[A-Z_]+`)
	readByFlags := map[string]bool{}
	for _, name := range envName.FindAllString(root.PersistentFlags().FlagUsages(), -1) {
		readByFlags[name] = true
	}
	for _, g := range guides {
		body := read(t, filepath.Join("guides", g.File))
		lines := 0
		for _, block := range preBlock.FindAllStringSubmatch(body, -1) {
			for _, line := range strings.Split(html.UnescapeString(block[1]), "\n") {
				args := strings.Fields(line)
				if len(args) == 0 || args[0] != root.Name() {
					continue
				}
				lines++
				c, _, err := root.Find(args[1:])
				if err != nil || (c == root && len(args) > 1) {
					t.Errorf("%s: %q names no command", g.File, line)
					continue
				}
				for _, a := range args[1:] {
					name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
					if strings.HasPrefix(a, "--") && c.Flag(name) == nil {
						t.Errorf("%s: %s has no flag --%s", g.File, c.CommandPath(), name)
					}
				}
			}
		}
		if lines == 0 {
			t.Errorf("%s quotes no command line", g.File)
		}
		for _, name := range envName.FindAllString(body, -1) {
			if !readByFlags[name] {
				t.Errorf("%s: no flag reads %s", g.File, name)
			}
		}
	}
}
