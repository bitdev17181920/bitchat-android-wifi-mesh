# BitChat WiFi Mesh — Implementation Plan

## Overview

Six phases, building from safe refactoring to full production deployment.
Each phase is independently testable before proceeding to the next.

---

## Phase 1: Transport Interface Extraction (Android)

**Goal:** Define a MeshTransport interface and wrap existing BLE code in it, without changing any behavior.

**Risk:** Zero — pure refactoring. BLE mesh continues to work identically.

### New Files

| File | Location | Purpose |
|---|---|---|
| `MeshTransport.kt` | `mesh/` | Interface defining transport contract |
| `TransportManager.kt` | `mesh/` | Orchestrates multiple transports, handles deduplication |

### Modified Files

| File | Change |
|---|---|
| `BluetoothConnectionManager.kt` | Add `implements MeshTransport`. Wrap existing `broadcastPacket()`, `sendToPeer()`, etc. in interface methods. Add `onPacketReceived` callback alongside existing delegate pattern. |
| `BluetoothMeshService.kt` | Create `TransportManager` containing `BluetoothConnectionManager`. Replace ~15 `connectionManager.broadcastPacket()` calls with `transportManager.broadcastPacket()`. Replace `connectionManager.sendToPeer()` similarly. |

### MeshTransport Interface Design

```kotlin
interface MeshTransport {
    val transportType: TransportType
    val isActive: Boolean

    fun start(): Boolean
    fun stop()
    fun isReusable(): Boolean

    // Packet sending
    fun broadcastPacket(routed: RoutedPacket)
    fun sendToPeer(peerID: String, routed: RoutedPacket): Boolean
    fun sendPacketToPeer(peerID: String, packet: BitchatPacket): Boolean

    // Peer tracking
    fun getReachablePeerIDs(): Set<String>

    // Incoming packets
    var onPacketReceived: ((packet: BitchatPacket, peerID: String, sourceAddress: String?) -> Unit)?
    var onPeerConnected: ((sourceAddress: String) -> Unit)?
    var onPeerDisconnected: ((sourceAddress: String) -> Unit)?
}

enum class TransportType { BLUETOOTH, WIFI_MESH }
```

### TransportManager Design

```kotlin
class TransportManager {
    private val transports = mutableListOf<MeshTransport>()

    fun addTransport(transport: MeshTransport)
    fun removeTransport(transport: MeshTransport)

    // Sends to ALL active transports (public messages reach all reachable nodes)
    fun broadcastPacket(routed: RoutedPacket)

    // Tries transports in priority order: WiFi mesh → BLE
    fun sendToPeer(peerID: String, routed: RoutedPacket): Boolean

    // Aggregate all reachable peers across transports
    fun getReachablePeerIDs(): Set<String>

    // Incoming packets from any transport feed into single callback
    var onPacketReceived: ((BitchatPacket, String, String?) -> Unit)?
}
```

### Test Plan

1. Build the app with the refactored code
2. Run on an Android phone
3. Verify BLE mesh works identically (peer discovery, messaging, encryption)
4. Verify no regressions in any existing functionality

---

## Phase 2: Relay Daemon (Router Side)

**Goal:** A production-quality relay daemon that runs on OpenWrt mesh routers.

**Language:** Go (cross-compiles to MIPS, handles concurrency natively, single static binary)

### Functionality

1. **TLS Server** — listens on port 7275 for phone connections
2. **Client Manager** — tracks connected phones, assigns rate limit buckets
3. **Packet Router** — receives packets from phones, forwards to:
   - Other phones connected to this router (local fanout)
   - Other relay daemons via UDP multicast on bat0 (mesh forwarding)
4. **Mesh Listener** — receives packets from bat0 multicast, delivers to local phones
5. **Rate Limiter** — per-client and global limits
6. **PoW Challenger** — generates challenges, verifies solutions
7. **Packet Buffer** — circular buffer of recent packets for store-and-forward
8. **Health Monitor** — exposes simple HTTP status endpoint for mesh operators

