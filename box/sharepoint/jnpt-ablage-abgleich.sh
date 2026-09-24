#!/bin/bash
# jnpt-ablage-abgleich.sh — Zwei-Wege-Abgleich eines Einstiegspunkts der Ablage mit der
# Use-Case-Site des Kunden in SharePoint. Beisteller auf dem Host, kein Produktcode.
#
# LAEUFT ALS uid 65532 (systemd User=), NICHT als root: fs.protected_hardlinks=1 auf der
# Box — eine Datei, die root anlegt, kann der Gateway-Prozess (65532) nicht per os.Link
# verschieben (EPERM). Was der Abgleich aus SharePoint holt, muss 65532 gehoeren.
#
# Vorgaben, je eine Zeile:
#   1 app-only        -> client_credentials im Remote, Secret aus Datei (Env), kein Login
#   2 beide Richtungen, Takt 5 min, Sperre  -> rclone bisync + flock + Timer
#   3 Konflikt = beide Fassungen  -> --conflict-resolve none (Suffix ..path1/..path2)
#   4 Loeschungen mit Netz        -> --backup-dir1 (lokal, AUSSERHALB von /ablage) + --backup-dir2
#   5 Punkt-Dateien raus          -> --exclude ".*" --exclude ".*/**"  (.jnpt-write-* bleibt lokal)
#   6 SharePoint-Eigenheiten      -> rclone meldet nicht uebertragbare Namen als Fehler (Exit != 0)
#   7 nur die Use-Case-Site       -> Remote [ablage], Pfad OBEN
# Erster Lauf: `--resync` (Voll-Abgleich) — nur ueber `sh … resync`, nie automatisch.
# ⚠️ Kein grep in der Pipeline: grep gibt 1 zurueck, wenn nichts durchkommt — mit pipefail
# saehe ein stiller, erfolgreicher Lauf wie ein Fehler aus (gemessen).
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin
REMOTE=ablage
# <<< ANPASSEN: Ordner in der Site und lokaler Einstiegspunkt unter /srv/jnpt/ablage.
# LOKAL muss zu ReadWritePaths in jnpt-ablage-abgleich.service passen.
OBEN="Beispiel"
LOKAL=/srv/jnpt/ablage/beispiel
PAPIERKORB_LOKAL=/srv/jnpt/ablage-papierkorb/beispiel
PAPIERKORB_OBEN="Papierkorb/$OBEN"
CONF=/var/lib/jnpt/rclone-ablage/rclone.conf   # jnpt-eigen, beschreibbar: rclone legt das kurzlebige Zugriffs-Token dort ab
SECRET=/etc/jnpt/rclone/ablage.secret
WORK=/var/lib/jnpt/rclone-bisync          # StateDirectory der Unit (ProtectSystem=strict)
LOCK=$WORK/lauf.lock

mkdir -p "$WORK"
exec 9>"$LOCK"
flock -n 9 || { echo "abgleich: laeuft bereits — Abbruch" >&2; exit 1; }
[ -s "$SECRET" ] || { echo "abgleich: ABBRUCH — $SECRET fehlt" >&2; exit 1; }
[ -d "$LOKAL" ] || { echo "abgleich: ABBRUCH — $LOKAL fehlt" >&2; exit 1; }
RCLONE_CONFIG_ABLAGE_CLIENT_SECRET=$(cat "$SECRET"); export RCLONE_CONFIG_ABLAGE_CLIENT_SECRET
mkdir -p "$PAPIERKORB_LOKAL"

MODUS=${1:-lauf}
EXTRA=""
if [ "$MODUS" = "resync" ]; then
  EXTRA="--resync"
  # Wächterdateien fuer --check-access auf beiden Seiten anlegen (einmalig).
  touch "$LOKAL/RCLONE_TEST"
  rclone --config "$CONF" touch "$REMOTE:$OBEN/RCLONE_TEST"
fi

# --check-access: auf BEIDEN Seiten muss RCLONE_TEST liegen — sonst wuerde eine leere
# Seite (Mount weg, Site weg) die andere leerraeumen. Liegt im Einstiegspunkt, nicht in
# einer Ablage; list_files sieht sie nicht.
rclone bisync "$LOKAL" "$REMOTE:$OBEN" \
  --config "$CONF" --workdir "$WORK" $EXTRA \
  --check-access --max-delete 50 \
  --conflict-resolve none --conflict-loser num --conflict-suffix "konflikt-lokal,konflikt-sharepoint" \
  --backup-dir1 "$PAPIERKORB_LOKAL" --backup-dir2 "$REMOTE:$PAPIERKORB_OBEN" \
  --exclude ".*" --exclude ".*/**" --exclude "~\$*" --exclude "desktop.ini" \
  --create-empty-src-dirs --compare size,modtime --slow-hash-sync-only \
  --retries 3 --low-level-retries 10 --stats 0 2>&1 | sed -e "/^$/d" -e "s/^/abgleich: /" || {
    echo "abgleich: ROT — bisync Exit $? (bei 'lock' oder 'must run --resync': sh $0 resync)" >&2; exit 1; }
echo "abgleich: Lauf ($MODUS) beendet."
