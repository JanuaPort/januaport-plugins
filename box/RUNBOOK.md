# Runbook: JanuaPort Box

Schritt-für-Schritt-Provisionierung der **JanuaPort Box** — ein lüfterloser
Mini-PC, der vorbereitet wird und anschließend **beim Kunden** steht. Die
Dateien liegen in diesem Ordner; was sie tun und warum, steht in
[`README.md`](README.md) und [`CLAUDE.md`](CLAUDE.md).

> **Maßstab:** Eine **Ersatzbox muss aus diesem Dokument und einer Sicherung
> herstellbar sein.** Was nur im Kopf oder nur auf einer Box existiert,
> existiert nicht. Wer beim Durchlauf etwas ergänzen muss, ergänzt es hier.

> **Schichtung:** Hier steht die **Provisionierung** — Gerät, Betriebssystem,
> Verschlüsselung, Härtung, Stack, Netz. Die **Inbetriebnahme** (Admin-Zugang,
> Tokens, Integrationen, Teams), **Sicherung/Wiederherstellung**, **Update** und
> **Lizenz** sind Funktionen von JanuaPort und stehen in dessen
> Betriebsdokumentation. Hier stehen nur die box-spezifischen Zusätze — zwei
> Beschreibungen derselben Schritte laufen auseinander.

**Platzhalter:** `<box>` = Host der Box (für SSH), `<BOX_NAME>` = Box-Name
(`JNPT_DOMAIN`), `<LAN-IP>` = reservierte Adresse der Box im Kundennetz,
`<Kunden-CIDR>` = LAN-Segment des Kunden, `<tenant>` = Verzeichnis-ID im
Entra-Tenant des Kunden, `<tailnet>` = Name des Tailnets. Beispiel-Domains sind
`example.com`, Beispiel-Adressen RFC 5737.

**Installationsort:** Der Inhalt dieses Ordners liegt auf der Box unter
**`/opt/jnpt/box`**. Alle Pfade in Units und Skripten gehen davon aus.

---

## 1. Voraussetzungen

### Hardware

- Mini-PC der Klasse **Intel N100/N150**, lüfterlos, für Dauerbetrieb
  freigegeben, x86-64, **kabelgebundenes LAN** (WLAN wird ohnehin abgeschaltet).
- 8 GB RAM, 500 GB SSD (SATA oder NVMe).
- **TPM 2.0** — bei dieser Klasse meist ein Firmware-TPM („PTT"/„fTPM"). Ohne
  TPM kein Auto-Unlock und damit kein unbeaufsichtigter Reboot.
- Monitor und Tastatur **nur für die Installation**; im Betrieb läuft die Box
  headless.
- Ein Sicherungsmedium nach der Ziel-Weiche in Abschnitt 8.

### BIOS/UEFI

- [ ] **TPM 2.0 aktiviert.**
- [ ] **Secure Boot aktiviert** — es geht in die TPM-Messung ein (PCR 7). Wer es
      später umschaltet, sperrt sich aus (Recovery-Key nötig).
- [ ] **Einschalten nach Stromausfall** („Restore on AC Power Loss" = *Power
      On*). Sonst bleibt die Box nach einem Stromausfall aus, und niemand vor
      Ort weiß, dass sie einen Knopf hat.
- [ ] WLAN/Bluetooth im BIOS abschalten, falls angeboten.
- [ ] Boot-Reihenfolge: interne SSD zuerst.

### Netz — was vom Kunden kommt

- LAN-Segment (`<Kunden-CIDR>`) und eine **DHCP-Reservierung** für die Box.
  Das ist im Regelfall der **einzige** Handgriff des IT-Dienstleisters.
- **Ausgehendes HTTPS** zu: `ghcr.io` (Image), dem Update-Feed und dem
  Lizenzserver des Herstellers (`*.januaport.ai`), `api.openai.com` (Tunnel),
  `login.microsoftonline.com` und `graph.microsoft.com` (Anmeldung, ggf.
  SharePoint), Debian- und Docker-Paketquellen; bei Fernwartung die Tailscale-
  Koordination. **Eingehend: nichts.**
- Kein Subnetz im Kundennetz (auch keine VPN-Route) darf `10.201.44.0/24`
  enthalten oder umfassen — das ist das feste Netz des Stacks (Abschnitt 9).
- Gibt es eine Netzfreigabe oder SharePoint als Sicherungsziel? (Abschnitt 8/10)
- Ist Fernwartung vereinbart? Ohne schriftliches Einverständnis kein
  Wartungszugang (Abschnitt 5.4).

### Bereithalten

- Debian-stable-Netinstall-Image auf einem USB-Stick.
- Der gepinnte GHCR-Tag, der ausgeliefert wird (`JANUAPORT_IMAGE`).
- Ein SSH-Public-Key für den Wartungszugang.
- Zwei blickdichte, versiegelbare Umschläge (Abschnitt 2.3) und ein
  Notfall-Einleger, der physisch bei der Box liegt.

> ⛔ **Auslieferungsvoraussetzung:** Update-Feed und Lizenz müssen stehen, bevor
> die Box das Haus verlässt — sonst hat der Kunde eine Appliance ohne
> Update-Weg, und am Ende der Testphase stoppt der Betrieb.

## 2. Provisionierung

### 2.1 Betriebssystem

Debian **stable, minimal** — kein Desktop.

- [ ] Sprache/Locale nach Kunde, Zeitzone (Voreinstellung im Härtungsskript:
      `Europe/Berlin`).
- [ ] Hostname z. B. `jnpt-box`.
- [ ] Netzwerk: **kabelgebunden, DHCP**.
- [ ] Root-Passwort setzen und notieren — es geht in Umschlag A.
- [ ] **Partitionierung: „Verschlüsseltes LVM" (LUKS)**, Passphrase notieren.
- [ ] Softwareauswahl: **nur** „SSH-Server" und „Standard-Systemwerkzeuge".

Nach dem ersten Boot:

```bash
apt-get update && apt-get -y full-upgrade
install -d -m 0700 /root/.ssh
$EDITOR /root/.ssh/authorized_keys      # Wartungs-Public-Key eintragen
chmod 0600 /root/.ssh/authorized_keys
```

> ⚠️ **Erst den Key hinterlegen und einen Login damit testen, dann härten.**
> Die Härtung schaltet die Passwort-Anmeldung ab und lässt SSH nur über das
> Tailnet zu — ein vergessener Key heißt: Monitor und Tastatur wieder anschließen.

### 2.2 LUKS: Recovery-Key und TPM2-Auto-Unlock

Die Box steht physisch zugänglich beim Kunden: Diebstahl darf keine Daten
preisgeben. Gleichzeitig muss sie nach dem nächtlichen Sicherheits-Reboot
**ohne Menschen** wieder hochkommen — deshalb TPM2-Auto-Unlock.

```bash
apt-get install -y cryptsetup-initramfs tpm2-tools clevis clevis-luks clevis-tpm2 clevis-initramfs
lsblk -o NAME,SIZE,FSTYPE,MOUNTPOINT        # Partition vom Typ crypto_LUKS
CRYPTDEV=/dev/<partition>                   # ⚠️ an die echte Partition anpassen

# 1) Recovery-Key: ein zweiter, unabhängiger Schlüssel. Wird GENAU EINMAL
#    ausgegeben — sofort abschreiben, er geht in Umschlag A.
systemd-cryptenroll --recovery-key "$CRYPTDEV"

# 2) TPM2 über Clevis binden, an PCR 7 (Secure-Boot-Zustand)
clevis luks bind -d "$CRYPTDEV" tpm2 '{"pcr_bank":"sha256","pcr_ids":"7"}'
clevis luks list -d "$CRYPTDEV"             # tpm2-Eintrag mit pcr_ids "7"

# 3) initramfs neu bauen — /etc/crypttab bleibt, wie der Installer sie schrieb
update-initramfs -u -k all
lsinitramfs /boot/initrd.img-$(uname -r) | grep -cE "clevis|tss"   # muss > 0 sein
reboot
```

- **Warum Clevis und nicht `systemd-cryptenroll --tpm2-device`:** Debians
  `cryptsetup-initramfs` ignoriert die crypttab-Option `tpm2-device=` und
  probiert keine LUKS2-Token aus. Ein so angelegter Slot schadet nicht, tut aber
  nichts — die Box bleibt an der Passphrase-Abfrage stehen.
- **Warum nur PCR 7:** Mit PCR 0 bricht **jedes BIOS-Update** das Auto-Unlock —
  auf einer headless-Box ein garantierter Vor-Ort-Einsatz. Der Preis: Wer
  physischen Zugriff hat und eine andere signierte Umgebung bootet, kommt näher
  an die Platte. Für ein Einzelgerät akzeptiert, hier festgehalten.
- **Beweis, nicht optional:** Die Box muss **zehnmal hintereinander** ohne
  Passphrase durchbooten. Der nächtliche Reboot der Sicherheitsupdates hängt
  daran.

### 2.3 Die Schlüssel gehen zum Kunden

**Der Hersteller behält keine Kopie.** Er ermöglicht, er ist nicht Mitwisser der
Daten. Zuerst den Master-Key des Tresors erzeugen (er verschlüsselt alle
Zugangsdaten der Integrationen):

```bash
openssl rand -hex 32     # 64 Hex-Zeichen = Master-Key
```

