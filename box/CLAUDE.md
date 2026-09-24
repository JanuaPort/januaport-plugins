# CLAUDE.md — `box` (Provisionierung der JanuaPort Box)

Für Agenten, die an diesem Ordner arbeiten. Die Anleitung für Betreiber steht
in `RUNBOOK.md`, die Übersicht in `README.md` — hier steht, warum die Hülle so
aussieht, wie sie aussieht.

## Verantwortung

Die **Hülle** einer JanuaPort-Anlage auf einem Mini-PC beim Kunden: Host-Härtung,
Boot-Ordnung, Compose-Stack, Reverse-Proxy-Flächen, Vorlagen und die
Provisionierungs-Checkliste. **Kein Gateway-Code**, kein Go, kein Terraform —
eine Kiste auf einem Tisch hat keinen Provider und keine API; der Bootstrap ist
ein Mensch mit `RUNBOOK.md`.

**Nicht hier, mit Absicht:**

- **Inbetriebnahme, Sicherung/Restore, Update, Lizenz** sind Funktionen des
  Produkts (`jnpt backup`/`restore`, Update-Aktuator, Lizenz-Abruf). Die Hülle
  ruft sie auf und nennt nur ihre box-spezifischen Zusätze. Wer hier einen
  Schritt findet, den die JanuaPort-Dokumentation auch beschreibt, hat einen
  Fehler gefunden.
- **Update-Aktuator und Tunnel-Stack** (Compose, Units, tmpfiles-Regel) gelten
  für jede Anlage und kommen mit JanuaPort. Hier liegt nur das Box-Profil des
  Tunnel-Clients (`tunnel/`).
- **EBICS-Abholer** liegt in `../ebics-abholer/`; die Hülle steuert nur die
  Firewall-Regel für seine Bridge und die Netz-Anpassung bei (`RUNBOOK.md` §11).
- Kein Golden Image, kein PXE, keine Serienfertigung, kein Call-Home.