### File Structure

```
relay-daemon/
├── main.go               # Entry point, CLI flags, signal handling
├── server.go             # TLS listener, client connection lifecycle
├── client.go             # Per-client state, rate limiting, read/write loops
├── handshake.go          # Connection handshake (hello, PoW, attestation)
├── router.go             # Packet routing (local fanout + mesh forwarding)
├── mesh.go               # UDP multicast on bat0 (send/receive)
├── ratelimit.go          # Token bucket rate limiter
├── pow.go                # Proof-of-work challenge generation and verification
├── buffer.go             # Circular packet buffer for store-and-forward
├── dedup.go              # Bloom filter for packet deduplication
├── config.go             # Configuration (ports, limits, difficulty)
├── go.mod                # Go module definition
├── Makefile              # Cross-compilation targets (mipsel for Mango)
└── install/
    ├── bitchat-relay.init # OpenWrt procd init script
    └── install.sh         # One-line installer for routers
```

### Build and Deploy

```bash
# Cross-compile for Mango (MIPS little-endian)
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w" -o bitchat-relay

# Copy to router
scp bitchat-relay root@10.0.0.1:/usr/bin/
scp install/bitchat-relay.init root@10.0.0.1:/etc/init.d/bitchat-relay

# Enable and start
ssh root@10.0.0.1 "chmod +x /etc/init.d/bitchat-relay && /etc/init.d/bitchat-relay enable && /etc/init.d/bitchat-relay start"
```

### Configuration

```
Port:              7275 (TLS, phone connections)
Mesh port:         7276 (UDP multicast, inter-daemon)
Max clients:       20 (per daemon, configurable)
Rate limit:        10 packets/sec per client, 100 packets/sec global
Max packet size:   65536 bytes
PoW difficulty:    20 bits (adjustable based on load)
Packet buffer:     1000 packets (circular)
Keepalive:         30 seconds
TLS cert:          Auto-generated on first boot, stored in /etc/bitchat/
```

### Test Plan

1. Build relay daemon and deploy to both Mango routers
2. Verify TLS listener accepts connections (openssl s_client)
3. Write a simple Go/Python test client that completes handshake and sends packets
4. Verify packets are forwarded between daemons via bat0 multicast
5. Verify rate limiting rejects excessive traffic
6. Verify PoW challenge/response works

---

## Phase 3: WiFiMeshTransport (Android Side)

**Goal:** Android transport that connects to relay daemons on mesh routers.

### New Files

| File | Location | Purpose |
|---|---|---|
| `WiFiMeshTransport.kt` | `mesh/wifi/` | MeshTransport implementation for WiFi mesh |
| `WiFiMeshConfig.kt` | `mesh/wifi/` | Constants (port, timeouts, PoW params) |
| `RelayConnection.kt` | `mesh/wifi/` | Single TLS connection to a relay daemon |
| `RelayDiscovery.kt` | `mesh/wifi/` | Gateway probe + mDNS discovery |
| `ProofOfWork.kt` | `mesh/wifi/` | PoW solver for relay authentication |

### WiFiMeshTransport Lifecycle

```
App starts
  │
  ├─ Register NetworkCallback for WiFi state changes
  │
  ▼
WiFi connected?
  │ no → transport inactive, nothing to do
  │ yes
  ▼
Probe gateway IP on port 7275
  │ no response → try mDNS discovery
  │ no mDNS → transport inactive (not a mesh network)
  │ found relay
  ▼
TLS handshake + certificate pin verification
  │ cert mismatch → reject (possible MITM)
  │ cert valid
  ▼
Send HELLO → receive CHALLENGE → solve PoW → send SOLUTION
  │ rejected → log error, back off, retry later
  │ accepted
  ▼
Transport active — begin packet exchange
  │
  ├─ Read loop: receive packets from relay → onPacketReceived callback
  ├─ Write loop: broadcastPacket/sendToPeer → send to relay
  ├─ Keepalive: send ping every 30s
  │
  ▼
WiFi disconnected? → transport inactive, clean up connection
WiFi reconnected? → restart discovery and connection
```

