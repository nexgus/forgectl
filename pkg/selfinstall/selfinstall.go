// Package selfinstall 將正在執行的 forgectl binary 安裝到本機系統, 並建立一個
// 穩定的 forgectl 入口, 使其可在任何位置直接執行; uninstall 則完整移除.
//
// 此套件只處理本機檔案系統與 OS 層面的安裝, 與 GitHub / GitLab 平台無關, 故獨立於
// pkg/forge 之外. 各平台的落點與連結方式不同, 平台相依的流程分置於 selfinstall_unix.go
// 與 selfinstall_windows.go; 本檔為跨平台共用的入口與輔助函式.
package selfinstall

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"forgectl/pkg/version"
)

// vendorNamespace 是安裝目錄的命名空間層 (供應者識別), 各平台共用.
// toolName 是工具與穩定入口的名稱.
const (
	vendorNamespace = "augustus.sanchung"
	toolName        = "forgectl"
)

// Install 將目前執行的 binary 安裝到系統並建立 forgectl 入口. 進度訊息寫至 w.
// 任一步失敗即中止並回傳錯誤 (呼叫端據此以非 0 退出).
func Install(w io.Writer) error { return runInstall(w) }

// Uninstall 移除已安裝的檔案與 forgectl 入口. 個別步驟失敗時印出警告但不中止, 繼續
// 後續步驟; 若期間發生任何錯誤, 最終回傳非 nil (呼叫端據此以非 0 退出).
func Uninstall(w io.Writer) error { return runUninstall(w) }

// canonicalName 回傳本 binary 在 vendor 目錄內應有的標準檔名, 與 build.sh 的產物逐字
// 一致: forgectl-<version>-<os>-<arch>, Windows 另加 .exe. 名稱由編譯期事實
// (version.String, runtime.GOOS, runtime.GOARCH) 直接構造, 不讀取 os.Executable() 的
// 磁碟檔名 -- 故無論透過 symlink / hard link 執行, 或使用者把下載檔改了名, 落點檔名都正確
// 且唯一. 來源位元組仍取自正在執行的 binary 本身, 其身分 (版本 / 平台 / 架構) 無從造假.
func canonicalName() string {
	name := fmt.Sprintf("%s-%s-%s-%s", toolName, version.String, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// selfRealPath 回傳目前執行檔解析過 symlink 後的真實路徑, 作為複製來源. 安裝後再次執行
// 時, 入口可能是個 symlink, 解析後才會指回 vendor 內的版本檔 (hard link 無可跟隨的目標,
// 本身即實體檔). 注意: 來源只決定複製的位元組, 落點檔名一律由 canonicalName 決定.
func selfRealPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("無法取得自身執行檔路徑: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("無法解析自身執行檔路徑 %q: %w", exe, err)
	}
	return real, nil
}

// copyExecutable 以 "寫入同目錄暫存檔再 rename" 的方式把 src 複製到 dst, 盡量讓 dst
// 的更新具原子性, 並設為 0755. dst 已存在時先移除再 rename (Windows 的 rename 不覆寫
// 既有檔案). 注意: 不可在 dst 正被執行時覆寫它 (Windows 會回報共享違規).
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("開啟來源 %q: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".forgectl-*")
	if err != nil {
		return fmt.Errorf("建立暫存檔: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 成功 rename 後此處為 no-op; 失敗時清掉暫存檔.

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("複製內容至暫存檔: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("設定暫存檔權限: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("關閉暫存檔: %w", err)
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("移除既有檔案 %q: %w", dst, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("放置 %q: %w", dst, err)
	}
	return nil
}

// pathContainsDir 判斷 PATH 形式的環境變數值是否包含 dir (clean 後比對; Windows 不分
// 大小寫).
func pathContainsDir(pathEnv, dir string) bool {
	want := filepath.Clean(dir)
	for _, p := range filepath.SplitList(pathEnv) {
		if p == "" {
			continue
		}
		got := filepath.Clean(p)
		if runtime.GOOS == "windows" {
			if strings.EqualFold(got, want) {
				return true
			}
		} else if got == want {
			return true
		}
	}
	return false
}

// isWithin 判斷 path 是否等於 dir 或位於 dir 之下 (clean 後比對).
func isWithin(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