| | Umschlag A — „Zugang zur Box" | Umschlag B — „Schlüssel zu den Daten" |
|---|---|---|
| Inhalt | LUKS-Recovery-Key, LUKS-Passphrase, Root-Passwort | Master-Key (64 Hex) |
| Beschriftung | Seriennummer, Datum, „nur im Notfall öffnen" | Seriennummer, Datum, „getrennt von Sicherungen aufbewahren" |
| Aufbewahrung | Safe des Kunden | **anderer Ort** als die Sicherungen |

- [ ] Beide versiegelt, Siegelnummern notiert; Empfang schriftlich bestätigt.
- [ ] Kunde weiß: **Verliert er beide Schlüssel und stirbt das TPM/Board, sind
      die Daten weg.** Genau dafür gibt es die Sicherung (Abschnitt 8).
- [ ] Beim Hersteller wird nichts gespeichert — keine Datei, kein
      Passwortmanager-Eintrag, kein Ticket-Anhang, kein Chat-Verlauf.
- [ ] Den **Fingerabdruck des Master-Keys** aus der ersten Sicherung auf dem
      Notfall-Einleger notieren (kein Geheimnis, eine Einwegfunktion). Er zeigt
      nach einer Rotation, welcher Schlüssel zu welcher Sicherung gehört.

### 2.4 Die Dateien auf die Box

```bash
install -d -m 0755 /opt/jnpt
# den Ordner box/ dieses Repositorys nach /opt/jnpt/box bringen, z. B.:
git clone --depth 1 https://github.com/JanuaPort/januaport-plugins.git /tmp/jp
cp -r /tmp/jp/box /opt/jnpt/box && rm -rf /tmp/jp
# den Stand notieren — die Box trägt eine Kopie, kein Git-Arbeitsverzeichnis
echo "<commit> $(date -I)" > /opt/jnpt/box/.stand
```

## 3. Härtung

`scripts/hardening.sh` ist **idempotent** und macht in sieben Schritten:
Pakete · Zeit (NTP, Zeitzone) · unattended-upgrades mit nächtlichem
Reboot-Fenster · nftables deny-inbound · SSH nur Public-Key und nur über
`tailscale0` · WLAN/Bluetooth aus · **Boot-Ordnung „Netz vor Docker"**. Jede
Sektion trägt ihre Begründung im Skript. Es muss aus `/opt/jnpt/box/scripts`
laufen, weil es die Boot-Dateien aus `../boot/` installiert.

```bash
cd /opt/jnpt/box/scripts
./hardening.sh                                          # Vorbereitung: alles zu
./hardening.sh --https lan --lan-cidr <Kunden-CIDR>     # Regelbetrieb im Kundennetz
```

| Aufruf | Wirkung |
|---|---|
| `./hardening.sh` | **Default: alles zu.** Richtig für die Vorbereitung — und ausreichend für den Tunnel und den Funnel-Rückfall, denn beide sind rein ausgehend. |
| `--https lan --lan-cidr <CIDR>` | 443 (Werkzeug-Fläche) **und** 8443 (Betreiber-Fläche) nur aus dem Kunden-LAN. |
| `--https public` | 443 aus allen Netzen. Für die Box **nicht vorgesehen**; 8443 bleibt auch dann zu. |
| `--reboot-zeit HH:MM` | Wartungsfenster für den Reboot nach Sicherheitsupdates (Default 03:30). |

Kontrolle:

```bash
nft list ruleset                          # policy drop in input und forward, jnpt0-Ausnahmen
sshd -T | grep -iE 'passwordauth|permitrootlogin|pubkeyauth'
rfkill list                               # Soft blocked: yes (oder keine Hardware)
timedatectl                               # Zeitzone, NTP aktiv
systemctl is-enabled unattended-upgrades nftables ifupdown-wait-online jnpt-stack-nachstart
grep -E '^(auto|allow-hotplug)' /etc/network/interfaces   # beide Zeilen für die LAN-Buchse
grep -E '^WAIT_ONLINE' /etc/default/networking            # METHOD=route, TIMEOUT=120
cat /etc/default/jnpt-netz                                # JNPT_LAN_IFACE=<LAN-Buchse>
systemctl show docker -p ExecStartPre | grep -q jnpt-netz-warten && echo netz-warten-ok
```

> **SSH ist danach faktisch unerreichbar — Absicht.** Port 22 ist nur über
> `tailscale0` offen, und das gibt es erst mit dem Wartungszugang. Solange die
> Box auf dem Tisch steht: Monitor und Tastatur.
>
> ⚠️ **Umstellung am laufenden System** (z. B. `none` → `lan`): Das Skript lädt
> das Ruleset neu, `flush ruleset` räumt dabei auch Dockers NAT-Regeln ab, und
> das Skript startet Docker deshalb neu. Kurzer Stack-Neustart —
> Wartungsfenster.
>
> ⚠️ **`AllowTcpForwarding no` schließt auch `ssh -L` aus.** Ein Tunnel auf die
> Admin-GUI ist kein Weg; der Weg ist der Wartungs-Container (Abschnitt 5.4).

