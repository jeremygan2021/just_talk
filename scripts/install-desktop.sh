#!/usr/bin/env bash
# install-desktop.sh — install the just-talk .desktop file and icon.
#
# Usage:
#     ./scripts/install-desktop.sh             # install everything (icon + .desktop files)
#     ./scripts/install-desktop.sh uninstall   # remove what we installed
#
# After running, "Just Talk" appears in the application menu and on the
# desktop (if ~/Desktop exists). Clicking the icon starts the daemon,
# which puts a status icon in the system tray.

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXEC_PATH="${JUST_TALK_BIN:-$PROJECT_ROOT/build/just-talk}"
ICON_PATH="$PROJECT_ROOT/build/just-talk.png"
TEMPLATE="$PROJECT_ROOT/packaging/just-talk.desktop"
ICON_NAME="just-talk.png"

usage() {
    cat <<USAGE
usage: $0 [install|uninstall]

Environment:
    JUST_TALK_BIN    Path to the just-talk binary (default: <project>/build/just-talk)
USAGE
}

require_path() {
    if [[ ! -x "$1" ]]; then
        echo "error: $1 is not executable. Run 'make build' first." >&2
        exit 1
    fi
}

render_template() {
    sed -e "s|__EXEC_PATH__|$1|g" \
        -e "s|__ICON_PATH__|$2|g" \
        "$TEMPLATE"
}

install_icon_user() {
    local target_dir="$HOME/.local/share/icons/hicolor/256x256/apps"
    mkdir -p "$target_dir"
    cp "$ICON_PATH" "$target_dir/$ICON_NAME"
    echo "✓ icon installed to $target_dir/$ICON_NAME"
}

install_desktop_user() {
    local target_dir="$HOME/.local/share/applications"
    mkdir -p "$target_dir"
    # Strip the extension so the desktop entry resolves via the icon
    # theme rather than baking in a single file path.
    local icon_theme_name="${ICON_NAME%.*}"
    render_template "$EXEC_PATH" "$icon_theme_name" > "$target_dir/just-talk.desktop"
    chmod 0644 "$target_dir/just-talk.desktop"
    echo "✓ .desktop installed to $target_dir/just-talk.desktop"
}

install_desktop_visible() {
    if [[ -d "$HOME/Desktop" ]]; then
        render_template "$EXEC_PATH" "$ICON_PATH" > "$HOME/Desktop/just-talk.desktop"
        chmod 0755 "$HOME/Desktop/just-talk.desktop"
        # mark as trusted so the file manager shows the icon
        gio set "$HOME/Desktop/just-talk.desktop" metadata::trusted true 2>/dev/null || true
        echo "✓ desktop shortcut installed to $HOME/Desktop/just-talk.desktop"
    else
        echo "(skipping ~/Desktop/just-talk.desktop — no ~/Desktop directory)"
    fi
}

refresh_caches() {
    if command -v update-desktop-database >/dev/null 2>&1; then
        update-desktop-database "$HOME/.local/share/applications" 2>/dev/null || true
    fi
    if command -v gtk-update-icon-cache >/dev/null 2>&1; then
        gtk-update-icon-cache -f -t "$HOME/.local/share/icons/hicolor" 2>/dev/null || true
    fi
}

do_install() {
    require_path "$EXEC_PATH"
    if [[ ! -f "$ICON_PATH" ]]; then
        echo "error: icon not found at $ICON_PATH. Run 'make icons' first." >&2
        exit 1
    fi
    install_icon_user
    install_desktop_user
    install_desktop_visible
    refresh_caches
    echo
    echo "Done. Search the app menu for 'Just Talk', or double-click the"
    echo "desktop shortcut. The daemon shows a status icon in the tray."
}

do_uninstall() {
    rm -f "$HOME/.local/share/applications/just-talk.desktop"
    rm -f "$HOME/Desktop/just-talk.desktop"
    rm -f "$HOME/.local/share/icons/hicolor/256x256/apps/$ICON_NAME"
    refresh_caches
    echo "✓ uninstalled"
}

case "${1:-install}" in
    install)   do_install ;;
    uninstall) do_uninstall ;;
    -h|--help) usage ;;
    *)         usage; exit 2 ;;
esac
