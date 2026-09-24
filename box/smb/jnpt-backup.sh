#!/bin/sh
# jnpt-backup.sh — taegliche Sicherung auf die SMB-Freigabe des Kunden.
#
# ⚠️ ZWEI GRUENDE, WARUM DIESES SKRIPT EXISTIERT — beide gemessen, nicht vermutet:
#
# 1. Die Sicherungs-Freigabe haengt NUR am Host (/srv/jnpt/sicherung), nie unter
#    /ablage — der Container sieht sie nicht, also kann keine Ablage und damit
#    keine KI je darauf zeigen. Das ist der Grund, warum hier kopiert und nicht
#    direkt geschrieben wird: `jnpt backup --output` liefe im Container und
#    braeuchte das Ziel dort.
#    (Historie: Bis 0.53.0 kam ein zweiter Grund dazu — `--output` legte den
#    Klartext-Zwischenstand im ZIELverzeichnis an und scheiterte auf CIFS mit
#    `database is locked (5) (SQLITE_BUSY)`. Seit 0.54.0 liegt er immer neben der
#    Datenbank. Der Weg hier bleibt derselbe.)
#
# 2. Faellt der Mount weg, sieht der Mountpoint aus wie ein leeres Verzeichnis,
#    und `jnpt backup` legt sein Zielverzeichnis per MkdirAll selbst an — es
#    schreibt dann fehlerfrei auf die LOKALE Platte, und niemand merkt es, bis
#    jemand restoren will. Deshalb wird der Mount belegt, bevor etwas passiert.
#    (Zweite, strukturelle Sicherung: der Mountpoint traegt `chattr +i`.)
set -eu

ABLAGE=/srv/jnpt/sicherung
MARKER="$ABLAGE/.jnpt-freigabe"
ZIEL_AUF_FREIGABE="$ABLAGE"
LOKAL_IM_CONTAINER=/data/backups
LOKAL_AUF_HOST=/var/lib/docker/volumes/jnpt_jnpt-data/_data/backups
COMPOSE="docker compose -f /opt/jnpt/box/docker-compose.box.yml"

# --- 1. Ist ueberhaupt etwas gemountet? -------------------------------------
if ! mountpoint -q "$ABLAGE"; then
	echo "jnpt-backup: ABBRUCH — $ABLAGE ist nicht gemountet. Es wird NICHT lokal ausgewichen." >&2
	exit 1
fi

# --- 2. Ist es die RICHTIGE Freigabe? ---------------------------------------
# Ein Mount allein beweist das nicht. Die Markerdatei liegt auf der Freigabe und
# wird dort einmalig von Hand angelegt (RUNBOOK.md).
if [ ! -e "$MARKER" ]; then
	echo "jnpt-backup: ABBRUCH — gemountet, aber die Markerdatei fehlt: falsche Freigabe?" >&2
	exit 1
fi

# --- 3. Lokal erzeugen (siehe Grund 1) --------------------------------------
NAME="jnpt-$(date -u +%Y%m%dT%H%M%SZ).jnpt-bkp"
$COMPOSE exec -T jnpt /jnpt backup --output "$LOKAL_IM_CONTAINER/$NAME" >/dev/null
QUELLE="$LOKAL_AUF_HOST/$NAME"
[ -s "$QUELLE" ] || { echo "jnpt-backup: ABBRUCH — lokale Sicherung fehlt: $NAME" >&2; exit 1; }

# --- 4. Auf die Freigabe legen, erst unter Tarnnamen ------------------------
# Ein Kopiervorgang ueber das Netz ist nicht atomar: Bricht er ab, laege sonst
# ein halbes Artefakt unter dem richtigen Namen — schlimmer als keines. Deshalb
# erst `.teil`, dann umbenennen (das bleibt INNERHALB der Freigabe).
mkdir -p "$ZIEL_AUF_FREIGABE"
cp "$QUELLE" "$ZIEL_AUF_FREIGABE/$NAME.teil"
sync
mv "$ZIEL_AUF_FREIGABE/$NAME.teil" "$ZIEL_AUF_FREIGABE/$NAME"

# --- 5. Gegenlesen: gleiche Groesse, gleiche Pruefsumme ---------------------
if [ "$(stat -c%s "$QUELLE")" != "$(stat -c%s "$ZIEL_AUF_FREIGABE/$NAME")" ]; then
	echo "jnpt-backup: ABBRUCH — Groesse auf der Freigabe weicht ab." >&2
	exit 1
fi
if [ "$(sha256sum < "$QUELLE" | cut -d' ' -f1)" != "$(sha256sum < "$ZIEL_AUF_FREIGABE/$NAME" | cut -d' ' -f1)" ]; then
	echo "jnpt-backup: ABBRUCH — Pruefsumme auf der Freigabe weicht ab." >&2
	exit 1
fi

# --- 6. Lokale Zwischenkopie entfernen --------------------------------------
# Die Aufbewahrung auf der Freigabe ist NICHT Sache dieses Skripts — sie legt der
# Betreiber auf dem Server fest.
rm -f "$QUELLE"
echo "jnpt-backup: fertig — $(ls -l "$ZIEL_AUF_FREIGABE/$NAME" | awk '{print $1, $3, $5}') $NAME"
