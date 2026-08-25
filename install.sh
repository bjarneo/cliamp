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
ASSET="$BINARY"
if [ "$OS" = "windows" ]; then
    ASSET="cliamp-windows-${ARCH}.zip"
fi

URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

echo "Downloading ${ASSET}..."
TMP=$(mktemp)
CHECKSUMS=$(mktemp)
TMP_DIR=""
trap 'rm -f "$TMP" "$CHECKSUMS"; [ -z "$TMP_DIR" ] || rm -rf "$TMP_DIR"' EXIT HUP INT TERM
if command -v curl > /dev/null; then
    curl -fSL -o "$TMP" "$URL"
elif command -v wget > /dev/null; then
    wget -qO "$TMP" "$URL"
else
    echo "Error: curl or wget required" >&2; exit 1
fi

# A release without a matching checksum is not installable.
CHECKSUM_URL="https://github.com/${REPO}/releases/latest/download/checksums.txt"
if command -v curl > /dev/null; then
    curl -fSL -o "$CHECKSUMS" "$CHECKSUM_URL"
elif command -v wget > /dev/null; then
    wget -qO "$CHECKSUMS" "$CHECKSUM_URL"
fi

EXPECTED=$(awk -v file="$ASSET" '$2 == file || $2 == "*" file { print $1 }' "$CHECKSUMS")
if [ -z "$EXPECTED" ]; then
    echo "Error: release has no checksum for ${ASSET}" >&2
    exit 1
fi
if command -v sha256sum > /dev/null; then
    ACTUAL=$(sha256sum "$TMP" | awk '{print $1}')
elif command -v shasum > /dev/null; then
    ACTUAL=$(shasum -a 256 "$TMP" | awk '{print $1}')
else
    echo "Error: sha256sum or shasum is required" >&2
    exit 1
fi
if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "Error: checksum mismatch" >&2
    echo "  expected: $EXPECTED" >&2
    echo "  got:      $ACTUAL" >&2
    exit 1
fi
echo "Checksum verified."

if [ "$OS" = "windows" ]; then
    if ! command -v unzip > /dev/null; then
        echo "Error: unzip is required for Windows installation" >&2
        exit 1
    fi
    TMP_DIR=$(mktemp -d)
    unzip -q "$TMP" -d "$TMP_DIR"
    PACKAGE_DIR="${TMP_DIR}/cliamp-windows-${ARCH}"
    if [ ! -f "${PACKAGE_DIR}/cliamp.exe" ]; then
        echo "Error: Windows release archive is missing cliamp.exe" >&2
        exit 1
    fi
    chmod +x "${PACKAGE_DIR}/cliamp.exe"
    if [ -w "$INSTALL_DIR" ]; then
        cp "${PACKAGE_DIR}"/* "$INSTALL_DIR/"
    else
        sudo cp "${PACKAGE_DIR}"/* "$INSTALL_DIR/"
    fi
    INSTALLED="${INSTALL_DIR}/cliamp.exe"
else
    chmod +x "$TMP"
    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP" "${INSTALL_DIR}/cliamp"
    else
        sudo mv "$TMP" "${INSTALL_DIR}/cliamp"
    fi
    TMP=""
    INSTALLED="${INSTALL_DIR}/cliamp"
fi

echo "Installed cliamp to ${INSTALLED}"

# Install desktop entry + icon (Linux only).
# Pick user-local share dir for ~/.local/bin installs, /usr/local/share otherwise.
if [ "$OS" = "linux" ]; then
    case "$INSTALL_DIR" in
        "$HOME"/*)
            SHARE_DIR="${XDG_DATA_HOME:-$HOME/.local/share}"
            SUDO=""
            ;;
        *)
            SHARE_DIR="/usr/local/share"
            SUDO="sudo"
            [ -w "$SHARE_DIR" ] && SUDO=""
            ;;
    esac

    APP_DIR="${SHARE_DIR}/applications"
    ICON_DIR="${SHARE_DIR}/icons/hicolor/512x512/apps"

    ICON_TMP=$(mktemp)
    DESKTOP_TMP=$(mktemp)
    ICON_URL="https://raw.githubusercontent.com/${REPO}/HEAD/Cliamp.png"

    GOT_ICON=false
    if command -v curl > /dev/null; then
        curl -fSL -o "$ICON_TMP" "$ICON_URL" 2>/dev/null && GOT_ICON=true
    elif command -v wget > /dev/null; then
        wget -qO "$ICON_TMP" "$ICON_URL" 2>/dev/null && GOT_ICON=true
    fi

    cat > "$DESKTOP_TMP" <<EOF
[Desktop Entry]
Name=cliamp
GenericName=Music Player
Comment=A retro terminal music player inspired by Winamp 2.x
Exec=${INSTALL_DIR}/cliamp
Icon=cliamp
Terminal=true
Type=Application
Categories=Audio;Music;Player;AudioVideo;ConsoleOnly;
Keywords=music;audio;player;terminal;tui;winamp;radio;podcast;
StartupNotify=false
EOF

    $SUDO mkdir -p "$APP_DIR" "$ICON_DIR"
    $SUDO install -m 0644 "$DESKTOP_TMP" "${APP_DIR}/cliamp.desktop"
    if [ "$GOT_ICON" = true ]; then
        $SUDO install -m 0644 "$ICON_TMP" "${ICON_DIR}/cliamp.png"
        echo "Installed icon to ${ICON_DIR}/cliamp.png"
    fi
    rm -f "$ICON_TMP" "$DESKTOP_TMP"

    echo "Installed desktop entry to ${APP_DIR}/cliamp.desktop"

    if command -v update-desktop-database > /dev/null; then
        $SUDO update-desktop-database "$APP_DIR" 2>/dev/null || true
    fi
    if command -v gtk-update-icon-cache > /dev/null; then
        $SUDO gtk-update-icon-cache -q "${SHARE_DIR}/icons/hicolor" 2>/dev/null || true
    fi
fi
