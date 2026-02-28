# BitChat WiFi Mesh Transport — Design & Security Whitepaper

## 1. Introduction

BitChat is a decentralized peer-to-peer messaging app that works without the internet. It currently supports two transport modes:

1. **Bluetooth Low Energy (BLE) mesh** — short-range, phone-to-phone, max 7 hops
2. **Nostr protocol** — internet-based, uses global relay servers

This document describes the design of a **third transport mode: WiFi Mesh** — a network of dedicated WiFi routers that form an autonomous mesh, enabling communication without the internet, at greater range and bandwidth than Bluetooth.

### 1.1 Why WiFi Mesh?

| Limitation of BLE | WiFi Mesh Solution |
|---|---|
| ~30m range per hop | ~100m+ per router (outdoor) |
| ~7 hops max (practical) | Unlimited hops via batman-adv routing |
| ~100 Kbps throughput | ~20-50 Mbps throughput |
| Drains phone battery (scanning) | Routers are always-on, wall-powered |
| Requires phones to be awake | Routers relay 24/7, phones wake on message |
| Max ~10 direct peers | Hundreds of users per router |

### 1.2 Transport Hierarchy

The three transports form a fallback chain:

```
Internet available? ──yes──► Nostr (global reach, relay servers)
        │ no
WiFi mesh available? ──yes──► WiFi Mesh (local/regional, high bandwidth)
        │ no
BLE available? ──yes──► Bluetooth Mesh (close range, phone-to-phone)
        │ no
Queue message for later delivery
```

The app automatically selects the best available transport. Users don't need to choose.

### 1.3 Key Principle: Transport Independence

The WiFi mesh transport carries the exact same encrypted packets as BLE and Nostr. The encryption layer (Noise Protocol) operates above the transport layer. This means:

- Private messages are end-to-end encrypted regardless of transport
- Message signatures (Ed25519) are verified regardless of transport
- The relay daemon and routers never see plaintext private messages
- A message can traverse multiple transports (WiFi mesh → BLE → WiFi mesh) seamlessly

---

## 2. System Architecture

### 2.1 Component Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                        ANDROID PHONE                              │
│                                                                    │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │                      BitChat App                             │  │
│  │                                                               │  │
│  │  ChatViewModel ──► MessageRouter ──► TransportManager         │  │
│  │                                           │                    │  │
│  │                              ┌────────────┼────────────┐      │  │
│  │                              │            │            │      │  │
│  │                         WiFiMesh     Bluetooth     Nostr      │  │
│  │                         Transport    Transport   Transport    │  │
│  │                              │            │            │      │  │
│  └──────────────────────────────┼────────────┼────────────┼──┘  │
│                                 │            │            │      │
│                            TCP/TLS       BLE GATT    WebSocket   │
└─────────────────────────────────┼────────────┼────────────┼──────┘
                                  │            │            │
                           WiFi radio     BLE radio     Internet
                                  │            │
                     ┌────────────┘            └──── (phone-to-phone)
                     │
              ┌──────▼──────┐         ┌─────────────┐
              │  Router #1  │◄═══════►│  Router #2  │
              │  (Mango)    │ 802.11s │  (Mango)    │
              │             │ batman  │             │
              │ relay-daemon│  -adv   │ relay-daemon│
              │ WiFi AP     │         │ WiFi AP     │
              └─────────────┘         └─────────────┘
