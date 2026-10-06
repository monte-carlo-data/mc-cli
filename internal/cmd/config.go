// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

// The credentials file every Monte Carlo tool shares, and the keys this CLI writes in it.
const (
	profilesFile = "profiles.ini"
	keyID        = "mcd_id"
	keyToken     = "mcd_token"
	keyClientID  = "mcd_oauth_client_id"
	keySecret    = "mcd_oauth_client_secret"
	keyInstance  = "mcd_instance_id"
)

// The CLI's own settings file, beside profiles.ini. The other tools do not read it.
const (
	cliFile       = "cli.ini"
	cliSection    = "defaults"
	cliKeyProfile = "profile"
)

// The credentials file holds secrets; the settings file does not.
const (
	configDirMode    = 0o700
	credentialsMode  = 0o600
	settingsFileMode = 0o644
)

// INI syntax this package reads and writes.
const (
	sectionOpen        = "["
	sectionClose       = "]"
	commentPrefixes    = "#;"
	keyValueDelimiters = "=:"
)

// iniFile edits an INI file line by line. Lines it does not touch, including comments and keys
// it does not know, are written back exactly as read.
type iniFile struct {
	path  string
	lines []string
}

func loadINI(path string) (*iniFile, error) {
	f := &iniFile{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(data), "\n")
	if text != "" {
		f.lines = strings.Split(text, "\n")
	}
	return f, nil
}

// save writes the file atomically: it writes the new content to a temp file in the same
// directory, syncs it, and renames it over path, so a reader never observes a partial write.
// It also tightens permissions a previous tool may have left too loose: the file to mode, and,
// for the credentials file, the directory to configDirMode.
func (f *iniFile) save(mode os.FileMode) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return err
	}
	content := strings.Join(f.lines, "\n")
	if content != "" {
		content += "\n"
	}
	tmp, err := os.CreateTemp(dir, ".profiles-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte(content)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// The rename carries the temp file's mode, so a file another tool created looser is
	// replaced by one at the mode this file needs.
	if err := os.Rename(tmpPath, f.path); err != nil {
		return err
	}
	if mode == credentialsMode {
		if err := os.Chmod(dir, configDirMode); err != nil {
			return err
		}
	}
	return nil
}

func sectionName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, sectionOpen) && strings.HasSuffix(t, sectionClose) {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// keyOf is the lower-cased key of a `key = value` or `key: value` line, else "".
func keyOf(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.ContainsRune(commentPrefixes, rune(t[0])) {
		return ""
	}
	if _, ok := sectionName(t); ok {
		return ""
	}
	idx := strings.IndexAny(t, keyValueDelimiters)
	if idx < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(t[:idx]))
}

func valueOf(line string) string {
	idx := strings.IndexAny(line, keyValueDelimiters)
	return strings.TrimSpace(line[idx+1:])
}

// sectionRange is the half-open line range of a section's body, or (-1, -1).
func (f *iniFile) sectionRange(name string) (int, int) {
	start := -1
	for i, line := range f.lines {
		if n, ok := sectionName(line); ok {
			if start >= 0 {
				return start, i
			}
			if n == name {
				start = i + 1
			}
		}
	}
	if start >= 0 {
		return start, len(f.lines)
	}
	return -1, -1
}

func (f *iniFile) sections() []string {
	var out []string
	for _, line := range f.lines {
		if n, ok := sectionName(line); ok {
			out = append(out, n)
		}
	}
	return out
}

func (f *iniFile) hasSection(name string) bool {
	start, _ := f.sectionRange(name)
	return start >= 0
}

// get is the value of the section's first line for key, or "" and false when the section or
// key is absent. The file this CLI writes holds at most one line per key; a later duplicate,
// for example from a hand edit, is ignored.
func (f *iniFile) get(section, key string) (string, bool) {
	start, end := f.sectionRange(section)
	for i := start; start >= 0 && i < end; i++ {
		if keyOf(f.lines[i]) == key {
			return valueOf(f.lines[i]), true
		}
	}
	return "", false
}

// set replaces the key's first line in place and removes any later duplicate in the section,
// or appends a new line, creating the section at the end of the file when needed.
func (f *iniFile) set(section, key, value string) {
	start, end := f.sectionRange(section)
	if start < 0 {
		if len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) != "" {
			f.lines = append(f.lines, "")
		}
		f.lines = append(f.lines, sectionOpen+section+sectionClose)
		start, end = len(f.lines), len(f.lines)
	}
	entry := key + " = " + value
	replaced := false
	for i := start; i < end; i++ {
		if keyOf(f.lines[i]) != key {
			continue
		}
		if !replaced {
			f.lines[i] = entry
			replaced = true
			continue
		}
		f.lines = append(f.lines[:i], f.lines[i+1:]...)
		end--
		i--
	}
	if replaced {
		return
	}
	// Insert before the blank lines that separate this section from the next.
	insert := end
	for insert > start && strings.TrimSpace(f.lines[insert-1]) == "" {
		insert--
	}
	f.lines = append(f.lines[:insert], append([]string{entry}, f.lines[insert:]...)...)
}

// unset removes every line in the section whose key matches.
func (f *iniFile) unset(section, key string) {
	start, end := f.sectionRange(section)
	for i := start; start >= 0 && i < end; i++ {
		if keyOf(f.lines[i]) != key {
			continue
		}
		f.lines = append(f.lines[:i], f.lines[i+1:]...)
		end--
		i--
	}
}

func profilesPath(dir string) string { return filepath.Join(dir, profilesFile) }

func cliPath(dir string) string { return filepath.Join(dir, cliFile) }

// activeProfile is the profile chosen with "profile use", or "".
func activeProfile(dir string) (string, error) {
	f, err := loadINI(cliPath(dir))
	if err != nil {
		return "", err
	}
	name, _ := f.get(cliSection, cliKeyProfile)
	return name, nil
}

func setActiveProfile(dir, name string) error {
	f, err := loadINI(cliPath(dir))
	if err != nil {
		return err
	}
	f.set(cliSection, cliKeyProfile, name)
	return f.save(settingsFileMode)
}
