//go:build !windows

package selfinstall

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// unixLayout 描述 Linux / macOS 的安裝落點. 正式安裝使用 defaultUnixLayout; 測試可改用
// 暫存目錄並關閉權限檢查, 以在不需 root 的情況下完整驗證流程.
type unixLayout struct {
	vendorParent string // 命名空間層; uninstall 僅在其為空時收掉, 永不動其上層 (/opt)
	vendorDir    string // 實際存放各版本 binary 的目錄
	linkDir      string // 穩定入口所在目錄 (須在 PATH 上)
	linkPath     string // 穩定入口 (symlink) 的完整路徑
	requireRoot  bool   // 是否強制 root 權限 (測試關閉)
}

func defaultUnixLayout() unixLayout {
	parent := filepath.Join("/opt", vendorNamespace)
	return unixLayout{
		vendorParent: parent,
		vendorDir:    filepath.Join(parent, toolName),
		linkDir:      "/usr/local/bin",
		linkPath:     filepath.Join("/usr/local/bin", toolName),
		requireRoot:  true,
	}
}

func runInstall(w io.Writer) error {
	src, err := selfRealPath()
	if err != nil {
		return err
	}
	return defaultUnixLayout().install(w, src, canonicalName())
}

func runUninstall(w io.Writer) error {
	return defaultUnixLayout().uninstall(w)
}

// install 執行 Linux / macOS 的安裝流程; 任一步失敗即中止並回傳錯誤. src 為複製來源,
// name 為落點檔名 (正式安裝固定為 canonicalName, 不取 src 的 basename); 測試可注入兩者.
func (lo unixLayout) install(w io.Writer, src, name string) error {
	if lo.requireRoot && os.Geteuid() != 0 {
		return fmt.Errorf("安裝需要 root 權限, 請以 sudo 重新執行")
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

	// 3. (僅 macOS) 清除下載隔離屬性, 否則 Gatekeeper 會阻擋未簽章的 binary 執行.
	if err := clearQuarantine(dst); err != nil {
		return fmt.Errorf("清除隔離屬性 %q: %w", dst, err)
	}

	// 4. 處理既有的穩定入口: 一般檔直接刪; symlink 顯示其指向後再刪.
	if err := lo.removeExistingLink(w); err != nil {
		return err
	}

	// 5. 建立穩定入口 symlink, 指向本次安裝的版本檔.
	if err := os.MkdirAll(lo.linkDir, 0o755); err != nil {
		return fmt.Errorf("建立目錄 %q: %w", lo.linkDir, err)
	}
	if err := os.Symlink(dst, lo.linkPath); err != nil {
		return fmt.Errorf("建立連結 %q -> %q: %w", lo.linkPath, dst, err)
	}
	fmt.Fprintf(w, "已建立連結: %s -> %s\n", lo.linkPath, dst)

	// 6. 確認入口目錄在 PATH 上; 不在則僅警告, 不擅自修改使用者的 shell 設定.
	if !pathContainsDir(os.Getenv("PATH"), lo.linkDir) {
		fmt.Fprintf(w, "警告: %s 不在 PATH 上, 請將它加入 PATH 後重新開啟終端機.\n", lo.linkDir)
	}

	fmt.Fprintf(w, "安裝完成, 現在可於任何位置執行 %s.\n", toolName)
	return nil
}

// removeExistingLink 移除安裝目標位置既有的入口 (若有), 並印出其性質供使用者確認.
func (lo unixLayout) removeExistingLink(w io.Writer) error {
	info, err := os.Lstat(lo.linkPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("檢視 %q: %w", lo.linkPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(lo.linkPath)
		fmt.Fprintf(w, "移除既有連結: %s -> %s\n", lo.linkPath, target)
	} else {
		fmt.Fprintf(w, "移除既有檔案: %s\n", lo.linkPath)
	}
	if err := os.Remove(lo.linkPath); err != nil {
		return fmt.Errorf("移除 %q: %w", lo.linkPath, err)
	}
	return nil
}

// uninstall 執行 Linux / macOS 的移除流程; 個別步驟失敗只警告不中止, 期間有錯則最終回傳非 nil.
func (lo unixLayout) uninstall(w io.Writer) error {
	if lo.requireRoot && os.Geteuid() != 0 {
		return fmt.Errorf("移除需要 root 權限, 請以 sudo 重新執行")
	}

	var failed bool
	warn := func(format string, args ...any) {
		failed = true
		fmt.Fprintf(w, "警告: "+format+"\n", args...)
	}

	// 1. 刪除整個 vendor 目錄 (含全部版本檔).
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

	// 2. 命名空間層僅在已空時收掉 (可能有其他工具共用), 永不動其上層 (/opt).
	//    非空或不存在皆屬正常, 不視為錯誤.
	if err := os.Remove(lo.vendorParent); err == nil {
		fmt.Fprintf(w, "已移除空目錄: %s\n", lo.vendorParent)
	}

	// 3. 移除穩定入口, 但僅限指向本工具 vendor 目錄的 symlink, 避免誤刪同名的他人安裝.
	lo.removeOwnedLink(w, warn)

	if failed {
		return fmt.Errorf("移除過程發生錯誤")
	}
	fmt.Fprintf(w, "移除完成.\n")
	return nil
}

// removeOwnedLink 只在 linkPath 確為指向本工具 vendor 目錄的 symlink 時移除它; 否則保留並警告.
func (lo unixLayout) removeOwnedLink(w io.Writer, warn func(string, ...any)) {
	info, err := os.Lstat(lo.linkPath)
	if os.IsNotExist(err) {
		fmt.Fprintf(w, "略過: %s 不存在\n", lo.linkPath)
		return
	}
	if err != nil {
		warn("檢視 %q 失敗: %v", lo.linkPath, err)
		return
	}
	if info.Mode()&os.ModeSymlink == 0 {
		warn("%s 是一般檔案而非本工具建立的連結, 為求安全予以保留", lo.linkPath)
		return
	}
	target, err := os.Readlink(lo.linkPath)
	if err != nil {
		warn("讀取連結 %q 失敗: %v", lo.linkPath, err)
		return
	}
	if !isWithin(lo.vendorDir, target) {
		warn("%s 指向 %s, 非本工具 vendor 目錄, 為求安全予以保留", lo.linkPath, target)
		return
	}
	if err := os.Remove(lo.linkPath); err != nil {
		warn("移除 %q 失敗: %v", lo.linkPath, err)
		return
	}
	fmt.Fprintf(w, "已移除連結: %s\n", lo.linkPath)
}
