package main

import (
	"crypto/tls"
	"log"
	"sync"
	"time"
)

type writeMsg struct {
	frameType byte
	payload   []byte
}

// Client represents a single phone connected over TLS.
// All writes are serialized through writeCh → WriteLoop goroutine
// to avoid concurrent conn.Write calls.
type Client struct {
	conn       *tls.Conn
	peerID     string
	addr       string
	limiter    *TokenBucket
	lastActive time.Time
	mu         sync.Mutex
	closed     bool
	writeCh    chan writeMsg
}

func NewClient(conn *tls.Conn, peerID string, cfg *Config) *Client {
	return &Client{
		conn:       conn,
		peerID:     peerID,
		addr:       conn.RemoteAddr().String(),
		limiter:    NewTokenBucket(cfg.ClientPacketsPerSec, cfg.ClientBurstSize),
		lastActive: time.Now(),
		writeCh:    make(chan writeMsg, 64),
	}
}

// ReadLoop reads frames from the phone until the connection is closed or errors.
// DATA frames are routed; PING frames get a PONG reply.
func (c *Client) ReadLoop(router *Router, cfg *Config) {
	defer func() {
		c.Close()
		router.RemoveClient(c)
	}()

	for {
		c.conn.SetReadDeadline(time.Now().Add(cfg.KeepaliveTimeout))
		frame, err := ReadFrame(c.conn, cfg.MaxPacketSize)
		if err != nil {
			log.Printf("[%s] read: %v", c.addr, err)
			return
		}

		c.mu.Lock()
		c.lastActive = time.Now()
		c.mu.Unlock()

		switch frame.Type {
		case FrameData:
			if !c.limiter.Allow() {
				log.Printf("[%s] client rate limited, dropping", c.addr)
				continue
			}
			if !router.GlobalLimiter.Allow() {
				log.Printf("[%s] global rate limit, dropping", c.addr)
				continue
			}
			router.RouteFromClient(c, frame.Payload)

		case FramePing:
			c.Send(FramePong, nil)

		default:
			log.Printf("[%s] unexpected frame 0x%02x", c.addr, frame.Type)
		}
	}
}

// WriteLoop drains writeCh and sends frames to the phone.
// Exits when writeCh is closed (via Client.Close).
func (c *Client) WriteLoop() {
	for msg := range c.writeCh {
		if err := WriteFrame(c.conn, msg.frameType, msg.payload); err != nil {
			log.Printf("[%s] write: %v", c.addr, err)
			c.Close()
			return
		}
	}
}

// Send enqueues a frame for the WriteLoop. Non-blocking: drops if the
// channel is full (back-pressure on a slow client).
// Mutex is held through the select to prevent a concurrent Close()
// from closing the channel between the flag check and the send.
func (c *Client) Send(frameType byte, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.writeCh <- writeMsg{frameType, payload}:
	default:
		log.Printf("[%s] write buffer full, dropping frame", c.addr)
	}
}

func (c *Client) SendData(data []byte) {
	c.Send(FrameData, data)
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.writeCh)
	c.conn.Close()
}
