#!/usr/bin/env python3
"""Listen for messages on the WiFi mesh and also send a greeting."""
import sys, ssl, socket, struct, hashlib, time

HOST = sys.argv[1] if len(sys.argv) > 1 else "192.168.1.1"
PORT = 7275
PEER_ID = b"pctest01"

def write_frame(sock, ftype, payload=b""):
    sock.sendall(struct.pack("!BI", ftype, len(payload)) + payload)

def read_frame(sock):
    header = b""
    while len(header) < 5:
        chunk = sock.recv(5 - len(header))
        if not chunk:
            raise ConnectionError("Connection closed")
        header += chunk
    ftype = header[0]
    length = struct.unpack("!I", header[1:5])[0]
    payload = b""
    while len(payload) < length:
        chunk = sock.recv(length - len(payload))
        if not chunk:
            raise ConnectionError("Connection closed")
        payload += chunk
    return ftype, payload

def solve_pow(nonce, difficulty):
    for sol in range(2**63):
        data = nonce + struct.pack("!Q", sol)
        h = hashlib.sha256(data).digest()
        full = difficulty // 8
        rem = difficulty % 8
        ok = True
        for i in range(full):
            if h[i] != 0:
                ok = False
                break
        if ok and rem > 0:
            mask = (0xFF << (8 - rem)) & 0xFF
            if h[full] & mask != 0:
                ok = False
        if ok:
            return sol

def decode_packet(data):
    if len(data) < 22:
        print(f"  [raw {len(data)} bytes]: {data.hex()}", flush=True)
        return
    version = data[0]
    msg_type = data[1]
    hdr = 13 if version < 2 else 15
    if version < 2:
        plen = struct.unpack("!H", data[12:14])[0]
    else:
        plen = struct.unpack("!I", data[12:16])[0]
    sender = data[hdr:hdr+8].hex()
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
    print(f"  [{tname}] from {sender}: {text}", flush=True)

def build_packet(msg):
    payload = msg.encode("utf-8")
    ts = int(time.time() * 1000)
    buf = bytearray()
    buf.append(0x01)
    buf.append(0x02)
    buf.append(0x07)
    buf += struct.pack("!Q", ts)
    buf.append(0x00)
    buf += struct.pack("!H", len(payload))
    buf += (PEER_ID + b"\x00" * 8)[:8]
    buf += payload
    return bytes(buf)

# Connect
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
raw = socket.create_connection((HOST, PORT), timeout=10)
sock = ctx.wrap_socket(raw, server_hostname=HOST)
print(f"Connected to {HOST}:{PORT}", flush=True)

# Handshake
hello = struct.pack("!H", 1) + bytes([len(PEER_ID)]) + PEER_ID
write_frame(sock, 0x01, hello)
ftype, payload = read_frame(sock)
nonce, diff = payload[:32], payload[32]
print(f"Solving PoW (difficulty={diff})...", flush=True)
sol = solve_pow(nonce, diff)
write_frame(sock, 0x03, struct.pack("!Q", sol))
ftype, payload = read_frame(sock)
if ftype == 0x04:
    print("Handshake ACCEPTED!", flush=True)
else:
    print(f"REJECTED: {payload}", flush=True)
    sys.exit(1)

# Send greeting
pkt = build_packet("Hello from PC over WiFi mesh!")
write_frame(sock, 0x10, pkt)
print("[SENT] Hello from PC over WiFi mesh!", flush=True)

# Listen
print("\nListening for messages from phone (60s)...", flush=True)
sock.settimeout(60)
try:
    while True:
        ftype, payload = read_frame(sock)
        if ftype == 0x10:
            print(f"\nDATA frame ({len(payload)} bytes):", flush=True)
            decode_packet(payload)
        elif ftype == 0x21:
            pass
        else:
            print(f"Frame 0x{ftype:02x} ({len(payload)} bytes)", flush=True)
except socket.timeout:
    print("\nTimeout reached.", flush=True)
except Exception as e:
    print(f"\nError: {e}", flush=True)
sock.close()
print("Done.", flush=True)
