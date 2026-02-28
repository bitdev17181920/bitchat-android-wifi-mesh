package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	cfg := DefaultConfig()

	flag.IntVar(&cfg.TLSPort, "port", cfg.TLSPort, "TLS listen port for phone connections")
	flag.IntVar(&cfg.MeshPort, "mesh-port", cfg.MeshPort, "UDP multicast port for inter-daemon mesh")
	flag.StringVar(&cfg.MeshInterface, "mesh-iface", cfg.MeshInterface, "batman-adv network interface")
	flag.StringVar(&cfg.MeshMulticast, "mesh-group", cfg.MeshMulticast, "multicast group address")
	flag.StringVar(&cfg.CertDir, "cert-dir", cfg.CertDir, "TLS certificate directory")
	flag.IntVar(&cfg.MaxClients, "max-clients", cfg.MaxClients, "maximum simultaneous phone connections")
	difficulty := flag.Int("pow-difficulty", int(cfg.PoWDifficulty), "proof-of-work difficulty (leading zero bits)")
	certHashes := flag.String("allowed-cert-hash", "", "comma-separated APK cert SHA-256 hashes (hex); empty = open")
	keyDir := flag.String("key-dir", cfg.KeyDir, "directory for relay Ed25519 signing key")
	caPubKey := flag.String("ca-pubkey", cfg.CAPubKey, "CA public key (hex) for relay certificate verification; empty = open")
	crlPath := flag.String("crl-path", cfg.CRLPath, "path to certificate revocation list file")
	flag.Parse()
	cfg.PoWDifficulty = uint8(*difficulty)
	cfg.KeyDir = *keyDir
	cfg.CAPubKey = *caPubKey
	cfg.CRLPath = *crlPath

	if *certHashes != "" {
		cfg.AllowedCertHashes = make(map[string]bool)
		for _, h := range strings.Split(*certHashes, ",") {
			h = strings.TrimSpace(strings.ToLower(h))
			if h != "" {
				cfg.AllowedCertHashes[h] = true
			}
		}
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("BitChat Relay Daemon starting")
	log.Printf("  TLS port:       %d", cfg.TLSPort)
	log.Printf("  Mesh port:      %d (multicast %s)", cfg.MeshPort, cfg.MeshMulticast)
	log.Printf("  Mesh interface: %s", cfg.MeshInterface)
	log.Printf("  Max clients:    %d", cfg.MaxClients)
	log.Printf("  PoW difficulty: %d bits", cfg.PoWDifficulty)
	log.Printf("  Cert directory: %s", cfg.CertDir)

	relayAuth, err := NewRelayAuth(cfg.KeyDir, cfg.CAPubKey, cfg.CRLPath)
	if err != nil {
		log.Fatalf("FATAL: relay auth init failed: %v", err)
	}
	log.Printf("  Relay pubkey:   %s", relayAuth.PublicKeyHex())
	if relayAuth.HasCA() {
		log.Printf("  CA mode:        ENABLED")
		if relayAuth.HasCertificate() {
			log.Printf("  Relay cert:     loaded")
		} else {
			log.Printf("  Relay cert:     MISSING (run mesh-ca sign to issue one)")
		}
	} else {
		log.Printf("  CA mode:        disabled (open mesh)")
	}
	if len(cfg.AllowedCertHashes) > 0 {
		log.Printf("  APK hashes:     %d allowed", len(cfg.AllowedCertHashes))
	} else {
		log.Printf("  APK hashes:     open (any app accepted)")
	}

	router := NewRouter(cfg)

	mesh, err := NewMeshLink(cfg)
	if err != nil {
		log.Printf("WARNING: mesh link unavailable: %v", err)
		log.Printf("Running in standalone mode (no inter-router forwarding)")
	} else {
		mesh.SetRouter(router)
		mesh.SetAuth(relayAuth)
		router.SetMesh(mesh)
		go mesh.RecvLoop()
		defer mesh.Close()
		if relayAuth.HasCA() {
			log.Printf("Mesh link active on %s (%s:%d) [CA-verified Ed25519]", cfg.MeshInterface, cfg.MeshMulticast, cfg.MeshPort)
		} else {
			log.Printf("Mesh link active on %s (%s:%d) [Ed25519 signed, open]", cfg.MeshInterface, cfg.MeshMulticast, cfg.MeshPort)
		}
	}

	server, err := NewServer(cfg, router)
	if err != nil {
		log.Fatalf("FATAL: server start failed: %v", err)
	}
	defer server.Close()
	go server.Serve()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %s — shutting down", sig)
}
