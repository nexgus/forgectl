#!/bin/bash
# Build forgectl for every supported platform.
#
# Targets: windows/amd64, linux/amd64, linux/arm64, darwin/arm64.
#
# Each binary is written to bin/ as forgectl-<version>-<os>-<arch> (with a
# .exe suffix on Windows). A short symlink for the host platform
# (bin/forgectl, or bin/forgectl.exe on a Windows host) points at the matching
# binary for convenience.
#
# Usage: bash build.sh

# pipefail makes a pipeline fail if any stage fails; -e exits on the first
# unhandled error.
set -eo pipefail

# Change to the script's directory (the repo root) so the script can be run
# from any location.
cd "$(dirname "${BASH_SOURCE[0]}")"

# BIN is the output binary name and the cmd/ subdirectory name.
# MODULE is the Go module path, used to address the version package for
# -ldflags injection.
BIN=forgectl
MODULE=forgectl

# Reject unknown arguments rather than ignoring them.
if [ "$#" -gt 0 ]; then
    echo "Error: unexpected argument: $1" >&2
    echo "Usage: bash build.sh" >&2
    exit 1
fi

# Version metadata injected into ${MODULE}/pkg/version at build time. COMMIT
# falls back to "unknown" outside a git checkout or before the first commit.
COMMIT=$(git describe --match=NeVeRmAtCh --always --abbrev=8 --dirty 2>/dev/null || echo "unknown")
GOVER=$(go version | cut -d ' ' -f 3)
VER=$(grep 'const String' pkg/version/version.go | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$VER" ]; then
    echo "Error: failed to extract the version from pkg/version/version.go." >&2
    exit 1
fi

# BuildDate is the build machine's local time in ISO 8601 with a timezone
# offset (for example 2026-06-04T08:19:01+0800). It is captured once so that
# every binary from this build carries the same timestamp.
BUILDDATE=$(date +%Y-%m-%dT%H:%M:%S%z)

LDFLAGS="-w \
-X ${MODULE}/pkg/version.GitCommitHash=${COMMIT} \
-X ${MODULE}/pkg/version.GoVersion=${GOVER} \
-X ${MODULE}/pkg/version.BuildDate=${BUILDDATE}"

# Target platforms as "os/arch" pairs.
TARGETS=(
    "windows/amd64"
    "linux/amd64"
    "linux/arm64"
    "darwin/arm64"
)

# build_target compiles one os/arch pair into bin/. forgectl needs no cgo, so
# every target is built with CGO_ENABLED=0 for a static, cross-compilable
# binary.
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

# mklink creates a short symlink for the host platform that points at the
# matching binary, when such a binary was built.
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
