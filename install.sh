#!/bin/bash
set -e

BINARY_NAME="localvoice"
INSTALL_DIR="$HOME/.local/bin"
INSTALL_PATH="$INSTALL_DIR/$BINARY_NAME"
PLIST_SRC="com.localvoiceai.localvoice.plist"
PLIST_DEST="$HOME/Library/LaunchAgents/com.localvoiceai.localvoice.plist"
LABEL="com.localvoiceai.localvoice"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  LocalVoiceAI Installer"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Check Apple Silicon
if [[ "$(uname -m)" != "arm64" ]]; then
  echo "ERROR: LocalVoiceAI requires an Apple Silicon Mac (M1 or later)."
  exit 1
fi

# Check Homebrew dependencies
echo "Checking dependencies..."
MISSING=()
command -v whisper-cli &>/dev/null || MISSING+=("whisper-cpp")
# portaudio is a library, check via brew
brew list portaudio &>/dev/null 2>&1 || MISSING+=("portaudio")

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "Installing missing dependencies: ${MISSING[*]}"
  brew install "${MISSING[@]}"
fi

# Install binary
echo "Installing $BINARY_NAME to $INSTALL_PATH..."
mkdir -p "$INSTALL_DIR"
cp "$BINARY_NAME" "$INSTALL_PATH"
chmod +x "$INSTALL_PATH"
codesign -s - --force "$INSTALL_PATH"

# Install LaunchAgent plist
echo "Installing LaunchAgent..."
mkdir -p "$HOME/Library/LaunchAgents"
sed "s|INSTALL_PATH|$INSTALL_PATH|g" "$PLIST_SRC" > "$PLIST_DEST"
launchctl unload "$PLIST_DEST" 2>/dev/null || true
launchctl load "$PLIST_DEST"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Installed successfully!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "NEXT: Grant permissions once in System Settings → Privacy & Security:"
echo ""
echo "  1. Accessibility    → add: $INSTALL_PATH"
echo "  2. Input Monitoring → add: $INSTALL_PATH"
echo ""
echo "Then start the service:"
echo "  launchctl start $LABEL"
echo ""
echo "To stop:"
echo "  launchctl stop $LABEL"
echo ""
echo "Logs: tail -f /tmp/localvoice.log"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
