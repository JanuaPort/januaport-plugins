"""Spike #1044 P0 (jobobject-proc-cap): versucht, mehr Prozesse zu starten als der Deckel erlaubt."""
import json
import subprocess
import sys
import time
from pathlib import Path

sys.stdin.readline()
LIMIT = 3  # muss zum ActiveProcessLimit des Go-Programms passen
spawns, procs = [], []
for i in range(1, 6):
    try:
        p = subprocess.Popen(["ping", "-n", "120", "127.0.0.1"], stdout=subprocess.DEVNULL)
    except OSError as e:
        spawns.append(f"{i}:OSError({e.winerror})")
        continue
    time.sleep(1)
    rc = p.poll()
    spawns.append(f"{i}:lebt" if rc is None else f"{i}:tot(rc=0x{rc & 0xFFFFFFFF:x})")
    procs.append(p)
survived = sum(1 for p in procs if p.poll() is None)
Path(sys.argv[1]).write_text(json.dumps({"limit": LIMIT, "spawns": spawns, "survived": survived}))
for p in procs:
    p.kill()
