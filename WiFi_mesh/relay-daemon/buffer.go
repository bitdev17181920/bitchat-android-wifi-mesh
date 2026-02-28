package main

import "sync"

// PacketBuffer is a fixed-size circular buffer that stores copies of
// recent packets for store-and-forward delivery to newly connected clients.
type PacketBuffer struct {
	mu      sync.Mutex
	packets [][]byte
	size    int
	head    int
	count   int
}

func NewPacketBuffer(size int) *PacketBuffer {
	return &PacketBuffer{
		packets: make([][]byte, size),
		size:    size,
	}
}

func (pb *PacketBuffer) Add(data []byte) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	pkt := make([]byte, len(data))
	copy(pkt, data)

	pb.packets[pb.head] = pkt
	pb.head = (pb.head + 1) % pb.size
	if pb.count < pb.size {
		pb.count++
	}
}

// GetAll returns buffered packets in oldest-first order.
func (pb *PacketBuffer) GetAll() [][]byte {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	result := make([][]byte, 0, pb.count)
	start := pb.head - pb.count
	if start < 0 {
		start += pb.size
	}
	for i := 0; i < pb.count; i++ {
		idx := (start + i) % pb.size
		if pb.packets[idx] != nil {
			result = append(result, pb.packets[idx])
		}
	}
	return result
}
