"""Spike #1044 P0 (jobobject-kill): startet Notepad und einen Helfer, der einen Enkel startet."""
import json
import subprocess
import sys
import time
from pathlib import Path

sys.stdin.readline()  # erst loslaufen, wenn der Job-Handle zugewiesen ist
out = Path(sys.argv[1])
helper_out = out.with_suffix(".helper.json")
helper_out.unlink(missing_ok=True)

notepad = subprocess.Popen(["notepad.exe"])
helper = subprocess.Popen([sys.executable, "-I", str(Path(__file__).with_name("grandchild.py")), str(helper_out)])

deadline = time.time() + 15
while not helper_out.exists() and time.time() < deadline:
    time.sleep(0.2)
grand = json.loads(helper_out.read_text()) if helper_out.exists() else {}

import os
pids = {"python": os.getpid(), "notepad": notepad.pid, "helper": helper.pid}
if "ping" in grand:
    pids["enkel_ping"] = grand["ping"]
time.sleep(2)  # Notepad soll sein Fenster haben
out.write_text(json.dumps(pids))
time.sleep(600)
