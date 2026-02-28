package main

import (
	"crypto/sha256"
	"sync"
)

// PacketHash returns the first 8 bytes of SHA-256 as a compact identifier
// for deduplication. 64 bits gives a negligible collision probability at
// our packet volumes (< 1 in 10^12 per 10 000 packets).
func PacketHash(data []byte) [8]byte {
	h := sha256.Sum256(data)
	var id [8]byte
	copy(id[:], h[:8])
	return id
}

// DedupFilter tracks recently seen packet hashes. When the map reaches
// maxEntries it is cleared entirely — a brief window of potential
// re-delivery that the app-level gossip dedup already handles.
type DedupFilter struct {
	mu   sync.Mutex
	seen map[[8]byte]struct{}
	max  int
}

func NewDedupFilter(maxEntries int) *DedupFilter {
	return &DedupFilter{
		seen: make(map[[8]byte]struct{}, maxEntries),
		max:  maxEntries,
	}
}

// IsDuplicate returns true if this hash was already seen.
// Otherwise it records the hash and returns false.
func (d *DedupFilter) IsDuplicate(hash [8]byte) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.seen[hash]; exists {
		return true
	}
	if len(d.seen) >= d.max {
		d.seen = make(map[[8]byte]struct{}, d.max)
	}
	d.seen[hash] = struct{}{}
	return false
}
