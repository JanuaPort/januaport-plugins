"""Linux/amd64 CPython-3.12-Lock aus pip-Resolverbericht mit Artefakthashes erstellen."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import argparse


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", default="requirements.in")
    parser.add_argument("--output", default="requirements.txt")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="pi-lock-") as tmp:
        bericht = Path(tmp) / "resolver.json"
        subprocess.run([sys.executable, "-m", "pip", "install", "--dry-run", "--ignore-installed",
                        "--report", str(bericht), "-r", args.input],
                       check=True)
        pakete = json.loads(bericht.read_text())["install"]
    zeilen = ["# Linux/amd64, CPython 3.12; erzeugt mit python pin_requirements.py.",
              "# Ein SHA-256 je auf dieser Plattform aufgelöstem Wheel; keine Modellgewichte.",
              "--extra-index-url https://download.pytorch.org/whl/cpu", ""]
    for paket in sorted(pakete, key=lambda p: p["metadata"]["name"].casefold()):
        meta = paket["metadata"]
        digest = paket["download_info"]["archive_info"]["hashes"]["sha256"]
        zeilen.append(f"{meta['name']}=={meta['version']} --hash=sha256:{digest}")
    Path(args.output).write_text("\n".join(zeilen) + "\n", encoding="utf-8", newline="\n")
    print(f"{len(pakete)} Pakete mit Hashes nach {args.output} geschrieben.")


if __name__ == "__main__":
    main()
