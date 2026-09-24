#!/usr/bin/env bash
#
# hardening.sh — Grundhärtung der JanuaPort Box (Debian stable minimal).
# Anleitung: ../RUNBOOK.md, Härtung.
#
# ⚠️ Die Box steht beim Kunden — es gibt KEINE Cloud-Firewall davor. Der Host
# muss sich selbst schützen. Die Praxis einer Cloud-VM (Firewall beim Provider,
# Host-Firewall inaktiv) darf hier NICHT kopiert werden.
#
# Das Skript ist IDEMPOTENT: mehrfaches Ausführen ändert nichts Zusätzliches und
# startet Dienste nur neu, wenn sich eine Konfigurationsdatei wirklich geändert
# hat. Es ist damit auch das Werkzeug, um eine Freigabe später umzustellen
# (z. B. von `none` auf `lan`, wenn die Box ins Kundennetz zieht).
#
# Was das Skript NICHT tut (bewusst):
#   - Es installiert KEIN Docker und KEIN Tailscale (Fernwartung ist ein
#     Opt-in des Kunden, kein Provisionierungs-Automatismus).
#   - Es fasst LUKS/TPM2 nicht an (passiert im Installer bzw. von Hand).
#   - Es legt keine Benutzer und keine SSH-Keys an.
#
# Aufruf (als root):
#   ./hardening.sh                                  # Default: alles zu
#   ./hardening.sh --https lan --lan-cidr 192.0.2.0/24
#   ./hardening.sh --https public                   # für die Box NICHT vorgesehen
#
set -euo pipefail

# ── Parameter ────────────────────────────────────────────────────────────────
# Die 443-Freigabe ist parametrisiert, weil sie vom Standort abhängt (bei der
# Vorbereitung: zu; im Kundennetz: nur das Kunden-LAN). Default ist `none` — eine
# Box, die frisch gehärtet wurde, ist von außen NICHT erreichbar. Das Öffnen ist
# ein bewusster, dokumentierter Schritt, kein Nebeneffekt der Provisionierung.
HTTPS_MODE="none"   # none | lan | public
LAN_CIDR=""
ZEITZONE="Europe/Berlin"
REBOOT_ZEIT="03:30"

SSHD_DROPIN="/etc/ssh/sshd_config.d/10-jnpt-box.conf"
NFT_CONF="/etc/nftables.conf"
IFACES_CONF="/etc/network/interfaces"
MODPROBE_CONF="/etc/modprobe.d/jnpt-box-disable-radios.conf"
UU_CONF="/etc/apt/apt.conf.d/52jnpt-box-unattended-upgrades"
UU_PERIODIC="/etc/apt/apt.conf.d/20auto-upgrades"

log()  { printf '[hardening] %s\n' "$*"; }
warn() { printf '[hardening] WARNUNG: %s\n' "$*" >&2; }
fehler() { printf '[hardening] FEHLER: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
hardening.sh — Grundhärtung der JanuaPort Box v1

  --https none|lan|public   443-Freigabe in der Host-Firewall (Default: none)
  --lan-cidr <CIDR>         Pflicht bei --https lan, z. B. 192.0.2.0/24
  --reboot-zeit HH:MM       Wartungsfenster für unattended-upgrades (Default 03:30)
  -h, --help                diese Hilfe

Beispiele:
  ./hardening.sh
  ./hardening.sh --https lan --lan-cidr 192.0.2.0/24
EOF
}

# Ein fehlender Wert soll eine LESBARE Meldung geben, nicht einen stillen
# `shift`-Abbruch — das Skript wird im Zweifel unter Zeitdruck beim Kunden getippt.
wert_pflicht() {
  [ "$1" -ge 2 ] || fehler "$2 erwartet einen Wert"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --https)       wert_pflicht $# "$1"; HTTPS_MODE="$2";  shift 2 ;;
    --lan-cidr)    wert_pflicht $# "$1"; LAN_CIDR="$2";    shift 2 ;;
    --reboot-zeit) wert_pflicht $# "$1"; REBOOT_ZEIT="$2"; shift 2 ;;
    -h|--help)     usage; exit 0 ;;
    *)             usage >&2; fehler "unbekannter Parameter: $1" ;;
  esac
done

case "$HTTPS_MODE" in
  none|public) ;;
  lan) [ -n "$LAN_CIDR" ] || fehler "--https lan verlangt --lan-cidr <CIDR>" ;;
  *)   fehler "--https erwartet none|lan|public (bekommen: '$HTTPS_MODE')" ;;
