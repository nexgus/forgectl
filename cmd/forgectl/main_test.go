package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
)

// parse 將真實語法套用至 args, 並綁定至全新的 CLI,
// 使每個測試互相獨立且可平行執行.
func parse(t *testing.T, args ...string) (*CLI, *kong.Context, error) {
	t.Helper()
	var c CLI
	parser, err := newParser(&c)
	if err != nil {
		t.Fatalf("newParser: %v", err)
	}
	ctx, err := parser.Parse(args)
	return &c, ctx, err
}

func TestReleaseList(t *testing.T) {
	t.Parallel()
	c, ctx, err := parse(t, "--source", "github", "release", "list", "owner/repo", "--json")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Source != "github" {
		t.Errorf("Source = %q, want github", c.Source)
	}
	if c.Release.List.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", c.Release.List.Repo)
	}
	if !c.Release.List.JSON {
		t.Error("JSON = false, want true")
	}
	if got := ctx.Command(); got != "release list <repo>" {
		t.Errorf("Command() = %q, want %q", got, "release list <repo>")
	}
}

// TestPing 固定 ping 的語法: 攜帶全域旗標, 不接受位置參數.
func TestPing(t *testing.T) {
	t.Parallel()
	t.Run("flags", func(t *testing.T) {
		t.Parallel()
		c, ctx, err := parse(t, "--source", "gitlab", "--host", "https://gitlab.example.com", "--insecure", "ping")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.Source != "gitlab" {
			t.Errorf("Source = %q, want gitlab", c.Source)
		}
		if c.Host != "https://gitlab.example.com" {
			t.Errorf("Host = %q, want https://gitlab.example.com", c.Host)
		}
		if !c.Insecure {
			t.Error("Insecure = false, want true")
		}
		if got := ctx.Command(); got != "ping" {
			t.Errorf("Command() = %q, want %q", got, "ping")
		}
	})
	t.Run("rejects positional arg", func(t *testing.T) {
		t.Parallel()
		if _, _, err := parse(t, "--source", "github", "ping", "owner/repo"); err == nil {
			t.Error("ping with a positional argument should error")
		}
	})
}

// TestSelfNoSourceRequired 固定 self 指令豁免 --source: 它只操作本機 forgectl, 與
// 託管平台無關, 故不帶 --source 也應解析成功.
func TestSelfNoSourceRequired(t *testing.T) {
	t.Parallel()
	for _, sub := range []string{"install", "uninstall"} {
		t.Run(sub, func(t *testing.T) {
			t.Parallel()
			_, ctx, err := parse(t, "self", sub)
			if err != nil {
				t.Fatalf("parse self %s: %v", sub, err)
			}
			if got, want := ctx.Command(), "self "+sub; got != want {
				t.Errorf("Command() = %q, want %q", got, want)
			}
		})
	}
}

func TestSourceValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"missing", []string{"release", "list", "r"}, true},
		{"invalid enum", []string{"--source", "bitbucket", "release", "list", "r"}, true},
		{"github", []string{"--source", "github", "release", "list", "r"}, false},
		{"gitlab", []string{"--source", "gitlab", "release", "list", "r"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parse(t, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// TestReleaseCreateNoteXor 固定 xor:"note" 約束: --note 與 --note-file 必須且只能擇一.
func TestReleaseCreateNoteXor(t *testing.T) {
	t.Parallel()
	base := []string{"--source", "github", "release", "create", "r", "v1"}
	tests := []struct {
		name    string
		extra   []string
		wantErr bool
	}{
		{"note only", []string{"--note", "hi"}, false},
		{"note-file only", []string{"--note-file", "/tmp/notes.md"}, false},
		{"neither", nil, true},
		{"both", []string{"--note", "hi", "--note-file", "/tmp/notes.md"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := append(append([]string{}, base...), tt.extra...)
			_, _, err := parse(t, args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestAssetUploadPaths(t *testing.T) {
	t.Parallel()
	c, ctx, err := parse(t, "--source", "github", "asset", "upload", "r", "v1", "a.bin=alpha", "b.bin")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"a.bin=alpha", "b.bin"}
	if got := c.Asset.Upload.Paths; !equal(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
	if got := ctx.Command(); got != "asset upload <repo> <version> <path>" {
		t.Errorf("Command() = %q", got)
	}
}

// TestAssetDownloadOptionalPattern 固定可變參數 pattern 為選填.
func TestAssetDownloadOptionalPattern(t *testing.T) {
	t.Parallel()
	t.Run("no pattern", func(t *testing.T) {
		t.Parallel()
		c, ctx, err := parse(t, "--source", "github", "asset", "download", "r", "v1")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(c.Asset.Download.Patterns) != 0 {
			t.Errorf("Patterns = %v, want empty", c.Asset.Download.Patterns)
		}
		if got := ctx.Command(); got != "asset download <repo> <version>" {
			t.Errorf("Command() = %q", got)
		}
	})
	t.Run("with patterns", func(t *testing.T) {
		t.Parallel()
		c, _, err := parse(t, "--source", "github", "asset", "download", "r", "v1", "*.bin", "*.zip")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := c.Asset.Download.Patterns; !equal(got, []string{"*.bin", "*.zip"}) {
			t.Errorf("Patterns = %v, want [*.bin *.zip]", got)
		}
	})
}

// TestRunDispatch 驗證每個指令的 Run 能正確串接至 forge:
// 每個 Run 從全域旗標建立 client 並觸及對應的處理器.
// 將 host 指向已關閉的位址, 使各處理器在建立 client 並嘗試實際操作後才報錯
// (連線被拒, 或對於 upload 而言, 相同路徑之後的工作).
func TestRunDispatch(t *testing.T) {
	// 隔離 credential 搜尋路徑, 避免測試讀取真實檔案.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())

	// 立即關閉的 server: 其位址沒有任何監聽者, 任何網路呼叫都會被迅速拒絕.
	srv := httptest.NewServer(http.NewServeMux())
	dead := srv.URL
	srv.Close()

	g := &Globals{Source: "github", Host: dead, Token: "x"}
	missing := filepath.Join(t.TempDir(), "nope.bin")
	runs := map[string]func() error{
		"release list":   func() error { return (&ReleaseListCmd{Repo: "o/r"}).Run(g) },
		"release create": func() error { return (&ReleaseCreateCmd{Repo: "o/r", Version: "v1", Note: "n"}).Run(g) },
		"asset upload":   func() error { return (&AssetUploadCmd{Repo: "o/r", Version: "v1", Paths: []string{missing}}).Run(g) },
		"asset download": func() error { return (&AssetDownloadCmd{Repo: "o/r", Version: "v1"}).Run(g) },
	}
	for name, run := range runs {
		if err := run(); err == nil {
			t.Errorf("%s: expected an error against a closed host", name)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
