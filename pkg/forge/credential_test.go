package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfig 在每個平台都將 credential 搜尋路徑指向全新的暫存目錄,
// 使測試不會讀取 (或受影響於) 開發者真實的 credential file.
// 使用 t.Setenv, 因此呼叫端不可平行執行.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
}

// writeCredential 在 isolateConfig 重導後, 將 credential file 寫入此平台
// 搜尋順序中的第一個目錄.
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

	// 扁平格式的 credential file 適用所有 host.
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
	if !strings.Contains(a.TokenSource, "--token 旗標") {
		t.Errorf("TokenSource = %q, want it to mention the flag", a.TokenSource)
	}
}

func TestResolveAuthTokenFileOverride(t *testing.T) {
	isolateConfig(t)
	writeCredential(t, "credential.toml", `token = "file_tok"`)

	// token file 內容含有空白、BOM 與零寬空格作為 padding;
	// 解析後只應保留 token 本體.
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
	// 無檔案、無旗標: 匿名使用合法, 不應回傳 error.
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

	// 階層格式中未列出的 host 應回傳空值, 而非 error.
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
