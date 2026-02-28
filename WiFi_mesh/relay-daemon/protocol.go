package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// Frame types for the phone ↔ relay wire protocol.
const (
	FrameHello     byte = 0x01
	FrameChallenge byte = 0x02
	FrameSolution  byte = 0x03
	FrameAccept    byte = 0x04
	FrameReject    byte = 0x05
	FrameData      byte = 0x10
	FramePing      byte = 0x20
	FramePong      byte = 0x21
)

const ProtocolVersion uint16 = 1

// Frame is a length-prefixed message: [1-byte type][4-byte big-endian length][payload].
type Frame struct {
	Type    byte
	Payload []byte
}

func ReadFrame(conn net.Conn, maxPayload int) (*Frame, error) {
	var header [5]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	frameType := header[0]
	length := binary.BigEndian.Uint32(header[1:5])

	if int(length) > maxPayload {
		return nil, fmt.Errorf("frame too large: %d > %d", length, maxPayload)
	}

	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(conn, payload); err != nil {
			return nil, fmt.Errorf("read payload: %w", err)
		}
	}

	return &Frame{Type: frameType, Payload: payload}, nil
}

// WriteFrame writes a complete frame as a single TLS record for efficiency.
func WriteFrame(conn net.Conn, frameType byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = frameType
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := conn.Write(buf)
	return err
}
