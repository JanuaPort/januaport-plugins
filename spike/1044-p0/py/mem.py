"""Spike #1044 P0 (jobobject-mem-cap): alloziert in 50-MB-Schritten bis 1000 MB."""
import json
import sys
from pathlib import Path

sys.stdin.readline()
out = Path(sys.argv[1])
chunks, reached, outcome = [], 0, "ohne Grenze durchgelaufen"
for _ in range(20):
    try:
        chunks.append(bytearray(50 * 1024 * 1024))
        for off in range(0, len(chunks[-1]), 4096):
            chunks[-1][off] = 1  # Seiten wirklich anfassen
    except MemoryError:
        outcome = "MemoryError"
        break
    reached += 50
    out.write_text(json.dumps({"reached_mb": reached, "outcome": "beendet ohne Meldung (Prozess abgebrochen)"}))
out.write_text(json.dumps({"reached_mb": reached, "outcome": outcome}))
