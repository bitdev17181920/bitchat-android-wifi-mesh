package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// GenerateChallenge creates a random 32-byte nonce for proof-of-work.
func GenerateChallenge() ([32]byte, error) {
	var nonce [32]byte
	_, err := rand.Read(nonce[:])
	return nonce, err
}

// VerifyPoW checks that SHA256(nonce || solution) has at least `difficulty`
// leading zero bits.
func VerifyPoW(nonce [32]byte, solution uint64, difficulty uint8) bool {
	var buf [40]byte
	copy(buf[:32], nonce[:])
	binary.BigEndian.PutUint64(buf[32:], solution)
	hash := sha256.Sum256(buf[:])
	return hasLeadingZeros(hash[:], difficulty)
}

// SolvePoW brute-forces a solution (used for testing only).
func SolvePoW(nonce [32]byte, difficulty uint8) (uint64, error) {
	var buf [40]byte
	copy(buf[:32], nonce[:])
	for sol := uint64(0); ; sol++ {
		binary.BigEndian.PutUint64(buf[32:], sol)
		hash := sha256.Sum256(buf[:])
		if hasLeadingZeros(hash[:], difficulty) {
			return sol, nil
		}
		if sol == ^uint64(0) {
			return 0, fmt.Errorf("exhausted uint64 space without solution")
		}
	}
}

func hasLeadingZeros(hash []byte, n uint8) bool {
	full := n / 8
	rem := n % 8
	for i := uint8(0); i < full; i++ {
		if hash[i] != 0 {
			return false
		}
	}
	if rem > 0 {
		mask := byte(0xFF << (8 - rem))
		if hash[full]&mask != 0 {
			return false
		}
	}
	return true
}
