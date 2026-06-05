//go:build windows

package selfinstall

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// windowsLayout 描述 Windows 的安裝落點. Windows 沒有預設就在 PATH 的系統 bin 目錄,
// 故把 vendor 目錄本身加入機器層級 PATH; 穩定入口 forgectl.exe 以 hard link 建在同一
// 目錄 (同 volume, 不需 symlink 特權). cmd 下輸入 forgectl 會由 PATHEXT 解析為 forgectl.exe.
type windowsLayout struct {
	vendorParent string
	vendorDir    string
	stablePath   string // vendorDir\forgectl.exe
	requirePriv  bool   // 是否強制系統管理員權限 (測試關閉)
}

func defaultWindowsLayout() windowsLayout {
	base := os.Getenv("ProgramFiles")
	if base == "" {
		base = `C:\Program Files`
	}
	parent := filepath.Join(base, vendorNamespace)
	dir := filepath.Join(parent, toolName)
	return windowsLayout{
		vendorParent: parent,
		vendorDir:    dir,
		stablePath:   filepath.Join(dir, toolName+".exe"),
		requirePriv:  true,
	}
}

func runInstall(w io.Writer) error {
	src, err := selfRealPath()
	if err != nil {
		return err
	}
	return defaultWindowsLayout().install(w, src, canonicalName())
}

func runUninstall(w io.Writer) error {
	return defaultWindowsLayout().uninstall(w)
}