esac

# Ein Tippfehler im Wartungsfenster fiele sonst erst Wochen später auf — dann,
# wenn ein Sicherheitsupdate nicht durchkommt.
printf '%s' "$REBOOT_ZEIT" | grep -qE '^([01][0-9]|2[0-3]):[0-5][0-9]$' \
  || fehler "--reboot-zeit erwartet HH:MM (bekommen: '$REBOOT_ZEIT')"

[ "$(id -u)" -eq 0 ] || fehler "muss als root laufen (sudo ./hardening.sh …)"
[ -f /etc/debian_version ] || warn "kein Debian erkannt — das Skript ist für Debian stable gebaut"

# ── Hilfsfunktion: idempotent schreiben ──────────────────────────────────────
# Schreibt stdin nach <pfad>, aber NUR wenn sich der Inhalt unterscheidet.
# Rückgabe 0 = geändert, 1 = unverändert. Deshalb IMMER in einem `if` aufrufen
# (sonst bricht `set -e` beim „unverändert"-Fall ab).
schreibe_datei() {
  local pfad="$1" modus="$2" tmp
  tmp="$(mktemp)"
  cat >"$tmp"
  if [ -f "$pfad" ] && cmp -s "$tmp" "$pfad"; then
    rm -f "$tmp"
    return 1
  fi
  install -D -o root -g root -m "$modus" "$tmp" "$pfad"
  rm -f "$tmp"
  return 0
}

