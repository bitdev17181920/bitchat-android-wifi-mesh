package main

import "time"

type Config struct {
	// Network
	TLSPort       int
	MeshPort      int
	MeshInterface string
	MeshMulticast string
	CertDir       string

	// Capacity
	MaxClients    int
	MaxPacketSize int

	// Per-client rate limiting
	ClientPacketsPerSec float64
	ClientBurstSize     int

	// Global rate limiting
	GlobalPacketsPerSec float64
	GlobalBurstSize     int

	// Proof of Work
	PoWDifficulty uint8

	// Store-and-forward buffer
	BufferSize int

	// Deduplication
	DedupMaxEntries int

	// Timeouts
	KeepaliveInterval time.Duration
	KeepaliveTimeout  time.Duration
	HandshakeTimeout  time.Duration

	// APK cert hash attestation (hex-encoded SHA-256 hashes)
	AllowedCertHashes map[string]bool

	// Ed25519 relay-to-relay signing
	KeyDir   string
	CAPubKey string // hex-encoded CA public key; empty = open/legacy mode
	CRLPath  string // path to certificate revocation list file
}

func DefaultConfig() *Config {
	return &Config{
		TLSPort:       7275,
		MeshPort:      7276,
		MeshInterface: "bat0",
		MeshMulticast: "239.0.7.2",
		CertDir:       "/etc/bitchat",

		MaxClients:    20,
		MaxPacketSize: 65536,

		ClientPacketsPerSec: 10,
		ClientBurstSize:     20,
		GlobalPacketsPerSec: 100,
		GlobalBurstSize:     200,

		PoWDifficulty: 20,

		BufferSize:      1000,
		DedupMaxEntries: 10000,

		KeepaliveInterval: 30 * time.Second,
		KeepaliveTimeout:  90 * time.Second,
		HandshakeTimeout:  30 * time.Second,

		AllowedCertHashes: nil,
		KeyDir:            "/etc/bitchat",
		CAPubKey:          "",
		CRLPath:           "/etc/bitchat/revoked.crl",
	}
}
