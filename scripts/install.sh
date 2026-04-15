#!/usr/bin/env bash
#
# install.sh — build and install squash-ide to ~/.local/bin (by default).
#
# Usage:
#   ./scripts/install.sh                    # install to ~/.local/bin
#   BIN_DIR=/usr/local/bin ./scripts/install.sh
#   VERSION=v0.1.0 ./scripts/install.sh     # stamp an explicit version
#
# Requires: go (1.24+).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
BINARY="${BINARY:-squash-ide}"

if ! command -v go >/dev/null 2>&1; then
    echo "error: go is not installed or not on PATH" >&2
    exit 1
fi

# Resolve version from git tag if not set explicitly.
if [ -z "${VERSION:-}" ]; then
    if git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
        VERSION="$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
    else
        VERSION="dev"
    fi
fi

echo "building $BINARY ($VERSION) ..."
mkdir -p "$REPO_ROOT/bin"
CGO_ENABLED=0 go build \
    -C "$REPO_ROOT" \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$REPO_ROOT/bin/$BINARY" \
    ./cmd/squash-ide

mkdir -p "$BIN_DIR"
install -m 0755 "$REPO_ROOT/bin/$BINARY" "$BIN_DIR/$BINARY"

echo "installed $BIN_DIR/$BINARY"
case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "warning: $BIN_DIR is not on your PATH. Add it to your shell rc:"
       echo "  export PATH=\"$BIN_DIR:\$PATH\""
       ;;
esac