# ── 1. Pakete ────────────────────────────────────────────────────────────────
# Warum die Prüfung vor `apt-get update`: Ein Wiederholungslauf soll auch ohne
# Netz durchlaufen (z. B. um nur die Firewall-Freigabe umzustellen). Nur wenn wirklich etwas fehlt, wird das Netz gebraucht.
pakete_installieren() {
  local pakete=(nftables unattended-upgrades apt-listchanges rfkill systemd-timesyncd)
  local fehlend=()
  local p
  for p in "${pakete[@]}"; do
    dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q "^install ok installed$" || fehlend+=("$p")
  done
  if [ ${#fehlend[@]} -eq 0 ]; then
    log "1/7 Pakete: nichts zu tun"
    return 0
  fi
  log "1/7 Pakete: installiere ${fehlend[*]}"
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${fehlend[@]}"
}

# ── 2. Zeit ──────────────────────────────────────────────────────────────────
# Warum: Das Audit-Log ist der Nachweis, wer wann welches Werkzeug aufgerufen
# hat. Ein driftender oder in der
# falschen Zone laufender Zeitstempel macht diesen Nachweis wertlos — und die
# Box läuft headless, niemand korrigiert die Uhr von Hand. Europe/Berlin, weil
# der Betreiber seine Protokolle in seiner eigenen Zeit liest.
zeit_konfigurieren() {
  if [ "$(timedatectl show -p Timezone --value)" != "$ZEITZONE" ]; then
    log "2/7 Zeit: Zeitzone → $ZEITZONE"
    timedatectl set-timezone "$ZEITZONE"
  else
    log "2/7 Zeit: Zeitzone bereits $ZEITZONE"
  fi
  if [ "$(timedatectl show -p NTP --value)" != "yes" ]; then
    log "2/7 Zeit: NTP einschalten"
    timedatectl set-ntp true
  fi
  systemctl enable --quiet systemd-timesyncd 2>/dev/null || warn "systemd-timesyncd nicht aktivierbar"
}

# ── 3. unattended-upgrades ───────────────────────────────────────────────────
# Warum: Die Box hat keinen Administrator vor Ort. OS-Security-Updates müssen
# unbeaufsichtigt laufen, sonst veraltet die Kiste still.
#
# ⚠️ Der nächtliche Reboot ist die Stelle, an der zwei Entscheidungen
# aufeinandertreffen: Ohne funktionierenden TPM2-Auto-Unlock (RUNBOOK.md) bleibt
# die Box nach dem Reboot an der LUKS-Passphrase-Abfrage stehen und ist weg —
# headless, beim Kunden, ohne Monitor. Deshalb ist der 10×-Reboot-Beweis
# Auslieferungsvoraussetzung, NICHT eine Fleißaufgabe.
#
# `Automatic-Reboot-WithUsers "true"`: auf einer headless-Box gibt es keine
# interaktive Sitzung, die man schützen müsste; ein hängengebliebener
# SSH-Wartungslogin darf das Sicherheitsupdate nicht blockieren.
unattended_upgrades_konfigurieren() {
  local geaendert=0
  if schreibe_datei "$UU_PERIODIC" 0644 <<'EOF'
// Von box/scripts/hardening.sh verwaltet — Änderungen gehen beim
// nächsten Lauf verloren.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
  then geaendert=1; fi

  if schreibe_datei "$UU_CONF" 0644 <<EOF
// Von box/scripts/hardening.sh verwaltet — Änderungen gehen beim
// nächsten Lauf verloren. Sortiert bewusst NACH 50unattended-upgrades, damit
// diese Werte die Distributions-Defaults überschreiben.
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-WithUsers "true";
Unattended-Upgrade::Automatic-Reboot-Time "$REBOOT_ZEIT";
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
EOF
  then geaendert=1; fi

  systemctl enable --quiet unattended-upgrades 2>/dev/null || warn "unattended-upgrades.service nicht aktivierbar"
  if [ "$geaendert" -eq 1 ]; then
    log "3/7 unattended-upgrades: konfiguriert (Reboot-Fenster $REBOOT_ZEIT)"
  else
    log "3/7 unattended-upgrades: bereits konfiguriert"
  fi
}

# ── 4. Host-Firewall (nftables) ──────────────────────────────────────────────
# Warum deny-inbound by default: On-prem gibt es keine vorgelagerte Firewall
# Alles, was der Kunde nicht ausdrücklich braucht, ist zu.
#
# Warum `iifname "tailscale0"` statt `iif`: `iif` löst den Interface-Namen beim
# LADEN der Regel in einen Index auf und schlägt fehl, wenn das Interface (noch)
# nicht existiert. Genau das ist hier der Normalfall — ohne Fernwartungs-Opt-in
# gibt es kein `tailscale0`. `iifname` vergleicht den Namen zur Laufzeit; die
# Regel lädt sauber und greift automatisch, sobald der Wartungszugang eingerichtet
# wird. Ohne Wartungsprofil ist SSH damit faktisch unerreichbar — das ist ABSICHT
# (SSH ist der Wartungsweg, kein Betriebsweg des Kunden).
firewall_konfigurieren() {
  # ⚠️ Die 443-Politik lebt in der FORWARD-Kette, nicht in input: Docker schreibt veroeffentlichte Container-Ports per DNAT
  # VOR der Routing-Entscheidung um — diese Pakete laufen durch forward und
  # sehen die input-Kette nie. Eine 443-Regel in input waere wirkungslos.
  local https_regeln
  case "$HTTPS_MODE" in
    none)
      https_regeln='    # HTTPS: KEINE Freigabe (Default).
    # Die Box ist eingehend nicht erreichbar. Richtig fuer die Vorbereitung
    # vor der Auslieferung — und ausreichend fuer den Standardweg der Box
    # (OpenAI Secure MCP Tunnel) wie fuer den Rueckfall Funnel: beide sind
    # rein AUSGEHEND und brauchen hier keine einzige Regel. Nur der LAN-Zugriff
    # im Kundennetz braucht die Freigabe:
    #   ./hardening.sh --https lan --lan-cidr <CIDR>'
      ;;
    lan)
      https_regeln="    # HTTPS: nur aus dem Kunden-LAN ($LAN_CIDR)
    # zu den veroeffentlichten Container-Ports (DNAT -> forward, s. Kommentar oben).
    ip saddr $LAN_CIDR oifname \"jnpt0\" tcp dport 443 accept
    ip saddr $LAN_CIDR oifname \"jnpt0\" udp dport 443 accept
    # Betreiber-Flaeche: Admin-GUI/-MCP der Box, NUR im Kunden-LAN.
    # TLS terminiert die interne CA (Caddyfile.admin).
    ip saddr $LAN_CIDR oifname \"jnpt0\" tcp dport 8443 accept"
      ;;
    public)
      https_regeln='    # HTTPS: OEFFENTLICH — Portweiterleitung 443.
    # Fuer die Box NICHT vorgesehen: Standard ist der Tunnel, Rueckfall
    # Funnel — beide ausgehend, ohne eingehenden Port.
    # Port 80 bleibt bewusst ZU: die ACME-HTTP-01-Challenge braucht ihn, die
    # DNS-01-Challenge nicht. Wird HTTP-01 gewaehlt, ist die
    # 80er-Freigabe ein eigener, bewusster Folgeschritt.
    oifname "jnpt0" tcp dport 443 accept
    oifname "jnpt0" udp dport 443 accept
    # Die Betreiber-Flaeche 8443 bleibt hier bewusst ZU: Admin ist NIE
    # oeffentlich. Oeffentlich 443 plus LAN-Admin zugleich kennt dieses
    # Skript nicht — nicht raten.'
      ;;
  esac

  local tmp_regeln
  tmp_regeln="$(mktemp)"
  cat >"$tmp_regeln" <<EOF
