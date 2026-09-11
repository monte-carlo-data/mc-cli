package cmd

import (
	"fmt"
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
	cliFile           = "cli.ini"
	cliSection        = "defaults"
	cliKeyProfile     = "profile"
	configDirMode     = 0o700
	credentialsMode   = 0o600
	settingsFileMode  = 0o644
	sectionOpen       = "["
	sectionClose      = "]"
	commentPrefixes   = "#;"
	keyValueDelimiter = "="
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

func (f *iniFile) save(mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(f.path), configDirMode); err != nil {
		return err
	}
	content := strings.Join(f.lines, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(f.path, []byte(content), mode)
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
	idx := strings.IndexAny(t, "=:")
	if idx < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(t[:idx]))
}

func valueOf(line string) string {
	idx := strings.IndexAny(line, "=:")
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

func (f *iniFile) get(section, key string) (string, bool) {
	start, end := f.sectionRange(section)
	for i := start; start >= 0 && i < end; i++ {
		if keyOf(f.lines[i]) == key {
			return valueOf(f.lines[i]), true
		}
	}
	return "", false
}

// set replaces the key's line in place, or appends it to the section, creating the section
// at the end of the file when needed.
func (f *iniFile) set(section, key, value string) {
	start, end := f.sectionRange(section)
	if start < 0 {
		if len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) != "" {
			f.lines = append(f.lines, "")
		}
		f.lines = append(f.lines, sectionOpen+section+sectionClose)
		start, end = len(f.lines), len(f.lines)
	}
	entry := key + " " + keyValueDelimiter + " " + value
	for i := start; i < end; i++ {
		if keyOf(f.lines[i]) == key {
			f.lines[i] = entry
			return
		}
	}
	// Insert before the blank lines that separate this section from the next.
	insert := end
	for insert > start && strings.TrimSpace(f.lines[insert-1]) == "" {
		insert--
	}
	f.lines = append(f.lines[:insert], append([]string{entry}, f.lines[insert:]...)...)
}

func (f *iniFile) unset(section, key string) {
	start, end := f.sectionRange(section)
	for i := start; start >= 0 && i < end; i++ {
		if keyOf(f.lines[i]) == key {
			f.lines = append(f.lines[:i], f.lines[i+1:]...)
			return
		}
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

// profileSummary is one row of "profile list".
type profileSummary struct {
	Name     string `json:"name"`
	Auth     string `json:"auth"`
	Instance string `json:"instance"`
	ID       string `json:"id"`
	Active   bool   `json:"active"`
}

func summarize(f *iniFile, name, active string) profileSummary {
	s := profileSummary{Name: name, Active: name == active}
	s.Instance, _ = f.get(name, keyInstance)
	if id, ok := f.get(name, keyClientID); ok && id != "" {
		s.Auth, s.ID = "oauth", id
	} else if id, ok := f.get(name, keyID); ok && id != "" {
		s.Auth, s.ID = "token", id
	} else {
		s.Auth = "none"
	}
	return s
}

func profileNotFound(f *iniFile, name string) error {
	names := f.sections()
	if len(names) == 0 {
		return fmt.Errorf("profile %q not found; %s has no profiles yet", name, f.path)
	}
	return fmt.Errorf("profile %q not found in %s; profiles: %s", name, f.path, strings.Join(names, ", "))
}
