#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT_DIR"

# shellcheck disable=SC1091
. "$ROOT_DIR/scripts/lib-paths.sh"
resolve_urgentry_paths "$0"

OUTPUT="urgentry"
PKG="./cmd/urgentry"
VERSION_FILE_FALLBACK="$(cat "$REPO_ROOT/VERSION" 2>/dev/null || echo dev)"
BUILD_VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "$VERSION_FILE_FALLBACK")}"
BUILD_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
BUILD_TAGS="${URGENTRY_BUILD_TAGS:-netgo,osusergo}"
BUILD_STRIP="${URGENTRY_BUILD_STRIP:-1}"
BUILD_TRIMPATH="${URGENTRY_BUILD_TRIMPATH:-1}"
BUILD_OMIT_VCS_STAMP="${URGENTRY_BUILD_OMIT_VCS_STAMP:-1}"
BUILD_GCFLAGS="${URGENTRY_BUILD_GCFLAGS:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      OUTPUT="$2"
      shift 2
      ;;
    --pkg)
      PKG="$2"
      shift 2
      ;;
    --version)
      BUILD_VERSION="$2"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [ -z "${CGO_ENABLED:-}" ]; then
  CGO_ENABLED=0
  export CGO_ENABLED
fi

LDFLAGS="-X urgentry/internal/config.Version=${BUILD_VERSION} -X urgentry/internal/config.Commit=${BUILD_COMMIT} -X urgentry/internal/config.BuildDate=${BUILD_DATE}"
if [ "$BUILD_STRIP" = "1" ]; then
  LDFLAGS="-s -w ${LDFLAGS}"
fi

set -- build
if [ "$BUILD_TRIMPATH" = "1" ]; then
  set -- "$@" -trimpath
fi
if [ "$BUILD_OMIT_VCS_STAMP" = "1" ]; then
  set -- "$@" -buildvcs=false
fi
if [ -n "$BUILD_TAGS" ]; then
  set -- "$@" -tags "$BUILD_TAGS"
fi
if [ -n "$BUILD_GCFLAGS" ]; then
  set -- "$@" -gcflags "$BUILD_GCFLAGS"
fi
set -- "$@" -ldflags "$LDFLAGS" -o "$OUTPUT" "$PKG"

go "$@"
