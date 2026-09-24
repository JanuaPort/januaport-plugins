#!/bin/sh
# rclone-probe.sh <sicherung|ablage> — kommt rclone app-only (client_credentials) an die
# Bibliothek? Liste, neutrale Datei schreiben, umbenennen, loeschen. Nur Status + Zaehler.
set -eu
N=$1; CONF=/etc/jnpt/rclone/rclone-$N.conf; [ "$N" = ablage ] && CONF=/var/lib/jnpt/rclone-ablage/rclone.conf; SECRET=/etc/jnpt/rclone/$N.secret
UP=$(printf '%s' "$N" | tr a-z A-Z)
eval "export RCLONE_CONFIG_${UP}_CLIENT_SECRET=\"\$(cat $SECRET)\""
RC="rclone --config $CONF --stats 0"
echo "== [$N] lsd (Wurzel der Bibliothek) =="
$RC lsd "$N:" >/dev/null && echo "  OK   Bibliothek erreichbar ($($RC lsjson "$N:" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))') Eintraege in der Wurzel)" || { echo "  ROT  lsd scheitert (rc $?)"; exit 1; }
echo "== [$N] schreiben / umbenennen / loeschen (neutral) =="
P=".jnpt-probe-rclone"; F=$(mktemp); echo "JanuaPort rclone-Probe, neutraler Inhalt" > "$F"
$RC copyto "$F" "$N:$P/probe.txt" && echo "  OK   copyto" || { echo "  ROT  copyto (Grant write?)"; rm -f "$F"; exit 1; }
$RC moveto "$N:$P/probe.txt" "$N:$P/probe-umbenannt.txt" && echo "  OK   moveto (serverseitig)" || echo "  ROT  moveto"
$RC lsjson "$N:$P" | python3 -c 'import json,sys; e=json.load(sys.stdin); print("  INFO", len(e), "Datei(en), Groesse", [x["Size"] for x in e])'
$RC purge "$N:$P" && echo "  OK   purge (Probe-Ordner weg -> Papierkorb der Site)" || echo "  ROT  purge"
rm -f "$F"
