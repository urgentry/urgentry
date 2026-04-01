#!/usr/bin/env bash
# Build release binaries, archives, and checksums for multiple platforms.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION=${1:-${VERSION:-dev}}
PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
OUTPUT_DIR="dist"

mkdir -p "$OUTPUT_DIR"
rm -f "$OUTPUT_DIR"/SHA256SUMS

checksum_file() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1"
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1"
    else
        echo "need shasum or sha256sum on PATH" >&2
        exit 1
    fi
}

for platform in $PLATFORMS; do
    os="${platform%/*}"
    arch="${platform#*/}"
    artifact="urgentry-${VERSION}-${os}-${arch}"
    output="$OUTPUT_DIR/$artifact"
    binary_name="urgentry"
    if [ "$os" = "windows" ]; then
        output="${output}.exe"
        binary_name="urgentry.exe"
    fi

    echo "Building $os/$arch..."
    GOOS=$os GOARCH=$arch VERSION="$VERSION" bash ./scripts/build-urgentry.sh --output "$output"

    stage_dir="$(mktemp -d)"
    cp "$output" "$stage_dir/$binary_name"
    archive="$OUTPUT_DIR/$artifact.tar.gz"
    tar -C "$stage_dir" -czf "$archive" "$binary_name"
    rm -rf "$stage_dir"
done

(
    cd "$OUTPUT_DIR"
    for file in *; do
        if [ ! -f "$file" ]; then
            continue
        fi
        if [ "$file" = "SHA256SUMS" ]; then
            continue
        fi
        checksum_file "$file"
    done > SHA256SUMS
)

echo "Release binaries:"
ls -lh "$OUTPUT_DIR/"
