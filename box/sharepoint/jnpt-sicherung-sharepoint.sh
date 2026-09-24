#!/bin/sh
# jnpt-sicherung-sharepoint.sh — taegliche Sicherung der Box in die SharePoint-Site
# „Sicherung" des Kunden. Bringer auf dem Host, kein Produktcode.
#
# Ablauf: jnpt backup (lokal, verschluesselt, AES-256-GCM) -> Upload unter Tarnnamen
# `.teil` -> serverseitig umbenennen -> Groesse gegenlesen -> lokale Kopie weg ->
# Generationen aufraeumen. Jeder Fehler = Exit != 0, der Timer wiederholt; nie ein
# halbes Artefakt unter dem richtigen Namen (deshalb `.teil` + moveto).
#
# Secret: NUR in /etc/jnpt/rclone/sicherung.secret (0600 root), zur Laufzeit als Env an
# rclone — steht nie in rclone.conf, nie in einem Log, nie in einer Prozessliste
# (Env ist fuer andere Nutzer nicht lesbar; ps zeigt nur Argumente).
#
# Aufbewahrung (Mechanik, NICHT Politik — die legt der Betreiber fest): alles der
# letzten 14 Tage, dazu je Monat der aelteste Stand der letzten 3 Monate.
set -eu
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
REMOTE=sicherung
ZIEL=sicherungen
CONF=/etc/jnpt/rclone/rclone-sicherung.conf
SECRET=/etc/jnpt/rclone/sicherung.secret
LOKAL_IM_CONTAINER=/data/backups
LOKAL_AUF_HOST=/var/lib/docker/volumes/jnpt_jnpt-data/_data/backups
COMPOSE="docker compose -f /opt/jnpt/box/docker-compose.box.yml"
LOCK=/run/lock/jnpt-sicherung-sharepoint.lock
TAGE_TAEGLICH=14
MONATE=3

exec 9>"$LOCK"
flock -n 9 || { echo "jnpt-sicherung: laeuft bereits — Abbruch" >&2; exit 1; }
[ -s "$SECRET" ] || { echo "jnpt-sicherung: ABBRUCH — $SECRET fehlt" >&2; exit 1; }
grep -q "^\[$REMOTE\]" "$CONF" || { echo "jnpt-sicherung: ABBRUCH — Remote [$REMOTE] fehlt in $CONF (sp-einrichten.py)" >&2; exit 1; }
RCLONE_CONFIG_SICHERUNG_CLIENT_SECRET=$(cat "$SECRET"); export RCLONE_CONFIG_SICHERUNG_CLIENT_SECRET
RC="rclone --config $CONF --retries 3 --low-level-retries 10 --stats 0 -q"

# --- 1. Ist das Ziel da? (Abbruch vor dem Backup, nicht danach) ---------------
$RC lsd "$REMOTE:" >/dev/null || { echo "jnpt-sicherung: ABBRUCH — Bibliothek nicht erreichbar" >&2; exit 1; }
$RC mkdir "$REMOTE:$ZIEL"

# --- 2. Lokal erzeugen ------------------------------------------------------
NAME="jnpt-$(date -u +%Y%m%dT%H%M%SZ).jnpt-bkp"
$COMPOSE exec -T jnpt /jnpt backup --output "$LOKAL_IM_CONTAINER/$NAME" >/dev/null
QUELLE="$LOKAL_AUF_HOST/$NAME"
[ -s "$QUELLE" ] || { echo "jnpt-sicherung: ABBRUCH — lokale Sicherung fehlt: $NAME" >&2; exit 1; }
GROESSE=$(stat -c%s "$QUELLE")

# --- 3. Hochladen unter Tarnnamen, dann umbenennen (serverseitig) -------------
$RC copyto "$QUELLE" "$REMOTE:$ZIEL/$NAME.teil" || { echo "jnpt-sicherung: ABBRUCH — Upload fehlgeschlagen" >&2; $RC deletefile "$REMOTE:$ZIEL/$NAME.teil" 2>/dev/null || true; exit 1; }
$RC moveto "$REMOTE:$ZIEL/$NAME.teil" "$REMOTE:$ZIEL/$NAME"

# --- 4. Gegenlesen: Groesse auf der Gegenseite ----------------------------------
OBEN=$($RC lsjson "$REMOTE:$ZIEL/$NAME" | python3 -c 'import json,sys; print((json.load(sys.stdin) or [{}])[0].get("Size", -1))')
[ "$OBEN" = "$GROESSE" ] || { echo "jnpt-sicherung: ABBRUCH — Groesse weicht ab (lokal $GROESSE, oben $OBEN)" >&2; exit 1; }
rm -f "$QUELLE"
echo "jnpt-sicherung: $NAME hochgeladen ($GROESSE Bytes), lokale Kopie entfernt."

# --- 5. Generationen aufraeumen ---------------------------------------------------
# Entfernte Dateien landen im Papierkorb der Site (Graph loescht nie endgueltig).
$RC lsjson "$REMOTE:$ZIEL" | python3 -c '
import json, sys, datetime, re
tage, monate = int(sys.argv[1]), int(sys.argv[2])
jetzt = datetime.datetime.now(datetime.timezone.utc)
eintraege = []
for e in json.load(sys.stdin):
    m = re.fullmatch(r"jnpt-(\d{8}T\d{6}Z)\.jnpt-bkp", e.get("Name", ""))
    if m:
        eintraege.append((datetime.datetime.strptime(m.group(1), "%Y%m%dT%H%M%SZ").replace(tzinfo=datetime.timezone.utc), e["Name"]))
eintraege.sort()
behalten = set()
for t, n in eintraege:
    if (jetzt - t).days < tage:
        behalten.add(n)
aeltester_je_monat = {}
for t, n in eintraege:
    aeltester_je_monat.setdefault((t.year, t.month), n)
monate_liste = sorted(aeltester_je_monat)[-monate:]
for k in monate_liste:
    behalten.add(aeltester_je_monat[k])
for t, n in eintraege:
    if n not in behalten:
        print(n)
' "$TAGE_TAEGLICH" "$MONATE" | while read -r ALT; do
  $RC deletefile "$REMOTE:$ZIEL/$ALT" && echo "jnpt-sicherung: aufgeraeumt: $ALT"
done
echo "jnpt-sicherung: Bestand oben: $($RC lsjson "$REMOTE:$ZIEL" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))') Artefakte."
