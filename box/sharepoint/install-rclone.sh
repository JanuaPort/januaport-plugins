#!/bin/sh
# install-rclone.sh — rclone als statisches Binary aus dem offiziellen GitHub-Release,
# Pruefsumme gegen SHA256SUMS desselben Releases. Debian-apt liefert 1.60 —
# zu alt: der Client-Credentials-Fluss kam mit 1.69.0 (Changelog 2025-01-12).
set -eu
V=${1:-v1.75.1}
T=$(mktemp -d); cd "$T"
curl -fsSLO "https://github.com/rclone/rclone/releases/download/$V/rclone-$V-linux-amd64.zip"
curl -fsSLO "https://github.com/rclone/rclone/releases/download/$V/SHA256SUMS"
grep " rclone-$V-linux-amd64.zip$" SHA256SUMS | sha256sum -c - || { echo "PRUEFSUMME FALSCH — nicht installiert" >&2; exit 1; }
python3 -c "import zipfile; zipfile.ZipFile('rclone-$V-linux-amd64.zip').extract('rclone-$V-linux-amd64/rclone', '.')"
install -m 0755 "rclone-$V-linux-amd64/rclone" /usr/local/bin/rclone
cd /; rm -rf "$T"
/usr/local/bin/rclone version | head -1
echo "Pruefsumme: $(sha256sum /usr/local/bin/rclone | cut -c1-16)…  (Quelle: github.com/rclone/rclone $V)"
