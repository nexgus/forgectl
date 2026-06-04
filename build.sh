#!/bin/bash
# 為所有支援的平台編譯 forgectl.
#
# 目標平台: windows/amd64, linux/amd64, linux/arm64, darwin/arm64.
#
# 每個 binary 以 forgectl-<version>-<os>-<arch> 的命名寫入 bin/ (Windows 加 .exe 副檔名).
# 並為當前主機平台建立一個短 symlink (bin/forgectl, Windows 主機則為 bin/forgectl.exe),
# 指向對應的 binary 以便直接呼叫.
#
# Usage: bash build.sh

# pipefail 使管道中任一階段失敗時整個管道即失敗; -e 在第一個未處理的錯誤時退出.
set -eo pipefail

# 切換至腳本所在目錄 (repo 根目錄), 使腳本可從任意位置執行.
cd "$(dirname "${BASH_SOURCE[0]}")"

# BIN 是輸出 binary 的名稱, 也是 cmd/ 子目錄的名稱.
# MODULE 是 Go module 路徑, 用於 -ldflags 注入時定位 version package.
BIN=forgectl
MODULE=forgectl

# 拒絕未知引數, 不予略過.
if [ "$#" -gt 0 ]; then
    echo "Error: unexpected argument: $1" >&2
    echo "Usage: bash build.sh" >&2
    exit 1
fi

# 在編譯時注入至 ${MODULE}/pkg/version 的版本後設資料. 若在 git checkout 之外或尚無任何
# commit, COMMIT 回退為 "unknown".
COMMIT=$(git describe --match=NeVeRmAtCh --always --abbrev=8 --dirty 2>/dev/null || echo "unknown")
GOVER=$(go version | cut -d ' ' -f 3)
VER=$(grep 'const String' pkg/version/version.go | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$VER" ]; then
    echo "Error: failed to extract the version from pkg/version/version.go." >&2
    exit 1
fi

# BuildDate 是編譯機器的本地時間, 格式為帶時區偏移的 ISO 8601
# (例如 2026-06-04T08:19:01+0800). 此值擷取一次後, 本次編譯的所有 binary 都帶有相同時間戳記.
BUILDDATE=$(date +%Y-%m-%dT%H:%M:%S%z)

LDFLAGS="-w \
-X ${MODULE}/pkg/version.GitCommitHash=${COMMIT} \
-X ${MODULE}/pkg/version.GoVersion=${GOVER} \
-X ${MODULE}/pkg/version.BuildDate=${BUILDDATE}"

# 目標平台, 以 "os/arch" 配對表示.
TARGETS=(
    "windows/amd64"
    "linux/amd64"
    "linux/arm64"
    "darwin/arm64"
)

# build_target 將一個 os/arch 配對編譯至 bin/. forgectl 不需要 cgo, 故一律以
# CGO_ENABLED=0 編譯. Linux 目標因此為完全靜態連結, 不依賴 libc, 可跨發行版執行;
# Windows 的 PE 不含 C runtime. macOS 為平台限制的例外: Apple 僅允許經由
# libSystem.B.dylib 進入 syscall, 不支援完全靜態的執行檔, 故 darwin 目標必然動態
# 連結 libSystem (以及 TLS 會用到的 CoreFoundation / Security 等) 系統函式庫.
function build_target {
    local os="$1" arch="$2"
    local out="bin/${BIN}-${VER}-${os}-${arch}"
    if [ "$os" = "windows" ]; then
        out="${out}.exe"
    fi
    echo "Building ${os}/${arch} -> ${out} ..."
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
        -ldflags "${LDFLAGS}" -o "${out}" "./cmd/${BIN}" \
        || { echo "Error: failed to build ${os}/${arch}." >&2; exit 1; }
}

# mklink 為主機平台建立一個短 symlink, 指向對應的 binary (前提是該 binary 已被編譯).
function mklink {
    local os arch suffix=""
    os=$(uname | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    case "$arch" in
        x86_64) arch="amd64" ;;
        arm64 | aarch64) arch="arm64" ;;
    esac
    [ "$os" = "windows" ] && suffix=".exe"

    local binary="${BIN}-${VER}-${os}-${arch}${suffix}"
    if [ -f "bin/${binary}" ]; then
        ln -fs "${binary}" "bin/${BIN}${suffix}"
    fi
}

echo "Building ${BIN} ${VER} (${COMMIT}) with ${GOVER}."
mkdir -p bin

for target in "${TARGETS[@]}"; do
    build_target "${target%/*}" "${target#*/}"
done

mklink

echo
echo "Built binaries:"
for f in bin/*; do
    [ -L "$f" ] && continue
    [ -f "$f" ] && echo "  $(basename "$f")"
done

echo
echo "Symlinks:"
for f in bin/*; do
    [ -L "$f" ] || continue
    echo "  $(basename "$f") -> $(readlink "$f")"
done
