package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// flagString is the flag's value, read from a file when it starts with @.
func flagString(cmd *cobra.Command, name string) (string, error) {
	v, err := cmd.Flags().GetString(name)
	if err != nil {
		return "", err
	}
	return expandValue(v)
}

func flagInt(cmd *cobra.Command, name string) (int32, error) {
	return cmd.Flags().GetInt32(name)
}

func flagBool(cmd *cobra.Command, name string) (bool, error) {
	return cmd.Flags().GetBool(name)
}

func flagStringSlice(cmd *cobra.Command, name string) ([]string, error) {
	return cmd.Flags().GetStringSlice(name)
}

// flagStringMap parses repeated key=value entries, or a single @<path> holding a JSON object.
func flagStringMap(cmd *cobra.Command, name string) (map[string]string, error) {
	entries, err := cmd.Flags().GetStringArray(name)
	if err != nil {
		return nil, err
	}
	if len(entries) == 1 && strings.HasPrefix(entries[0], "@") {
		raw, err := readValueFile(entries[0][1:])
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", name, err)
		}
		out := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, fmt.Errorf("--%s: %s does not hold a JSON object of strings: %w", name, entries[0][1:], err)
		}
		return out, nil
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--%s: %q is not key=value", name, entry)
		}
		out[key] = value
	}
	return out, nil
}

// flagSecret is the flag's value, or a hidden terminal prompt when --<name>-prompt is set.
func flagSecret(cmd *cobra.Command, name string) (string, error) {
	prompt := name + "-prompt"
	if cmd.Flags().Lookup(prompt) == nil || !changed(cmd, prompt) {
		return flagString(cmd, name)
	}
	if changed(cmd, name) {
		return "", fmt.Errorf("pass --%s or --%s, not both", name, prompt)
	}
	return readSecret(name)
}

// readSecret prompts on the terminal with echo off. Without a terminal there is nothing to
// prompt on, and the caller is told to use @<path> instead.
func readSecret(name string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("--%s-prompt needs a terminal; pass --%s @<path> instead", name, name)
	}
	fmt.Fprintf(os.Stderr, "%s: ", name)
	secret, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(secret), nil
}

// changed reports whether any of the flags was passed on the command line.
func changed(cmd *cobra.Command, names ...string) bool {
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// requireAny errors unless at least one of the flags was passed.
func requireAny(cmd *cobra.Command, names ...string) error {
	if changed(cmd, names...) {
		return nil
	}
	if len(names) == 1 {
		return fmt.Errorf("--%s is required", names[0])
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "--" + name
	}
	return fmt.Errorf("one of %s or %s is required", strings.Join(quoted[:len(quoted)-1], ", "), quoted[len(quoted)-1])
}

// expandValue reads the file a @<path> value names; any other value passes through.
func expandValue(v string) (string, error) {
	if !strings.HasPrefix(v, "@") {
		return v, nil
	}
	return readValueFile(v[1:])
}

// readValueFile returns the file's contents without its trailing newline.
func readValueFile(path string) (string, error) {
	path, err := expandHome(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}