**Boot-Ordnung — warum drei Dinge statt einem:** Der Debian-Installer schreibt
die LAN-Buchse als `allow-hotplug`; `network-online.target` gilt dann sofort als
erreicht, ohne Adresse und ohne Nameserver. Docker startet hinein, Caddy kann
die LAN-Adresse nicht binden („cannot assign requested address"), und ein
Start-Fehler ist kein Crash — keine `restart:`-Politik holt ihn nach. Die Box
ist nach dem nächtlichen Reboot von außen tot. Deshalb: `auto`-Zeile +
`WAIT_ONLINE_METHOD=route`, `jnpt-netz-warten` vor `docker.service` (wartet auf
Adresse, Route **und** DNS, höchstens 180 s) und `jnpt-stack-nachstart.service`
(`compose up -d` 20 s nach Docker). Ein Rennen, das zehnmal gewonnen wird, ist
nicht behoben — deshalb die zweite Sicherung.

## 4. Stack in Betrieb nehmen

### 4.1 Docker CE

Aus dem offiziellen Docker-Repository (der Compose-Stand der Distribution ist zu
alt). ⚠️ Pfad `…/linux/debian`, nicht `…/linux/ubuntu`.

```bash
apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
  https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker
```

### 4.2 Stack starten

```bash
cd /opt/jnpt/box
cp .env.example .env && $EDITOR .env
#   JANUAPORT_IMAGE  gepinnter Tag, kein :latest
#   JNPT_DOMAIN      der Box-Name (<BOX_NAME>), JNPT_CADDY_TLS="tls internal"
#   JNPT_BIND_ADDR   <LAN-IP> — leer heißt zu
#   JNPT_SSO_* / JNPT_PUBLIC_URL   Abschnitt 5.1
#   JNPT_UPDATE_*    Feed-URL und Prüfschlüssel (Abschnitt 7; Pflicht vor Auslieferung)

# Master-Key als Datei-Secret — uid 65532, sonst kann der Container ihn nicht lesen
printf '%s\n' '<Master-Key aus 2.3>' > /tmp/mk
install -o 65532 -g 65532 -m 0400 /tmp/mk secrets/jnpt_master_key
shred -u /tmp/mk

docker compose -f docker-compose.box.yml pull
docker compose -f docker-compose.box.yml up -d
docker compose -f docker-compose.box.yml ps
```

- **Immer `-f docker-compose.box.yml`.** Ohne `-f` sucht Compose die
  Standardnamen, steigt ins Elternverzeichnis auf und findet womöglich ein
  anderes Projekt — kein Fehler, nur ein anderer Stack. `docker compose ls`
  nennt die benutzte Datei.
- **Nie `-p`.** Der Projektname steht in der Datei (`name: jnpt`). Ein anderes
  `-p` legt frische, **leere** Volumes an; die Daten liegen dann im anderen
  Volume (`docker volume ls` zeigt zwei `*_jnpt-data`).
- **Master-Key:** 64 Hex-Zeichen, nicht 32 rohe Bytes — sonst Startabbruch
  („kaputte Key-Datei"). Gehört er nicht uid 65532: „permission denied".

### 4.3 Inbetriebnahme — die zwei box-spezifischen Abweichungen

Die Inbetriebnahme selbst (Admin-Passwort, Admin-Token, Integrationen, Teams,
Tokens) folgt der JanuaPort-Dokumentation. Auf der Box gilt zusätzlich:

1. **Der Master-Key existiert schon** (2.3) und kommt als Secret. Die Anlage
   erzeugt beim Erststart keinen eigenen.
2. **Die Admin-Fläche ist nie öffentlich.** Erreichbar lokal auf der Box, im
   Kunden-LAN über 8443 (4.4) und im Tailnet über den Wartungszugang (5.4).
   CLI-Aufrufe laufen als:

```bash
cd /opt/jnpt/box
docker compose -f docker-compose.box.yml exec jnpt /jnpt admin reset-password
docker compose -f docker-compose.box.yml exec jnpt /jnpt admin-token create --label <zweck>
```

> ⚠️ **Das Admin-Passwort VOR dem Transport setzen — Pflicht.** Solange
> `GET /api/admin/status` `setup_required: true` meldet, beansprucht **die
> erste Person, die die Fläche öffnet**, den Admin-Zugang — im Kunden-LAN über
> 8443 also jeder im Netz. Ein Arbeits-Passwort setzen; am Aufbautag ersetzt es
> der Betreiber durch sein eigenes. Gegenprobe vor dem Einpacken:
> `{"setup_required":false}`.

### 4.4 Betreiber-Fläche im Kunden-LAN (8443)

Der Betreiber trägt jeden Geheimnis-Wert **selbst** ein; Zugangsdaten laufen
nie durch den Hersteller. Sein Weg ist `https://<LAN-IP>:8443` — die volle
Instanz, nur aus dem Kunden-CIDR. Drei Schichten, alle bewusst: Compose-Bind
auf die LAN-IP · Host-Firewall nur im Modus `lan` · eine eigene Caddy-Instanz
mit interner CA (`Caddyfile.admin`).

Einmaliger Handgriff: das Root-Zertifikat der internen CA exportieren und beim
Betreiber importieren.

```bash
cd /opt/jnpt/box
docker compose -f docker-compose.box.yml exec caddy-admin \
  cat /data/caddy/pki/authorities/local/root.crt > jnpt-box-admin-ca.crt
```

⚠️ Die Box hat **zwei** Wurzeln: die der Werkzeug-Fläche (443, Volume
`jnpt_caddy-data`) und die der Betreiber-Fläche (8443, Volume
`jnpt_caddy-admin-data`). Beide heißen gleich und unterscheiden sich nur im
Fingerabdruck. Über 443/8443 liefert Caddy nur Blatt- und Zwischenzertifikat,
nie die Wurzel — die Datei wird mitgebracht.

### 4.5 Lizenz

Ab dem Ende der Testphase stoppt die Anlage den Betrieb ohne gültige Lizenz.
Der Kunde legt im Portal des Herstellers Abruf-URL und Abruf-Secret an und
trägt beide **selbst** auf der Betreiber-Fläche ein (*Lizenz → Abruf
einrichten*, dann *Lizenz vom Server abrufen*). Gegenprobe:

```bash
docker compose -f docker-compose.box.yml exec jnpt /jnpt license status   # active · plan · seats · expires
```

- Die **Installations-Kennung** (Lizenz-Seite, 32 Hex, kein Geheimnis)
  notieren. Eine Ersatzbox hat eine neue Kennung — eine an die alte gebundene
  Lizenz wäre dort fremd.
- **Update und Lizenz in einem Zug**: eine scharfe Anlage läuft nie ohne
  Lizenz auf ihr Testende zu.

## 5. Anbindung

### 5.1 Standardweg: OpenAI Secure MCP Tunnel

Ein Beisteller-Container (`ghcr.io/openai/tunnel-client`, **gepinnter Tag**)
verbindet sich per HTTPS-Long-Polling **ausgehend** mit OpenAI und reicht
MCP-Aufrufe **direkt im Compose-Netz** an `http://jnpt:8484` weiter — nicht
über Caddy, ohne veröffentlichten Port, ohne Firewall-Regel. Ein Timeout von
außen auf die Box ist der Soll-Zustand.

Der Tunnel-Stack (Compose, systemd-Units für den Schlüsselwechsel,
tmpfiles-Regel) kommt mit JanuaPort; hier liegt nur das Box-Profil
(`tunnel/profiles/box.yaml.example`, Erwartungen an den Stack:
[`tunnel/README.md`](tunnel/README.md)).

**Auf der Box:**

| Pfad | Inhalt |
|---|---|
| `/opt/jnpt/tunnel/docker-compose.tunnel.yml` | eigener Stack, tritt `jnpt_default` bei — Gateway zuerst starten |
| `/opt/jnpt/tunnel/profiles/box.yaml` | Profil: Kennung, Log, Health, **ein Kanal je Verbindungspunkt**, harpoon-Ziele. **Kein Secret.** |
| `/opt/jnpt/tunnel/.env` | `CA_BUNDLE=/usr/local/share/ca-certificates/jnpt-box-caddy-ca.crt` |
| `/run/jnpt/tunnel/` | tmpfs, 0750, uid 65532 — hier legt das Gateway den Laufzeit-Schlüssel aus dem Tresor ab |

**Einbau:**

```bash
mkdir -p /opt/jnpt/tunnel/profiles
# Compose, tmpfiles-Regel und Units des Tunnel-Stacks aus der JanuaPort-Auslieferung ablegen,
# dann: systemd-tmpfiles --create <regel> ; ls -ld /run/jnpt/tunnel   # drwxr-x--- 65532 65532
cp /opt/jnpt/box/tunnel/profiles/box.yaml.example /opt/jnpt/tunnel/profiles/box.yaml
$EDITOR /opt/jnpt/tunnel/profiles/box.yaml       # tunnel_id und <BOX_NAME>

# Laufzeit-Schlüssel in der Admin-GUI eintragen: Verbinden → Credentials →
# Stelle `openai-tunnel`. Er liegt dann im Tresor; das Gateway legt ihn ab:
ls -l /run/jnpt/tunnel/openai-runtime-key        # 65532, Größe > 0 — mehr nicht ansehen
# ERST JETZT die Path-Unit des Stacks einschalten (vorher gibt es keine Datei)

echo 'CA_BUNDLE=/usr/local/share/ca-certificates/jnpt-box-caddy-ca.crt' >> /opt/jnpt/tunnel/.env
cd /opt/jnpt/tunnel && docker compose -f docker-compose.tunnel.yml up -d
docker compose -f docker-compose.tunnel.yml logs 2>&1 | grep -E 'started|channels registered|x509|unsupported channel|invalid harpoon'
curl -s http://127.0.0.1:8081/healthz            # live
```

- Ein `Unauthorized` **einmalig beim Start** ist Soll (Start-Probe ohne Ausweis).
- `x509` = Vertrauensstellung fehlt (Punkt 3 unten). `unsupported channel
  "harpoon"` = kein Ziel registriert. `invalid harpoon target` = Komma in einer
  Zielbeschreibung.
- Die Path-Unit startet den Client neu, **sobald sich der Schlüssel ändert**.
  Ohne sie liefe er nach einer Rotation mit dem alten Schlüssel weiter
  (`401 invalid_api_key`). Das Gateway schreibt die Datei nur bei
  Byte-Abweichung: „neu eingetragen" ohne Log-Zeile heißt „derselbe Wert".

**Der Box-Name ohne öffentliches DNS und ohne öffentliches Zertifikat.** Der
Tunnel-Client muss den Box-Namen nur für einen Zweck per HTTPS erreichen: den
harpoon-Weg zu den Anmelde-Metadaten. Dafür braucht es weder A-Record noch
Let's Encrypt:

1. **Der Name existiert nur in der Box, im Identity-Provider und im
   Client-Dialog.** Im Compose-Netz löst er per Netzwerk-Alias auf den `caddy`
   der Box auf (steht in der Compose). Der Host kennt ihn über `/etc/hosts`
   (`<LAN-IP> <BOX_NAME>`, für den Aktuator).
2. **Caddy stellt das Zertifikat aus seiner internen CA aus**
   (`JNPT_CADDY_TLS="tls internal"`).
3. **Der Tunnel-Client vertraut dieser CA** über `CA_BUNDLE` — additiv zu den
   System-Wurzeln. ⚠️ Nicht `SSL_CERT_FILE`: Go **ersetzt** damit die
   System-Wurzeln, und `api.openai.com` fällt weg. Wurzel in den Host-Truststore:

   ```bash
   install -m 0644 /var/lib/docker/volumes/jnpt_caddy-data/_data/caddy/pki/authorities/local/root.crt \
           /usr/local/share/ca-certificates/jnpt-box-caddy-ca.crt
   update-ca-certificates
   ```

   Gegenprobe beim Einbau: ein `mcp.server_urls`-Ziel testweise auf
   `https://<BOX_NAME>/mcp` legen — verbindet der Client ohne `x509`, steht die
   Vertrauensstellung; danach zurück auf `http://jnpt:8484/mcp`.
4. **harpoon-Ziele:** `https://<BOX_NAME>` und `https://login.microsoftonline.com`.
   ⛔ **Nie `http://jnpt:8484` als harpoon-Ziel** — harpoon gibt dem Modell
   HTTP-Zugriff auf das Ziel; nur Caddys Whitelist hält die Admin-Fläche fern.

**Werte der Anmeldung** (Entra-Tenant des Kunden; der Box-Name steht an drei
Stellen zeichengleich: `.env`, `identifierUris` in Entra, Feld „Ressource" im
Client-Dialog):

| Variable | Wert |
|---|---|
| `JNPT_DOMAIN` | `<BOX_NAME>` unter einer im Tenant des Kunden **verifizierten** Domäne |
| `JNPT_SSO_ENABLED` | `true` — **ohne ihn sind alle SSO-Werte wirkungslos** (kein Metadaten-Endpunkt, 401 bleibt blankes `Bearer`) |
| `JNPT_PUBLIC_URL` | `https://<BOX_NAME>/mcp` |
| `JNPT_SSO_ISSUER` | `https://login.microsoftonline.com/<tenant>/v2.0` |
| `JNPT_SSO_AUDIENCE` | Anwendungs-ID der **Ressourcen**-Registrierung |
| `JNPT_SSO_AUTH_SCOPE` | der Scope, den die Ressourcen-Registrierung wirklich trägt, plus `offline_access` |
| `JNPT_SSO_CLIENT_ID` | Anwendungs-ID der **getrennten Client**-Registrierung — nicht die Audience |
| `JNPT_SSO_GROUPS_CLAIM` | `roles` |
| `JNPT_SSO_REQUIRED_ROLE` | **Wert** (`appRoles[].value`) der App-Rolle, die sich anmelden darf |

Auf der Entra-Seite (Handgriffe des Kunden-Admins): **zwei** Registrierungen —
sonst stirbt jede Verbindung nach 60–90 Minuten mit `AADSTS90009`. Ressource:
Token-Version 2 (`api.requestedAccessTokenVersion: 2`), `https://<BOX_NAME>/mcp`
als `identifierUris`, eigener Scope, einmalige Admin-Einwilligung. Client: die
Rückruf-URI des KI-Clients unter `publicClient.redirectUris` (nicht `web`),
Berechtigung auf den Ressourcen-Scope und `openid`/`profile`/`offline_access`.
⛔ Keine „Zuweisung erforderlich" auf den Unternehmensanwendungen: Wer herein
darf und was er darf, entscheidet JanuaPort (deny-by-default).

**Betrieb:**

- **Schein-Grün messen, nicht glauben.** `healthz = live` und ein pollender
  Client sagen nichts über den Weg; bei Nutzung muss `commands_polled`
  steigen. Bleibt es bei 0, liegt der Fehler **vor** der Box.
- **Eine App, ein Link.** Der KI-Client behält jede je angelegte Verbindung
  einer App und kann je Aufruf eine davon wählen; eine tote Alt-Verbindung
  scheitert, **bevor** der Tunnel-Client etwas sieht. Nach jedem „erneut
  verbinden" die alte Verbindung trennen.
- **Update des Clients** ist ein Handgriff (Abschnitt 7): Tag hochziehen,
  `up -d --force-recreate`, Kanäle im Log und `commands_polled` prüfen.

### 5.2 Zwei Gates beim SSO-Login

| | Gate | wo | prüft |
|---|---|---|---|
| 1 | Basis-Gate | `JNPT_SSO_REQUIRED_ROLE` | trägt das Token die App-Rolle im Claim `roles`? |
| 2 | Freigabe | Admin-GUI: Zugang → SSO → Gruppen-Freigabe | hat die Person ein Konto **oder** eine freigegebene Rolle/Gruppe? |

⚠️ **Beide protokollieren eine Abweisung nicht** (fail-closed). Fehlt die
Freigabe, sieht der erste Login wie ein Fehler beim Identity-Provider aus (ein
nacktes 401) und ist keiner. Die Team-Brücke (`idp_groups` am Team) vergibt nur
Rechte an jemanden, der schon herein darf — sie lässt niemanden herein. Also
vor dem ersten Login die Freigabe eintragen, dieselbe Rolle wie im Basis-Gate.

### 5.3 Clients im Kundennetz: Token-Weg und LAN-Alias

Für alles, was kein Mensch ist — RPA, Skripte — ist ein **Token** der Weg:
einer je Zweck, werkzeuggenau zugeschnitten, mit **Besitzer** (ohne Besitzer
bekommt ein Token keine Kartei) und mit Ablaufdatum. Solche Clients sprechen
die Werkzeug-Fläche `:443` an, nie `:8443`.

**LAN-Alias**, damit Rechner im Kundennetz die Box unter einem internen Namen
erreichen statt über eine `hosts`-Zeile je PC:

```bash
nslookup <ALIAS> <DNS-Server-des-Kunden>    # ZUERST: muss <LAN-IP> liefern
# .env: JNPT_LAN_ALIAS=<ALIAS>
docker compose -f docker-compose.box.yml up -d caddy
CA=/usr/local/share/ca-certificates/jnpt-box-caddy-ca.crt
curl -s -o /dev/null -w "%{http_code}\n" --resolve <ALIAS>:443:<LAN-IP> --cacert $CA https://<ALIAS>/mcp              # 401
curl -s -o /dev/null -w "%{http_code}\n" --resolve <BOX_NAME>:443:<LAN-IP> --cacert $CA https://<BOX_NAME>/mcp        # 401
curl -s -o /dev/null -w "%{http_code}\n" --resolve <ALIAS>:443:<LAN-IP> --cacert $CA https://<ALIAS>/api/admin/users  # 404
```

- Den A-Record legt der IT-Dienstleister an; die Box registriert sich nicht
  selbst. Dass ein Windows-Rechner einen `.local`-Namen auflöst, beweist nichts
  (mDNS/LLMNR).
- Der Alias ist ein **Transportname, keine Identität**: `JNPT_PUBLIC_URL` bleibt.
- Die Clients brauchen die Wurzel der Werkzeug-Fläche (`…-caddy-ca`) — im
  Zertifikatspeicher des Kontos, unter dem der Client läuft.
- ⚠️ **`curl.exe` unter Windows** scheitert ohne `--ssl-no-revoke`, obwohl das
  Zertifikat stimmt (interne CA ohne Sperrlisten-Verteilpunkt). Für die Abnahme
  zählt das Werkzeug, das später arbeitet. Der Beleg Ende zu Ende ist eine
  Audit-Zeile auf der Box, nicht eine Anzeige im Client.
- ⚠️ **Power Automate „Webdienst aufrufen":** den Schalter „Anfragetext
  codieren" ausschalten — sonst geht das JSON prozent-kodiert raus
  (`400 malformed payload … invalid character '%'`).

### 5.4 Wartungszugang über Tailscale (Opt-in)

Der Wartungszugang existiert **nur**, wenn der Kunde ihn schriftlich einräumt:
**wer** zugreift, **wofür**, **auf welchem Weg**. Kein stilles Default, kein
Phone-Home, kein stehendes Monitoring — ein Dauertunnel zu jeder Kundenbox wäre
im Kompromittierungsfall ein Sprungbrett zu allen.

**Zwei getrennte Schalter**, einzeln abschaltbar:

**(a) Wartungs-Container — die volle Instanz im Tailnet**

```bash
cd /opt/jnpt/box
# Auth-Key aus der Tailscale-Konsole: nicht-ephemeral, mit ACL-Tag, einmal nutzbar
printf '%s\n' '<tskey-auth-…>' > /tmp/ak
install -m 0600 /tmp/ak secrets/tailscale_authkey
shred -u /tmp/ak
# .env: COMPOSE_PROFILES=wartung  (dann startet jeder Aufruf das Modul mit, auch der Boot)
docker compose -f docker-compose.box.yml up -d
```

Danach ist die volle Instanz unter `https://<TS_HOSTNAME>.<tailnet>.ts.net`
erreichbar — und **nur** dort. Dafür muss HTTPS im Tailnet aktiv sein
(Tailscale-Konsole → DNS). 🔒 **Dieser Knoten wird nie per Funnel
veröffentlicht.**

**(b) Host-Knoten — SSH über das Tailnet.** Der Container läuft im Userspace
und bringt den Host **nicht** ins Tailnet:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up --ssh=false --accept-dns=false --hostname=<box>-host --authkey '<tskey-auth-…>'
ip -brief addr show tailscale0        # jetzt greift die nftables-Regel für Port 22
tailscale dns status | head -3        # disabled
```

⚠️ **`--accept-dns=false` ist Pflicht.** Sonst hängt Tailscale den Resolver des
Hosts auf MagicDNS um, und die Container erben ihn beim Start. Nachträglich:
`tailscale set --accept-dns=false`.

**ACLs: Standard ist Sperre.** Box-Knoten tragen ein **eigenes Tag** (z. B.
`tag:box`), nie ein Nutzerkonto. Keine Regel gibt den Admin-Geräten des
Herstellers Zugriff auf `tag:box` — außer der **benannten** Opt-in-Regel, die
man mit einem Handgriff entfernt. Beispiel in `grants`-Syntax:

```json
{"src":["autogroup:member"], "dst":["autogroup:member"], "ip":["*"]},
{"src":["autogroup:admin"],  "dst":["tag:box"],          "ip":["tcp:22","tcp:443"]}
```

- ⚠️ **Die Gegenprobe braucht einen Knoten mit fremder Identität.** Ist die
  Opt-in-Regel an eine Person gebunden, erbt jeder Knoten derselben Identität
  den Zugang; ein Versuch von dort gelingt und sieht aus wie ein ACL-Fehler.
- ⚠️ `tailscale ping` ist kein Ersatz — es läuft am ACL-Filter für TCP vorbei.

**Sichtbarkeit für den Kunden** (gehört ins Übergabegespräch und auf den
Notfall-Einleger):

```bash
docker compose -f docker-compose.box.yml ps   # das Modul heißt „tailscale"
systemctl status tailscaled                   # der Host-Zugang
tailscale funnel status                       # was ist öffentlich? Im Standardbetrieb: nichts
```

**Entzug — vollständig, in dieser Reihenfolge.** „Container gestoppt" reicht
nicht; Auth-Key und Knoten existieren weiter.

```bash
tailscale logout && systemctl disable --now tailscaled                 # 1) Host-Zugang
cd /opt/jnpt/box
docker compose -f docker-compose.box.yml --profile wartung down tailscale   # 2) Modul
docker volume rm jnpt_tailscale-state                                  #    Knoten-Identität
shred -u secrets/tailscale_authkey                                     # 3) Secret
# 4) .env: COMPOSE_PROFILES=wartung wieder auskommentieren
docker compose -f docker-compose.box.yml up -d
```

5) In der Tailscale-Konsole: Auth-Key widerrufen, **beide** Knoten entfernen,
Entzug mit Datum protokollieren. Danach ist SSH wieder unerreichbar — der
gewollte Ruhezustand. Der Regelfall eines **vorübergehenden** Entzugs ist
schlichter: nur die benannte ACL-Regel entfernen.

### 5.5 Rückfall: Tailscale Funnel auf genau `/mcp`

Fällt der Tunnel-Weg aus, ist Tailscale Funnel der Rückfall: ebenfalls rein
ausgehend, auch hinter CGNAT, TLS endet auf der Box. Er hängt am **Host-Knoten**
(5.4 b), nie am Wartungs-Container — der trägt die volle Instanz.

- Grenzen: keine eigene Domain (`…ts.net`), Abhängigkeit vom Tailscale-Konto,
  Relays mit Bandbreiten-Grenze.
- ⚠️ **Nicht abschließend belegt:** die pfadgenaue Form. Entweder begrenzt der
  Funnel auf `/mcp` (dann braucht `jnpt` einen Loopback-Port — eine zweite
  Deklaration der Fläche neben dem Caddyfile), oder er zeigt auf Caddy (dann
  muss Caddy unter dem `…ts.net`-Namen antworten). **Kein Funnel im
  Kundenbetrieb ohne die Gegenprobe**, dass `/`, `/admin/mcp` und
  `/api/admin/…` von außen nicht antworten.
- `tailscale funnel status` muss **genau einen** Pfad zeigen. Zeigt es `/` oder
  den Wartungsknoten: sofort `tailscale funnel reset`.

## 6. Prüfen — ein echter MCP-Rundlauf, nicht `/healthz`

Erfolgskriterium ist nie „der Prozess läuft", sondern „der Kunde kann
arbeiten".

```bash
# 1) Liveness (notwendig, nicht hinreichend)
curl -fsS --cacert /usr/local/share/ca-certificates/jnpt-box-caddy-ca.crt https://<BOX_NAME>/healthz

# 2) MCP-Rundlauf mit einem Test-Token
curl -fsS https://<BOX_NAME>/mcp \
  -H "Authorization: Bearer <test-token>" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2025-06-18","capabilities":{},
                 "clientInfo":{"name":"box-abnahme","version":"1"}}}'
