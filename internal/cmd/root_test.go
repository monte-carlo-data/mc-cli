// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"strings"
	"testing"
)

// TestRootHelpLeadsWithSetup checks that help opens with configure, lists the setup commands
// first, and files every other command under Resources except the utilities.
func TestRootHelpLeadsWithSetup(t *testing.T) {
	code, stdout, stderr := runExit(t, context.Background(), "--help")
	if code != exitOK {
		t.Fatalf("exit %d; stderr:\n%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, `Get started: run "montecarlo configure"`) {
		t.Fatalf("help does not open with configure:\n%s", stdout)
	}

	sections := map[string][]string{}
	current := ""
	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
			current = line
		case strings.HasPrefix(line, "  ") && current != "" && current != "Flags:" && current != "Usage:":
			sections[current] = append(sections[current], strings.Fields(line)[0])
		}
	}
	if got := strings.Join(sections["Getting started:"], " "); got != "configure profile whoami" {
		t.Errorf("Getting started: %s", got)
	}
	if got := strings.Join(sections["Additional Commands:"], " "); got != "completion help version" {
		t.Errorf("Additional Commands: %s", got)
	}
	resources := map[string]bool{}
	for _, name := range sections["Resources:"] {
		resources[name] = true
	}
	for _, c := range rootCmd.Commands() {
		if c.IsAvailableCommand() && c.GroupID != groupGettingStarted && !utilityCommands[c.Name()] && !resources[c.Name()] {
			t.Errorf("%s is not listed under Resources", c.Name())
		}
	}
	if !strings.Contains(stdout, "Getting started:") || strings.Index(stdout, "Getting started:") > strings.Index(stdout, "Resources:") {
		t.Errorf("Getting started is not listed first:\n%s", stdout)
	}
}
