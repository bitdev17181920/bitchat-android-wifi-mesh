package main

import (
	"fmt"
	"log"
	"net"
)

// Mesh packet header sizes
const (
	pubKeyLen = 32
	certLen   = 64
	sigLen    = 64
	// CA mode: [32 pubkey][64 cert][64 sig][payload] = 160 byte header
	caHeaderLen = pubKeyLen + certLen + sigLen
)

// MeshLink handles UDP multicast communication with other relay daemons
// over the batman-adv interface. Outgoing packets are signed with Ed25519
// and include the relay's CA certificate. Incoming packets are verified
// against the CA and checked for revocation.
type MeshLink struct {
	sendConn *net.UDPConn
	recvConn *net.UDPConn
	cfg      *Config
	router   *Router
	auth     *RelayAuth
}

func NewMeshLink(cfg *Config) (*MeshLink, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", cfg.MeshMulticast, cfg.MeshPort))
	if err != nil {
		return nil, fmt.Errorf("resolve multicast: %w", err)
	}

	iface, err := net.InterfaceByName(cfg.MeshInterface)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", cfg.MeshInterface, err)
	}

	addrs, err := iface.Addrs()
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses on %s: %w", cfg.MeshInterface, err)
	}
	var localIP net.IP
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			localIP = ipnet.IP
			break
		}
	}
	if localIP == nil {
		return nil, fmt.Errorf("no IPv4 address on %s", cfg.MeshInterface)
	}

	sendConn, err := net.DialUDP("udp4", &net.UDPAddr{IP: localIP}, addr)
	if err != nil {
		return nil, fmt.Errorf("dial multicast: %w", err)
	}

	recvConn, err := net.ListenMulticastUDP("udp4", iface, addr)
	if err != nil {
		sendConn.Close()
		return nil, fmt.Errorf("listen multicast: %w", err)
	}
	recvConn.SetReadBuffer(1 << 20)

	return &MeshLink{
		sendConn: sendConn,
		recvConn: recvConn,
		cfg:      cfg,
	}, nil
}

func (m *MeshLink) SetRouter(r *Router) { m.router = r }
func (m *MeshLink) SetAuth(a *RelayAuth) { m.auth = a }

// Send transmits data via UDP multicast with Ed25519 signature.
// CA mode: [32 pubkey][64 cert][64 sig][payload]
// Legacy (no CA): [32 pubkey][64 sig][payload]
func (m *MeshLink) Send(data []byte) {
	if m.auth == nil {
		return
	}

	sig := m.auth.Sign(data)

	if m.auth.HasCA() && m.auth.HasCertificate() {
		msg := make([]byte, caHeaderLen+len(data))
		copy(msg[:pubKeyLen], m.auth.PublicKey)
		copy(msg[pubKeyLen:pubKeyLen+certLen], m.auth.Certificate)
		copy(msg[pubKeyLen+certLen:caHeaderLen], sig)
		copy(msg[caHeaderLen:], data)
		if _, err := m.sendConn.Write(msg); err != nil {
			log.Printf("mesh send: %v", err)
		} else {
			log.Printf("mesh send: %d bytes (CA-signed)", len(data))
		}
	} else {
		legacyHeaderLen := pubKeyLen + sigLen
		msg := make([]byte, legacyHeaderLen+len(data))
		copy(msg[:pubKeyLen], m.auth.PublicKey)
		copy(msg[pubKeyLen:legacyHeaderLen], sig)
		copy(msg[legacyHeaderLen:], data)
		if _, err := m.sendConn.Write(msg); err != nil {
			log.Printf("mesh send: %v", err)
		} else {
			log.Printf("mesh send: %d bytes (legacy-signed)", len(data))
		}
	}
}

// RecvLoop reads UDP multicast packets from other relay daemons.
// CA mode expects [32 pubkey][64 cert][64 sig][payload].
// Legacy mode expects [32 pubkey][64 sig][payload].
func (m *MeshLink) RecvLoop() {
	buf := make([]byte, m.cfg.MaxPacketSize+caHeaderLen)
	for {
		n, _, err := m.recvConn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("mesh recv: %v", err)
			continue
		}

		if m.auth == nil || n <= pubKeyLen+sigLen {
			continue
		}

		pubKey := make([]byte, pubKeyLen)
		copy(pubKey, buf[:pubKeyLen])

		// Step 1: skip our own multicast echo
		if m.auth.IsSelf(pubKey) {
			continue
		}

		// Step 2: check revocation list
		if m.auth.IsRevoked(pubKey) {
			log.Printf("mesh recv: REVOKED key %x…", pubKey[:8])
			continue
		}

		if m.auth.HasCA() {
			// CA mode: [32 pubkey][64 cert][64 sig][payload]
			if n <= caHeaderLen {
				continue
			}
			cert := make([]byte, certLen)
			copy(cert, buf[pubKeyLen:pubKeyLen+certLen])
			sig := make([]byte, sigLen)
			copy(sig, buf[pubKeyLen+certLen:caHeaderLen])
			data := make([]byte, n-caHeaderLen)
			copy(data, buf[caHeaderLen:n])

			// Step 3: verify certificate against CA (caches on success)
			if !m.auth.VerifyCertificate(pubKey, cert) {
				log.Printf("mesh recv: invalid CA cert from %x…", pubKey[:8])
				continue
			}

			// Step 4: verify packet signature
			if !m.auth.Verify(pubKey, sig, data) {
				log.Printf("mesh recv: invalid signature from %x…", pubKey[:8])
				continue
			}

			if m.router != nil {
				m.router.RouteFromMesh(data)
			}
		} else {
			// Legacy/open mode: [32 pubkey][64 sig][payload]
			legacyHeaderLen := pubKeyLen + sigLen
			if n <= legacyHeaderLen {
				continue
			}
			sig := make([]byte, sigLen)
			copy(sig, buf[pubKeyLen:legacyHeaderLen])
			data := make([]byte, n-legacyHeaderLen)
			copy(data, buf[legacyHeaderLen:n])

			if !m.auth.Verify(pubKey, sig, data) {
				log.Printf("mesh recv: invalid signature from %x…", pubKey[:8])
				continue
			}

			if m.router != nil {
				m.router.RouteFromMesh(data)
			}
		}
	}
}

func (m *MeshLink) Close() {
	m.sendConn.Close()
	m.recvConn.Close()
}
