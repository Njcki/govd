package handlers

import (
	"os"
	"path/filepath"
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
