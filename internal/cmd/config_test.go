package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const legacyProfiles = `# written by the Monte Carlo CLI
[default]
mcd_id = legacy-id
mcd_token = legacy-token
mcd_agent_image_host = docker.io

[staging]
mcd_id: staging-id
mcd_token: staging-token
`

func writeProfiles(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(profilesPath(dir), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestINIRoundTripPreservesForeignLines(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	f.set("default", keyClientID, "cid")
	f.set("default", keySecret, "secret")
	f.unset("default", keyID)
	f.unset("default", keyToken)
	if err := f.save(credentialsMode); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(profilesPath(dir))
	want := `# written by the Monte Carlo CLI
[default]
mcd_agent_image_host = docker.io
mcd_oauth_client_id = cid
mcd_oauth_client_secret = secret

[staging]
mcd_id: staging-id
mcd_token: staging-token
`
	if string(got) != want {
		t.Fatalf("file after edit:\n%s\nwant:\n%s", got, want)
	}
}

func TestINIGetIsCaseInsensitiveOnKeysAndAcceptsColons(t *testing.T) {
	dir := writeProfiles(t, "[p]\nMCD_ID = A\nmcd_token: B\n")
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := f.get("p", keyID); !ok || v != "A" {
		t.Fatalf("mcd_id = %q, %v", v, ok)
	}
	if v, ok := f.get("p", keyToken); !ok || v != "B" {
		t.Fatalf("mcd_token = %q, %v", v, ok)
	}
	if _, ok := f.get("missing", keyID); ok {
		t.Fatal("a missing section reported a key")
	}
}

func TestINISetAppendsANewSectionAfterABlankLine(t *testing.T) {
	dir := writeProfiles(t, "[a]\nk = 1\n")
	f, _ := loadINI(profilesPath(dir))
	f.set("b", "k", "2")
	if err := f.save(credentialsMode); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(profilesPath(dir))
	if string(got) != "[a]\nk = 1\n\n[b]\nk = 2\n" {
		t.Fatalf("got:\n%s", got)
	}
}

func TestINISaveCreatesTheDirectoryWithTheGivenMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh", ".mcd")
	f, _ := loadINI(profilesPath(dir))
	f.set("default", keyID, "x")
	if err := f.save(credentialsMode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != credentialsMode {
		t.Fatalf("mode %o, want %o", info.Mode().Perm(), credentialsMode)
	}
}

func TestSaveTightensModesLeftByAnotherTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not meaningful on Windows")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, ".mcd")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := profilesPath(dir)
	if err := os.WriteFile(path, []byte("[default]\nmcd_id = old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := loadINI(path)
	if err != nil {
		t.Fatal(err)
	}
	f.set("default", keyID, "new")
	if err := f.save(credentialsMode); err != nil {
		t.Fatal(err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != credentialsMode {
		t.Fatalf("file mode = %o, want %o", fileInfo.Mode().Perm(), credentialsMode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != configDirMode {
		t.Fatalf("dir mode = %o, want %o", dirInfo.Mode().Perm(), configDirMode)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".profiles-") {
			t.Fatalf("save left a temp file behind: %s", e.Name())
		}
	}
}

func TestActiveProfileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if name, err := activeProfile(dir); err != nil || name != "" {
		t.Fatalf("fresh dir: %q, %v", name, err)
	}
	if err := setActiveProfile(dir, "dev"); err != nil {
		t.Fatal(err)
	}
	name, err := activeProfile(dir)
	if err != nil || name != "dev" {
		t.Fatalf("after set: %q, %v", name, err)
	}
	got, _ := os.ReadFile(cliPath(dir))
	if !strings.Contains(string(got), "[defaults]\nprofile = dev") {
		t.Fatalf("cli.ini:\n%s", got)
	}
}

func TestSetCollapsesDuplicateKeysAndUnsetRemovesAll(t *testing.T) {
	dir := writeProfiles(t, "[default]\nmcd_token = first\nmcd_id = kept\nmcd_token = second\n")
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	f.set("default", keyToken, "new")
	tokenLines := 0
	for _, line := range f.lines {
		if keyOf(line) == keyToken {
			tokenLines++
			if valueOf(line) != "new" {
				t.Fatalf("token line: %q", line)
			}
		}
	}
	if tokenLines != 1 {
		t.Fatalf("%d lines for mcd_token after set, want 1", tokenLines)
	}

	f.unset("default", keyToken)
	for _, line := range f.lines {
		if keyOf(line) == keyToken {
			t.Fatalf("mcd_token line survived unset: %q", line)
		}
	}
	if v, ok := f.get("default", keyID); !ok || v != "kept" {
		t.Fatalf("unrelated key: %q, %v", v, ok)
	}
}

func TestSaveRoundTripsCRLFLineEndingsOnUntouchedLines(t *testing.T) {
	dir := writeProfiles(t, "[default]\r\nmcd_id = old\r\nmcd_agent_image_host = docker.io\r\n")
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	f.set("default", keyID, "new")
	if err := f.save(credentialsMode); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(profilesPath(dir))
	want := "[default]\r\nmcd_id = new\nmcd_agent_image_host = docker.io\r\n"
	if string(got) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestValueKeepsAnInlineSemicolonVerbatim(t *testing.T) {
	dir := writeProfiles(t, "[p]\nmcd_id = abc ; not a comment here\n")
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := f.get("p", keyID); !ok || v != "abc ; not a comment here" {
		t.Fatalf("got %q, %v", v, ok)
	}
}

func TestProfileNotFoundEmptyFileMessage(t *testing.T) {
	dir := t.TempDir()
	f, err := loadINI(profilesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	err = profileNotFound(f, "dev")
	if err == nil || !strings.Contains(err.Error(), "has no profiles yet") {
		t.Fatalf("err = %v", err)
	}
}

func TestProfileListWithNoProfilesFile(t *testing.T) {
	dir := t.TempDir()
	out, err := execute(t, "profile", "list", "--config-dir", dir, "--output", "table")
	if err != nil {
		t.Fatal(err)
	}
	if want := "NAME  AUTH  INSTANCE  ID  ACTIVE\n"; out != want {
		t.Fatalf("table:\n%q\nwant:\n%q", out, want)
	}

	out, err = execute(t, "profile", "list", "--config-dir", dir, "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("json: %q", out)
	}
}

func TestSummarizeReportsTheMechanism(t *testing.T) {
	dir := writeProfiles(t, "[o]\nmcd_oauth_client_id = c\nmcd_oauth_client_secret = s\nmcd_instance_id = us1\n[t]\nmcd_id = i\nmcd_token = k\n[n]\n")
	f, _ := loadINI(profilesPath(dir))
	o := summarize(f, "o", "o")
	if o.Auth != "oauth" || o.ID != "c" || o.Instance != "us1" || !o.Active {
		t.Fatalf("oauth summary: %+v", o)
	}
	tk := summarize(f, "t", "o")
	if tk.Auth != "token" || tk.ID != "i" || tk.Active {
		t.Fatalf("token summary: %+v", tk)
	}
	if n := summarize(f, "n", "o"); n.Auth != "none" {
		t.Fatalf("empty summary: %+v", n)
	}
}