#!/usr/sbin/nft -f
# Von box/scripts/hardening.sh verwaltet — Aenderungen gehen beim
# naechsten Lauf verloren. Modus dieser Datei: --https $HTTPS_MODE
flush ruleset

table inet filter {
  chain input {
    type filter hook input priority filter; policy drop;

    # Loopback: der Stack redet ueber 127.0.0.1 mit sich selbst (Caddy-Bind).
    iif "lo" accept

    # Antworten auf selbst aufgebaute Verbindungen (Update-Pull, Tailscale-DERP,
    # ausgehende Upstream-Calls der Konnektoren).
    ct state established,related accept
    ct state invalid drop

    # ICMPv6 vollstaendig: Neighbor Discovery und Router Advertisements sind in
    # IPv6 kein Komfort, sondern Voraussetzung fuer Konnektivitaet.
    meta l4proto ipv6-icmp accept
    # ICMPv4 nur das Noetige (u. a. Path-MTU-Discovery).
    ip protocol icmp icmp type { echo-request, destination-unreachable, time-exceeded, parameter-problem } accept

    # DHCP-Client: Broadcast-Antworten werden von conntrack nicht zuverlaessig
    # als "related" erkannt — ohne diese Regel bricht die Lease-Erneuerung und
    # die Box verliert nach Stunden bis Tagen ihre IP.
    udp sport 67 udp dport 68 accept
    udp sport 547 udp dport 546 accept

    # SSH ausschliesslich ueber das Tailscale-Interface.
    # Kein Wartungszugang eingerichtet = kein tailscale0 = kein SSH. Absicht.
    # Hinweis: Tailscale selbst braucht KEINE Inbound-Regel (NAT-Traversal/DERP
    # laeuft ueber Outbound). Eine Freigabe von udp/41641 wuerde nur haeufiger
    # direkte statt ueber DERP geroutete Verbindungen erlauben — wir bleiben zu.
    iifname "tailscale0" tcp dport 22 accept

    # 443 steht bewusst NICHT hier: veroeffentlichte Container-Ports werden per
    # DNAT VOR dem Routing umgeschrieben und laufen durch die forward-Kette —
    # die 443-Politik steht dort.
  }

  # Die Box ist kein Router — mit EINER Ausnahme: dem Container-Verkehr des
  # eigenen Stacks. ACHTUNG, nftables-Semantik:
  # ein accept beendet nur die EIGENE Kette; am selben Hook muss JEDE Tabelle
  # das Paket durchlassen. Dockers eigene Freigaben (iptables-nft, eigene
  # Tabellen) genuegen deshalb NICHT — ohne die Ausnahmen hier ist jeder
  # Container-Verkehr tot (kein Upstream-Call, kein Update-Check, keine
  # LAN-Antwort), waehrend alle Container fehlerfrei laufen: die teuerste
  # Fehlerklasse einer headless-Box, weil symptomlos.
  # Die Stack-Bridge heisst dafuer fest jnpt0 (gepinnt in
  # docker-compose.box.yml) — deterministisch statt br-<hash>-Raten.
  chain forward {
    type filter hook forward priority filter; policy drop;

    ct state established,related accept
    ct state invalid drop

    # Ausgehend + Container untereinander: alles AUS der Stack-Bridge.
    iifname "jnpt0" accept

    # EBICS-Abholer (optional, ../ebics-abholer): eigener Stack, eigene Bridge —
    # GEPINNT auf jnpt-ebics0 (Compose-Overlay + systemd-Drop-in, RUNBOOK.md).
    # Er darf genau eines: ausgehendes HTTPS zur Bank. Ohne diese Zeile ist sein
    # Egress lautlos tot (DNS geht trotzdem).
    iifname "jnpt-ebics0" tcp dport 443 accept

    # Eingehend ZU den Containern gilt dieselbe 443-Politik wie der Modus:
$https_regeln

    # docker0 (Default-Bridge) bleibt bewusst zu: der Stack nutzt sie nicht.
    # Diagnose-Container brauchen --network jnpt_default (RUNBOOK.md, Prüfen).
  }

  # Ausgehend offen: Update-Pull, Tailscale, NTP und die Upstream-Systeme der
  # Konnektoren. Eine Outbound-Whitelist waere fuer ein Gateway, dessen Zweck
  # das Ansprechen beliebiger Kundensysteme ist, nicht sinnvoll wartbar.
  chain output {
    type filter hook output priority filter; policy accept;
  }
}
EOF

  # Syntaxpruefung VOR dem Ueberschreiben — eine kaputte Ruleset-Datei wuerde
  # die Box beim naechsten Boot ohne Firewall (oder ohne Netz) hochkommen lassen.
  if ! nft -c -f "$tmp_regeln"; then
    rm -f "$tmp_regeln"
    fehler "nftables-Ruleset ist syntaktisch ungueltig — nichts geaendert"
  fi

  if schreibe_datei "$NFT_CONF" 0644 <"$tmp_regeln"; then
    log "4/7 Firewall: Ruleset geschrieben (--https $HTTPS_MODE)"
    nft -f "$NFT_CONF"
    # ⚠️ `flush ruleset` räumt auch die von Docker gepflegten NAT-/Filter-Regeln
    # ab (Debian nutzt iptables-nft — alles liegt im selben Ruleset). Beim
    # Umstellen am LEBENDEN System (z. B. none→lan)
    # verlören laufende Container sonst ihre Port-Veröffentlichung, bis Docker
    # neu startet. Deshalb: Docker neu starten, wenn es läuft — kurzer
    # Stack-Neustart, ins Wartungsfenster legen.
    if systemctl is-active --quiet docker 2>/dev/null; then
      log "4/7 Firewall: Docker läuft — Neustart, damit dessen NAT-Regeln zurückkommen"
      systemctl restart docker || warn "Docker-Neustart fehlgeschlagen — Container-Ports prüfen (docker compose ps)"
    fi
  else
    log "4/7 Firewall: Ruleset unveraendert"
  fi
  rm -f "$tmp_regeln"
  systemctl enable --quiet nftables 2>/dev/null || warn "nftables.service nicht aktivierbar"
  systemctl is-active --quiet nftables || systemctl start nftables
}

