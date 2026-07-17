#!/usr/bin/env bash
# Install meeting-scribe as a background service (user-level, no sudo).
# Detects the OS: macOS -> LaunchAgent, Linux -> systemd user unit.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

case "$(uname -s)" in
  Darwin)
    TARGET_DIR="$HOME/Library/LaunchAgents"
    TARGET="$TARGET_DIR/com.scribe.plist"
    mkdir -p "$TARGET_DIR"
    # Replace the USERNAME placeholder with the real home directory.
    sed "s|/Users/USERNAME|$HOME|g" "$PROJECT_DIR/install/macos/com.scribe.plist" > "$TARGET"
    launchctl bootout "gui/$(id -u)/com.scribe" 2>/dev/null || true
    launchctl bootstrap "gui/$(id -u)" "$TARGET"
    echo "Installed and started LaunchAgent com.scribe"
    echo "Logs: /tmp/scribe.out.log /tmp/scribe.err.log"
    ;;
  Linux)
    TARGET_DIR="$HOME/.config/systemd/user"
    TARGET="$TARGET_DIR/scribe.service"
    mkdir -p "$TARGET_DIR"
    # Adjust paths if the project does not live in ~/projects/meeting-scribe.
    sed "s|%h/projects/meeting-scribe|$PROJECT_DIR|g" \
        "$PROJECT_DIR/install/systemd/scribe.service" > "$TARGET"
    systemctl --user daemon-reload
    systemctl --user enable --now scribe
    echo "Installed and started systemd user service 'scribe'"
    echo "Logs: journalctl --user -u scribe -f"
    ;;
  *)
    echo "Unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac

echo
echo "IMPORTANT: set ASSEMBLYAI_API_KEY for the service before real use:"
echo "  - Linux: systemctl --user edit scribe  -> add [Service] Environment=ASSEMBLYAI_API_KEY=..."
echo "  - macOS: add an EnvironmentVariables dict to ~/Library/LaunchAgents/com.scribe.plist"
