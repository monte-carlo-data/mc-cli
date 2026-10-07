// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

// TestVersionFromBuildInfoFillsInTheDevDefaults is F19: a module version and vcs settings fill
// in for the ldflags-set globals when a binary was built without them, for example "go install".
func TestVersionFromBuildInfoFillsInTheDevDefaults(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef0123456789"},
			{Key: "vcs.time", Value: "2026-09-11T00:00:00Z"},
		},
	}
	v, c, d := versionFromBuildInfo(info, true)
	if v != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", v)
	}
	if c != "abcdef012345" {
		t.Errorf("commit = %q, want a 12-character prefix", c)
	}
	if d != "2026-09-11T00:00:00Z" {
		t.Errorf("date = %q", d)
	}
}

// TestVersionFromBuildInfoLeavesDevAloneWithoutInfo is F19: ok false, the case ReadBuildInfo
// itself returns for a binary built without module support, leaves the "dev" defaults standing.
func TestVersionFromBuildInfoLeavesDevAloneWithoutInfo(t *testing.T) {
	v, c, d := versionFromBuildInfo(nil, false)
	if v != version || c != commit || d != date {
		t.Errorf("got %s %s %s, want the dev defaults %s %s %s", v, c, d, version, commit, date)
	}
}

// TestVersionFromBuildInfoSkipsAnUnstampedMainVersion is F19: "(devel)" is what ReadBuildInfo
// reports for a binary built from a local checkout without a pinned version, and is not itself
// a usable version string.
func TestVersionFromBuildInfoSkipsAnUnstampedMainVersion(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	v, _, _ := versionFromBuildInfo(info, true)
	if v != version {
		t.Errorf("version = %q, want the dev default %q", v, version)
	}
}

// TestVersionTable and TestVersionJSON are F7: end-to-end through the real command. They avoid
// asserting the literal "dev"/"none"/"unknown" defaults because the test binary itself is built
// with module support, so the command's own debug.ReadBuildInfo fallback fills in real values.
func TestVersionTable(t *testing.T) {
	out, err := execute(t, "version", "--output", "table")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, binaryName+" ") {
		t.Fatalf("does not start with the binary name: %q", out)
	}
	if !strings.Contains(out, "(") || !strings.HasSuffix(out, ")\n") {
		t.Fatalf("does not carry the (commit, date) parenthetical: %q", out)
	}
}

func TestVersionJSON(t *testing.T) {
	out, err := execute(t, "version", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"version", "commit", "date"} {
		if got[key] == "" {
			t.Fatalf("%s is empty: %v", key, got)
		}
	}
}

// stubBuildInfo makes the binary's build info info for the rest of the test.
func stubBuildInfo(t *testing.T, info *debug.BuildInfo) {
	t.Helper()
	saved := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, true }
	t.Cleanup(func() { readBuildInfo = saved })
}

// A release build's stamped values win over its build info.
func TestResolvedVersionPrefersTheStampedValues(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}})
	saved := [3]string{version, commit, date}
	version, commit, date = "v0.1.4", "0123456789ab", "2026-10-07T12:00:00Z"
	t.Cleanup(func() { version, commit, date = saved[0], saved[1], saved[2] })

	if v, c, d := resolvedVersion(); v != "v0.1.4" || c != "0123456789ab" || d != "2026-10-07T12:00:00Z" {
		t.Errorf("got %s %s %s, want the stamped values", v, c, d)
	}
}
