package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RelayAuth manages an Ed25519 keypair for signing inter-relay mesh
// packets and verifying packets from CA-certified peer relays.
//
// Trust model:
//   - Each relay has its own Ed25519 keypair (generated on first run)
//   - A CA signs each relay's public key, producing a 64-byte certificate
//   - On mesh recv, the packet's signer pubkey is verified against the CA
//   - Revoked keys are checked against a periodically reloaded CRL file
type RelayAuth struct {
	PrivateKey  ed25519.PrivateKey
	PublicKey   ed25519.PublicKey
	Certificate []byte // 64-byte CA signature over this relay's public key

	CAPubKey    ed25519.PublicKey // the CA public key (trusted root)

	mu          sync.RWMutex
	revokedKeys map[string]bool // hex pubkeys that have been revoked
	crlPath     string
	crlModTime  time.Time

	// Peer certificate cache: hex(pubkey) → already verified against CA
	certCache   map[string]bool
	certCacheMu sync.RWMutex
}

// NewRelayAuth loads or generates an Ed25519 keypair, loads the CA
// public key and this relay's certificate, and initializes the CRL.
func NewRelayAuth(keyDir string, caKeyHex string, crlPath string) (*RelayAuth, error) {
	auth := &RelayAuth{
		revokedKeys: make(map[string]bool),
		certCache:   make(map[string]bool),
		crlPath:     crlPath,
	}

	// Load CA public key
	if caKeyHex != "" {
		caPub, err := hex.DecodeString(strings.TrimSpace(caKeyHex))
		if err != nil || len(caPub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid CA public key hex (need %d bytes)", ed25519.PublicKeySize)
		}
		auth.CAPubKey = ed25519.PublicKey(caPub)
	}

	// Load or generate relay keypair
	keyPath := filepath.Join(keyDir, "relay_ed25519.key")
	if data, err := os.ReadFile(keyPath); err == nil && len(data) == ed25519.PrivateKeySize {
		auth.PrivateKey = ed25519.PrivateKey(data)
		auth.PublicKey = auth.PrivateKey.Public().(ed25519.PublicKey)
		log.Printf("Loaded relay signing key: %s…", hex.EncodeToString(auth.PublicKey)[:16])
	} else {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ed25519 key: %w", err)
		}
		auth.PrivateKey = priv
		auth.PublicKey = pub

		if err := os.MkdirAll(keyDir, 0700); err != nil {
			return nil, fmt.Errorf("create key dir: %w", err)
		}
		if err := os.WriteFile(keyPath, []byte(priv), 0600); err != nil {
			return nil, fmt.Errorf("save key: %w", err)
		}
		log.Printf("Generated new relay signing key: %s…", hex.EncodeToString(pub)[:16])
	}

	// Load relay certificate (CA's signature over our public key)
	certPath := filepath.Join(keyDir, "relay.cert")
	if certData, err := os.ReadFile(certPath); err == nil {
		cert, err := hex.DecodeString(strings.TrimSpace(string(certData)))
		if err == nil && len(cert) == ed25519.SignatureSize {
			auth.Certificate = cert
			log.Printf("Loaded relay certificate")
		} else {
			log.Printf("WARNING: invalid relay certificate file, will run without cert")
		}
	}

	// Load CRL
	auth.loadCRL()

	// Background CRL reload every 60 seconds
	go auth.crlReloadLoop()

	return auth, nil
}

func (a *RelayAuth) Sign(data []byte) []byte {
	return ed25519.Sign(a.PrivateKey, data)
}

func (a *RelayAuth) Verify(pubKey, signature, data []byte) bool {
	if len(pubKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pubKey), data, signature)
}

// IsSelf returns true if pubKey is our own (for ignoring multicast echo).
func (a *RelayAuth) IsSelf(pubKey []byte) bool {
	return hex.EncodeToString(pubKey) == hex.EncodeToString(a.PublicKey)
}

// IsRevoked returns true if the given relay public key is on the CRL.
func (a *RelayAuth) IsRevoked(pubKey []byte) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.revokedKeys[hex.EncodeToString(pubKey)]
}

// IsTrustedLegacy checks trust in non-CA (open) mode. Only rejects
// own key and revoked keys.
func (a *RelayAuth) IsTrustedLegacy(pubKey []byte) bool {
	if a.IsSelf(pubKey) {
		return false
	}
	return !a.IsRevoked(pubKey)
}

// VerifyCertificate checks if a relay's certificate (CA signature over
// its public key) is valid, and caches the result if so.
func (a *RelayAuth) VerifyCertificate(pubKey, cert []byte) bool {
	if a.CAPubKey == nil || len(cert) != ed25519.SignatureSize {
		return false
	}
	if !ed25519.Verify(a.CAPubKey, pubKey, cert) {
		return false
	}
	pubHex := hex.EncodeToString(pubKey)
	a.certCacheMu.Lock()
	a.certCache[pubHex] = true
	a.certCacheMu.Unlock()
	return true
}

func (a *RelayAuth) PublicKeyHex() string {
	return hex.EncodeToString(a.PublicKey)
}

func (a *RelayAuth) HasCertificate() bool {
	return len(a.Certificate) == ed25519.SignatureSize
}

func (a *RelayAuth) HasCA() bool {
	return a.CAPubKey != nil
}

func (a *RelayAuth) loadCRL() {
	if a.crlPath == "" {
		return
	}
	info, err := os.Stat(a.crlPath)
	if err != nil {
		return
	}
	if info.ModTime().Equal(a.crlModTime) {
		return
	}
	data, err := os.ReadFile(a.crlPath)
	if err != nil {
		return
	}

	newRevoked := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "" && !strings.HasPrefix(line, "#") {
			newRevoked[line] = true
		}
	}

	a.mu.Lock()
	a.revokedKeys = newRevoked
	a.crlModTime = info.ModTime()
	a.mu.Unlock()

	if len(newRevoked) > 0 {
		log.Printf("CRL reloaded: %d revoked keys", len(newRevoked))
	}
}

func (a *RelayAuth) crlReloadLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.loadCRL()
	}
}
