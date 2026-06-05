//go:build !windows

package selfinstall

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// tempLayout 回傳指向暫存目錄的 unixLayout, 並關閉 root 檢查, 以在不需 root 的情況下
// 完整驗證安裝 / 移除流程.
func tempLayout(t *testing.T) unixLayout {
	t.Helper()
	root := t.TempDir()
	parent := filepath.Join(root, "opt", vendorNamespace)
	return unixLayout{
		vendorParent: parent,
		vendorDir:    filepath.Join(parent, toolName),
		linkDir:      filepath.Join(root, "bin"),
		linkPath:     filepath.Join(root, "bin", toolName),
		requireRoot:  false,
	}
}

func writeFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnixInstallThenUninstall(t *testing.T) {
	lo := tempLayout(t)
	// 來源檔名刻意與落點名不同 (模擬經由 symlink / hard link 執行, 或下載檔被改名),
	// 驗證落點檔名取決於傳入的 name 而非 src 的 basename.
	src := writeFakeBinary(t, t.TempDir(), "renamed-download")
	const name = "forgectl-9.9.9-linux-amd64"

	var buf bytes.Buffer
	if err := lo.install(&buf, src, name); err != nil {
		t.Fatalf("install: %v", err)
	}

	dst := filepath.Join(lo.vendorDir, name)
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("預期複製出的版本檔 %s: %v", dst, err)
	}
	target, err := os.Readlink(lo.linkPath)
	if err != nil {
		t.Fatalf("預期入口為 symlink %s: %v", lo.linkPath, err)
	}
	if target != dst {
		t.Errorf("symlink target = %q, want %q", target, dst)
	}

	buf.Reset()
	if err := lo.uninstall(&buf); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(lo.vendorDir); !os.IsNotExist(err) {
		t.Errorf("vendorDir 應已移除, stat err = %v", err)
	}
	if _, err := os.Lstat(lo.linkPath); !os.IsNotExist(err) {
		t.Errorf("入口應已移除, lstat err = %v", err)
	}
	// vendorParent 已空, 應一併收掉.
	if _, err := os.Stat(lo.vendorParent); !os.IsNotExist(err) {
		t.Errorf("空的 vendorParent 應已移除, stat err = %v", err)
	}
}

// TestUnixInstallAccumulatesVersions 固定設計: 多次安裝累積版本檔, 入口指向最新.
func TestUnixInstallAccumulatesVersions(t *testing.T) {
	lo := tempLayout(t)
	srcDir := t.TempDir()
	var buf bytes.Buffer

	src := writeFakeBinary(t, srcDir, "download")
	if err := lo.install(&buf, src, "forgectl-1.0.0-linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if err := lo.install(&buf, src, "forgectl-2.0.0-linux-amd64"); err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{"forgectl-1.0.0-linux-amd64", "forgectl-2.0.0-linux-amd64"} {
		if _, err := os.Stat(filepath.Join(lo.vendorDir, n)); err != nil {
			t.Errorf("預期版本檔 %s 仍在: %v", n, err)
		}
	}
	target, _ := os.Readlink(lo.linkPath)
	if want := filepath.Join(lo.vendorDir, "forgectl-2.0.0-linux-amd64"); target != want {
		t.Errorf("入口 target = %q, want %q (應指向最新)", target, want)
	}
}

// TestUnixUninstallPreservesForeign 固定安全設計: 命名空間層有其他工具時保留, 入口若非
// 指向本工具 vendor 則保留, 並回報非 0.
func TestUnixUninstallPreservesForeign(t *testing.T) {
	lo := tempLayout(t)
	srcDir := t.TempDir()
	var buf bytes.Buffer

	src := writeFakeBinary(t, srcDir, "download")
	if err := lo.install(&buf, src, "forgectl-1.0.0-linux-amd64"); err != nil {
		t.Fatal(err)
	}

	// 命名空間層下放一個其他工具目錄, 模擬共用.
	sibling := filepath.Join(lo.vendorParent, "othertool")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	// 把入口換成指向別處 (非本工具 vendor) 的 symlink.
	if err := os.Remove(lo.linkPath); err != nil {
		t.Fatal(err)
	}
	foreign := writeFakeBinary(t, srcDir, "other")
	if err := os.Symlink(foreign, lo.linkPath); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	err := lo.uninstall(&buf)
	if err == nil {
		t.Errorf("uninstall 保留外來入口時應回報非 nil")
	}
	// 外來入口應保留.
	if got, _ := os.Readlink(lo.linkPath); got != foreign {
		t.Errorf("外來入口應保留指向 %q, got %q", foreign, got)
	}
	// 命名空間層因仍有 sibling 而保留.
	if _, err := os.Stat(lo.vendorParent); err != nil {
		t.Errorf("仍有其他工具時, vendorParent 應保留: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Errorf("其他工具目錄應保留: %v", err)
	}
}
