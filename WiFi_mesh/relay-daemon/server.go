package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Server struct {
	cfg      *Config
	router   *Router
	listener net.Listener
}

func NewServer(cfg *Config, router *Router) (*Server, error) {
	cert, err := loadOrGenerateCert(cfg.CertDir)
	if err != nil {
		return nil, fmt.Errorf("TLS cert: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", cfg.TLSPort), tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return &Server{cfg: cfg, router: router, listener: listener}, nil
}

func (s *Server) Serve() {
	log.Printf("TLS server listening on :%d", s.cfg.TLSPort)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("accept: %v", err)
			return
		}

		if s.router.ClientCount() >= s.cfg.MaxClients {
			log.Printf("max clients (%d), rejecting %s", s.cfg.MaxClients, conn.RemoteAddr())
			conn.Close()
			continue
		}

		go s.handleConn(conn.(*tls.Conn))
	}
}

func (s *Server) handleConn(conn *tls.Conn) {
	peerID, err := PerformHandshake(conn, s.cfg)
	if err != nil {
		log.Printf("handshake failed (%s): %v", conn.RemoteAddr(), err)
		conn.Close()
		return
	}

	client := NewClient(conn, peerID, s.cfg)
	s.router.AddClient(client)

	go client.WriteLoop()
	client.ReadLoop(s.router, s.cfg) // blocks until disconnect
}

func (s *Server) Close() {
	s.listener.Close()
}

// loadOrGenerateCert loads an existing TLS keypair from certDir, or
// generates a new self-signed ECDSA P-256 certificate valid for 10 years.
func loadOrGenerateCert(certDir string) (tls.Certificate, error) {
	certFile := filepath.Join(certDir, "relay.crt")
	keyFile := filepath.Join(certDir, "relay.key")

	if _, err := os.Stat(certFile); err == nil {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}

	if err := os.MkdirAll(certDir, 0700); err != nil {
		return tls.Certificate{}, fmt.Errorf("mkdir %s: %w", certDir, err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "bitchat-relay"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return tls.Certificate{}, fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return tls.Certificate{}, fmt.Errorf("write key: %w", err)
	}

	log.Printf("Generated self-signed TLS certificate: %s", certFile)
	return tls.LoadX509KeyPair(certFile, keyFile)
}