### Network Detection (Android)

```kotlin
// Register for WiFi connectivity changes
val networkRequest = NetworkRequest.Builder()
    .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
    .build()

connectivityManager.registerNetworkCallback(networkRequest, object : ConnectivityManager.NetworkCallback() {
    override fun onAvailable(network: Network) {
        // WiFi connected — start relay discovery
    }
    override fun onLost(network: Network) {
        // WiFi disconnected — deactivate transport
    }
})
```

### Permissions Required

Add to `AndroidManifest.xml`:
```xml
<uses-permission android:name="android.permission.ACCESS_WIFI_STATE" />
<uses-permission android:name="android.permission.CHANGE_WIFI_STATE" />
<uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
```

### Test Plan

1. Deploy relay daemon on Mango routers (Phase 2 complete)
2. Connect phone to Mango AP WiFi
3. Verify WiFiMeshTransport discovers the relay daemon
4. Verify TLS connection established
5. Verify handshake (PoW + attestation) completes
6. Send a test packet from phone → verify it arrives at relay daemon
7. Send a test packet from relay daemon → verify it arrives at phone
8. Disconnect WiFi → verify transport becomes inactive
9. Reconnect WiFi → verify transport reconnects automatically

---

## Phase 4: MessageRouter Update

**Goal:** Integrate WiFi mesh into the message routing priority chain.

### Modified Files

| File | Change |
|---|---|
| `MessageRouter.kt` | Add WiFi mesh as first option for private messages. Check `transportManager.getWiFiMeshPeerIDs()` before falling through to BLE and Nostr. |
| `BluetoothMeshService.kt` | Expose `transportManager` APIs for checking WiFi mesh peer reachability. Add `isWiFiMeshPeerReachable(peerID)` convenience method. |

### Updated Routing Logic

```
sendPrivate(content, toPeerID):
  1. Is peer reachable via WiFi mesh AND has established Noise session?
     → Send via WiFi mesh transport (high bandwidth, reliable)
  2. Is peer connected via BLE AND has established Noise session?
     → Send via BLE transport (local fallback)
  3. Can send via Nostr (has Nostr pubkey mapping)?
     → Send via Nostr transport (internet)
  4. None available?
     → Queue message + initiate Noise handshake
```

For public/channel messages (broadcast):
- TransportManager already sends to ALL active transports
- No routing logic change needed for broadcasts

### Test Plan

1. Two phones, each connected to a different Mango AP
2. Send private message → verify it routes via WiFi mesh
3. Disconnect phone A's WiFi → send another message → verify it falls back to BLE (if in BLE range) or queues
4. Reconnect WiFi → verify queued messages are delivered
5. Test broadcast messages → verify they arrive via WiFi mesh

---

## Phase 5: Security Hardening

**Goal:** Production-grade security for public deployment.

### 5.1 App Attestation (Android)

- Integrate Google Play Integrity API in `WiFiMeshTransport`
- On connect, request integrity token and include in SOLUTION frame
- Relay daemon verifies token with Google servers
- Fallback: APK certificate hash for sideloaded builds

### 5.2 Relay-to-Relay Authentication

- Each relay daemon generates an Ed25519 key pair on first boot
- Relay daemons sign their mesh multicast packets
- Unknown relay signatures are flagged but not rejected (allows new nodes to join)
- Mesh operators can maintain an allowlist of known relay keys

### 5.3 802.11s Mesh Encryption

- Enable SAE (Simultaneous Authentication of Equals) on mesh links
- Shared mesh password prevents unauthorized routers from joining
- Configure via OpenWrt UCI:
  ```
  config wifi-iface 'mesh0'
      option encryption 'sae'
      option key 'mesh-operator-password'
  ```