```

### 2.2 Android App Components

#### MeshTransport Interface

A common interface implemented by all local mesh transports:

- `start()` / `stop()` — lifecycle management
- `broadcastPacket(packet)` — send to all reachable peers
- `sendToPeer(peerID, packet)` — send to a specific peer
- `getConnectedPeerIDs()` — list reachable peers
- `onPacketReceived` — callback for incoming packets

#### TransportManager

Orchestrates multiple MeshTransport implementations:

- Forwards `broadcastPacket()` to ALL active transports simultaneously
- For `sendToPeer()`, tries WiFi mesh first, then BLE
- Deduplicates packets arriving from multiple transports
- Monitors transport availability and notifies the UI

#### WiFiMeshTransport

The new transport that connects to relay daemons on mesh routers:

- Monitors WiFi connectivity via Android NetworkCallback API
- Discovers relay daemons using gateway probe (primary) and mDNS (fallback)
- Maintains a TLS connection to the relay daemon
- Sends/receives BitchatPacket binary frames over the connection
- Handles reconnection, keepalive, and graceful degradation

#### BluetoothTransport (existing, renamed)

The existing `BluetoothConnectionManager` wrapped in the MeshTransport interface. Internal BLE logic is unchanged.

#### MessageRouter (modified)

Updated routing priority for private messages:

```
1. WiFi mesh peer reachable + Noise session established → WiFi mesh
2. BLE mesh peer connected + Noise session established  → BLE
3. Nostr public key mapping available                    → Nostr (internet)
4. None available                                         → Queue + initiate handshake
```

### 2.3 Relay Daemon

A lightweight service running on each mesh router. Written in Go for performance and easy cross-compilation.

**Responsibilities:**
- Accept TLS connections from phones on the AP interface
- Authenticate connecting clients (app attestation + proof of work)
- Receive BitchatPacket frames from phones
- Forward packets to other phones connected to this router
- Forward packets across the batman-adv mesh to relay daemons on other routers
- Receive packets from the mesh and deliver to local phones
- Rate limiting and abuse detection per client
- Store-and-forward for recently seen packets (circular buffer)

**Inter-daemon communication:**
- UDP multicast on the bat0 interface (batman-adv virtual interface)
- Port 7276
- Same length-prefixed framing as phone connections
- Deduplication via packet hash bloom filter

### 2.4 Router Stack

Each mesh router runs:

```
┌────────────────────────────────────────────┐
│ Application: relay-daemon (Go binary)       │
├────────────────────────────────────────────┤
│ Network: batman-adv (bat0 virtual iface)    │
│          DHCP server (for AP clients)       │
│          TLS listener (port 7275)           │
│          UDP multicast (port 7276)          │
├────────────────────────────────────────────┤
│ Wireless: 802.11s mesh (phy0-mesh0)         │
│           WiFi AP (wlan0-ap)                │
├────────────────────────────────────────────┤
│ OS: OpenWrt 24.10+                          │
├────────────────────────────────────────────┤
│ Hardware: Any router with open-source       │
│           WiFi drivers (mt76, ath9k, etc.)  │
└────────────────────────────────────────────┘
```

---

## 3. Security Model

### 3.1 Threat Model

We defend against the following adversaries:

| Adversary | Capability | Goal |
|---|---|---|
| Casual snooper | Nearby with a WiFi adapter | Read messages |
| Script kiddie | Custom scripts, no special hardware | Disrupt the mesh, spam |
| Skilled hacker | Rooted phone, Frida, reverse engineering | Bypass authentication, flood |
| Rogue operator | Deploys fake mesh node | Intercept traffic, MITM |
| Nation-state | Zero-days, custom hardware, unlimited budget | Surveillance, targeted attacks |

### 3.2 Defense in Depth (7 Layers)

#### Layer 1: WiFi Encryption

**What:** WPA2-PSK (or WPA3-SAE/OWE where supported) on the AP.

**Protects against:** Casual radio sniffing. Even with a shared password, WPA2 creates per-station encryption keys via the 4-way handshake.

**Configuration:**
- Public mesh: WPA2 with a well-known password (published in app, on signage)
- Private mesh: WPA2/WPA3 with an operator-chosen password
- Advanced: OWE (Opportunistic Wireless Encryption) for zero-config encrypted open networks

#### Layer 2: TLS (Phone ↔ Relay Daemon)

**What:** TLS 1.3 connection between the BitChat app and the relay daemon.

**Protects against:** Man-in-the-middle attacks on the local WiFi network, packet inspection by other WiFi clients.

**Implementation:**
- Relay daemon generates a self-signed TLS certificate on first boot
- Certificate fingerprint is embedded in the mesh's configuration (shared via QR code or app)
- The BitChat app performs certificate pinning — rejects connections to relays with unknown certificates
- This prevents a rogue device on the same WiFi from impersonating the relay

**Key terms:**
- **TLS (Transport Layer Security):** The protocol that puts the "S" in HTTPS. Encrypts the data flowing between two endpoints.
- **Certificate pinning:** Instead of trusting any certificate signed by any authority, the app only trusts a specific certificate fingerprint. Even if an attacker obtains a valid TLS certificate from a certificate authority, the app rejects it because it's not the pinned one.
- **Self-signed certificate:** A TLS certificate not issued by a public authority. The relay generates its own. This is fine because we use pinning, not the public CA trust model.

#### Layer 3: App Attestation

**What:** The relay daemon verifies that the connecting client is a genuine, unmodified BitChat app.

**Implementation (Android):**
- Uses Google Play Integrity API
- On connection, the app requests a Play Integrity token from Google
- The token is sent to the relay daemon
- The relay daemon verifies the token with Google's servers
- Token includes: app package name, signing certificate hash, device integrity verdict

**What this blocks:**
- Python scripts, curl, netcat — no valid token
- Modified/repackaged APKs — different signing certificate hash
- Emulators — device integrity check fails

**What this does NOT block:**
- Rooted phones with Magisk hiding root (advanced attacker)
- Nation-state level attacks

**Fallback for non-Play-Store builds (F-Droid, sideloaded):**
- Alternative attestation: client presents its APK signing certificate hash
- Relay daemon maintains an allowlist of known legitimate certificate hashes
- Less secure than Play Integrity but allows open-source distribution

#### Layer 4: Proof of Work

**What:** Before the relay daemon accepts a connection, the client must solve a computational puzzle.

**How it works:**
1. Relay daemon sends a random challenge (nonce) to the phone
2. Phone must find a value X such that `SHA256(nonce + X)` has N leading zero bits
3. N is adjustable based on network load (more load = harder puzzle)
4. Phone sends X back to the relay daemon
5. Relay daemon verifies in microseconds (verification is always fast)
6. Connection proceeds only after correct solution

**Why this helps:**
- Legitimate user: solves one puzzle (~0.5 seconds), connects, chats
- Attacker opening 1,000 connections: must solve 1,000 puzzles (~500 seconds of CPU time)
- DDoS requires proportional attacker CPU investment
- Difficulty auto-scales: when the relay is under load, difficulty increases, making attacks more expensive

**Key term:**
- **Proof of Work (PoW):** A concept where you prove you've expended computational effort. Bitcoin mining uses PoW. We use a much lighter version — milliseconds, not minutes.

#### Layer 5: Rate Limiting and Anomaly Detection

**What:** Server-side enforcement that no client can abuse the relay, regardless of what software they run.

**Per-client limits:**
- Max 10 packets/second
- Max 50 KB/second throughput
- Max 64 KB per individual packet
- Max 1,000 packets per 5-minute window

**Global limits:**
- Max 20 simultaneous client connections per relay daemon
- Max 100 packets/second aggregate relay throughput
- Max 500 KB/second aggregate bandwidth

**Anomaly detection:**
- Track rolling average packet rate per client
- If a client exceeds 10x the average rate: temporary 10-minute ban
- If a client sends >50% malformed packets: permanent ban until reconnect
- Exponential backoff on repeated bans (10min → 30min → 1hr → 24hr)

**Why this is the most important layer:**
- It works regardless of client software
- A compromised client can connect but can't do meaningful damage
- The relay daemon controls the connection — the client cannot bypass server-side checks

#### Layer 6: Noise Protocol End-to-End Encryption (Existing)

**What:** BitChat's existing encryption for private messages, unchanged by the WiFi mesh transport.

**Protocol:** Noise_XX_25519_ChaChaPoly_SHA256

**Properties:**
- **End-to-end:** Encryption keys are generated and stored on phones only. Relay daemons, routers, and any intermediary never possess the keys.
- **Forward secrecy:** Each session uses ephemeral keys. Compromising today's key doesn't reveal yesterday's messages.
- **Mutual authentication:** Both parties verify each other's identity during the handshake.
- **Deniability:** Neither party can prove to a third party what the other said.

**Key terms:**
- **X25519:** An elliptic curve Diffie-Hellman key exchange algorithm. Two parties each generate a key pair, exchange public keys, and derive a shared secret — without ever transmitting the secret itself. Even an observer who sees both public keys cannot compute the shared secret.
- **ChaChaPoly (ChaCha20-Poly1305):** A symmetric encryption algorithm (used after key exchange). ChaCha20 encrypts the data; Poly1305 authenticates it (ensures it hasn't been tampered with).
- **Forward secrecy:** Even if an attacker records all encrypted traffic and later steals a phone, they cannot decrypt past messages because the ephemeral keys were deleted after use.

#### Layer 7: Mesh Resilience (batman-adv)

**What:** The mesh network itself is resilient to node failures, attacks, and jamming.

**Properties:**
- **Self-healing:** When a node goes down, batman-adv reroutes traffic within seconds
- **No single point of failure:** Any node can be removed without killing the network
- **Decentralized routing:** No central controller; every node makes independent routing decisions
- **Encrypted mesh links:** Using 802.11s SAE (Simultaneous Authentication of Equals), mesh peering requires a shared secret, preventing unauthorized nodes from joining

**Mesh authentication (802.11s SAE):**
- All legitimate mesh routers share a mesh password
- New routers must know the password to join the mesh at the radio layer
- A rogue router without the password cannot establish mesh links
- SAE is resistant to offline dictionary attacks (unlike WPA2-PSK)

### 3.3 Packet Authenticity

Every BitchatPacket includes an Ed25519 digital signature from the sender:

```
[Packet header + payload] → SHA-256 hash → Ed25519 sign with sender's private key → 64-byte signature appended
```

**What this guarantees:**
- **Authenticity:** The packet was created by the claimed sender (only they have the private key)
- **Integrity:** The packet has not been modified in transit (any change invalidates the signature)
- **Non-repudiation:** The sender cannot deny sending the message (the signature proves it)

**What this means for relay daemons:**
- A relay daemon cannot forge messages
- A relay daemon cannot modify messages
- A rogue relay daemon can only drop messages (selective forwarding attack), which batman-adv mitigates by routing around unresponsive nodes

### 3.4 What Remains Vulnerable (Honest Assessment)

| Attack | Possible? | Mitigation |
|---|---|---|
| Traffic analysis (who talks to whom) | Yes, at mesh level | Cover traffic, message padding, random delays (all existing in BitChat) |
| Selective packet dropping by rogue node | Yes | batman-adv routes around; gossip sync recovers missed messages |
| Local radio jamming | Yes (physical attack) | Mesh density; BLE fallback on different frequency |
| Device theft (physical) | Yes | Emergency wipe (triple-tap); keys stored in Android Keystore (hardware-backed on modern phones) |
| Compromising one phone | Yes | Only that phone's messages exposed; other users unaffected |
| Denial of service via flooding | Limited | Rate limiting caps damage to one connection's quota |

---

## 4. Wire Protocol

### 4.1 Phone ↔ Relay Daemon (TCP/TLS)

**Connection handshake:**
```
1. TCP connect to relay daemon (port 7275)
2. TLS 1.3 handshake (certificate pinning)
3. Client sends: HELLO frame
   [4 bytes: magic 0x42435748 ("BCWH" = BitChat WiFi Hello)]
   [2 bytes: protocol version (0x0001)]
   [32 bytes: client's Noise public key fingerprint]
4. Server sends: CHALLENGE frame
   [4 bytes: magic 0x42435743 ("BCWC" = BitChat WiFi Challenge)]
   [32 bytes: PoW nonce]
   [1 byte: PoW difficulty (number of leading zero bits)]
5. Client solves PoW, sends: SOLUTION frame
   [4 bytes: magic 0x42435753 ("BCWS" = BitChat WiFi Solution)]
   [8 bytes: PoW solution]
   [variable: Play Integrity token or APK certificate hash]
6. Server verifies, sends: ACCEPT or REJECT frame
   [4 bytes: magic 0x42435741 ("BCWA") or 0x42435752 ("BCWR")]
7. Bidirectional packet exchange begins
```

**Packet framing (after handshake):**
```
[4 bytes: payload length, big-endian uint32, max 65536]
[N bytes: BitchatPacket binary data (identical to BLE format)]
```

**Keepalive:**
```
[4 bytes: 0x00000000 (zero-length frame = keepalive ping)]
```
Sent every 30 seconds by both sides. If no data or keepalive received for 90 seconds, connection is considered dead.

### 4.2 Relay Daemon ↔ Relay Daemon (UDP Multicast on bat0)

**Multicast group:** `239.66.67.87` (B=66, C=67, W=87 in ASCII — "BCW")
**Port:** 7276

**Packet format:**
```
[4 bytes: magic 0x42434D50 ("BCMP" = BitChat Mesh Packet)]
[8 bytes: relay daemon ID (first 8 bytes of relay's key fingerprint)]
[4 bytes: packet hash (first 4 bytes of SHA-256, for deduplication)]
[4 bytes: payload length, big-endian uint32]
[N bytes: BitchatPacket binary data]
```

**Deduplication:** Each relay daemon maintains a bloom filter of recently seen packet hashes (last 10,000 packets). Duplicate packets from the multicast group are silently dropped.

---

## 5. Hardware Recommendations

### 5.1 Router Tiers

#### Edge Node (Small Coverage, 10-20 users)

**Recommended:** GL.iNet GL-MT300N-V2 (Mango)
- **CPU:** MediaTek MT7628NN, 580 MHz MIPS
- **RAM:** 128 MB DDR2
- **Flash:** 16 MB NOR
- **WiFi:** 2.4 GHz 802.11b/g/n, 300 Mbps
- **Price:** ~$25 USD
- **Range:** ~50m indoor, ~100m outdoor
- **Power:** 5V/1A USB (power bank compatible)
- **Pros:** Cheap, portable, low power, runs OpenWrt natively
- **Cons:** Single band (2.4 GHz only), limited CPU for high connection counts
- **Use case:** Personal mesh, small gatherings, temporary deployments

#### Neighborhood Node (Medium Coverage, 50-100 users)

**Recommended:** GL.iNet GL-MT3000 (Beryl AX)
- **CPU:** MediaTek MT7981B, 1.3 GHz dual-core ARM
- **RAM:** 512 MB DDR4
- **Flash:** 256 MB NAND
- **WiFi:** 2.4 GHz + 5 GHz, WiFi 6 (802.11ax), 3000 Mbps
- **Price:** ~$70 USD
- **Range:** ~80m indoor, ~200m outdoor (with 5 GHz mesh backhaul)
- **Power:** 5V/3A USB-C
- **Pros:** Dual-band (5 GHz for mesh backhaul, 2.4 GHz for clients), fast CPU
- **Cons:** Higher power consumption
- **Use case:** Neighborhood mesh, community centers, parks

#### Backbone Node (High Capacity, 200+ users)

**Recommended:** Ubiquiti UniFi 6 Mesh or similar enterprise outdoor AP
- **CPU:** Quad-core ARM, 1+ GHz
- **RAM:** 512 MB+
- **WiFi:** Tri-band or dual-band WiFi 6
- **Price:** ~$180-300 USD
- **Range:** ~250m outdoor with directional antennas
- **Power:** PoE (Power over Ethernet) — single cable for data + power
- **Pros:** Weatherproof, high client capacity, excellent range
- **Cons:** Expensive, requires PoE switch, may need custom OpenWrt build
- **Use case:** City-scale mesh, permanent outdoor installations

### 5.2 Deployment Patterns

#### Personal / Protest (2-5 nodes)

```
[Mango] ──── [Mango] ──── [Mango]
  │              │              │
5 users      5 users       5 users
```
- All Mango edge nodes
- Battery/USB powered
- Range: ~300m total
- Cost: ~$75-125

#### Neighborhood (10-30 nodes)

```
[Mango]──[Beryl]──[Mango]
   │        │         │
  [Mango]  [Mango]  [Mango]
```
- Beryl AX as backbone nodes (mounted on rooftops)
- Mango as edge nodes (in homes/businesses)
- 5 GHz mesh backhaul between Beryl nodes, 2.4 GHz for clients
- Range: ~1 km²
- Cost: ~$500-1000

#### City-Scale (100-1000+ nodes)

```
Zone A ═══ Zone B ═══ Zone C
  │          │          │
[Backbone]=[Backbone]=[Backbone]
  │    │     │    │     │    │
[Edge][Edge][Edge][Edge][Edge][Edge]
```
- Enterprise backbone nodes with directional antennas
- Beryl AX as zone routers
- Mango as edge/indoor nodes
- Segmented mesh with zone-aware routing
- Range: entire city
- Cost: $5,000-50,000 depending on density

### 5.3 Minimum Requirements for Any Router

- OpenWrt 23.05+ support (for mt76/ath9k open-source WiFi drivers)
- 64 MB+ RAM (relay daemon needs ~10-20 MB with Go runtime)
- 16 MB+ flash (for OpenWrt + relay daemon binary)
- 802.11s mesh support in the WiFi driver
- batman-adv kernel module available

---

## 6. Scaling Architecture

### 6.1 Message Scoping

Not every message needs to reach every node. Messages are scoped:

- **Public mesh messages:** Flooded within the local zone (5-20 nodes) using TTL-based propagation
- **Private messages:** Source-routed along the shortest path (v2 protocol with route field)
- **Cross-zone messages:** Forwarded by zone gateway nodes to the destination zone

### 6.2 Zone Architecture

At scale (100+ nodes), the mesh is divided into zones:

- Each zone is a batman-adv mesh domain (up to ~50 nodes)
- Zones are interconnected via gateway nodes that peer with multiple zones
- The relay daemon on gateway nodes bridges traffic between zones
- Routing between zones uses a lightweight distributed routing table

### 6.3 Store-and-Forward at Scale

For large deployments, the relay daemon implements distributed caching:

- Each relay stores recent packets in a circular buffer (configurable, default 1,000 packets)
- When a phone connects, it requests missed packets since its last connection
- For very large meshes, a DHT (distributed hash table) assigns packet storage responsibility based on recipient peer ID hash
- This ensures storage load is distributed evenly across nodes

### 6.4 Target Performance

| Metric | Edge Node (Mango) | Backbone Node |
|---|---|---|
| Concurrent phones | 20 | 200+ |
| Packets/second | 100 | 1,000+ |
| Bandwidth | 5 Mbps | 50+ Mbps |
| Relay daemon RAM | 10 MB | 50 MB |
| Packet buffer | 1,000 packets | 10,000 packets |

---

## 7. Comparison with Alternatives

### 7.1 Why Not WiFi Direct?

WiFi Direct (P2P) creates direct phone-to-phone WiFi links without an access point. Android supports it.

**Why we chose dedicated routers instead:**
- WiFi Direct requires both phones to be awake and nearby
- No mesh routing — only direct links
- Drains phone battery
- Android limits background WiFi Direct operations
- Maximum ~8 peers per group

Dedicated routers solve all of these: they're always on, route automatically, and phones just connect to an AP.

### 7.2 Why Not WiFi Aware (NAN)?

WiFi Aware (Neighbor Awareness Networking) is an Android API for device-to-device WiFi.

**Why we don't use it:**
- Requires Android 8.0+ AND hardware support (many phones lack it)
- Limited range (~50m, similar to BLE)
- No mesh routing
- Google can change/restrict the API at any time

### 7.3 Why Not LoRa?

LoRa is a long-range, low-power radio protocol used for IoT.

**Why it's complementary, not a replacement:**
- Very low bandwidth (~0.3-50 Kbps) — can send text but not media
- Requires specialized hardware (LoRa radios, not in phones)
- Great for emergency text-only mesh (Meshtastic project)
- Could be a future 4th transport for ultra-long-range text

### 7.4 Why batman-adv Over Other Mesh Protocols?

| Protocol | Type | Pros | Cons |
|---|---|---|---|
| **batman-adv** (chosen) | Layer 2, kernel module | Fast, transparent to apps, mature | Linux-only, kernel dependency |
| OLSR | Layer 3, userspace | Widely deployed, cross-platform | Higher overhead, slower convergence |
| Babel | Layer 3, userspace | Good for heterogeneous networks | Less tested at scale |
| 802.11s native | Layer 2, driver-level | No extra software | Limited routing intelligence, no mesh optimization |

batman-adv was chosen because:
1. It operates at Layer 2 (Ethernet frames), making the mesh transparent to all applications
2. It's a Linux kernel module — very fast, no userspace overhead
3. Mature project (15+ years of development)
4. Used in real-world community mesh networks (Freifunk in Germany, thousands of nodes)
5. Self-healing and decentralized routing

---

## 8. Privacy Considerations

### 8.1 Metadata Protection

Even with encrypted content, metadata can reveal communication patterns:

- **Who talks to whom:** Visible to relay daemons handling the traffic
- **When:** Timestamps on packets
- **How much:** Packet sizes

**Mitigations (existing in BitChat):**
- **Message padding:** All messages are padded to standard block sizes, hiding true message length
- **Cover traffic:** Random dummy messages are sent periodically, masking real communication patterns
- **Ephemeral peer IDs:** Peer IDs are derived from Noise key fingerprints and rotate with key rotation

### 8.2 No Registration, No Phone Numbers

BitChat requires no account, email, phone number, or any identifying information. Identity is purely cryptographic — a key pair generated on the phone.

### 8.3 Emergency Wipe

Triple-tap the BitChat logo to instantly clear all data: messages, keys, identity. The app returns to a fresh state with a new random identity.

### 8.4 Relay Daemon Privacy

The relay daemon is designed to be privacy-respecting:
- No logging of message contents (it can't read private messages anyway)
- Connection logs are ephemeral (in-memory only, not written to disk)
- No user tracking or analytics
- Packet buffer is a fixed-size circular buffer that overwrites old data automatically

---

## 9. Open Questions and Future Work

### 9.1 Post-Quantum Cryptography

Current encryption (X25519, Ed25519) is secure against classical computers but theoretically vulnerable to future quantum computers running Shor's algorithm. Future versions should consider:
- Noise Protocol with hybrid key exchange (X25519 + Kyber/ML-KEM)
- Ed25519 + Dilithium/ML-DSA for signatures

### 9.2 Incentive Mechanisms

For city-scale deployment, mesh operators need incentives to maintain nodes. Possible approaches:
- Community-funded (donations, grants)
- Mesh tokens (cryptocurrency-based incentives for relaying — controversial)
- Municipal funding (public infrastructure)

### 9.3 Interoperability with Meshtastic / LoRa

A future transport could bridge BitChat with Meshtastic (LoRa mesh) for ultra-long-range text-only communication.

### 9.4 iOS Support

The WiFi mesh transport design is platform-agnostic. An iOS implementation would use the same relay protocol and could interoperate with Android devices on the same mesh.

---

## 10. Glossary

| Term | Definition |
|---|---|
| **802.11s** | IEEE standard for WiFi mesh networking at Layer 2. Allows routers to form mesh links without a central AP. |
| **AP (Access Point)** | A WiFi radio that broadcasts an SSID and allows client devices (phones, laptops) to connect. |
| **batman-adv** | B.A.T.M.A.N. Advanced — a Layer 2 mesh routing protocol implemented as a Linux kernel module. Creates a virtual interface (bat0) that makes all mesh nodes appear to be on the same local network. |
| **BLE** | Bluetooth Low Energy — a short-range, low-power wireless protocol used for phone-to-phone mesh. |
| **Bloom filter** | A space-efficient probabilistic data structure for testing set membership. Used for packet deduplication. May have false positives (says "seen" when not) but never false negatives. |
| **Certificate pinning** | A security technique where an app only trusts a specific TLS certificate, not any certificate signed by a certificate authority. |
| **Cover traffic** | Fake messages sent to mask real communication patterns, preventing traffic analysis. |
| **DHT** | Distributed Hash Table — a decentralized key-value store spread across multiple nodes. Each node is responsible for a subset of keys. |
| **Ed25519** | A digital signature algorithm using elliptic curves. Used to sign BitchatPackets to prove authenticity. |
| **Forward secrecy** | A property where compromising long-term keys doesn't reveal past session keys or past messages. |
| **Frida** | A reverse-engineering tool that can hook and modify function calls in running apps. |
| **mDNS** | Multicast DNS — a protocol for discovering services on a local network without a central DNS server. |
| **MITM** | Man-in-the-Middle — an attack where the attacker intercepts communication between two parties. |
| **Noise Protocol** | A framework for building cryptographic protocols. BitChat uses the XX pattern with X25519 key exchange and ChaCha20-Poly1305 encryption. |
| **OpenWrt** | An open-source Linux distribution designed for embedded network devices (routers). |
| **OWE** | Opportunistic Wireless Encryption — a WPA3 feature that encrypts open WiFi networks without requiring a password. |
| **Play Integrity API** | A Google service that lets apps prove they are genuine, unmodified, and running on a legitimate device. |
| **PoW (Proof of Work)** | A mechanism requiring computational effort before an action is allowed. Makes flooding attacks expensive. |
| **SAE** | Simultaneous Authentication of Equals — a WPA3 key exchange protocol resistant to offline dictionary attacks. |
| **Side-channel attack** | Extracting secret information from physical emanations (power consumption, electromagnetic radiation, timing, sound) rather than breaking the cryptographic algorithm itself. |
| **SSID** | Service Set Identifier — the name of a WiFi network visible to users. |
| **Sybil attack** | An attack where one entity creates many fake identities to gain disproportionate influence in a network. |
| **TLS** | Transport Layer Security — a protocol for encrypting data in transit between two endpoints. |
| **TTL** | Time To Live — a counter on each packet that decrements at each hop. When it reaches zero, the packet is dropped. Prevents infinite loops. |
| **WPA2/WPA3** | WiFi Protected Access — security protocols that encrypt WiFi radio traffic. WPA3 is the newer, stronger version. |
| **X25519** | An elliptic curve Diffie-Hellman function for key exchange. Two parties derive a shared secret without transmitting it. |
