package main

import (
	"log"
	"sync"
)

// Router is the central packet hub. It receives packets from local
// clients and from the mesh, deduplicates them, buffers them for
// store-and-forward, and fans them out to the appropriate destinations.
type Router struct {
	mu            sync.RWMutex
	clients       map[*Client]bool
	mesh          *MeshLink
	buffer        *PacketBuffer
	dedup         *DedupFilter
	GlobalLimiter *TokenBucket
}

func NewRouter(cfg *Config) *Router {
	return &Router{
		clients:       make(map[*Client]bool),
		buffer:        NewPacketBuffer(cfg.BufferSize),
		dedup:         NewDedupFilter(cfg.DedupMaxEntries),
		GlobalLimiter: NewTokenBucket(cfg.GlobalPacketsPerSec, cfg.GlobalBurstSize),
	}
}

func (r *Router) SetMesh(m *MeshLink) { r.mesh = m }

func (r *Router) AddClient(c *Client) {
	r.mu.Lock()
	r.clients[c] = true
	count := len(r.clients)
	r.mu.Unlock()

	log.Printf("Client connected: %s (peer %s) [%d total]", c.addr, c.peerID, count)

	for _, pkt := range r.buffer.GetAll() {
		c.SendData(pkt)
	}
}

func (r *Router) RemoveClient(c *Client) {
	r.mu.Lock()
	delete(r.clients, c)
	count := len(r.clients)
	r.mu.Unlock()

	log.Printf("Client disconnected: %s (peer %s) [%d remaining]", c.addr, c.peerID, count)
}

func (r *Router) ClientCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}

// RouteFromClient handles a packet sent by a connected phone.
// It deduplicates, buffers, fans out to other local clients, and
// forwards to the mesh for delivery to other relay daemons.
func (r *Router) RouteFromClient(sender *Client, data []byte) {
	hash := PacketHash(data)
	if r.dedup.IsDuplicate(hash) {
		return
	}

	r.buffer.Add(data)

	// Local fanout — every other phone on this router gets the packet
	r.mu.RLock()
	for client := range r.clients {
		if client != sender {
			client.SendData(data)
		}
	}
	r.mu.RUnlock()

	// Mesh forwarding — relay daemons on other routers
	if r.mesh != nil {
		r.mesh.Send(data)
	}
}

// RouteFromMesh handles a packet received from another relay daemon
// via the batman-adv mesh. Delivers to all locally connected phones.
func (r *Router) RouteFromMesh(data []byte) {
	hash := PacketHash(data)
	if r.dedup.IsDuplicate(hash) {
		return
	}

	r.buffer.Add(data)

	r.mu.RLock()
	count := 0
	for client := range r.clients {
		client.SendData(data)
		count++
	}
	r.mu.RUnlock()
	log.Printf("mesh recv: %d bytes → delivered to %d local clients", len(data), count)
}
