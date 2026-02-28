# BitChat WiFi Mesh Transport

An offline WiFi mesh transport layer for [BitChat](https://github.com/permissionlesstech/bitchat-android). Phones connect to low-cost mesh routers, and messages hop router-to-router over 802.11s + batman-adv — no internet, no cell towers, no accounts.

## Why WiFi Mesh

BitChat already has Bluetooth mesh (BLE) and internet (Nostr) transports. WiFi mesh fills the gap between them:

| | Bluetooth | WiFi Mesh | Nostr (Internet) |
|---|-----------|-----------|-------------------|
| Range | ~100m per hop | ~30m per router indoors, ~200m outdoors; chain routers for km+ | Global |
| Throughput | ~100 kbps | ~10 Mbps+ | Depends on ISP |
| Infrastructure | None (peer-to-peer) | Cheap routers (~$25 each) | Internet connection + relay servers |
| Internet required | No | No | Yes |
| Stability | Connections drop when people move | Fixed infrastructure, always-on | Depends on connectivity |

**WiFi mesh solves the main limitation of Bluetooth**: range and stability. Bluetooth peers come and go as people move. WiFi mesh routers are fixed infrastructure — plug them in, they form a self-healing mesh, and every phone within WiFi range can communicate with every other phone on any router in the mesh. Adding a router extends coverage. It's like building your own cell network for $50-100.

All three transports **run simultaneously**. The app sends broadcasts on all active transports at once and routes DMs through the best available path (WiFi mesh > Bluetooth > Nostr). If one transport goes down, the others keep working. No manual switching needed.

## How It Works

```
┌──────────┐                                          ┌──────────┐
│  Phone A │──WiFi──►┌──────────┐  802.11s  ┌──────────┐◄──WiFi──│  Phone B │
└──────────┘         │ Router 1 │◄═batman══►│ Router 2 │         └──────────┘
                     │ relay    │  ad mesh  │ relay    │
                     └──────────┘           └──────────┘
                           ▲                      ▲
                      WiFi │                      │ WiFi
                     ┌──────────┐            ┌──────────┐
                     │  Phone C │            │  Phone D │
                     └──────────┘            └──────────┘
```

1. Each router runs OpenWrt with **802.11s** (IEEE wireless mesh standard) and **batman-adv** (layer-2 mesh routing). Routers discover each other automatically and form a self-healing mesh backbone.

2. Each router also runs a **relay daemon** (Go binary, port 7275). The daemon accepts TLS connections from phones, performs a proof-of-work handshake to prevent spam, and forwards packets to all connected phones and across the mesh to other routers.

3. The **Android app** auto-detects when the phone joins a router's WiFi. It probes the gateway IP for a relay daemon, opens a TLS connection, solves the PoW challenge, and starts exchanging BitchatPackets — the same binary packet format used by Bluetooth mesh.

4. **Inter-router forwarding** uses UDP multicast over batman-adv. Each packet is signed with Ed25519 and optionally verified against a CA certificate. This prevents rogue routers from injecting packets into the mesh.

## Supported Routers

Any router that runs **OpenWrt** with **802.11s** and **batman-adv** support works. The relay daemon is a single static Go binary with no dependencies.

### Tested

| Router | CPU | RAM | Price | Build Target | Notes |
|--------|-----|-----|-------|-------------|-------|
| GL.iNet GL-MT300N-V2 (Mango) | MIPS 580MHz | 128MB | ~$25 | `mipsle` | Pocket-sized, USB-powered, ideal for portable mesh |

### Compatible (untested, should work)

| Router | CPU | Build Target | Notes |
|--------|-----|-------------|-------|
| GL.iNet GL-AR300M (Shadow) | MIPS 650MHz | `mips` | Dual Ethernet, external antenna |
| GL.iNet GL-AR750S (Slate) | MIPS 775MHz | `mips` | Dual-band, better range |
| GL.iNet GL-MT1300 (Beryl) | ARM Cortex-A7 | `arm` | Faster CPU, good for dense mesh |
| TP-Link Archer C7 | MIPS 720MHz | `mips` | Popular, cheap on used market |
| Xiaomi Mi Router 4A Gigabit | MIPS 880MHz | `mipsle` | Very cheap (~$15 used) |
| Any OpenWrt device | Varies | See below | Check [OpenWrt table of hardware](https://openwrt.org/toh/start) |

**Requirements**: the router must support OpenWrt, have a WiFi radio that supports 802.11s mesh (most do), and have enough flash/RAM for the relay binary (~5MB). The relay itself is statically compiled — no external libraries needed on the router.

### Cross-compilation targets

The relay daemon is pure Go. Cross-compile for your router's architecture:

```sh
# MIPS little-endian, soft float (GL.iNet Mango, Xiaomi 4A)
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w" -o bitchat-relay .

# MIPS big-endian (GL.iNet Shadow/Slate, TP-Link Archer)
GOOS=linux GOARCH=mips GOMIPS=softfloat go build -ldflags="-s -w" -o bitchat-relay .

# ARM (GL.iNet Beryl, Raspberry Pi)
GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o bitchat-relay .

# ARM64 (newer routers, RPi 4)
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bitchat-relay .

# x86_64 (PC/VM testing)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bitchat-relay .
```

## Setup Guide

### Step 1: Flash OpenWrt

Flash [OpenWrt](https://openwrt.org/) on your router. Use the firmware finder at [firmware-selector.openwrt.org](https://firmware-selector.openwrt.org/) to get the correct image for your model. Follow the installation guide for your specific device.

After flashing, SSH into the router:

```sh
ssh root@192.168.1.1
```

### Step 2: Configure the Mesh

Run this on **every router** in the mesh. All routers must use the same `mesh_id` and `channel`.

```sh
# Install batman-adv
opkg update
opkg install kmod-batman-adv batctl

# Create 802.11s mesh interface
uci set wireless.radio0.channel='6'

uci set wireless.mesh0=wifi-iface
uci set wireless.mesh0.device='radio0'
uci set wireless.mesh0.network='mesh0'
uci set wireless.mesh0.mode='mesh'
uci set wireless.mesh0.mesh_id='bitchat-mesh'
uci set wireless.mesh0.encryption='none'
uci commit wireless

# Attach batman-adv to the mesh interface
uci set network.mesh0=interface
uci set network.mesh0.proto='batadv_hardif'
uci set network.mesh0.master='bat0'

uci set network.bat0=interface
uci set network.bat0.proto='batadv'
uci set network.bat0.routing_algo='BATMAN_IV'

# Give bat0 an IP (use a unique IP per router: 10.0.0.1, 10.0.0.2, etc.)
uci set network.bat0.ipaddr='10.0.0.1'
uci set network.bat0.netmask='255.255.255.0'

uci commit network

# Apply
wifi reload
/etc/init.d/network restart
```

**Verify the mesh**: after configuring at least two routers, check that they see each other:

```sh
batctl o
# Should list the other router(s) with their MAC addresses
```

### Step 3: Set Up the WiFi AP

Each router needs a WiFi access point for phones to connect to. If your router only has one radio, the AP and mesh share it (on the same channel). Dual-radio routers can use one radio for mesh and one for the AP.

```sh
# Create AP (adjust SSID/password as needed)
uci set wireless.ap0=wifi-iface
uci set wireless.ap0.device='radio0'
uci set wireless.ap0.network='lan'
uci set wireless.ap0.mode='ap'
uci set wireless.ap0.ssid='BitChat-Node1'
uci set wireless.ap0.encryption='psk2'
uci set wireless.ap0.key='your-password-here'
uci commit wireless
wifi reload
```

> **Tip**: use a different SSID per router (e.g., `BitChat-Node1`, `BitChat-Node2`) so you can tell which node a phone is connected to. Or use the same SSID across all routers for seamless roaming.

### Step 4: Build and Deploy the Relay Daemon

**Requirements**: Go 1.21+ on your build machine.

```sh
cd relay-daemon

# Build for your router architecture (example: MIPS little-endian)
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w" -o bitchat-relay .

# Copy to router
scp bitchat-relay root@192.168.1.1:/usr/bin/bitchat-relay

# Copy install files
scp install/bitchat-relay.init root@192.168.1.1:/etc/init.d/bitchat-relay
scp install/install.sh root@192.168.1.1:/tmp/install.sh

# Install and start
ssh root@192.168.1.1 'chmod +x /usr/bin/bitchat-relay /etc/init.d/bitchat-relay && sh /tmp/install.sh'
```

The relay auto-generates a self-signed TLS certificate on first run and stores it in `/etc/bitchat/`. It listens on port 7275.

**Verify it's running**:

```sh
ssh root@192.168.1.1 'logread -e bitchat'
# Should show: "BitChat Relay Daemon starting" and "TLS server listening on :7275"
```

Repeat for every router in the mesh.

### Step 5: (Optional) Set Up a Certificate Authority

For multi-router meshes, a CA prevents rogue routers from joining. Without a CA, any router with a valid Ed25519 key can join the mesh (which is fine for personal/trusted deployments).

```sh
cd relay-daemon

# Build the CA tool
go build -o mesh-ca ./cmd/mesh-ca

# Generate CA keypair
./mesh-ca init bitchat
# Creates: bitchat.cakey (SECRET) and bitchat.capub

# Get each relay's public key (shown on startup in logs)
ssh root@192.168.1.1 'logread -e "Relay pubkey"'
# Output: Relay pubkey: abc123...

# Sign each relay
./mesh-ca sign bitchat.cakey <relay-pubkey-hex>
# Creates: <prefix>.relaycert

# Deploy cert to each relay
scp <prefix>.relaycert root@192.168.1.1:/etc/bitchat/relay.cert
```

Then start the relay with the CA public key:

```sh
# In /etc/init.d/bitchat-relay, add the flag:
bitchat-relay -ca-pubkey <ca-pubkey-hex> -cert-dir /etc/bitchat
```

To revoke a compromised router:

```sh
./mesh-ca revoke revoked.crl <relay-pubkey-hex>
scp revoked.crl root@<every-router>:/etc/bitchat/revoked.crl
```

### Step 6: (Optional) Harden the Routers

For production deployments, lock down each router:

```sh
# First, deploy your SSH public key
cat ~/.ssh/id_rsa.pub | ssh root@192.168.1.1 'cat >> /etc/dropbear/authorized_keys'

# Verify key-based login works (in a NEW terminal!)
ssh root@192.168.1.1

# If it works, run the hardening script
scp install/harden.sh root@192.168.1.1:/tmp/
ssh root@192.168.1.1 'sh /tmp/harden.sh'
```

The hardening script:
- Disables password authentication (SSH key-only)
- Configures nftables firewall (only ports 22 and 7275 open)
- Installs a read-only filesystem toggle (`bitchat-readonly on/off`)
- Disables unnecessary services (web UI, telnet, UPnP)

> **Warning**: Always verify key-based SSH works BEFORE running the hardening script, or you will lock yourself out. If locked out, use your router's failsafe/recovery mode to re-enable password auth.

### Step 7: Build and Install the Android App

```sh
cd bitchat-android

# Build (requires Android SDK + JDK 17)
./gradlew assembleDebug

# Install via USB
adb install app/build/outputs/apk/debug/app-arm64-v8a-debug.apk
```

Connect the phone to any router's WiFi AP. The app will:
1. Detect the WiFi connection via Android `NetworkCallback`
2. Probe the gateway IP on port 7275 to find the relay
3. Open a TLS connection and solve the PoW challenge (~2-4 seconds)
4. Start sending and receiving messages

The WiFi Mesh channel appears in the channel picker alongside Bluetooth Mesh and Location channels.

## Relay Daemon Reference

### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `7275` | TLS listen port for phone connections |
| `-mesh-port` | `7276` | UDP multicast port for inter-router mesh |
| `-mesh-iface` | `bat0` | batman-adv network interface name |
| `-mesh-group` | `239.0.7.2` | Multicast group address for mesh |
| `-cert-dir` | `/etc/bitchat` | Directory for TLS certs (auto-generated if missing) |
| `-max-clients` | `20` | Max simultaneous phone connections |
| `-pow-difficulty` | `20` | Proof-of-work difficulty (leading zero bits) |
| `-allowed-cert-hash` | (empty) | Comma-separated APK cert SHA-256 hashes; empty = accept any app |
| `-key-dir` | `/etc/bitchat` | Directory for Ed25519 relay signing key |
| `-ca-pubkey` | (empty) | CA public key hex; empty = open mesh (no CA verification) |
| `-crl-path` | `/etc/bitchat/revoked.crl` | Certificate revocation list file |

### Wire Protocol

Phone-to-relay communication uses length-prefixed TLS frames:

```
[1 byte type][4 bytes payload length (big-endian)][payload]
```

| Type | Code | Direction | Payload |
|------|------|-----------|---------|
| HELLO | `0x01` | Phone → Relay | `[2B version][1B peerID-len][peerID][32B APK cert hash (optional)]` |
| CHALLENGE | `0x02` | Relay → Phone | `[32B nonce][1B difficulty]` |
| SOLUTION | `0x03` | Phone → Relay | `[8B uint64 PoW answer]` |
| ACCEPT | `0x04` | Relay → Phone | (empty) |
| REJECT | `0x05` | Relay → Phone | `[error message]` |
| DATA | `0x10` | Bidirectional | `[BitchatPacket binary]` |
| PING | `0x20` | Phone → Relay | (empty) |
| PONG | `0x21` | Relay → Phone | (empty) |

### Rate Limits

| Limit | Default |
|-------|---------|
| Per-client | 10 packets/sec, burst 20 |
| Global | 100 packets/sec, burst 200 |
| Dedup buffer | 10,000 packet hashes |
| Store-and-forward | 1,000 packets |

## Project Structure

```
WiFi_mesh/
├── relay-daemon/              # Go relay daemon (runs on routers)
│   ├── main.go                # Entry point, flag parsing
│   ├── server.go              # TLS listener, connection handling
│   ├── handshake.go           # PoW challenge/response protocol
│   ├── client.go              # Per-client read/write loops
│   ├── router.go              # Packet fanout to clients + mesh
│   ├── mesh.go                # UDP multicast inter-router forwarding
│   ├── protocol.go            # Frame encoding/decoding
│   ├── pow.go                 # Proof-of-work generation/verification
│   ├── auth.go                # Ed25519 signing, CA verification
│   ├── dedup.go               # Packet deduplication
│   ├── ratelimit.go           # Token bucket rate limiter
│   ├── buffer.go              # Store-and-forward ring buffer
│   ├── config.go              # Configuration defaults
│   ├── Makefile               # Build targets (native, mipsle)
│   ├── cmd/
│   │   ├── mesh-ca/           # Certificate authority CLI tool
│   │   └── testclient/        # Go test client for debugging
│   └── install/
│       ├── install.sh         # Router installation script
│       ├── bitchat-relay.init # OpenWrt procd service file
│       └── harden.sh          # Security hardening script
└── docs/
    ├── WHITEPAPER.md          # Full design and security model
    └── IMPLEMENTATION_PLAN.md # Development phases
```

Key Android files (in `bitchat-android/app/src/main/java/com/bitchat/android/`):

| File | Purpose |
|------|---------|
| `mesh/wifi/WiFiMeshTransport.kt` | Transport lifecycle, WiFi detection, send/receive |
| `mesh/wifi/RelayDiscovery.kt` | Auto-detect relay IP by probing the gateway |
| `mesh/wifi/RelayConnection.kt` | TLS socket, PoW handshake, frame I/O |
| `mesh/wifi/WiFiMeshConfig.kt` | Protocol constants, ports, timeouts |
| `mesh/TransportManager.kt` | Orchestrates all transports, priority-based routing |
| `mesh/MeshTransport.kt` | Transport interface (implemented by BT and WiFi) |
| `services/MessageRouter.kt` | DM routing: mesh > Nostr > queue |

## Security Model

| Layer | Mechanism |
|-------|-----------|
| Phone ↔ Relay | TLS 1.3 (ECDSA P-256, self-signed or CA-signed) |
| Connection auth | Proof-of-work (SHA-256, configurable difficulty) |
| App attestation | Optional APK certificate hash verification |
| Router ↔ Router | Ed25519 signed UDP, optional CA certificate chain |
| Rogue router prevention | CA-based trust + certificate revocation list |
| Message encryption | Ed25519 signatures on BitchatPackets between peers |
| DM encryption | NIP-17 gift-wrap + Noise protocol (forward secrecy) |
| Rate limiting | Per-client and global token bucket |
| Packet replay | Hash-based deduplication (10K entry ring buffer) |

## License

Same as [BitChat Android](https://github.com/permissionlesstech/bitchat-android).