# ── 5. SSH ───────────────────────────────────────────────────────────────────
# Warum nur Public-Key: Ein Passwort auf einer Kiste, die jahrelang unbeaufsichtigt
# läuft, ist eine Frage der Zeit — und der SSH-Zugang ist der Wartungsweg,
# nicht der Betriebsweg des Kunden. Es gibt niemanden, der ein Passwort rotieren
# würde.
#
# Warum die Erreichbarkeitsgrenze in der Firewall und nicht als sshd-ListenAddress:
# Die Tailscale-Adresse (100.64.0.0/10) wird erst beim Tailnet-Beitritt vergeben
# und ist beim Boot noch nicht da. Ein hart eingetragenes `ListenAddress` würde
# sshd beim Start scheitern lassen, bevor tailscaled oben ist — eine
# Boot-Reihenfolge-Falle auf einer headless-Box, an die niemand herankommt.
# Die nftables-Regel (Abschnitt 4) bindet die Erreichbarkeit stattdessen an das
# INTERFACE und ist damit unabhängig von der Adressvergabe. Das Ergebnis ist
# dasselbe: erreichbar ausschließlich über das Tailscale-Interface.
ssh_konfigurieren() {
  if [ ! -f /etc/ssh/sshd_config ]; then
    log "5/7 SSH: kein sshd installiert — übersprungen"
    return 0
  fi

  # Debian bookworm+ liest sshd_config.d per Include. Fehlt die Zeile (ältere
  # oder handgepflegte Konfiguration), greift die Drop-in-Datei still NICHT —
  # ein lautloser Sicherheits-Nichteffekt. Deshalb prüfen und nachtragen.
  if ! grep -qE '^\s*Include\s+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config; then
    log "5/7 SSH: Include für sshd_config.d fehlt — wird vorangestellt"
    local tmp_sshd
    tmp_sshd="$(mktemp)"
    printf 'Include /etc/ssh/sshd_config.d/*.conf\n' >"$tmp_sshd"
    cat /etc/ssh/sshd_config >>"$tmp_sshd"
    install -o root -g root -m 0644 "$tmp_sshd" /etc/ssh/sshd_config
    rm -f "$tmp_sshd"
  fi

  # Sicherung der bisherigen Drop-in-Datei, um bei einem `sshd -t`-Fehler exakt
  # den vorherigen Zustand wiederherzustellen.
  local sicherung=""
  if [ -f "$SSHD_DROPIN" ]; then
    sicherung="$(mktemp)"
    cp "$SSHD_DROPIN" "$sicherung"
  fi

  if schreibe_datei "$SSHD_DROPIN" 0644 <<'EOF'
# Von box/scripts/hardening.sh verwaltet — Änderungen gehen beim
# nächsten Lauf verloren.
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
PermitEmptyPasswords no
# prohibit-password statt no: der Wartungszugang läuft als root über den
# hinterlegten Public-Key (kein Sudo-Umweg auf einer Appliance ohne Benutzer).
PermitRootLogin prohibit-password
X11Forwarding no
AllowAgentForwarding no
AllowTcpForwarding no
EOF
  then
    # Konfiguration erst prüfen, dann neu laden. Ein `sshd -t`-Fehler würde beim
    # Reload den Dienst killen — auf einer headless-Box wäre der Wartungszugang
    # damit endgültig weg.
    if ! sshd -t; then
      if [ -n "$sicherung" ]; then
        install -o root -g root -m 0644 "$sicherung" "$SSHD_DROPIN"
        rm -f "$sicherung"
      else
        rm -f "$SSHD_DROPIN"
      fi
      fehler "sshd-Konfiguration ungültig — Drop-in zurückgerollt"
    fi
    systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || warn "sshd-Reload fehlgeschlagen"
    log "5/7 SSH: nur Public-Key, Erreichbarkeit über tailscale0 (siehe Firewall)"
  else
    log "5/7 SSH: bereits konfiguriert"
  fi
  if [ -n "$sicherung" ]; then
    rm -f "$sicherung"
  fi
}

