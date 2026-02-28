#!/bin/sh
set -e

BINARY="/usr/bin/bitchat-relay"
INIT="/etc/init.d/bitchat-relay"
CERT_DIR="/etc/bitchat"

echo "=== BitChat Relay Daemon Installer ==="

mkdir -p "$CERT_DIR"

if [ ! -f "$BINARY" ]; then
    echo "Error: $BINARY not found."
    echo "Copy the binary first:"
    echo "  scp bitchat-relay root@<router-ip>:$BINARY"
    exit 1
fi

chmod +x "$BINARY"

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
if [ -f "$SCRIPT_DIR/bitchat-relay.init" ]; then
    cp "$SCRIPT_DIR/bitchat-relay.init" "$INIT"
    chmod +x "$INIT"
fi

"$INIT" enable
"$INIT" start

echo "Done. Check logs: logread -e bitchat"
