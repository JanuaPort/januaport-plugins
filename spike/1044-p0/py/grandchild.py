"""Spike #1044 P0: Helfer des Kindes; startet selbst einen Prozess (Enkel)."""
import json
import subprocess
import sys
import time
from pathlib import Path

ping = subprocess.Popen(["ping", "-n", "600", "127.0.0.1"], stdout=subprocess.DEVNULL)
Path(sys.argv[1]).write_text(json.dumps({"ping": ping.pid}))
time.sleep(600)
