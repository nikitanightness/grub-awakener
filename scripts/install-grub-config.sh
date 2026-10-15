#!/usr/bin/env sh

set -e

if [ "$(id -u)" -ne 0 ]; then
    echo "Error: This script must be run as root (e.g., using sudo)." >&2
    exit 1
fi

if [ ! -d "/etc/grub.d" ]; then
    echo "Error: Directory /etc/grub.d not found. Make sure GRUB is installed." >&2
    exit 1
fi

read_input() {
    prompt="$1"
    default_val="$2"
    if [ -n "$default_val" ]; then
        printf "%s [%s]: " "$prompt" "$default_val" >&2
    else
        printf "%s: " "$prompt" >&2
    fi
    if [ -e /dev/tty ]; then
        read -r val < /dev/tty
    else
        read -r val
    fi
    val=$(echo "$val" | tr -d '[:space:]')
    if [ -z "$val" ]; then
        echo "$default_val"
    else
        echo "$val"
    fi
}

echo "=== GRUB Awakener Configuration Setup ==="

AWAKENER_HOST=""
while [ -z "$AWAKENER_HOST" ]; do
    AWAKENER_HOST=$(read_input "Enter Awakener host (e.g. 192.168.1.1)")
    if [ -z "$AWAKENER_HOST" ]; then
        echo "Awakener host cannot be empty." >&2
    fi
done

AWAKENER_PORT=$(read_input "Enter Awakener port" "9009")

TARGET_FILE="/etc/grub.d/09_awakener"
TEMPLATE_URL="https://raw.githubusercontent.com/nikitanightness/grub-awakener/main/grub.d/09_awakener"
LOCAL_TEMPLATE=""

if [ -f "grub.d/09_awakener" ]; then
    LOCAL_TEMPLATE="grub.d/09_awakener"
elif [ -f "../grub.d/09_awakener" ]; then
    LOCAL_TEMPLATE="../grub.d/09_awakener"
fi

echo "Installing configuration to $TARGET_FILE..."

if [ -n "$LOCAL_TEMPLATE" ]; then
    sed -e "s|<SET_YOUR_BACKEND_IP_HERE>|${AWAKENER_HOST}|g" \
        -e "s|<SET_YOUR_BACKEND_PORT_HERE>|${AWAKENER_PORT}|g" \
        "$LOCAL_TEMPLATE" > "$TARGET_FILE"
else
    echo "Fetching template from GitHub ($TEMPLATE_URL)..."
    curl -fsSL "$TEMPLATE_URL" | \
        sed -e "s|<SET_YOUR_BACKEND_IP_HERE>|${AWAKENER_HOST}|g" \
            -e "s|<SET_YOUR_BACKEND_PORT_HERE>|${AWAKENER_PORT}|g" \
            > "$TARGET_FILE"
fi

chmod +x "$TARGET_FILE"
echo "Installed $TARGET_FILE successfully."

UPDATE_GRUB=$(read_input "Update GRUB now? (Y/n)" "Y")
case "$UPDATE_GRUB" in
    [yY][eE][sS]|[yY]|"")
        echo "Updating GRUB configuration..."
        if command -v update-grub >/dev/null 2>&1; then
            update-grub
        elif command -v grub-mkconfig >/dev/null 2>&1; then
            CFG_TARGET="/boot/grub/grub.cfg"
            [ ! -f "$CFG_TARGET" ] && [ -f "/boot/grub2/grub.cfg" ] && CFG_TARGET="/boot/grub2/grub.cfg"
            grub-mkconfig -o "$CFG_TARGET"
        elif command -v grub2-mkconfig >/dev/null 2>&1; then
            CFG_TARGET="/boot/grub2/grub.cfg"
            [ ! -f "$CFG_TARGET" ] && [ -f "/boot/grub/grub.cfg" ] && CFG_TARGET="/boot/grub/grub.cfg"
            grub2-mkconfig -o "$CFG_TARGET"
        else
            echo "Warning: Neither update-grub nor grub-mkconfig found. Please update GRUB manually." >&2
        fi
        echo "Done! GRUB Awakener is ready."
        ;;
    *)
        echo ""
        echo "⚠️  WARNING: GRUB was NOT updated."
        echo "   The script has installed /etc/grub.d/09_awakener, but Awakener will NOT work"
        echo "   until you regenerate your GRUB configuration (e.g. 'sudo update-grub' or 'sudo grub-mkconfig -o /boot/grub/grub.cfg')."
        ;;
esac
