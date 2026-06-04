package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfig points the credential search at fresh, empty temporary
// directories on every platform so a test never reads (or is influenced by) the
// developer's real credential file. It uses t.Setenv, so callers cannot run in
// parallel.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
}

// writeCredential writes a credential file into the first directory the search
// would consult on this platform, after isolateConfig has redirected it.
func writeCredential(t *testing.T, name, content string) {
	t.Helper()
	dirs := credentialDirs()
	if len(dirs) == 0 {
		t.Fatal("no credential directories on this platform")
	}
	dir := dirs[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAuthHierarchical(t *testing.T) {
	isolateConfig(t)
	writeCredential(t, "credential.toml", `
["github.com"]
token = "ghp_file"

["gitlab.corp.com"]
token = "glpat_file"
user = "deploy"
`)

	a, err := resolveAuth(Config{Source: "github"}, "github.com")
	if err != nil {
		t.Fatalf("resolveAuth github: %v", err)
	}
	if a.Token != "ghp_file" {
		t.Errorf("github token = %q, want ghp_file", a.Token)
	}
	if a.User != "" {
		t.Errorf("github user = %q, want empty", a.User)
	}
	if !strings.Contains(a.TokenSource, "credential file") {
		t.Errorf("github TokenSource = %q, want it to mention the credential file", a.TokenSource)
	}

	b, err := resolveAuth(Config{Source: "gitlab"}, "gitlab.corp.com")
	if err != nil {
		t.Fatalf("resolveAuth gitlab: %v", err)
	}
	if b.Token != "glpat_file" || b.User != "deploy" {
		t.Errorf("gitlab (token, user) = (%q, %q), want (glpat_file, deploy)", b.Token, b.User)
	}
}

func TestResolveAuthFlat(t *testing.T) {
	isolateConfig(t)
	writeCredential(t, "credential.yaml", "token: flat_tok\nuser: flat_user\n")

	// A flat file applies to every host.
	a, err := resolveAuth(Config{Source: "github"}, "anything.example.com")
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if a.Token != "flat_tok" || a.User != "flat_user" {
		t.Errorf("(token, user) = (%q, %q), want (flat_tok, flat_user)", a.Token, a.User)
	}
}

func TestResolveAuthFlagOverride(t *testing.T) {
	isolateConfig(t)
	writeCredential(t, "credential.json", `{"token":"file_tok","user":"file_user"}`)

	a, err := resolveAuth(Config{Source: "github", Token: "flag_tok", User: "flag_user"}, "github.com")
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if a.Token != "flag_tok" {
		t.Errorf("token = %q, want flag_tok (flag overrides file)", a.Token)
	}
	if a.User != "flag_user" {
		t.Errorf("user = %q, want flag_user (flag overrides file)", a.User)
	}
	if !strings.Contains(a.TokenSource, "--token flag") {
		t.Errorf("TokenSource = %q, want it to mention the flag", a.TokenSource)
	}
}

func TestResolveAuthTokenFileOverride(t *testing.T) {
	isolateConfig(t)
	writeCredential(t, "credential.toml", `token = "file_tok"`)

	// A token file whose body is padded with whitespace, a BOM, and a
	// zero-width space; only the token itself should survive.
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("  \n\t glpat-XYZ \n")...)
	raw = append(raw, 0xE2, 0x80, 0x8B) // trailing zero-width space
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := resolveAuth(Config{Source: "gitlab", TokenFile: tf}, "gitlab.com")
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if a.Token != "glpat-XYZ" {
		t.Errorf("token = %q, want glpat-XYZ (trimmed)", a.Token)
	}
	if !strings.Contains(a.TokenSource, "--token-file") {
		t.Errorf("TokenSource = %q, want it to mention --token-file", a.TokenSource)
	}
}

func TestResolveAuthNoCredentials(t *testing.T) {
	isolateConfig(t)
	// No file, no flags: anonymous is valid, not an error.
	a, err := resolveAuth(Config{Source: "github"}, "github.com")
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if a.Token != "" || a.TokenSource != "none" {
		t.Errorf("(token, source) = (%q, %q), want (\"\", none)", a.Token, a.TokenSource)
	}
}

func TestResolveAuthMultipleFilesError(t *testing.T) {
	isolateConfig(t)
	writeCredential(t, "credential.toml", `token = "a"`)
	writeCredential(t, "credential.yaml", "token: b\n")
	if _, err := resolveAuth(Config{Source: "github"}, "github.com"); err == nil {
		t.Error("resolveAuth should error when a directory holds multiple credential files")
	}
}

func TestParseCredentialFormats(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, ext, data string }{
		{"toml", "toml", "token = \"t\"\nuser = \"u\"\n"},
		{"yaml", "yaml", "token: t\nuser: u\n"},
		{"json", "json", `{"token":"t","user":"u"}`},
	}
	for _, tc := range cases {
		tok, usr, err := parseCredential([]byte(tc.data), tc.ext, "github.com")
		if err != nil || tok != "t" || usr != "u" {
			t.Errorf("%s: (token, user, err) = (%q, %q, %v), want (t, u, nil)", tc.name, tok, usr, err)
		}
	}

	// Hierarchical file that does not list the target host yields no
	// credentials, not an error.
	tok, _, err := parseCredential([]byte(`{"other.com":{"token":"x"}}`), "json", "github.com")
	if err != nil || tok != "" {
		t.Errorf("unlisted host: (token, err) = (%q, %v), want (\"\", nil)", tok, err)
	}
}

func TestParseCredentialMixedError(t *testing.T) {
	t.Parallel()
	data := "token = \"x\"\n[\"github.com\"]\ntoken = \"y\"\n"
	if _, _, err := parseCredential([]byte(data), "toml", "github.com"); err == nil {
		t.Error("parseCredential should error when flat and hierarchical forms are mixed")
	}
}
