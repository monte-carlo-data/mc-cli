// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNoticeFilesPicksLicenseAndNoticeFilesOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"LICENSE", "LICENSE.md", "license.txt", "LICENCE", "COPYING", "NOTICE.txt", "PATENTS",
		"README.md", "go.mod", "licenses.go", "noticeboard.go",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory named like a license file is not one.
	if err := os.Mkdir(filepath.Join(dir, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := noticeFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"COPYING", "LICENCE", "LICENSE", "LICENSE.md", "NOTICE.txt", "PATENTS", "license.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("noticeFiles = %q, want %q", got, want)
	}
}