**Herkunft:** bereinigte Fassung des Pilot-Stands aus dem nicht öffentlichen
Produkt-Repository (Stand ea613985). Bis nach dem GoLive wird dort gepflegt;
die Zusammenführung ist geplant (JanuaPort/januaport#876). Wer hier ändert,
prüft, ob die Änderung dort nachgezogen werden muss — und umgekehrt.

## Öffentliche Schnittstelle

**Installationsort:** Der Ordner liegt auf der Box als `/opt/jnpt/box`. Diesen
Pfad kennen `boot/jnpt-stack-nachstart.service`, `smb/jnpt-backup.sh`,
`sharepoint/jnpt-sicherung-sharepoint.sh` und das Runbook. `hardening.sh` findet
`../boot/` relativ zu sich selbst.

**Compose (`docker-compose.box.yml`):** Projekt `jnpt` (Top-Level `name:`),
Dienste `jnpt`, `caddy`, `caddy-admin`, `tailscale` (Profil `wartung`), Netz
`jnpt_default` mit Bridge `jnpt0`, Volumes `jnpt_jnpt-data`, `jnpt_caddy-data`,
`jnpt_caddy-admin-data`, `jnpt_tailscale-state` (+ `*-config`), Secrets
`jnpt_master_key`, `tailscale_authkey`.

**Variablen der `.env`** (nur die Hülle liest sie; `JNPT_*` des Produkts werden
nach dem Muster `${VAR:-}` durchgereicht, leer = aus):

| Variable | Wer liest | Bedeutung |
|---|---|---|
| `JANUAPORT_IMAGE` | Compose | Pflicht, gepinnter Tag |
| `JNPT_DOMAIN` | Caddy, Netz-Alias | Box-Name |
| `JNPT_CADDY_TLS` | Caddy | auf der Box `tls internal` |
| `JNPT_ACME_EMAIL` | Caddy | globale Option, bei interner CA unbenutzt |
| `JNPT_LAN_ALIAS` | Caddy | optionaler zweiter Name |
| `JNPT_BIND_ADDR` | Compose, caddy-admin | Host-Bind 443/8443, `default_sni` |
| `JNPT_ACME_BIND_ADDR` | Compose | Host-Bind 80, bleibt Loopback |
| `JNPT_ABLAGE_HOST` | Compose | Host-Pfad der Ablage (Default `/srv/jnpt/ablage`) |
| `TS_HOSTNAME` | tailscale | Knotenname des Wartungs-Containers |
| `COMPOSE_PROFILES` | Compose | `wartung` = Fernwartung eingeräumt |
| `JNPT_SSO_*`, `JNPT_PUBLIC_URL`, `JNPT_UPDATE_*`, `JNPT_LICENSE_FETCH_*`, `JNPT_MAX_VERIFY_CONCURRENCY` | Gateway | Durchreiche |

**Host-Pfade:** `/opt/jnpt/box` (Hülle), `/opt/jnpt/tunnel` (Tunnel-Stack),
`/run/jnpt/tunnel` (tmpfs, Laufzeit-Schlüssel), `/srv/jnpt/ablage` (Ablage,
`chattr +i`), `/srv/jnpt/sicherung` (nur Host), `/etc/jnpt/smb/`,
`/etc/jnpt/rclone/`, `/var/lib/jnpt/rclone-*`, `/etc/default/jnpt-netz`,
`/usr/local/share/ca-certificates/jnpt-box-{caddy,admin}-ca.crt`.

**Units:** `jnpt-stack-nachstart.service`, Drop-in
`docker.service.d/10-netz-vor-docker.conf` (+ `/usr/local/sbin/jnpt-netz-warten`),
`jnpt-backup.{service,timer}` und `srv-jnpt-*.mount` (SMB),
`jnpt-sicherung-sharepoint.{service,timer}`, `jnpt-ablage-abgleich.{service,timer}`.

**Feste Adressen:** `10.201.44.0/24`, dynamisch `.128/25`; `caddy` `.10`,
`tailscale` `.11`, `caddy-admin` `.12`; `JNPT_TRUSTED_PROXY_CIDR` = genau diese
drei.

## Invarianten

1. **`jnpt` veröffentlicht NIE einen Host-Port.** Wege hinein: `caddy`
   (gefiltert), `caddy-admin` (LAN), `tailscale` (Tailnet), Tunnel-Client
   (Compose-Netz).
2. **Die Werkzeug-Fläche (443) ist exakt die `@public`-Whitelist** in
   `Caddyfile`: `/mcp` (exakt), `/mcp/v/*`, `/healthz`,
   `/.well-known/oauth-protected-resource` (+ Subtree). Alles andere 404. Nie
   einen Admin-Pfad aufnehmen. Die Direktiven sind eine Kopie der
   Produkt-Whitelist und werden nicht eigenständig verändert.
3. **Kein öffentlicher Endpunkt.** Der Cloud-KI-Weg ist ausgehend (Tunnel).
   harpoon-Ziele zeigen nur auf Caddy (Box-Name), **nie** auf `jnpt:8484`. Im
   Funnel-Rückfall ist der Ingress ausschließlich `/mcp` am Host-Knoten; der
   Wartungs-Container wird nie veröffentlicht.
4. **Deny-inbound in zwei Schichten:** Compose-Bind default `127.0.0.1`,
   Host-Firewall `policy drop`. Keine Schicht allein ist der Schutz.
5. **SSH nur über `tailscale0`, nur Public-Key.** Ohne Opt-in kein
   `tailscale0`, kein Fernzugang — Absicht.
6. **Fernwartung nur mit Opt-in.** Das Profil `wartung` steht als
   `COMPOSE_PROFILES` in der `.env` und nirgends sonst — kein Skript und keine
   Unit schaltet es selbst ein. Tailnet-ACLs sperren per Standard; der Zugang
   ist eine benannte, entfernbare Regel.
7. **Kein Phone-Home, kein stehendes Monitoring durch den Hersteller.**
8. **Kein `:latest`.** `JANUAPORT_IMAGE` ist Pflicht ohne Default.
9. **Keine echten Secrets, Namen, Adressen oder Kundendaten im Repository.**
   Nur `*.example`, RFC-5737-Adressen, `example.com`; `.gitignore` hält `.env`
   und `secrets/*` draußen.
10. **Die Box bootet ohne Menschen vollständig** — Netz vor Docker, Host-Resolver
    bleibt beim DHCP, nach **jedem** Reboot laufen **alle** Container. Beweis
    ist eine Reboot-Serie, nicht ein Boot.
11. **`JNPT_TRUSTED_PROXY_CIDR` ist fest** `10.201.44.10,10.201.44.11,10.201.44.12`,
    nie das Subnetz, nie per `${…}` überschreibbar — am Netz hängt auch der
    Tunnel-Client. Subnetz, `ip_range`, drei `ipv4_address` und Liste ändern
    sich nur gemeinsam.
12. **Die Sicherung hängt nie unter `/ablage`.** Die KI sieht sie per
    Konstruktion nicht.
13. **Der Master-Key gehört dem Kunden.** Er wird bei der Einrichtung erzeugt,
    geht versiegelt an den Kunden; der Hersteller behält keine Kopie.

## Bewusste Design-Entscheidungen

- **Projektname in der Datei statt `-p`.** Ohne Namen leitet Compose ihn vom
  Verzeichnis ab und legt leere Volumes an. Ein `-p`, an das jeder Hand-Start
  denken muss, ist eine Falle mit Datenverlust als Preis.
- **Wartungsprofil über `COMPOSE_PROFILES` in der `.env`** statt
  `--profile wartung` in Units und Befehlen. So startet der Boot, der Aktuator
  und jeder Hand-Start dasselbe — und ohne Opt-in nie den Wartungszugang. (Die
  Pilot-Anlage rief das Profil noch fest in der Boot-Unit.)
- **Die Hülle ist in sich geschlossen.** `Caddyfile` und `tailscale-serve.json`
  liegen als Kopie hier statt als relativer Mount in ein Produkt-Repository.
  Preis: eine zweite Stelle für die Whitelist — deshalb Invariante 2.
- **Zwei Caddy-Instanzen.** Die Betreiber-Fläche braucht `default_sni` (IP-Aufrufe
  senden kein SNI), eine globale Option; im Haupt-Caddy hätte sie die Whitelist
  berührt.
- **Interne CA statt ACME.** Die Box hat keinen öffentlichen Namen; der
  Box-Name lebt nur in der Box, im Identity-Provider und im Client-Dialog. Der
  Tunnel-Client vertraut der Wurzel per `CA_BUNDLE`.
- **Tunnel als eigener Stack**, nicht in dieser Compose: eigener Update-Takt,
  eigener Lebenszyklus; ein Neustart des Clients reißt das Gateway nie mit.
  Einzige Berührung: der Netz-Alias `${JNPT_DOMAIN}` am `caddy`.
- **Festes Netz direkt in der Compose, kein Override** — der Aktuator kennt
  genau eine Datei; ein Override wäre eine zweite, an die jeder denken müsste.
- **SSH-Grenze in der Firewall (`iifname "tailscale0"`), nicht als
  `ListenAddress`.** Die Tailnet-Adresse gibt es beim Boot noch nicht;
  `ListenAddress` ließe sshd scheitern. `iifname` statt `iif`, weil `iif` beim
  Laden ein existierendes Interface verlangt.
- **TPM2 über Clevis an PCR 7.** Debians initramfs-tools kennen den
  systemd-Token-Weg nicht; PCR 0 bräche das Auto-Unlock bei jedem BIOS-Update.
- **Nächtlicher Reboot nur mit belegtem Auto-Unlock** — sonst ist er ein
  Ausfallrisiko.
- **Port 80 bleibt zu.** Keine ACME-Challenge nötig; kein Port auf Vorrat.
- **Ausgehend offen.** Eine Outbound-Whitelist wäre für ein Gateway, das
  beliebige Kundensysteme anspricht, nicht wartbar.
- **SMB/SharePoint als Host-Sache.** Das Gateway sieht nur ein Verzeichnis; ein
  Client im Binary bräche das statische, CGO-freie Binary.

## Stolperfallen

- **Docker startet vor dem Netz.** `allow-hotplug` + DHCP: `network-online` ist
  sofort „erreicht". Caddy scheitert am Bind, das ist ein Start-Fehler, kein
  Crash — keine Restart-Politik holt ihn. `ifupdown-wait-online` allein reicht
  nicht (der Link kam Sekunden nach „fertig"); deshalb `jnpt-netz-warten` und
  `jnpt-stack-nachstart`. Handheilung: `up -d --force-recreate caddy caddy-admin`
  (ein `restart` bindet die Ports nicht neu).
- **nftables ist ein UND über alle Tabellen.** Ein `accept` beendet nur die
  eigene Kette; Dockers Freigaben ersetzen die `jnpt0`-Ausnahmen in unserer
  forward-Kette nicht. Die 443-Politik lebt in **forward** (DNAT vor dem
  Routing), nicht in input.
- **`flush ruleset` räumt Dockers Regeln ab.** `hardening.sh` startet Docker
  nach einem Ruleset-Wechsel neu; bleibt „No chain … DOCKER-FORWARD", hilft
  `systemctl restart docker`.
- **`docker compose` ohne `-f`** findet über den Verzeichnis-Aufstieg eine
  fremde Compose — kein Fehler, ein anderes Projekt.
- **Non-root-Container und Datei-Secrets.** Compose ohne Swarm ignoriert
  `uid`/`mode`; das Secret muss uid 65532 gehören. Master-Key als Hex.
- **`TS_AUTHKEY_FILE` wird vom Tailscale-Image ignoriert** — der Entrypoint-
  Wrapper liest den Key aus dem Secret.
- **Der Wartungs-Container bringt den Host nicht ins Tailnet** (Userspace). Zwei
  Schalter: Container und Host-`tailscaled`; der Entzug braucht beide.
- **Tailscale hängt den Resolver um** — ohne `--accept-dns=false` erben die
  Container MagicDNS.
- **Ein geänderter `networks`-Block** legt `jnpt_default` neu an und scheitert,
  solange der Tunnel-Client daran hängt — `jnpt` bleibt gestoppt. Nur im
  Wartungsfenster nach `RUNBOOK.md` §9.
- **`propagation: rslave` am Ablage-Bind** — ohne bleiben später gemountete
  Einstiegspunkte im Container leer, lautlos.
- **Ablage-Ordner nie ersetzen.** Das Gateway nagelt sie an Gerät + Inode fest;
  Heilung `docker kill -s HUP jnpt-jnpt-1`. Für CIFS `serverino`.
- **Alles in einer Ablage gehört 65532** (`fs.protected_hardlinks=1`; `move_file`
  verschiebt per Hardlink). Der SharePoint-Abgleich läuft deshalb als 65532.
- **harpoon:** ohne Ziel `unsupported channel`, Komma in `description` =
  Endlos-Neustart; `log.level` ohne `log.format` = Endlos-Neustart;
  `CA_BUNDLE`, nicht `SSL_CERT_FILE`.
- **Aktuator-`DOMAIN`** muss auf eine Fläche mit vertrautem Zertifikat zeigen,
  sonst rollt er jedes Update zurück.
- **Das Admin-Passwort vor dem Transport setzen** — sonst beansprucht der
  Erste im Kunden-LAN den Admin-Zugang.
- **Namen nicht „aufräumen".** Volume-, DB- und Key-Namen auf einer laufenden
  Anlage umzubenennen kostet Daten.
- **Pilotwerte gehören in keine Datei dieses Repositorys** — nicht in
  Beispiele, nicht in Kommentare, nicht in Commit-Nachrichten. Lehren aus dem
  Betrieb werden als allgemeine Stolperfalle formuliert.

## Verifikation

- `bash -n` für alle Shell-Skripte, `python3 -m py_compile` für
  `scripts/lasttest.py` und `sharepoint/*.py`.
- `docker compose -f docker-compose.box.yml config -q` mit gesetztem
  `JANUAPORT_IMAGE`, einmal ohne und einmal mit `COMPOSE_PROFILES=wartung`
  (der Dienst `tailscale` erscheint nur dann). Ohne `JANUAPORT_IMAGE` bricht
  Compose ab — gewollt.
- Secret-Scan: `.github/workflows/secret-scan.yml` (gitleaks über die ganze
  Historie).
- Am Gerät: Reboot-Serie, Rundlauf nach `RUNBOOK.md` §6. `hardening.sh` ist
  ohne Debian-Host nicht prüfbar (nftables, sshd, rfkill).

## Verwandt

`README.md` · `RUNBOOK.md` · `tunnel/README.md` · `sharepoint/README.md` ·
`../ebics-abholer/`