# ── 6. Funk abschalten (WLAN + Bluetooth) ────────────────────────────────────
# Warum: Die Box hängt am Kabel. WLAN und Bluetooth sind auf ihr ohne Nutzen —
# und jede ungenutzte Funkschnittstelle ist eine Angriffsfläche, die niemand
# beobachtet (die Kiste steht beim Kunden, physisch zugänglich, ohne
# Bildschirm).
#
# Zwei Schichten, weil eine allein nicht trägt: `rfkill` ist ein Soft-Block, den
# ein Treiber oder ein Werkzeug wieder aufheben kann; die modprobe-Blockade
# verhindert, dass die Treiber überhaupt geladen werden. `cfg80211`/`mac80211`
# zu blockieren erfasst JEDEN WLAN-Treiber (alle hängen daran) — eine Liste
# konkreter Chipsatz-Module wäre beim nächsten Board unvollständig.
funk_abschalten() {
  if schreibe_datei "$MODPROBE_CONF" 0644 <<'EOF'
# Von box/scripts/hardening.sh verwaltet.
# WLAN: cfg80211/mac80211 sind die gemeinsame Basis ALLER WLAN-Treiber.
install cfg80211 /bin/false
install mac80211 /bin/false
# Bluetooth: Kernstack + der übliche USB-Transport.
install bluetooth /bin/false
install btusb /bin/false
EOF
  then
    log "6/7 Funk: modprobe-Blockade geschrieben (greift ab dem nächsten Boot)"
  else
    log "6/7 Funk: modprobe-Blockade bereits vorhanden"
  fi

  # Sofortwirkung für den laufenden Betrieb. `rfkill block` meldet keinen Fehler,
  # wenn gar keine passende Hardware da ist — die Box hat je nach Board keinen
  # Funk-Chip, das ist der gute Fall.
  rfkill block wifi || warn "rfkill block wifi fehlgeschlagen"
  rfkill block bluetooth || warn "rfkill block bluetooth fehlgeschlagen"

  local dienst
  for dienst in bluetooth.service wpa_supplicant.service; do
    if systemctl list-unit-files "$dienst" >/dev/null 2>&1 && systemctl is-enabled --quiet "$dienst" 2>/dev/null; then
      log "6/7 Funk: $dienst wird abgeschaltet"
      systemctl disable --now "$dienst" || warn "$dienst nicht abschaltbar"
    fi
  done
}

