// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestDocsRootMatchesHelp checks that every available command's usage, as DocsRoot returns it,
// is what --help prints for that command. Other tests may have executed the shared tree first,
// so tools/gendocs checks DocsRoot on a fresh one.
func TestDocsRootMatchesHelp(t *testing.T) {
	root := DocsRoot()
	for _, c := range availableCommands(root) {
		want := c.UsageString()
		args := append(strings.Fields(c.CommandPath())[1:], "--help")
		code, stdout, stderr := runExit(t, context.Background(), args...)
		if code != exitOK {
			t.Fatalf("%s: exit %d; stderr:\n%s", c.CommandPath(), code, stderr)
		}
		if !strings.HasSuffix(stdout, want) {
			t.Errorf("%s: --help does not end with its usage from DocsRoot.\nusage:\n%s\nhelp:\n%s", c.CommandPath(), want, stdout)
		}
	}
}

// TestDocsRootGroupsAndRepeats checks the utilities exist, every other root command has a
// group, and a second call changes nothing.
func TestDocsRootGroupsAndRepeats(t *testing.T) {
	root := DocsRoot()
	before := len(availableCommands(root))
	for _, c := range root.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}
		if utilityCommands[c.Name()] != (c.GroupID == "") {
			t.Errorf("%s: group %q", c.Name(), c.GroupID)
		}
	}
	for _, name := range []string{"help", "completion"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("%s is missing from the tree", name)
		}
	}
	if after := len(availableCommands(DocsRoot())); after != before {
		t.Errorf("second call: %d commands, first: %d", after, before)
	}
}

func availableCommands(cmd *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{cmd}
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() && !sub.IsAdditionalHelpTopicCommand() {
			out = append(out, availableCommands(sub)...)
		}
	}
	return out
}
