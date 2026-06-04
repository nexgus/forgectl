#!/bin/bash
# 執行 forgectl 的靜態檢查與測試套件.
#
# 不帶引數時, 依序執行完整本地關卡:
#   1. gofmt check  — 若任何 Go 檔案未通過 gofmt 格式化即失敗 (不重寫任何檔案).
#   2. go vet       — 回報可疑的程式碼結構.
#   3. go test      — 對整個 module 啟用 race detector 與 coverage 執行測試.
#
# 若提供引數, 這些引數將直接傳遞給 `go test`, 可在開發時縮小執行範圍:
#   bash test.sh ./pkg/cli
#   bash test.sh -run TestReleaseList ./pkg/cli
#   bash test.sh -v ./...
#
# Usage: bash test.sh [go test arguments...]

# pipefail 使管道中任一階段失敗時整個管道即失敗; -e 在第一個未處理的錯誤時退出.
set -eo pipefail

# 切換至腳本所在目錄 (repo 根目錄), 使腳本可從任意位置執行.
cd "$(dirname "${BASH_SOURCE[0]}")"

# 若提供引數, 將完整控制權交給 `go test`: 由呼叫者決定旗標與目標,
# 並跳過 gofmt/vet 前置檢查.
if [ "$#" -gt 0 ]; then
    exec go test "$@"
fi

echo "gofmt: checking formatting ..."
unformatted=$(gofmt -l .)
if [ -n "${unformatted}" ]; then
    echo "Error: the following files are not gofmt-clean:" >&2
    echo "${unformatted}" >&2
    echo "Run: gofmt -w ${unformatted}" >&2
    exit 1
fi

echo "go vet: examining packages ..."
go vet ./...

echo "go test: running the suite (race + coverage) ..."
go test -race -cover ./...

echo
echo "All checks passed."
