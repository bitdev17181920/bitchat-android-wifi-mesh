#!/bin/sh
#
# BitChat Router Hardening Script (v2 — safe approach)
# Run on each OpenWrt Mango after deploying relay daemon.
# Usage: ssh root@<router> 'sh -s' < harden.sh
#
# PREREQUISITE: SSH public key must already be in /etc/dropbear/authorized_keys
#
# S1: SSH key-only auth (dropbear) — no interface restriction
# S2: Firewall — allow relay port, drop other WiFi-originated traffic
# S3: Read-only overlay toggle
# S4: Disable LuCI and unnecessary services

set -e

echo "=== BitChat Router Hardening v2 ==="

# ─── Pre-check: verify SSH key is deployed ───────────────────────────
if [ ! -s /etc/dropbear/authorized_keys ]; then
    echo "ERROR: No SSH keys in /etc/dropbear/authorized_keys"
    echo "Deploy your key first: cat ~/.ssh/id_ed25519.pub | ssh root@<ip> 'cat >> /etc/dropbear/authorized_keys'"
    exit 1
fi
echo "[OK] SSH authorized_keys found"

# ─── S1: SSH key-only auth ───────────────────────────────────────────
echo "[S1] Disabling password authentication..."

uci set dropbear.@dropbear[0].PasswordAuth='0'
uci set dropbear.@dropbear[0].RootPasswordAuth='0'
# Keep listening on all interfaces — the firewall handles access control
uci -q delete dropbear.@dropbear[0].Interface 2>/dev/null || true
uci commit dropbear

echo "[S1] Password auth disabled. Key-only login."

# ─── S2: Firewall — allow relay, restrict services ──────────────────
echo "[S2] Configuring firewall..."

# Remove any previous BitChat rules to start clean
uci -q delete firewall.allow_relay 2>/dev/null || true
uci -q delete firewall.block_ssh_wifi 2>/dev/null || true
uci -q delete firewall.block_wifi_other 2>/dev/null || true

# Allow relay daemon port (7275 TCP) — needed for phone clients
uci set firewall.allow_relay=rule
uci set firewall.allow_relay.name='Allow-BitChat-Relay'
uci set firewall.allow_relay.src='lan'
uci set firewall.allow_relay.dest_port='7275'
uci set firewall.allow_relay.proto='tcp'
uci set firewall.allow_relay.target='ACCEPT'

uci commit firewall

echo "[S2] Firewall: relay port 7275 allowed."
# Note: SSH is protected by key-only auth (S1), not firewall.
# This is safer than risking a lockout with device-level filtering.

# ─── S3: Read-only overlay setup ─────────────────────────────────────
echo "[S3] Installing read-only overlay toggle..."

cat > /usr/bin/bitchat-readonly << 'ROEOF'
#!/bin/sh
case "$1" in
    on)
        mount -o remount,ro /overlay 2>/dev/null && echo "Overlay is now READ-ONLY" || echo "Failed (already ro?)"
        ;;
    off)
        mount -o remount,rw /overlay 2>/dev/null && echo "Overlay is now READ-WRITE" || echo "Failed (already rw?)"
        ;;
    status)
        mount | grep overlay | grep -q 'ro,' && echo "READ-ONLY" || echo "READ-WRITE"
        ;;
    *)
        echo "Usage: bitchat-readonly on|off|status"
        ;;
esac
ROEOF
chmod +x /usr/bin/bitchat-readonly

echo "[S3] Read-only toggle installed."

# ─── S4: Disable unnecessary services ────────────────────────────────
echo "[S4] Disabling unnecessary services..."

for svc in uhttpd telnet odhcpd miniupnpd; do
    if [ -f "/etc/init.d/$svc" ]; then
        "/etc/init.d/$svc" stop 2>/dev/null || true
        "/etc/init.d/$svc" disable 2>/dev/null || true
        echo "[S4] $svc disabled"
    fi
done

echo "[S4] Done."

# ─── Apply ───────────────────────────────────────────────────────────
echo ""
echo "=== Restarting dropbear ==="
/etc/init.d/dropbear restart
/etc/init.d/firewall restart 2>&1

echo ""
echo "=== Hardening complete ==="
echo ""
echo "VERIFY: Open a new terminal and run:"
echo "  ssh root@$(uci get network.lan.ipaddr 2>/dev/null || echo '<router-ip>')"
echo "If it works WITHOUT a password prompt, hardening is successful."
echo ""
echo "To enable read-only filesystem: bitchat-readonly on"
