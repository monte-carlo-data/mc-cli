// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

// Command gendocs writes the montecarlo command reference as a flat static site: an intro at
// index.html, the hand-written guides in guides/, one page per command named after its path
// (montecarlo_connections_list.html), style.css and the icons. The command pages carry what
// --help prints. No page has scripts, inline styles or anything loaded from elsewhere, so they
// load under a CSP of default-src 'none' with only same-origin styles and images. Every link
// within the site names a file, since its host serves no directory index.
package main

import (
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/monte-carlo-data/mc-cli/internal/cmd"
	"github.com/spf13/cobra"
)

//go:embed page.html.tmpl style.css favicon.png apple-touch-icon.png guides
var assets embed.FS

// The install commands, as README.md gives them; a test keeps the two in step. The pinned one
// shows the version the site was built from when that is a release.
const (
	installSh     = "curl -fsSL https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.sh | sh"
	installPs1    = "irm https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.ps1 | iex"
	installPinned = "curl -fsSL https://github.com/monte-carlo-data/mc-cli/releases/download/vX.Y.Z/install.sh | MONTECARLO_VERSION=vX.Y.Z sh"
)

// guides are written as they are, inside the shared layout, from guides/<File>.
var guides = []link{
	{Name: "Set up credentials with an OAuth client", File: "credentials-oauth.html"},
	{Name: "Set up credentials with an API token", File: "credentials-api-token.html"},
}

// staticFiles are copied as they are. The icons are montecarlo.ai's, as the API reference uses.
var staticFiles = []string{"style.css", "favicon.png", "apple-touch-icon.png"}

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func main() {
	out := flag.String("o", "site", "directory to write")
	version := flag.String("version", "", `version shown on every page: a release tag, or "preview of <sha>"`)
	flag.Parse()
	if err := run(cmd.DocsRoot(), *out, *version); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

type link struct{ Name, Short, File string }

type group struct {
	Title    string
	Commands []link
}

type page struct {
	Version     string
	Path        string
	Crumbs      []link // the ancestors, root first
	Paragraphs  []string
	Usage       []string
	Aliases     string
	Example     string
	Groups      []group
	Flags       string
	GlobalFlags string
}

type guidePage struct {
	Version, Title string
	Body           template.HTML
}

type intro struct {
	Version        string
	Root           link
	Guides         []link
	GettingStarted []link
	Commands       []link
	Install        []string
	Pinned         string
}

func run(root *cobra.Command, dir, version string) error {
	if version == "" {
		return errors.New("-version is required")
	}
	tmpl, err := template.ParseFS(assets, "page.html.tmpl")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range staticFiles {
		b, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}

	cmds := commands(root)
	for _, c := range cmds {
		if err := write(tmpl, "command", filepath.Join(dir, fileName(c)), commandPage(c, version)); err != nil {
			return err
		}
	}
	for _, g := range guides {
		body, err := assets.ReadFile("guides/" + g.File)
		if err != nil {
			return err
		}
		p := guidePage{Version: version, Title: g.Name, Body: template.HTML(body)}
		if err := write(tmpl, "guide", filepath.Join(dir, g.File), p); err != nil {
			return err
		}
	}
	in := intro{Version: version, Root: linkTo(root), Guides: guides, Install: []string{installSh, installPs1}, Pinned: installPinned}
	if releaseTag.MatchString(version) {
		in.Pinned = strings.ReplaceAll(installPinned, "vX.Y.Z", version)
	}
	for _, c := range cmds[1:] {
		in.Commands = append(in.Commands, link{Name: c.CommandPath(), Short: c.Short, File: fileName(c)})
	}
	for _, g := range subcommandGroups(root) {
		if g.Title == "Getting started" {
			in.GettingStarted = g.Commands
		}
	}
	return write(tmpl, "intro", filepath.Join(dir, "index.html"), in)
}

func write(tmpl *template.Template, name, path string, data any) error {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// commands is the tree, root first, without what cobra's own doc generators skip.
func commands(c *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{c}
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() && !sub.IsAdditionalHelpTopicCommand() {
			out = append(out, commands(sub)...)
		}
	}
	return out
}

func fileName(c *cobra.Command) string {
	return strings.ReplaceAll(c.CommandPath(), " ", "_") + ".html"
}

func linkTo(c *cobra.Command) link {
	return link{Name: c.Name(), Short: c.Short, File: fileName(c)}
}

// commandPage holds the sections of cobra's help for c, in its order.
func commandPage(c *cobra.Command, version string) page {
	p := page{
		Version:     version,
		Path:        c.CommandPath(),
		Example:     c.Example,
		Groups:      subcommandGroups(c),
		Flags:       strings.TrimRight(c.LocalFlags().FlagUsages(), " \n"),
		GlobalFlags: strings.TrimRight(c.InheritedFlags().FlagUsages(), " \n"),
	}
	for a := c.Parent(); a != nil; a = a.Parent() {
		p.Crumbs = append([]link{linkTo(a)}, p.Crumbs...)
	}
	text := c.Long
	if text == "" {
		text = c.Short
	}
	for _, para := range strings.Split(text, "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			p.Paragraphs = append(p.Paragraphs, para)
		}
	}
	if c.Runnable() {
		p.Usage = append(p.Usage, c.UseLine())
	}
	if c.HasAvailableSubCommands() {
		p.Usage = append(p.Usage, c.CommandPath()+" [command]")
	}
	if len(c.Aliases) > 0 {
		p.Aliases = c.NameAndAliases()
	}
	return p
}

// subcommandGroups lists c's subcommands as its help does: by group, in the order the groups
// were added, then those in none.
func subcommandGroups(c *cobra.Command) []group {
	var groups []group
	var rest []link
	byID := map[string]int{}
	for _, g := range c.Groups() {
		byID[g.ID] = len(groups)
		groups = append(groups, group{Title: strings.TrimSuffix(g.Title, ":")})
	}
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if i, ok := byID[sub.GroupID]; ok {
			groups[i].Commands = append(groups[i].Commands, linkTo(sub))
		} else {
			rest = append(rest, linkTo(sub))
		}
	}
	title := "Commands"
	if len(groups) > 0 {
		title = "Additional commands"
	}
	if len(rest) > 0 {
		groups = append(groups, group{Title: title, Commands: rest})
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g.Commands) > 0 {
			out = append(out, g)
		}
	}
	return out
}