# ── 7. Boot-Ordnung: Netz VOR Docker ─────────────────────────────────────────
# Warum: Der Debian-Installer schreibt die LAN-Buchse als `allow-hotplug` nach
# /etc/network/interfaces. udev bringt sie dann ASYNCHRON hoch, networking.service
# ist sofort „fertig", und network-online.target gilt als erreicht — ohne Adresse
# und ohne Nameserver. docker.service (After=network-online.target) startet
# genau hinein. Geräte-Befund nach einem nächtlichen unattended-upgrades-Reboot:
# caddy/caddy-admin konnten ihren Host-Port nicht an die LAN-Adresse binden
# („cannot assign requested address"); ein START-Fehler ist kein Crash,
# `restart: unless-stopped` greift nicht → beide Web-Flächen tagelang tot,
# unbemerkt. Der Gateway-Container startete mit leerer Nameserver-
# Liste (Dockers eingebauter DNS friert sie beim Start ein) und sah den
# Update-Feed nicht mehr. Zwei Handgriffe beheben die Klasse:
#   1. `auto <iface>` zusätzlich zu `allow-hotplug`: networking.service wartet
#      dann auf die DHCP-Lease (dhcpcd blockiert, bis die Adresse steht).
#   2. ifupdown-wait-online.service: network-online.target ist erst erreicht,
#      wenn alle `auto`-Interfaces oben sind — Docker hängt daran.
# Das Skript stellt die Ordnung her; der BEWEIS ist die Reboot-Serie
# (nach jedem Reboot müssen ALLE Container laufen, nicht nur der ohne
# Host-Port). Ergänzend hält RUNBOOK.md den Host-Resolver bei DHCP
# (`tailscale set --accept-dns=false`): ein Beisteller darf den Resolver der
# Kiste nicht umhängen.
#
# ⚠️ Die zwei Handgriffe REICHEN NICHT: ifupdown-wait-online meldete bei einem
# späteren Reboot nach 2 s „fertig", der NIC-Link kam 4 s später, die Lease
# noch später — dasselbe Bild. Drei Ergänzungen, am Gerät per Beweis-Reboot
# belegt („Netz da nach 56 s", alles grün ohne Handgriff):
#   3. /etc/default/networking WAIT_ONLINE_METHOD=route (TIMEOUT 120): warten
#      auf eine Default-Route statt auf den Zustand der ifupdown-Buchhaltung.
#   4. jnpt-netz-warten als ExecStartPre von docker.service (../boot/): Docker
#      startet erst, wenn Adresse, Route UND DNS da sind — höchstens 180 s,
#      danach trotzdem (ein Gerät ohne Netz soll lokal hochkommen).
#   5. jnpt-stack-nachstart.service: `compose up -d` 20 s nach Docker. Ein beim
#      Docker-Boot GESCHEITERTER Container bleibt trotz restart-Policy liegen;
#      das ist die zweite Sicherung, falls 4. einmal ins Leere wartet.
# Reboot-Beweis heißt „443 zurück", nicht „SSH zurück" (SSH läuft über
# Tailscale und hängt an keiner LAN-Adresse).
BOOT_DIR="$(cd "$(dirname "$0")/../boot" 2>/dev/null && pwd || true)"
NETWORKING_DEFAULT="/etc/default/networking"
NETZ_ENV="/etc/default/jnpt-netz"

# setze_wert schreibt KEY=WERT in eine Shell-Env-Datei: vorhandene (auch
# auskommentierte) Zeile ersetzen, sonst anhängen. Rückgabe wie schreibe_datei.
setze_wert() {
  local pfad="$1" key="$2" wert="$3"
  grep -qE "^${key}=${wert}\$" "$pfad" 2>/dev/null && return 1
  if grep -qE "^#?[[:space:]]*${key}=" "$pfad" 2>/dev/null; then
    sed -i -E "s|^#?[[:space:]]*${key}=.*|${key}=${wert}|" "$pfad"
  else
    printf '%s=%s\n' "$key" "$wert" >>"$pfad"
  fi
  return 0
}

