# V1: das Skript öffnet eine zweite Verbindung zum Socket und gibt sich als
# frische Sandbox aus. Erwartet: sofort geschlossen, der Lauf endet als
# limit/sandbox_lost.
import json
import socket
import struct
import time

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect("/run/script-runner/sandbox.sock")
body = json.dumps({"type": "hello", "instance": "attacker", "isolation": "standard", "seccomp": 2}).encode()
s.sendall(struct.pack(">I", len(body)) + body)
s.settimeout(5)
try:
    data = s.recv(4096)
    print("closed" if data == b"" else "got-data")
except Exception as e:
    print(type(e).__name__)
time.sleep(8)