# danach tools/list — es muss genau die für den Token freigegebenen Werkzeuge nennen

# 3) Container-Egress — beweist die forward-Kette der Host-Firewall
docker run --rm --network jnpt_default curlimages/curl:<tag> -fsS https://api.github.com/zen
```

- [ ] Container-Egress funktioniert (sonst: `nft list chain inet filter forward`
      — die `jnpt0`-Ausnahmen müssen da sein).
- [ ] `initialize` liefert eine Antwort, `tools/list` zeigt **genau** die
      freigegebenen Werkzeuge.
- [ ] Ein echter Werkzeug-Aufruf erreicht das Zielsystem und steht mit
      richtigem Zeitstempel im Audit.
- [ ] `/admin/mcp`, `/api/admin/…`, `/`, `/metrics` sind auf 443 **404** — das
      ist die Sicherheitsgrenze, kein Fehler.
- [ ] 8443 antwortet aus dem Kunden-CIDR (mit importierter Wurzel, ohne `-k`)
      und von außerhalb des CIDR **nicht**.
- [ ] **Der Kundenweg:** ein echter Werkzeug-Aufruf aus dem KI-Client durch den
      Tunnel, mit Anmeldung eines Nutzers, und der Aufruf steht im Audit.
      „Tunnel-Client gesund" zählt nicht.
- [ ] **Von außen antwortet nichts** — kein öffentlicher Endpunkt.
- [ ] **Ohne Opt-in kommt der Hersteller nicht auf die Box** (ACL-Gegenprobe).
- [ ] **Reboot-Serie:** nach jedem Reboot laufen **alle** Container, 443 und
      8443 antworten. Der Beweis heißt „443 zurück", nicht „SSH zurück" — SSH
      hängt am Tailnet, nicht an der LAN-Adresse.

**Lastsuite** (`scripts/lasttest.py`, nur Standardbibliothek): Stufen
paralleler MCP-Sessions plus Dauerlast gegen `https://<BOX_NAME>/mcp`,
protokolliert nur Zähler und Perzentile, nie Antwortinhalte. Der Token dafür
bekommt nur die Datei-Integration und wird danach widerrufen.

