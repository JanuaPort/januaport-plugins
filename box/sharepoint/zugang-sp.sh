#!/bin/sh
# zugang-sp.sh — die beiden Client-Secrets der SharePoint-Apps auf die Box bringen.
#
# DER EINE SCHRITT, DEN EIN MENSCH SELBST TIPPT (kein Secret durch einen Chat, ein
# Ticket oder ein KI-Werkzeug). Aufruf aus dem eigenen Terminal ueber das Tailnet:
#
#     ssh -t root@<box> sh /root/sp/zugang-sp.sh
#
# Fragt je App das Secret ohne Echo (zweimal) ab und schreibt:
#   /etc/jnpt/rclone/sicherung.secret   root:root    0600   (Bringer Sicherung laeuft als root)
#   /etc/jnpt/rclone/ablage.secret      65532:65532  0600   (Abgleich laeuft als uid 65532 —
#                                                            fs.protected_hardlinks=1: synchronisierte
#                                                            Dateien MUESSEN 65532 gehoeren, sonst
#                                                            scheitert move_file an os.Link)
# Dazu — nicht geheim, aber noetig — die Site-URLs der beiden SharePoint-Sites
# (z. B. https://<mandant>.sharepoint.com/sites/<Site>). Leer lassen = spaeter.
set -eu
D=/etc/jnpt/rclone
umask 077
mkdir -p "$D"; chown root:65532 "$D"; chmod 0750 "$D"

frag_secret() {  # $1 = Name, $2 = Datei, $3 = uid
  stty -echo; printf 'Client Secret der App "%s" (kein Echo): ' "$1"; read -r S1; stty echo; echo
  stty -echo; printf 'noch einmal: '; read -r S2; stty echo; echo
  [ "$S1" = "$S2" ] || { echo "stimmt nicht ueberein — nichts geschrieben." >&2; return 1; }
  [ -n "$S1" ] || { echo "leer — nichts geschrieben." >&2; return 1; }
  printf '%s' "$S1" > "$2"; chown "$3:$3" "$2"; chmod 0600 "$2"; unset S1 S2
  echo "  geschrieben: $2 ($(stat -c '%a %U' "$2"))"
}

echo "== App 'JanuaPort Sicherung' =="
frag_secret "JanuaPort Sicherung" "$D/sicherung.secret" 0
printf 'Site-URL der Sicherungs-Site (leer = spaeter): '; read -r U1

echo "== App 'JanuaPort Ablage' =="
frag_secret "JanuaPort Ablage" "$D/ablage.secret" 65532
printf 'Site-URL der Use-Case-Site (leer = spaeter): '; read -r U2

[ -n "$U1" ] && { printf 'SITE_URL=%s\n' "$U1" > "$D/sicherung.site"; chmod 0640 "$D/sicherung.site"; chown root:65532 "$D/sicherung.site"; }
[ -n "$U2" ] && { printf 'SITE_URL=%s\n' "$U2" > "$D/ablage.site"; chmod 0640 "$D/ablage.site"; chown root:65532 "$D/ablage.site"; }
echo
echo "Fertig. Naechster Schritt: python3 sp-einrichten.py sicherung ; python3 sp-einrichten.py ablage"
