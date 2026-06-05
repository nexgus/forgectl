//go:build darwin

package selfinstall

import (
	"fmt"
	"os/exec"
	"strings"
)

const quarantineAttr = "com.apple.quarantine"

// clearQuarantine 移除 macOS 的 com.apple.quarantine 擴充屬性, 否則 Gatekeeper 會阻擋
// 未簽章的下載檔執行. 先以 xattr -p 探測屬性是否存在: 不存在 (例如本機自建的 binary) 時
// 直接視為成功, 不誤判為錯誤; 存在才以 xattr -d 移除.
func clearQuarantine(path string) error {
	if err := exec.Command("/usr/bin/xattr", "-p", quarantineAttr, path).Run(); err != nil {
		return nil // 屬性不存在或無法讀取, 無需清除.
	}
	if out, err := exec.Command("/usr/bin/xattr", "-d", quarantineAttr, path).CombinedOutput(); err != nil {
		return fmt.Errorf("xattr -d 失敗: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