```bash
python3 lasttest.py --host <LAN-IP> --name <BOX_NAME> --ca jnpt-box-caddy-ca.crt \
  --token-file .tok --probe                  # zuerst: Handshake + Werkzeugnamen
python3 lasttest.py --host <LAN-IP> --name <BOX_NAME> --ca jnpt-box-caddy-ca.crt \
  --token-file .tok --box-ssh root@<box> --log lasttest.log
```

## 7. Update

**Das Gateway-Image** aktualisiert ein **Host-Dienst** von JanuaPort (Skript,
systemd-Unit und Timer), kein Container und kein `docker.sock` in einem
Container. Er wird mit JanuaPort ausgeliefert; seine Mechanik steht in dessen
Dokumentation. Box-spezifisch sind die Konfiguration und der Rundlauf:

```
# /etc/jnpt-updater.conf (0600 root) — gegenüber der Vorlage anders:
COMPOSE_FILE=/opt/jnpt/box/docker-compose.box.yml
ENV_FILE=/opt/jnpt/box/.env
CONTAINER=jnpt-jnpt-1
DOMAIN=<BOX_NAME>
```

- Der Aktuator misst nach jedem Update einen echten MCP-Rundlauf über
  `https://$DOMAIN/mcp` und rollt bei Rot zurück. Damit das auf der Box geht:
  `/etc/hosts` mit `<LAN-IP> <BOX_NAME>` und die Wurzel der Werkzeug-Fläche im
  Host-Truststore (5.1, Punkt 3). Gegenprobe: `curl -fsS https://<BOX_NAME>/healthz`
  ohne `-k`.
- ⚠️ `DOMAIN` darf nie auf eine Fläche ohne gültiges Zertifikat zeigen — dann
  rollt der Aktuator **jedes** Update zurück. Als Übergang geht
  `DOMAIN=<LAN-IP>:8443` mit der Wurzel der Betreiber-Fläche im Truststore.
- Der Prüf-Token ist ein Systemzugang des Produkts
  (`jnpt system-token rotate updater_healthcheck`), sein Klartext erscheint
  genau einmal; nie per `echo` auf die Kommandozeile.

**Modi:** `jnpt update plan` entscheidet. Ein Update **ohne** Schema-Migration
läuft unbeaufsichtigt (Sicherung → Pull by Digest → `up -d` → Rundlauf →
Rückrollen bei Rot); eines **mit** Migration wartet auf den Klick auf der
Betreiber-Fläche. Der Timer läuft alle 15 Minuten.

- ⚠️ **Vor der Übergabe von „Sofort" auf „Täglich um <Nachtzeit>" stellen.**
  „Sofort" ist bei der Einrichtung richtig; im Betrieb landet ein Update sonst
  mitten im Arbeitstag, mit kurzer Unterbrechung von `/mcp`.
- ⚠️ Vor angekündigten Terminen am Gerät oder beim Kunden das Update-Fenster
  im Blick haben — „Sofort" fragt niemanden.

**Alles daneben ist Handarbeit** — der signierte Update-Weg tauscht nur das
Gateway-Image:

| Beisteller | liegt als | Nachzug |
|---|---|---|
| Update-Aktuator | Skript + Unit/Timer | neue Fassung installieren, `daemon-reload`; `.conf` und Token bleiben |
| Tunnel-Client | `/opt/jnpt/tunnel/docker-compose.tunnel.yml` | gepinnten Tag hochziehen, `up -d --force-recreate`, Kanäle und `commands_polled` prüfen |
| Box-Hülle | `/opt/jnpt/box` (Kopie, Stand in `.stand`) | neuen Stand kopieren (`.env`, `secrets/` bleiben), `.stand` fortschreiben, `up -d`; `hardening.sh` erneut (idempotent, Wartungsfenster). ⚠️ Ändert der neue Stand den `networks`-Block: **nicht** so, sondern nach Abschnitt 9 |
| EBICS-Abholer | `/opt/jnpt/ebics-abholer` | siehe [`../ebics-abholer/`](../ebics-abholer/) |

Vor jedem Nachzug eine Sicherung (Abschnitt 8), danach der Rundlauf aus
Abschnitt 6.

## 8. Sicherung und Restore

**Die eine Mechanik ist `jnpt backup` / `jnpt restore` im Produkt** — dieselbe,
die der Update-Aktuator vor jedem Update aufruft. Hier steht bewusst kein
zweites Verfahren: Zwei Sicherungswege laufen im Ernstfall auseinander.

```bash
cd /opt/jnpt/box
docker compose -f docker-compose.box.yml exec jnpt /jnpt backup
# → /data/backups/jnpt-<zeitstempel>.jnpt-bkp, dazu der Fingerabdruck des Master-Keys
```

Box-spezifisch:

