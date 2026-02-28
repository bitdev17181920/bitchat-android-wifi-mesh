#!/usr/bin/env python3
"""
BitChat WiFi Mesh test client - sends/receives proper BitchatPacket format
so the Android app can understand messages.
"""

import ssl, socket, struct, hashlib, time, sys, threading

HOST = sys.argv[1] if len(sys.argv) > 1 else "192.168.1.1"
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 7275
PEER_ID = b"pctest01"  # 8 bytes

def write_frame(sock, ftype, payload=b""):
    sock.sendall(struct.pack("!BI", ftype, len(payload)) + payload)

def read_frame(sock):
    header = b""
    while len(header) < 5:
        header += sock.recv(5 - len(header))
    ftype = header[0]
    length = struct.unpack("!I", header[1:5])[0]
    payload = b""
    while len(payload) < length:
        payload += sock.recv(length - len(payload))
    return ftype, payload

def solve_pow(nonce, difficulty):
    for sol in range(2**63):
        data = nonce + struct.pack("!Q", sol)
        h = hashlib.sha256(data).digest()
        full_bytes = difficulty // 8
        rem_bits = difficulty % 8
        ok = True
        for i in range(full_bytes):
            if h[i] != 0:
                ok = False
                break
        if ok and rem_bits > 0:
            mask = 0xFF << (8 - rem_bits) & 0xFF
            if h[full_bytes] & mask != 0:
                ok = False
        if ok:
            return sol
    return 0

def build_packet(sender_id, message):
    """Build a v1 BitchatPacket: MESSAGE type"""
    payload = message.encode("utf-8")
    ts = int(time.time() * 1000)
    buf = bytearray()
    buf.append(0x01)           # version
    buf.append(0x02)           # type = MESSAGE
    buf.append(0x07)           # TTL = 7
    buf += struct.pack("!Q", ts)   # timestamp (8 bytes)
    buf.append(0x00)           # flags
    buf += struct.pack("!H", len(payload))  # payload length (2 bytes)
    sid = (sender_id + b"\x00" * 8)[:8]
    buf += sid                 # senderID (8 bytes)
    buf += payload             # payload
    return bytes(buf)

def decode_packet(data):
    """Decode a BitchatPacket and print it."""
    if len(data) < 22:
        print(f"  [raw {len(data)} bytes]")
        return
    version = data[0]
    msg_type = data[1]
    hdr = 13 if version < 2 else 15
    if version < 2:
        plen = struct.unpack("!H", data[12:14])[0]
    else:
        plen = struct.unpack("!I", data[12:16])[0]
    sender = data[hdr:hdr+8]
    sender_hex = sender.hex()
    flags = data[11]
    payload_start = hdr + 8
    if flags & 0x01:
        payload_start += 8
    payload = data[payload_start:payload_start+plen]
    types = {1: "ANNOUNCE", 2: "MESSAGE", 3: "LEAVE", 0x10: "NOISE_HS", 0x11: "NOISE_ENC"}
    tname = types.get(msg_type, f"0x{msg_type:02x}")
    try:
        text = payload.decode("utf-8")
    except:
        text = payload.hex()
    print(f"  [{tname}] from {sender_hex}: {text}")

def main():
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    raw = socket.create_connection((HOST, PORT), timeout=10)
    sock = ctx.wrap_socket(raw, server_hostname=HOST)
    print(f"Connected to {HOST}:{PORT}")

    # HELLO
    hello = struct.pack("!H", 1) + bytes([len(PEER_ID)]) + PEER_ID
    write_frame(sock, 0x01, hello)

    # CHALLENGE
    ftype, payload = read_frame(sock)
    assert ftype == 0x02, f"Expected CHALLENGE, got 0x{ftype:02x}"
    nonce = payload[:32]
    difficulty = payload[32]
    print(f"Solving PoW (difficulty={difficulty})...")
    solution = solve_pow(nonce, difficulty)
    write_frame(sock, 0x03, struct.pack("!Q", solution))

    # ACCEPT/REJECT
    ftype, payload = read_frame(sock)
    if ftype == 0x04:
        print("Handshake ACCEPTED!")
    else:
        print(f"Handshake REJECTED: {payload.decode()}")
        return

    # Read loop (background)
    def reader():
        try:
            while True:
                ftype, payload = read_frame(sock)
                if ftype == 0x10:  # DATA
                    decode_packet(payload)
                elif ftype == 0x21:  # PONG
                    pass
        except Exception as e:
            print(f"Read error: {e}")

    t = threading.Thread(target=reader, daemon=True)
    t.start()

    # Keepalive (background)
    def pinger():
        while True:
            time.sleep(30)
            try:
                write_frame(sock, 0x20)
            except:
                break
    threading.Thread(target=pinger, daemon=True).start()

    # Send loop
    print("\nType a message and press Enter to send:")
    try:
        while True:
            msg = input("> ")
            if not msg.strip():
                continue
            packet = build_packet(PEER_ID, msg.strip())
            write_frame(sock, 0x10, packet)
            print(f"  [SENT] {msg.strip()}")
    except (KeyboardInterrupt, EOFError):
        print("\nDisconnected.")
    finally:
        sock.close()

if __name__ == "__main__":
    main()
