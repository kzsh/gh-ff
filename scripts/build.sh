#!/usr/bin/env bash
#
# Cross-compile the release targets into dist/ as gh-ff-<goos>-<goarch>.
#
# gh-ff is pure Go — stdlib plus gopkg.in/yaml.v3, no cgo — so every platform
# comes off one host with GOOS/GOARCH and no Docker or remote builder.
# CGO_ENABLED=0 produces a static Linux binary, which is what a gnu/musl split
# exists to produce: one artifact per OS/arch serves both.
#
# publish.sh uploads whatever dist/ holds, so build every target you mean to
# release before publishing it.
#
# Requirements: the Go version named in go.mod, and git for the version
# stamp. The version is the tag on HEAD (see publish.sh); anywhere else it is
# git's describe output, so a non-release build says so in --version.
#
# Usage:
#   ./scripts/build.sh
#   ./scripts/build.sh --target linux/amd64 --target darwin/arm64
#   DIST=out ./scripts/build.sh          # override output directory

set -euo pipefail

cd "$(dirname "$0")/.."

BIN="gh-ff"
PKG="./cmd/gh-ff"
DIST="${DIST:-dist}"

DEFAULT_TARGETS=(
    linux/amd64
    linux/arm64
    darwin/arm64
)

usage() {
    cat <<'EOF'
Usage: build.sh [OPTIONS]

  --target OS/ARCH   Build only this target (repeatable). Defaults to
                     linux/amd64, linux/arm64, darwin/arm64.
  -h, --help         Show this message

Output goes to dist/, or to $DIST when set.
EOF
}

requested=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --target)
            requested+=("${2:?--target requires a value}")
            shift 2
            ;;
        -h | --help)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ ${#requested[@]} -gt 0 ]]; then
    targets=("${requested[@]}")
else
    targets=("${DEFAULT_TARGETS[@]}")
fi

# "v0.1.0" on a clean tagged commit; "v0.1.0-3-g1a2b3c4" or a "-dirty"
# suffix everywhere else. Commit date rather than build date, so the stamp is
# reproducible.
# --match keeps unrelated tags (latest, nightly) out of the stamp, so it is
# either a version tag or a version tag plus a describe suffix.
VERSION="$(git describe --tags --dirty --match 'v[0-9]*' 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(git show -s --format=%cI HEAD 2>/dev/null || echo unknown)"
LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

mkdir -p "$DIST"
echo "version: $VERSION ($COMMIT)"

for target in "${targets[@]}"; do
    case "$target" in
        */*) ;;
        *)
            echo "error: --target wants OS/ARCH, got $target" >&2
            exit 2
            ;;
    esac
    os="${target%/*}"
    arch="${target#*/}"
    out="$DIST/$BIN-$os-$arch"
    echo "==> $os/$arch"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="$LDFLAGS" -o "$out" "$PKG"
    echo "   -> $out"
done

echo ""
echo "Artifacts in $DIST/:"
ls -lh "$DIST/$BIN-"*