- **Die Sicherung gehört nicht nur ins Volume.** `/data/backups/` liegt auf
  derselben Platte wie die Datenbank. Der tägliche Abzug muss **von der Box
  weg**: auf eine Netzfreigabe (`smb/`), nach SharePoint (`sharepoint/`) oder
  auf eine USB-Festplatte an der Box (ein Stick als einziges Medium ist zu
  ausfallträchtig). Wer nichts davon nutzt, automatisiert selbst: ein
  systemd-Timer, der den Befehl oben ruft und das Artefakt wegbringt.
- **Der Master-Key liegt nie beim Artefakt** (Umschlag B). Ohne ihn ist kein
  Restore möglich — es gibt keinen Reset und keinen Support-Weg.
- **Der Fingerabdruck** steht neben den Sicherungsständen und auf dem
  Notfall-Einleger.
- Nicht in der Sicherung: der EBICS-Eingang (die Bank liefert Unquittiertes
  erneut) und der Bankzugang selbst (der liegt versiegelt beim Kunden).

**Restore-Probe ohne Risiko** — `jnpt restore` spielt nur in eine **leere**
Installation ein, also in ein Wegwerf-Volume:

```bash
# Artefakt nach /var/lib/jnpt/restore-probe/ legen und uid 65532 geben —
# restore läuft als 65532 im Container, unter /root wäre es unlesbar.
chown -R 65532:65532 /var/lib/jnpt/restore-probe
IMG=$(docker inspect -f '{{.Config.Image}}' jnpt-jnpt-1)
docker volume create jnpt-restore-probe
docker run --rm -v jnpt-restore-probe:/data \
  -v /var/lib/jnpt/restore-probe/<datei>:/restore.jnpt-bkp:ro \
  -v /opt/jnpt/box/secrets/jnpt_master_key:/run/secrets/jnpt_master_key:ro \
  -e JNPT_MASTER_KEY_FILE=/run/secrets/jnpt_master_key -e JNPT_DB_PATH=/data/jnpt.db \
  "$IMG" restore /restore.jnpt-bkp
docker run --rm -v jnpt-restore-probe:/data \
  -v /opt/jnpt/box/secrets/jnpt_master_key:/run/secrets/jnpt_master_key:ro \
  -e JNPT_MASTER_KEY_FILE=/run/secrets/jnpt_master_key -e JNPT_DB_PATH=/data/jnpt.db \
  "$IMG" token list | tail -n +2 | wc -l          # gegen die laufende Anlage vergleichen
docker volume rm -f jnpt-restore-probe; rm -rf /var/lib/jnpt/restore-probe
```

- [ ] **Restore-Drill vor dem Betrieb mit echten Daten:** einmal einspielen,
      einmal Rundlauf nach Abschnitt 6. Eine ungeprüfte Sicherung ist ein
      Versprechen.

### Ersatzbox

Bewusst kein RAID, keine USV-Pflicht, kein HA: das Konzept ist **Ersatzbox +
Restore**.

1. Ersatzgerät nach Abschnitt 1–4 provisionieren.
2. Sicherung mit `jnpt restore` einspielen, Master-Key aus Umschlag B — der
   **Fingerabdruck** entscheidet, ob es der richtige ist.
3. Lizenz prüfen: neue Installations-Kennung (4.5).
4. Anbindung wiederherstellen: Tunnel-Client neu einbauen, **derselbe
   Box-Name**, damit Entra-Einträge und Client-Verbindungen weitergelten.
5. Optionale Beisteller neu aufsetzen (Bankzugang aus der versiegelten
   Sicherung des Kunden — er ist nicht in der JanuaPort-Sicherung).
6. Rundlauf nach Abschnitt 6.
7. Altes Gerät: Werksreset.

### Werksreset

Die Daten muss man nicht löschen — es genügt, die **Schlüssel** zu vernichten.
Ohne Keyslot ist der LUKS-Container Rauschen.

- [ ] Aktuelle, **geprüfte** Sicherung vorhanden — sonst ist der Reset ein
      Datenverlust.
- [ ] Wartungszugang vollständig entzogen (5.4), inklusive
      `tailscale funnel reset` und beider Knoten in der Konsole.
- [ ] Bankzugang bedacht: Er liegt auf dieser Platte und wird mit dem Reset
      unlesbar. Der Kunde braucht seine versiegelte Sicherung.
- [ ] Kunde weiß, dass seine Umschläge danach wertlos sind.

```bash
systemctl disable --now ebics-abholer.timer 2>/dev/null || true
cd /opt/jnpt/box && docker compose -f docker-compose.box.yml --profile wartung down
CRYPTDEV=/dev/<partition>              # ⚠️ anpassen
cryptsetup luksErase "$CRYPTDEV"       # ⚠️ UNWIDERRUFLICH — alle Keyslots, auch TPM2 und Recovery
wipefs -a "$CRYPTDEV"
```

Vernichtung protokollieren (Datum, Seriennummer, wer). Danach ist die Box leer
und neu provisionierbar.

## 9. Festes Netz und Netz-Umbau

Die Login-Bremse zählt Fehlversuche je Quelle. Ohne weitere Angabe ist die
Quelle die Adresse des Proxys — hinter `caddy-admin` also **ein Zähler für alle
Betreiber**: Wer im LAN absichtlich falsch anmeldet, sperrt alle aus. Deshalb
trägt die Compose ein festes Netz, und nur die drei eigenen Proxys dürfen
`X-Forwarded-For` setzen:

| Was | Wert |
|---|---|
| Netz `jnpt_default` (Bridge `jnpt0`) | `10.201.44.0/24`, dynamisch nur `10.201.44.128/25` |
| `caddy` (443) | `10.201.44.10` |
| `tailscale` (Profil `wartung`) | `10.201.44.11` |
| `caddy-admin` (8443) | `10.201.44.12` |
| `JNPT_TRUSTED_PROXY_CIDR` | `10.201.44.10,10.201.44.11,10.201.44.12` — fest, nie das Subnetz |

Der Tunnel-Client bekommt **keine** feste Adresse und steht **nicht** in der
Liste: Er spricht `jnpt:8484` direkt und darf den Header nicht fälschen können.
Der EBICS-Abholer hängt gar nicht an diesem Netz.

