package main

// 本檔為 <version> 位置參數提供 semver 守門. release create / asset upload /
// asset download 都以版本字串為 join key, 漏填時後面的 <path> / <pattern> 會被當成
// 版本; 把版本欄位宣告為以下型別, kong 便在解析階段呼叫 Validate 攔截, 在送出任何
// 請求前放棄執行.

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// latestArg 是 asset download 的 <version> 接受的特殊值, 代表最新的正式 release;
// 與 pkg/forge 內對 "latest" 的處理一致.
const latestArg = "latest"

// isSemver 回報 s 是否為合法的版本字串. 驗證委由 golang.org/x/mod/semver, 它要求
// 前綴 "v"; 為同時接受帶與不帶 "v" 的寫法 (例如 v1.2.3 與 1.2.3), 缺前綴時先補上再
// 驗證. s 本身不被改寫 — 只用於判定真偽, 送往平台的仍是使用者輸入的原字串.
func isSemver(s string) bool {
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return semver.IsValid(s)
}

// Version 是 release create 與 asset upload 的 <version> 位置參數型別.
type Version string

// Validate 由 kong 在解析時呼叫, 確認該位置確實是一個版本.
func (v Version) Validate() error {
	if !isSemver(string(v)) {
		return fmt.Errorf("%q 不是合法的版本; 是否漏填了 <version>?", string(v))
	}
	return nil
}

// VersionOrLatest 是 asset download 的 <version> 位置參數型別: 與 Version 相同,
// 但額外接受特殊值 "latest".
type VersionOrLatest string

// Validate 由 kong 在解析時呼叫.
func (v VersionOrLatest) Validate() error {
	if string(v) == latestArg || isSemver(string(v)) {
		return nil
	}
	return fmt.Errorf("%q 不是合法的版本, 也不是 %q; 是否漏填了 <version>?", string(v), latestArg)
}
