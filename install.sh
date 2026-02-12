#!/bin/sh
# b installer — https://github.com/fentas/b
# Usage: curl -sSL https://github.com/fentas/b/releases/latest/download/install.sh | bash
set -e

REPO="fentas/b"
INSTALL_DIR="${B_INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  freebsd) OS="freebsd" ;;
  mingw*|msys*|cygwin*) OS="windows" ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  armv7*|armv6*)  ARCH="armv${ARCH#armv}" ; ARCH="${ARCH%%l*}" ;;
  i386|i686)      ARCH="386" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Determine file extension
EXT="tar.gz"
if [ "$OS" = "windows" ]; then
  EXT="zip"
fi

# Resolve latest version from GitHub API
LATEST=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')
if [ -z "$LATEST" ]; then
  echo "Failed to determine latest version" >&2
  exit 1
fi

ASSET="b-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/v${LATEST}/${ASSET}.${EXT}"

echo "Installing b v${LATEST} (${OS}/${ARCH})..."

# Create install directory
mkdir -p "$INSTALL_DIR"

# Download and extract
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if [ "$EXT" = "zip" ]; then
  curl -sSL "$URL" -o "$TMP/b.zip"
  unzip -q "$TMP/b.zip" -d "$TMP"
else
  curl -sSL "$URL" | tar xz -C "$TMP"
fi

# Install binary
cp "$TMP/b" "$INSTALL_DIR/b" 2>/dev/null || cp "$TMP/b.exe" "$INSTALL_DIR/b.exe" 2>/dev/null
chmod +x "$INSTALL_DIR/b" 2>/dev/null || true

echo "Installed b to $INSTALL_DIR/b"

# Check if install dir is in PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "Add b to your PATH by adding the following to your shell profile:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