> 🚨 **Eine Änderung am `networks`-Block ist ein Wartungsfenster, kein
> Update.** Ein `up -d` mit geändertem IPAM, während der Tunnel-Client am Netz
> hängt, **stoppt `jnpt`**, scheitert beim Entfernen des Netzes („has active
> endpoints") und lässt den Dienst gestoppt; auch `down` scheitert. Genau so ein
> `up -d` fahren der Aktuator, `jnpt-stack-nachstart` beim Boot und jeder
> Hand-Nachzug. Deshalb kommt eine solche Compose erst auf die Box, wenn der
> Tunnel-Stack unten ist.

**Ablauf** (als root, über SSH im Tailnet — die Sitzung hängt am Host, nicht am
Stack, und überlebt das Fenster; vorher beim Kunden ankündigen):

```bash
# 1) Aktuator anhalten, sichern, Kollision prüfen
systemctl stop jnpt-updater.timer
systemctl is-active jnpt-updater.service              # inactive
cd /opt/jnpt/box && docker compose -f docker-compose.box.yml exec jnpt /jnpt backup
ip -4 route                                           # nichts darf das neue Subnetz enthalten
docker network inspect $(docker network ls -q) --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}} {{end}}'

# 2) Neue Compose nur BEREITLEGEN und kommentarfrei vergleichen
mkdir -p /root/netz && cp -p docker-compose.box.yml /root/netz/docker-compose.box.yml.alt
# neue Fassung nach /root/netz/docker-compose.box.yml bringen, dann:
diff <(grep -v '^\s*#' /root/netz/docker-compose.box.yml.alt) <(grep -v '^\s*#' /root/netz/docker-compose.box.yml)
# Zeigt der Diff mehr als den Netz-Umbau, trägt die Box eine Hand-Änderung: erst klären.

# 3) Tunnel-Stack herunter — down, nicht stop (ein gestoppter Container hängt am alten Netz)
cd /opt/jnpt/tunnel && docker compose -f docker-compose.tunnel.yml down
docker network inspect jnpt_default --format '{{range .Containers}}{{.Name}} {{end}}'
# nur noch jnpt, caddy, caddy-admin (tailscale); hängt noch etwas: entfernen

# 4) ERST JETZT die neue Datei einsetzen, Netz neu (down OHNE -v — die Volumes bleiben)
install -m 0644 /root/netz/docker-compose.box.yml /opt/jnpt/box/docker-compose.box.yml
cd /opt/jnpt/box && docker compose -f docker-compose.box.yml down
docker network ls --filter name=jnpt_default          # leer
docker compose -f docker-compose.box.yml up -d

# 5) Tunnel-Stack wieder hoch, Aktuator wieder an
cd /opt/jnpt/tunnel && docker compose -f docker-compose.tunnel.yml up -d
systemctl start jnpt-updater.timer
```

Prüfen:

```bash
docker network inspect jnpt_default --format '{{json .IPAM.Config}} {{index .Options "com.docker.network.bridge.name"}}'
docker network inspect jnpt_default --format '{{range .Containers}}{{.Name}} {{.IPv4Address}}{{"\n"}}{{end}}'
docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' jnpt-jnpt-1 | grep TRUSTED_PROXY
nft list chain inet filter forward | grep jnpt0       # unverändert: die Regeln hängen am Bridge-Namen
curl -s http://127.0.0.1:8081/healthz                  # Tunnel-Client live
```

**Beleg:** von einem Rechner im Kunden-LAN über `https://<LAN-IP>:8443` einmal
absichtlich falsch anmelden. Im Audit steht beim jüngsten `login_failed` als
`source` die **Adresse dieses Rechners**, nicht `10.201.44.12`.

**Zurück:** dieselbe Reihenfolge mit der alten Datei. Auch der Rückweg legt das
Netz neu an und scheitert genauso, wenn der Tunnel-Client noch hängt.

| Bild | Ursache | Abhilfe |
|---|---|---|
| `up -d`: „has active endpoints", `jnpt` gestoppt | neue Datei lag, während der Tunnel-Client am Netz hing | `docker network inspect jnpt_default` zeigt, wer hängt; Tunnel-Stack `down`, dann Schritt 4 und 5 |
| „Pool overlaps with other one", oder Ziele im Kunden-LAN aus den Containern unerreichbar | ein Host- oder Docker-Netz enthält das Subnetz | zurück, ein anderes Subnetz wählen (Subnetz, `ip_range`, drei Adressen und Liste **gemeinsam**) |
| `source` = `10.201.44.12` | Liste nicht aktiv oder `caddy-admin` ohne feste Adresse | Datei und Container-Env prüfen |
| `source` = Gateway-Adresse des Netzes | Anmeldung von der Box selbst statt aus dem LAN | Beleg von einem anderen LAN-Rechner wiederholen |

### Standortwechsel: die LAN-Adresse steckt an fünf Stellen

Die Box wird vorbereitet und zieht dann ins Kundennetz. **Vor jeder
Fehlersuche** zuerst den Träger prüfen — eine nicht aufgelegte Dose sieht aus
wie ein Boot-Problem, und bei manchen Geräten ist nur die bei der Installation
benutzte Buchse aktiv:

```bash
ip -br link                      # NO-CARRIER = Kabel/Dose/Buchse, nicht Software
```

Dann in dieser Reihenfolge nachziehen:

```bash
ip -4 addr show <LAN-Buchse>                                              # 1) Adresse da
cd /opt/jnpt/box/scripts && ./hardening.sh --https lan --lan-cidr <Kunden-CIDR>   # 2) Firewall
$EDITOR /etc/hosts                                   # 3) <LAN-IP> <BOX_NAME> (für den Aktuator)
$EDITOR /opt/jnpt/box/.env                           # 4) JNPT_BIND_ADDR=<LAN-IP>
cd /opt/jnpt/box && docker compose -f docker-compose.box.yml up -d
curl -fsS --cacert /usr/local/share/ca-certificates/jnpt-box-admin-ca.crt https://<LAN-IP>:8443/healthz   # 5)
```

`default_sni` der Betreiber-Fläche ist `JNPT_BIND_ADDR`: Mit der neuen Adresse
stellt **dieselbe** interne CA ein neues Blattzertifikat aus — eine beim
Betreiber importierte Wurzel bleibt gültig.

## 10. Optional: Ablage und Sicherung über SharePoint oder SMB

Das Gateway sieht nur `/ablage` — ein Verzeichnis. SMB, SharePoint oder ein
USB-Medium sind **Host-Sache**; im Gateway gibt es bewusst keinen Client dafür.
Zwei Wege, je mit einer Sicherung, die **nie** unter `/ablage` hängt: Die KI
sieht die Sicherung per Konstruktion nicht.

**Vorher, für beide Wege:**

```bash
install -d /srv/jnpt/ablage /srv/jnpt/sicherung
# JETZT, solange nichts gemountet ist: Fällt ein Mount später weg, ist der Mountpoint
# ein leeres Verzeichnis, und jeder Schreiber landet unbemerkt auf der Systemplatte.
# Das Flag lässt ihn hart scheitern. Mit Mount stört es nicht.
chattr +i /srv/jnpt/ablage /srv/jnpt/sicherung
```

- 🚨 **`propagation: rslave` am Ablage-Bind ist Pflicht** (steht in der
  Compose). Ohne bleibt ein später eingehängter Einstiegspunkt im Container
  leer — ohne Fehler, ohne Log-Zeile. Gegenprobe:
  `PID=$(docker inspect -f '{{.State.Pid}}' jnpt-jnpt-1); grep /ablage/ /proc/$PID/mountinfo`.
- ⚠️ **Ablage-Ordner nie ersetzen, nur befüllen.** Das Gateway hält beim Laden
  Gerät und Inode jedes Ablage-Ordners fest. Ein gelöschter und neu angelegter,
  umbenannter oder per Symlink getauschter Ordner ergibt „… hat sich seit dem
  Laden geändert", bis zum Reload: `docker kill -s HUP jnpt-jnpt-1`.
- ⚠️ **Alles, was in einer Ablage landet, gehört uid 65532.** `move_file`
  verschiebt per Hardlink; `fs.protected_hardlinks=1` verbietet das für eine
  Datei, die root gehört.

### SMB/CIFS (`smb/`)

Der Betreiber stellt ein Konto bereit, das **nur** diese Freigabe darf, und
eine Server-ACL, die **nur diesem Konto** Lesen erlaubt — ein
Sicherungsartefakt ist die vollständige Installation.

```bash
apt-get install -y cifs-utils
install -d -m 700 /etc/jnpt/smb
umask 077; cat > /etc/jnpt/smb/ablage.cred <<'ENDE'
username=<konto>
password=<passwort>
domain=<domäne oder weglassen>
ENDE

# Sicherungsziel (nur Host) — CIFS mountet einen Unterordner, nie die Wurzel der Freigabe
cp /opt/jnpt/box/smb/srv-jnpt-sicherung.mount /etc/systemd/system/
$EDITOR /etc/systemd/system/srv-jnpt-sicherung.mount       # What=

# je Einstiegspunkt eine Unit; der Unit-NAME muss zum Pfad passen:
install -d /srv/jnpt/ablage/<name>                        # vorher: chattr -i /srv/jnpt/ablage
chattr +i /srv/jnpt/ablage/<name>
systemd-escape -p --suffix=mount /srv/jnpt/ablage/<name>   # → Dateiname der Unit
cp /opt/jnpt/box/smb/srv-jnpt-ablage-beispiel.mount.example /etc/systemd/system/<unit-name>
$EDITOR /etc/systemd/system/<unit-name>                    # What= und Where=

systemctl daemon-reload
systemctl enable --now srv-jnpt-sicherung.mount <unit-name>
touch /srv/jnpt/sicherung/.jnpt-freigabe                   # Marker AUF der Freigabe

# tägliche Sicherung
install -m 0755 /opt/jnpt/box/smb/jnpt-backup.sh /usr/local/sbin/
cp /opt/jnpt/box/smb/jnpt-backup.{service,timer} /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now jnpt-backup.timer
systemctl start jnpt-backup.service && journalctl -u jnpt-backup -n 5 --no-pager
```

- `jnpt-backup.sh` prüft `mountpoint -q` **und** den Marker (ein Mount beweist
  nur, dass *etwas* dort hängt), erzeugt lokal, kopiert unter `.teil`, benennt
  um und liest Größe und SHA-256 gegen. Die Aufbewahrung legt der Betreiber auf
  dem Server fest.
- Leerzeichen in `What=` nicht quoten; `\\server\freigabe\ordner` wird zu
  `//server/freigabe/ordner`.
- Auf CIFS sind die Dateirechte eine **Anzeige** aus den Mount-Optionen
  (`fchmod` ist ein No-Op). Der echte Schutz ist die ACL auf dem Server.
- `serverino` ist Voraussetzung für stabile Inodes; unterstützt der Server es
  nicht, fällt der Kernel still auf `noserverino` zurück (`dmesg`). Kontrolle:
  `findmnt -no OPTIONS /srv/jnpt/ablage/<name> | tr , '\n' | grep -x serverino`.
- Was der Use Case braucht, **als der Nutzer prüfen, der später schreibt**:

  ```bash
  setpriv --reuid=65532 --regid=65532 --clear-groups sh -c '
    D=/srv/jnpt/ablage/<name>/.probe; mkdir $D && echo x > $D/a &&
    ln $D/a $D/b && mv $D/b $D/c && rm $D/a $D/c && rmdir $D && echo "alles gut"'
  ```

### SharePoint (`sharepoint/`)

Zwei **Bringer** auf dem Host verbinden die **lokale** Ablage mit zwei Sites
des Kunden. Kundenseitig: zwei Sites, zwei App-Registrierungen, nur
`Sites.Selected`, Site-Grant `write` je App.

| Bringer | Richtung | läuft als | Takt | Site |
|---|---|---|---|---|
| `jnpt-sicherung-sharepoint.sh` | Box → Site | root | täglich 02:30 (+ bis 15 min) | Sicherung |
| `jnpt-ablage-abgleich.sh` | beide Richtungen (`rclone bisync`) | **uid 65532** | alle 5 min | Use Cases |

```bash
cd /opt/jnpt/box/sharepoint
sh install-rclone.sh v1.75.1            # rclone ≥ 1.69 (Client-Credentials), Release-Prüfsumme
groupadd -g 65532 jnpt; useradd -r -u 65532 -g 65532 -d /nonexistent -s /usr/sbin/nologin jnpt

install -d -o root -g jnpt -m 0750 /etc/jnpt/rclone
cp sicherung.app.example /etc/jnpt/rclone/sicherung.app; cp ablage.app.example /etc/jnpt/rclone/ablage.app
$EDITOR /etc/jnpt/rclone/*.app          # CLIENT_ID, TENANT_ID
chown root:jnpt /etc/jnpt/rclone/*.app; chmod 0640 /etc/jnpt/rclone/*.app

# Secrets und Site-URLs TIPPT DER MENSCH, im eigenen Terminal:
ssh -t root@<box> sh /opt/jnpt/box/sharepoint/zugang-sp.sh

python3 sp-einrichten.py sicherung      # Token? Site 200/403/404? Bibliothek? Schreibrecht? Wurzel 403?
python3 sp-einrichten.py ablage         # schreibt je Bringer den rclone-Remote — ohne Secret
sh rclone-probe.sh sicherung
setpriv --reuid=65532 --regid=65532 --clear-groups sh rclone-probe.sh ablage

# lokaler Einstiegspunkt des Use Case; Papierkorb AUSSERHALB von /ablage
chattr -i /srv/jnpt/ablage
install -d -o 65532 -g 65532 -m 0750 /srv/jnpt/ablage/<name> /srv/jnpt/ablage-papierkorb/<name>
chattr +i /srv/jnpt/ablage
# OBEN/LOKAL in jnpt-ablage-abgleich.sh und ReadWritePaths in der Unit auf <name> anpassen

install -m 0755 jnpt-sicherung-sharepoint.sh jnpt-ablage-abgleich.sh /usr/local/sbin/
install -m 0644 jnpt-*.service jnpt-*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl start jnpt-sicherung-sharepoint.service && journalctl -u jnpt-sicherung-sharepoint -n 5
setpriv --reuid=65532 --regid=65532 --clear-groups /usr/local/sbin/jnpt-ablage-abgleich.sh resync   # EINMAL
systemctl enable --now jnpt-sicherung-sharepoint.timer jnpt-ablage-abgleich.timer
```

- **Sicherung:** Sperre → Bibliothek erreichbar? (sonst Abbruch **vor** dem
  Backup) → `jnpt backup` lokal → Upload unter `.teil` → serverseitig
  umbenennen → Größe gegenlesen → lokale Kopie weg → Generationen aufräumen
  (14 Tage täglich, dazu je Monat der älteste Stand der letzten drei Monate;
  entfernte Artefakte landen im Papierkorb der Site).
- **Abgleich:** app-only · beide Richtungen, fester Takt, Sperre · Konflikt =
  beide Fassungen (`..konflikt-lokal` / `..konflikt-sharepoint`) · Löschungen
  mit Netz (lokaler Papierkorb außerhalb `/ablage`, Ordner `Papierkorb/` in
  der Site, höchstens 50 je Lauf) · Punkt-Dateien bleiben lokal · nicht
  übertragbare Namen sind ein Fehler, kein stilles Auslassen.
  `--check-access`: `RCLONE_TEST` liegt auf **beiden** Seiten; fehlt es auf
  einer, läuft nichts — eine leere Seite räumt nie die andere leer.
- **Stolperfallen:**
  1. Abgleich **als 65532**, nie als root (Hardlink-Verbot, s. o.).
  2. rclone schreibt sein kurzlebiges Zugriffs-Token **in die Config**; ist sie
     nicht beschreibbar, versucht es das zehnmal je Aufruf. Deshalb je Bringer
     eine eigene, beschreibbare Config; das Secret steht nie darin (Env
     `RCLONE_CONFIG_<NAME>_CLIENT_SECRET`).
  3. `grep` in einer `pipefail`-Pipeline macht aus einem stillen Erfolg einen
     Fehler — im Abgleich filtert `sed`.
  4. Die Site-URL kann die App mit `Sites.Selected` nicht selbst finden (Suche
     und Wurzel-Site sind 403 — gewollt). Sie kommt vom Kunden.
  5. Wer die Ablage-Ordner **in SharePoint** löscht oder umbenennt, ersetzt sie
     lokal (s. o., Reload). In der Kundenanleitung: Ordner nicht anfassen,
     Dateien ja.
  6. Das Client-Secret läuft ab (üblich: 12 Monate). Ablaufdatum führen,
     rechtzeitig per `zugang-sp.sh` erneuern.

| Symptom | Ursache | Griff |
|---|---|---|
| „Bibliothek nicht erreichbar" | Netz weg, Grant entzogen, Secret abgelaufen | `sp-einrichten.py sicherung`: 401 Token, 403 Grant, 404 Adresse |
| Abgleich: „must run --resync" | Listings weg oder Erstlauf | einmal `… resync` als 65532 |
| Konfliktkopien häufen sich | beide Seiten ändern dieselbe Datei | Arbeitsteilung klären — der Abgleich entscheidet bewusst nicht |
| `move_file` scheitert | Quelldatei gehört nicht 65532 | `chown 65532` |

## 11. Optional: EBICS-Abholer auf der Box

Der Bankabruf ist ein eigener Beisteller: [`../ebics-abholer/`](../ebics-abholer/).
Auf der Box kommen drei Dinge dazu:

- **Netz pinnen.** Die Host-Firewall kennt nur gepinnte Bridges; der Abholer
  bekommt `jnpt-ebics0` (die Regel steht in `hardening.sh`, nur 443 ausgehend).
  Ohne Pin: DNS geht, Egress ist lautlos tot. Ein Compose-Overlay neben der
  Abholer-Compose und ein systemd-Drop-in, das beide Dateien lädt:

  ```yaml
  # /opt/jnpt/ebics-abholer/docker-compose.box-netz.yml
  networks:
    default:
      driver_opts:
        com.docker.network.bridge.name: jnpt-ebics0
  ```

- **Bauen mit `docker build --network=host`** — der Build-Container hängt sonst
  an `docker0`, die die Firewall bewusst zu hält; `docker compose build` kennt
  kein `--network`.
- **Dasselbe Verzeichnis** (`/var/lib/jnpt/ebics-eingang`, uid 65532) als
  Ablage des Abholers und, schreibgeschützt, als Quelle der Datei-Integration —
  die vorbereitete Zeile in der Compose einkommentieren, **nachdem** der Ordner
  mit der richtigen Kennung angelegt ist.

## Stolperfallen und Fehlerbilder

| Bild | Ursache | Abhilfe |
|---|---|---|
| Nach einem Reboot `caddy`/`caddy-admin` `Exited (128)`, nur `jnpt` läuft | Docker startete vor dem Netz („cannot assign requested address") | sofort `docker compose -f docker-compose.box.yml up -d --force-recreate caddy caddy-admin` (ein bloßes `restart` bindet die Ports nicht neu); dauerhaft Boot-Ordnung aus Abschnitt 3 |
| Gateway oder Tunnel-Client ohne DNS („no external nameservers", „server misbehaving") | Container vor dem Resolver gestartet, oder Tailscale hat den Resolver umgehängt | `restart`; `tailscale set --accept-dns=false` |
| `docker compose ps` zeigt nur `jnpt` | ohne `-f` gestartet, anderes Projekt gefunden | immer `-f docker-compose.box.yml`; `docker compose ls` |
| Leere Datenbank nach Neustart | anderes `-p` → anderes Volume | ohne `-p` neu starten; die Daten liegen im anderen Volume |
| `jnpt` startet nicht: „permission denied" / „kaputte Key-Datei" | Secret gehört nicht 65532 / ist kein Hex | 4.2 |
| `up` scheitert: „DOCKER-FORWARD … No chain" | `flush ruleset` der Härtung hat Dockers Ketten abgeräumt | `systemctl restart docker`, dann `up` wiederholen |
| Container laufen, „können aber nichts" | forward-Kette frisst den Verkehr | `nft list chain inet filter forward` muss die `jnpt0`-Ausnahmen zeigen; Diagnose-Container brauchen `--network jnpt_default` |
| Box bleibt an der Passphrase stehen | TPM2-Unlock greift nicht: Secure Boot geändert, BIOS-Update, initramfs ohne Clevis | mit Passphrase booten; `clevis luks list` und initramfs auf Clevis prüfen (2.2); bis zur Klärung den automatischen Reboot abschalten |
| Kein SSH | so gebaut: SSH nur über `tailscale0` | Wartungszugang (5.4) oder Monitor und Tastatur |
| ACME-Fehler im Caddy-Log, 443 ohne TLS | `JNPT_CADDY_TLS` leer → ACME ohne öffentliche Erreichbarkeit | `JNPT_CADDY_TLS="tls internal"`, `up -d caddy` |
| Caddy antwortet mit leerem 200 | unbekannter `Host`-Header (Aufruf über die IP) | den Box-Namen als Host und SNI setzen (`--resolve`) |
| Aktuator rollt jedes Update zurück | `DOMAIN` zeigt auf eine Fläche ohne vertrautes Zertifikat | Abschnitt 7 |
| `/` antwortet unter dem Box-Namen durch den Tunnel | harpoon-Ziel zeigt auf `jnpt:8484` | Profil korrigieren — harpoon sieht nur Caddy |
| `/` antwortet auf der Funnel-Adresse | Admin-Fläche öffentlich | **sofort** `tailscale funnel reset` |
| `/admin/mcp` im LAN 404 | die Sicherheitsgrenze | Admin über 8443 oder das Tailnet |
| Kunde meldet „geht nicht", der Hersteller sieht nichts | kein stehendes Monitoring — gewollt | Kontaktweg steht auf dem Notfall-Einleger |