// install 執行 Windows 的安裝流程; 任一步失敗即中止並回傳錯誤. src 為複製來源, name 為
// 落點檔名 (正式安裝固定為 canonicalName, 不取 src 的 basename); 測試可注入兩者.
func (lo windowsLayout) install(w io.Writer, src, name string) error {
	if lo.requirePriv && !isElevated() {
		return fmt.Errorf("安裝需要系統管理員權限, 請以 \"以系統管理員身分執行\" 重新執行")
	}

	// 1. 確保 vendor 目錄存在.
	if err := os.MkdirAll(lo.vendorDir, 0o755); err != nil {
		return fmt.Errorf("建立目錄 %q: %w", lo.vendorDir, err)
	}

	// 2. 將自己複製進 vendor 目錄, 落點檔名固定為標準名 (改版多次則累積多個版本檔).
	dst := filepath.Join(lo.vendorDir, name)
	if err := copyExecutable(src, dst); err != nil {
		return err
	}
	fmt.Fprintf(w, "已複製: %s\n", dst)

	// 3. 處理既有的穩定入口. hard link 與一般檔在 Windows 上無從區分, 故一律先刪再建.
	if _, err := os.Lstat(lo.stablePath); err == nil {
		fmt.Fprintf(w, "移除既有檔案: %s\n", lo.stablePath)
		if err := os.Remove(lo.stablePath); err != nil {
			return fmt.Errorf("移除 %q: %w", lo.stablePath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("檢視 %q: %w", lo.stablePath, err)
	}

	// 4. 以 hard link 建立穩定入口, 指向本次安裝的版本檔.
	if err := os.Link(dst, lo.stablePath); err != nil {
		return fmt.Errorf("建立 hard link %q -> %q: %w", lo.stablePath, dst, err)
	}
	fmt.Fprintf(w, "已建立 hard link: %s -> %s\n", lo.stablePath, dst)

	// 5. 將 vendor 目錄加入機器層級 PATH (若尚未加入).
	added, err := addToMachinePath(lo.vendorDir)
	if err != nil {
		return fmt.Errorf("更新系統 PATH: %w", err)
	}
	if added {
		fmt.Fprintf(w, "已將 %s 加入系統 PATH.\n", lo.vendorDir)
	} else {
		fmt.Fprintf(w, "系統 PATH 已含 %s.\n", lo.vendorDir)
	}

	fmt.Fprintf(w, "安裝完成, 請開啟新的終端機後即可於任何位置執行 %s.\n", toolName)
	return nil
}

// uninstall 執行 Windows 的移除流程; 個別步驟失敗只警告不中止, 期間有錯則最終回傳非 nil.
func (lo windowsLayout) uninstall(w io.Writer) error {
	if lo.requirePriv && !isElevated() {
		return fmt.Errorf("移除需要系統管理員權限, 請以 \"以系統管理員身分執行\" 重新執行")
	}

	var failed bool
	warn := func(format string, args ...any) {
		failed = true
		fmt.Fprintf(w, "警告: "+format+"\n", args...)
	}

	// 1. 刪除整個 vendor 目錄 (含全部版本檔與穩定入口 hard link).
	switch _, err := os.Stat(lo.vendorDir); {
	case err == nil:
		if err := os.RemoveAll(lo.vendorDir); err != nil {
			warn("移除 %q 失敗: %v", lo.vendorDir, err)
		} else {
			fmt.Fprintf(w, "已移除目錄: %s\n", lo.vendorDir)
		}
	case os.IsNotExist(err):
		fmt.Fprintf(w, "略過: %s 不存在\n", lo.vendorDir)
	default:
		warn("檢視 %q 失敗: %v", lo.vendorDir, err)
	}

	// 2. 命名空間層僅在已空時收掉 (可能有其他工具共用), 永不動其上層 (Program Files).
	if err := os.Remove(lo.vendorParent); err == nil {
		fmt.Fprintf(w, "已移除空目錄: %s\n", lo.vendorParent)
	}

	// 3. 自機器層級 PATH 移除 vendor 目錄.
	if removed, err := removeFromMachinePath(lo.vendorDir); err != nil {
		warn("更新系統 PATH 失敗: %v", err)
	} else if removed {
		fmt.Fprintf(w, "已自系統 PATH 移除: %s\n", lo.vendorDir)
	}

	if failed {
		return fmt.Errorf("移除過程發生錯誤")
	}
	fmt.Fprintf(w, "移除完成, PATH 變更於新終端機生效.\n")
	return nil
}

// isElevated 透過 shell32!IsUserAnAdmin 判斷是否以系統管理員權限執行.
func isElevated() bool {
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin")
	if err := proc.Find(); err != nil {
		return false
	}
	ret, _, _ := proc.Call()
	return ret != 0
}

// machineEnvKey 是機器層級環境變數所在的登錄機碼.
const machineEnvKey = `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

// readMachinePath 以 reg 讀取機器層級 PATH 的原始值與其登錄型別.
func readMachinePath() (value, regType string, err error) {
	out, err := exec.Command("reg", "query", machineEnvKey, "/v", "Path").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("reg query: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// 目標列形如: "    Path    REG_EXPAND_SZ    C:\\Windows;C:\\Windows\\System32"
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "Path") {
			continue
		}
		i := strings.Index(line, "REG_")
		if i < 0 {
			continue
		}
		rest := line[i:] // "REG_EXPAND_SZ    <value>"
		if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
			return strings.TrimSpace(rest[sp:]), strings.TrimSpace(rest[:sp]), nil
		}
		return "", strings.TrimSpace(rest), nil // 空值的 PATH
	}
	return "", "", fmt.Errorf("reg query 輸出未含 Path 值")
}

// writeMachinePath 以 reg 寫回機器層級 PATH; regType 為空時預設 REG_EXPAND_SZ.
func writeMachinePath(value, regType string) error {
	if regType == "" {
		regType = "REG_EXPAND_SZ"
	}
	out, err := exec.Command("reg", "add", machineEnvKey, "/v", "Path", "/t", regType, "/d", value, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// addToMachinePath 把 dir 加入機器層級 PATH (若尚未存在), 回傳是否實際變更.
func addToMachinePath(dir string) (bool, error) {
	value, regType, err := readMachinePath()
	if err != nil {
		return false, err
	}
	for _, p := range strings.Split(value, ";") {
		if strings.EqualFold(strings.TrimSpace(p), dir) {
			return false, nil // 已在 PATH 上.
		}
	}
	next := value
	if next != "" && !strings.HasSuffix(next, ";") {
		next += ";"
	}
	next += dir
	if err := writeMachinePath(next, regType); err != nil {
		return false, err
	}
	return true, nil
}

// removeFromMachinePath 自機器層級 PATH 移除等於 dir 的項目, 回傳是否實際變更.
func removeFromMachinePath(dir string) (bool, error) {
	value, regType, err := readMachinePath()
	if err != nil {
		return false, err
	}
	parts := strings.Split(value, ";")
	kept := make([]string, 0, len(parts))
	removed := false
	for _, p := range parts {
		if strings.EqualFold(strings.TrimSpace(p), dir) {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return false, nil
	}
	if err := writeMachinePath(strings.Join(kept, ";"), regType); err != nil {
		return false, err
	}
	return true, nil
}