# netz_warten_installieren bringt 3.–5. auf den Host. $1 = die DHCP-Buchse
# (leer, wenn keine eindeutige gefunden wurde → Default im Skript, enp1s0).
netz_warten_installieren() {
  local iface="$1" geaendert=0
  [ -n "$BOOT_DIR" ] && [ -f "$BOOT_DIR/jnpt-netz-warten" ] \
    || { warn "box/boot/ nicht gefunden — jnpt-netz-warten NICHT installiert (Skript aus /opt/jnpt/box/scripts starten)"; return 1; }
  setze_wert "$NETWORKING_DEFAULT" WAIT_ONLINE_METHOD route && geaendert=1
  setze_wert "$NETWORKING_DEFAULT" WAIT_ONLINE_TIMEOUT 120 && geaendert=1
  if [ -n "$iface" ]; then
    if printf 'JNPT_LAN_IFACE=%s\n' "$iface" | schreibe_datei "$NETZ_ENV" 0644; then geaendert=1; fi
  fi
  if schreibe_datei /usr/local/sbin/jnpt-netz-warten 0755 <"$BOOT_DIR/jnpt-netz-warten"; then geaendert=1; fi
  if schreibe_datei /etc/systemd/system/docker.service.d/10-netz-vor-docker.conf 0644 <"$BOOT_DIR/10-netz-vor-docker.conf"; then geaendert=1; fi
  if schreibe_datei /etc/systemd/system/jnpt-stack-nachstart.service 0644 <"$BOOT_DIR/jnpt-stack-nachstart.service"; then geaendert=1; fi
  if [ "$geaendert" -eq 1 ]; then systemctl daemon-reload; fi
  if ! systemctl is-enabled --quiet jnpt-stack-nachstart.service 2>/dev/null; then
    systemctl enable --quiet jnpt-stack-nachstart.service && geaendert=1
  fi
  [ "$geaendert" -eq 1 ]
}

boot_ordnung_konfigurieren() {
  local geaendert=0 iface gefunden=0 lan_iface=""
  for iface in $(awk '$1=="iface" && $3=="inet" && $4=="dhcp" {print $2}' "$IFACES_CONF" 2>/dev/null); do
    gefunden=$((gefunden + 1))
    lan_iface="$iface"
    if grep -qE "^allow-hotplug[[:space:]]+$iface([[:space:]]|$)" "$IFACES_CONF" \
       && ! grep -qE "^auto[[:space:]]+$iface([[:space:]]|$)" "$IFACES_CONF"; then
      sed -i "/^allow-hotplug[[:space:]]\+$iface\([[:space:]]\|$\)/i auto $iface" "$IFACES_CONF"
      log "7/7 Boot-Ordnung: $iface zusätzlich als auto-Interface eingetragen"
      geaendert=1
    fi
  done
  [ "$gefunden" -ge 1 ] || warn "kein DHCP-Interface in $IFACES_CONF — Boot-Ordnung von Hand prüfen (interfaces.d/?)"
  # Nur eine EINDEUTIGE Buchse wird festgeschrieben; bei zwei DHCP-Buchsen
  # entscheidet der Betreiber (JNPT_LAN_IFACE in /etc/default/jnpt-netz).
  if [ "$gefunden" -gt 1 ]; then
    warn "mehrere DHCP-Interfaces — JNPT_LAN_IFACE in $NETZ_ENV von Hand setzen"
    lan_iface=""
  fi
  if netz_warten_installieren "$lan_iface"; then geaendert=1; fi
  if ! systemctl is-enabled --quiet ifupdown-wait-online.service 2>/dev/null; then
    if systemctl enable --quiet ifupdown-wait-online.service 2>/dev/null; then
      geaendert=1
    else
      warn "ifupdown-wait-online.service nicht aktivierbar — Docker kann vor dem Netz starten"
    fi
  fi
  if [ "$geaendert" -eq 1 ]; then
    log "7/7 Boot-Ordnung: Netz vor Docker (auto-Interface, wait-online route, jnpt-netz-warten, Stack-Nachstart) — greift ab dem nächsten Boot"
  else
    log "7/7 Boot-Ordnung: bereits konfiguriert"
  fi
}

main() {
  log "Start — Box-Grundhärtung (--https $HTTPS_MODE${LAN_CIDR:+ --lan-cidr $LAN_CIDR})"
  pakete_installieren
  zeit_konfigurieren
  unattended_upgrades_konfigurieren
  firewall_konfigurieren
  ssh_konfigurieren
  funk_abschalten
  boot_ordnung_konfigurieren
  log "Fertig."
  log ""
  log "Nächste Schritte laut RUNBOOK.md:"
  log "  Docker CE installieren, Stack starten (Projektname steht in der Compose — kein -p)"
  log "  Inbetriebnahme (Admin, Token, Integrationen), Lizenz, Update-Aktuator"
  log "  Anbindung: OpenAI Secure MCP Tunnel; Fernwartung nur nach Opt-in des Kunden"
  log ""
  log "Kontrolle:  nft list ruleset  ·  sshd -T | grep -i passwordauth  ·  rfkill list"
}

main
