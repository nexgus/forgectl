package selfinstall

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"forgectl/pkg/version"
)

// TestCanonicalName 固定落點檔名與 build.sh 的產物一致 (forgectl-<version>-<os>-<arch>,
// Windows 加 .exe), 且只由編譯期事實構造, 不受執行檔的磁碟檔名影響.
func TestCanonicalName(t *testing.T) {
	want := "forgectl-" + version.String + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got := canonicalName(); got != want {
		t.Errorf("canonicalName() = %q, want %q", got, want)
	}
}

func TestCopyExecutable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	// 預先放一個既有 dst, 驗證會被覆寫.
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyExecutable(src, dst); err != nil {
		t.Fatalf("copyExecutable: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("內容 = %q, want hello", string(got))
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("權限 = %v, want 0755", info.Mode().Perm())
	}
}

func TestPathContainsDir(t *testing.T) {
	sep := string(os.PathListSeparator)
	pathEnv := "/usr/bin" + sep + "/usr/local/bin" + sep + "/sbin"
	if !pathContainsDir(pathEnv, "/usr/local/bin") {
		t.Error("應判定 /usr/local/bin 在 PATH 上")
	}
	if !pathContainsDir(pathEnv, "/usr/local/bin/") {
		t.Error("尾端斜線經 clean 後應仍判定在 PATH 上")
	}
	if pathContainsDir(pathEnv, "/opt/bin") {
		t.Error("不應誤判 /opt/bin 在 PATH 上")
	}
}

func TestIsWithin(t *testing.T) {
	cases := []struct {
		dir, path string
		want      bool
	}{
		{"/opt/augustus.sanchung/forgectl", "/opt/augustus.sanchung/forgectl/forgectl-1.0.0", true},
		{"/opt/augustus.sanchung/forgectl", "/opt/augustus.sanchung/forgectl", true},
		{"/opt/augustus.sanchung/forgectl", "/opt/augustus.sanchung/other/x", false},
		{"/opt/augustus.sanchung/forgectl", "/usr/local/bin/forgectl", false},
		// 前綴相同但非子目錄, 不應命中.
		{"/opt/a", "/opt/ab", false},
	}
	for _, c := range cases {
		if got := isWithin(c.dir, c.path); got != c.want {
			t.Errorf("isWithin(%q, %q) = %v, want %v", c.dir, c.path, got, c.want)
		}
	}
}
