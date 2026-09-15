package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetscapeHasSessionID(t *testing.T) {
	ok := `# Netscape HTTP Cookie File
.instagram.com	TRUE	/	TRUE	0	sessionid	abc123
.instagram.com	TRUE	/	FALSE	0	ds_user_id	1
`
	if !netscapeHasSessionID(ok) {
		t.Fatal("expected sessionid present")
	}
	bad := `# Netscape HTTP Cookie File
.instagram.com	TRUE	/	FALSE	0	csrftoken	xyz
`
	if netscapeHasSessionID(bad) {
		t.Fatal("expected sessionid absent")
	}
	spaced := `# Netscape HTTP Cookie File
.instagram.com TRUE / TRUE 0 sessionid abc123
`
	if !netscapeHasSessionID(spaced) {
		t.Fatal("expected sessionid in space-separated jar")
	}
}

func TestNormalizeNetscapeTabs(t *testing.T) {
	spaced := "# Netscape HTTP Cookie File\n" +
		".instagram.com TRUE / TRUE 1893456000 sessionid abc%3Adef\n" +
		".instagram.com TRUE / TRUE 1893456000 ds_user_id 42\n"
	out := normalizeNetscapeTabs(spaced)
	if !netscapeHasSessionID(out) {
		t.Fatal("sessionid lost")
	}
	want1 := ".instagram.com\tTRUE\t/\tTRUE\t1893456000\tsessionid\tabc%3Adef"
	want2 := ".instagram.com\tTRUE\t/\tTRUE\t1893456000\tds_user_id\t42"
	if !stringsContainsLine(out, want1) || !stringsContainsLine(out, want2) {
		t.Fatalf("missing tab lines in:\n%s", out)
	}
	tabbed := ".instagram.com\tTRUE\t/\tTRUE\t1893456000\tsessionid\tx\n"
	if got := normalizeNetscapeTabs(tabbed); got != tabbed {
		t.Fatalf("tabbed mutated: %q -> %q", tabbed, got)
	}
}

func TestInstallIGCookieContentConvertsSpaces(t *testing.T) {
	dir := t.TempDir()
	orig := igCookiePath
	igCookiePath = filepath.Join(dir, "instagram.txt")
	t.Cleanup(func() { igCookiePath = orig })

	spaced := "# Netscape HTTP Cookie File\n.instagram.com TRUE / TRUE 0 sessionid abc%3Adef\n"
	if err := installIGCookieContent(spaced); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(igCookiePath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "\t") {
		t.Fatalf("expected tabs, got:\n%s", s)
	}
	if !netscapeHasSessionID(s) {
		t.Fatal("sessionid missing after install")
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 && parts[5] == "sessionid" {
			if parts[4] == "0" || parts[4] == "" {
				t.Fatalf("expiry not normalized: %q", parts[4])
			}
		}
	}
}

func TestIGCredentialsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := igCredentialsPath
	igCredentialsPath = filepath.Join(dir, "ig_credentials.env")
	t.Cleanup(func() { igCredentialsPath = orig })

	if err := upsertIGCredential(igCredKeyUsername, "demo_user"); err != nil {
		t.Fatalf("username: %v", err)
	}
	if err := upsertIGCredential(igCredKeyPassword, "secret-pass"); err != nil {
		t.Fatalf("password: %v", err)
	}

	creds, err := readIGCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if creds[igCredKeyUsername] != "demo_user" {
		t.Fatalf("username=%q", creds[igCredKeyUsername])
	}
	if creds[igCredKeyPassword] != "secret-pass" {
		t.Fatalf("password mismatch")
	}

	info, err := os.Stat(igCredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("credentials file too permissive: %o", info.Mode().Perm())
	}

	if err := deleteIGCredentials(); err != nil {
		t.Fatal(err)
	}
	if fileExists(igCredentialsPath) {
		t.Fatal("credentials should be deleted")
	}
}

func stringsContainsLine(s, want string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
