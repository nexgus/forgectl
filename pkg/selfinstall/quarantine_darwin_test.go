//go:build darwin

package selfinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestClearQuarantineDarwin 在真實 macOS 上驗證 clearQuarantine: 設上隔離屬性後清除,
// 並確認屬性已不存在; 另確認對沒有該屬性的檔案呼叫不會報錯.
func TestClearQuarantineDarwin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 無屬性時應視為成功 (no-op).
	if err := clearQuarantine(p); err != nil {
		t.Fatalf("對無隔離屬性的檔案應成功, got %v", err)
	}

	// 設上隔離屬性.
	set := exec.Command("/usr/bin/xattr", "-w", quarantineAttr, "0081;00000000;test;", p)
	if out, err := set.CombinedOutput(); err != nil {
		t.Skipf("無法設定隔離屬性, 略過 (可能環境限制): %v: %s", err, out)
	}
	// 確認確實有屬性.
	if err := exec.Command("/usr/bin/xattr", "-p", quarantineAttr, p).Run(); err != nil {
		t.Fatalf("前置: 應已有隔離屬性: %v", err)
	}

	// 清除後應不再存在.
	if err := clearQuarantine(p); err != nil {
		t.Fatalf("clearQuarantine: %v", err)
	}
	if err := exec.Command("/usr/bin/xattr", "-p", quarantineAttr, p).Run(); err == nil {
		t.Error("清除後隔離屬性仍存在")
	}
}
