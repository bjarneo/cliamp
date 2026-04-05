#!/bin/sh
set -e

REPO="bjarneo/cliamp"

# Determine install directory: prefer ~/.local/bin (no sudo), fall back to /usr/local/bin
if [ -z "$INSTALL_DIR" ]; then
    LOCAL_BIN="$HOME/.local/bin"
    if echo "$PATH" | tr ':' '\n' | grep -qx "$LOCAL_BIN"; then
        mkdir -p "$LOCAL_BIN"
        INSTALL_DIR="$LOCAL_BIN"
    else
        INSTALL_DIR="/usr/local/bin"
    fi
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

case "$OS" in
    linux|darwin) ;;
    mingw*|msys*|cygwin*) OS="windows" ;;
    *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

BINARY="cliamp-${OS}-${ARCH}"
if [ "$OS" = "windows" ]; then
    BINARY="${BINARY}.exe"
fi

URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"

echo "Downloading ${BINARY}..."
TMP=$(mktemp)
if command -v curl > /dev/null; then
    curl -fSL -o "$TMP" "$URL"
elif command -v wget > /dev/null; then
    wget -qO "$TMP" "$URL"
else
    echo "Error: curl or wget required" >&2; exit 1
fi

chmod +x "$TMP"

# Verify checksum if checksums.txt is available
CHECKSUM_URL="https://github.com/${REPO}/releases/latest/download/checksums.txt"
CHECKSUMS=$(mktemp)
GOT_CHECKSUMS=false
if command -v curl > /dev/null; then
    curl -fSL -o "$CHECKSUMS" "$CHECKSUM_URL" 2>/dev/null && GOT_CHECKSUMS=true
elif command -v wget > /dev/null; then
    wget -qO "$CHECKSUMS" "$CHECKSUM_URL" 2>/dev/null && GOT_CHECKSUMS=true
fi

if [ "$GOT_CHECKSUMS" = true ]; then
    EXPECTED=$(grep "${BINARY}$" "$CHECKSUMS" | awk '{print $1}')
    if [ -n "$EXPECTED" ]; then
        if command -v sha256sum > /dev/null; then
            ACTUAL=$(sha256sum "$TMP" | awk '{print $1}')
        elif command -v shasum > /dev/null; then
            ACTUAL=$(shasum -a 256 "$TMP" | awk '{print $1}')
        else
            ACTUAL=""
        fi
        if [ -n "$ACTUAL" ] && [ "$ACTUAL" != "$EXPECTED" ]; then
            echo "Error: checksum mismatch" >&2
            echo "  expected: $EXPECTED" >&2
            echo "  got:      $ACTUAL" >&2
            rm -f "$TMP" "$CHECKSUMS"
            exit 1
        fi
        if [ -n "$ACTUAL" ]; then
            echo "Checksum verified."
        fi
    fi
fi
rm -f "$CHECKSUMS"

if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP" "${INSTALL_DIR}/cliamp"
else
    sudo mv "$TMP" "${INSTALL_DIR}/cliamp"
fi

echo "Installed cliamp to ${INSTALL_DIR}/cliamp"