### 5.4 Router Hardening

- Disable SSH password login (key-only)
- Set overlay filesystem to read-only
- Enable relay daemon integrity check (hash verification on startup)
- Firewall rules: only allow ports 7275 (TLS), 7276 (mesh multicast), 22 (SSH)

### 5.5 Audit Logging

- Relay daemon logs security events (failed attestation, rate limit bans, malformed packets)
- Logs are in-memory only (privacy by design) but can optionally be forwarded to a syslog server for mesh operators

### Test Plan

1. Attempt connection from a Python script → verify rejected (no attestation)
2. Attempt to flood relay with packets → verify rate limited and banned
3. Attempt to join mesh with unauthorized router → verify rejected (SAE)
4. Verify relay daemon starts with correct binary hash
5. Verify PoW difficulty increases under load

---

## Phase 6: UI and UX

**Goal:** Users can see WiFi mesh status and understand what transport is being used.

### Changes

| File | Change |
|---|---|
| `ChatHeader.kt` | Add WiFi mesh status icon (green/yellow/hidden) |
| `MeshPeerListSheet.kt` | Show transport type per peer (WiFi/BLE/Nostr icon) |
| `DebugSettingsSheet.kt` | Add WiFi mesh debug info (relay address, connection status, packet stats) |

### Status Indicators

- 📶 Green WiFi icon: Connected to mesh relay, active
- 📶 Yellow WiFi icon: WiFi connected, relay discovered, connecting...
- (hidden): Not on a mesh WiFi network

### Test Plan

1. Connect to mesh WiFi → verify green icon appears
2. Disconnect WiFi → verify icon disappears
3. Check peer list → verify transport type shown per peer
4. Open debug settings → verify relay connection info displayed

---

## Implementation Order & Dependencies

```
Phase 1 ──────► Phase 3 ──────► Phase 4 ──────► Phase 6
(interface)     (android)       (routing)       (UI)
                    │
Phase 2 ────────────┘──────────► Phase 5
(relay daemon)                   (security)
```

- Phase 1 and 2 can be developed in parallel (no dependency)
- Phase 3 requires both Phase 1 (interface) and Phase 2 (relay to connect to)
- Phase 4 requires Phase 3
- Phase 5 can be developed alongside Phase 3/4
- Phase 6 is last (polish)

---

## Estimated Effort

| Phase | New Code | Modified Code | Effort |
|---|---|---|---|
| Phase 1: Transport interface | ~180 lines | ~200 lines (15 call sites) | 2-3 days |
| Phase 2: Relay daemon | ~800 lines Go | — | 5-7 days |
| Phase 3: WiFi transport | ~500 lines Kotlin | ~50 lines | 4-5 days |
| Phase 4: MessageRouter | — | ~60 lines | 1 day |
| Phase 5: Security | ~200 lines (both sides) | ~100 lines | 3-4 days |
| Phase 6: UI | ~100 lines | ~50 lines | 1-2 days |
| **Total** | **~1,780 lines** | **~460 lines** | **16-22 days** |

---

## Definition of Done

The WiFi mesh transport is production-ready when:

- [ ] Two phones on different mesh routers can exchange messages via WiFi mesh
- [ ] Private messages are end-to-end encrypted (Noise protocol works over WiFi mesh)
- [ ] App falls back to BLE when WiFi mesh is unavailable
- [ ] App falls back to Nostr when both WiFi mesh and BLE are unavailable
- [ ] Relay daemon survives 24-hour soak test without memory leaks or crashes
- [ ] Rate limiting blocks flooding attacks
- [ ] PoW challenge prevents automated connection spam
- [ ] App attestation blocks non-BitChat clients (casual attackers)
- [ ] Mesh routing works across 3+ router hops
- [ ] Store-and-forward delivers messages to phones that reconnect after going offline
- [ ] No regressions in existing BLE or Nostr functionality
