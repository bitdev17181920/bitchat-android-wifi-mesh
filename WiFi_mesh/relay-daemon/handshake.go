package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"
)

// PerformHandshake executes the server side of the connection handshake:
//
//	Client → HELLO  (version + peer ID)
//	Server → CHALLENGE (nonce + difficulty)
//	Client → SOLUTION  (uint64 PoW answer)
//	Server → ACCEPT | REJECT
//
// Returns the peer ID on success.
func PerformHandshake(conn net.Conn, cfg *Config) (string, error) {
	conn.SetDeadline(time.Now().Add(cfg.HandshakeTimeout))
	defer conn.SetDeadline(time.Time{}) // clear deadline after handshake

	// --- Step 1: Read HELLO ---
	hello, err := ReadFrame(conn, cfg.MaxPacketSize)
	if err != nil {
		return "", fmt.Errorf("read HELLO: %w", err)
	}
	if hello.Type != FrameHello {
		return "", fmt.Errorf("expected HELLO (0x%02x), got 0x%02x", FrameHello, hello.Type)
	}
	if len(hello.Payload) < 3 {
		return "", fmt.Errorf("HELLO too short: %d bytes", len(hello.Payload))
	}

	version := binary.BigEndian.Uint16(hello.Payload[0:2])
	peerIDLen := int(hello.Payload[2])
	if 3+peerIDLen > len(hello.Payload) {
		return "", fmt.Errorf("HELLO peer-ID length overflows payload")
	}
	peerID := string(hello.Payload[3 : 3+peerIDLen])

	if version != ProtocolVersion {
		_ = WriteFrame(conn, FrameReject, []byte(fmt.Sprintf("unsupported version %d", version)))
		return "", fmt.Errorf("unsupported protocol version %d", version)
	}

	// --- Step 1b: Verify APK cert hash (if enforcement is enabled) ---
	certHashOffset := 3 + peerIDLen
	if certHashOffset+32 <= len(hello.Payload) {
		certHash := hello.Payload[certHashOffset : certHashOffset+32]
		certHashHex := hex.EncodeToString(certHash)
		if len(cfg.AllowedCertHashes) > 0 {
			if !cfg.AllowedCertHashes[certHashHex] {
				_ = WriteFrame(conn, FrameReject, []byte("certificate not authorized"))
				return "", fmt.Errorf("rejected cert hash %s from peer %s", certHashHex, peerID)
			}
			log.Printf("Peer %s cert hash verified: %s…", peerID, certHashHex[:16])
		} else {
			log.Printf("Peer %s presented cert hash %s… (enforcement off)", peerID, certHashHex[:16])
		}
	} else if len(cfg.AllowedCertHashes) > 0 {
		_ = WriteFrame(conn, FrameReject, []byte("certificate hash required"))
		return "", fmt.Errorf("peer %s did not provide cert hash (required)", peerID)
	}

	// --- Step 2: Send CHALLENGE ---
	nonce, err := GenerateChallenge()
	if err != nil {
		return "", fmt.Errorf("generate challenge: %w", err)
	}
	challenge := make([]byte, 33)
	copy(challenge[:32], nonce[:])
	challenge[32] = cfg.PoWDifficulty

	if err := WriteFrame(conn, FrameChallenge, challenge); err != nil {
		return "", fmt.Errorf("write CHALLENGE: %w", err)
	}

	// --- Step 3: Read SOLUTION ---
	sol, err := ReadFrame(conn, cfg.MaxPacketSize)
	if err != nil {
		return "", fmt.Errorf("read SOLUTION: %w", err)
	}
	if sol.Type != FrameSolution {
		return "", fmt.Errorf("expected SOLUTION (0x%02x), got 0x%02x", FrameSolution, sol.Type)
	}
	if len(sol.Payload) != 8 {
		return "", fmt.Errorf("SOLUTION wrong size: %d (expected 8)", len(sol.Payload))
	}

	solution := binary.BigEndian.Uint64(sol.Payload)

	// --- Step 4: Verify PoW ---
	if !VerifyPoW(nonce, solution, cfg.PoWDifficulty) {
		_ = WriteFrame(conn, FrameReject, []byte("invalid proof of work"))
		return "", fmt.Errorf("invalid PoW from peer %s", peerID)
	}

	// --- Step 5: Accept ---
	if err := WriteFrame(conn, FrameAccept, nil); err != nil {
		return "", fmt.Errorf("write ACCEPT: %w", err)
	}

	return peerID, nil
}
