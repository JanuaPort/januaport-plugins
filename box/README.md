# JanuaPort Box — Provisionierung

Die Hülle, mit der aus einem lüfterlosen Mini-PC eine **JanuaPort Box** wird:
eine Appliance, auf der das JanuaPort-Gateway **beim Kunden** läuft, im eigenen
Netz, ohne einen einzigen eingehenden Port aus dem Internet. Dieser Ordner
enthält die Compose-Datei, das Härtungsskript, die Boot-Ordnung, Vorlagen und
die Anleitung. Schritt für Schritt: [`RUNBOOK.md`](RUNBOOK.md).

## Was es ist

- **Bauklasse Hülle:** Provisionierung eines Hosts, kein Bestandteil des
  Gateways. Kein Produktcode, keine eigene Laufzeit — Shell, systemd-Units,
  eine Compose-Datei und eine Checkliste. Das Gateway selbst kommt als fertiges
  Image aus GHCR.
- **Für ein Einzelgerät gebaut.** Der Bootstrap ist ein Mensch mit dieser
  Anleitung, kein Golden Image. Für eine Kiste ist das die richtige Größe — und
  im Ersatzfall ist es genau das, was jemand abarbeitet.
- **Was auf der Box läuft:**

```
  ChatGPT ◀══ausgehend══ Tunnel-Client ───────────┐  eigener Stack, Standardweg
  Kunden-LAN ──443──▶ caddy (Whitelist) ──────────┤
  Betreiber-LAN ─8443─▶ caddy-admin (volle Inst.) ┼──▶ jnpt:8484 (kein Host-Port)
  Tailnet ──▶ tailscale (Profil `wartung`, aus) ──┘   nur nach Opt-in des Kunden

  Host: Debian stable minimal · LUKS + TPM2-Auto-Unlock · nftables deny-inbound
```

- **Was es NICHT ist:**
  - **Kein Cloud-Dienst.** Die Box steht beim Kunden; Daten und Schlüssel
    bleiben dort. Der Master-Key geht versiegelt an den Kunden, der Hersteller
    behält keine Kopie.
  - **Kein öffentlicher Endpunkt.** Die Cloud-KI erreicht die Box über einen
    ausgehenden Tunnel; von außen antwortet die Box auf nichts.
  - **Kein stehender Fernzugang.** Wartung über Tailscale gibt es nur, wenn der
    Kunde sie ausdrücklich einräumt — sichtbar als eigenes Compose-Modul und
    jederzeit entziehbar.
  - **Kein Golden Image, keine Serienfertigung, kein Call-Home-Onboarding.**

## Datenvertrag zum Gateway

Die Hülle berührt das Gateway nur an benannten Stellen:

| Naht | Richtung | Was |
|---|---|---|
| Image | Box → GHCR | `JANUAPORT_IMAGE`, gepinnter Tag, kein `:latest` |
| Umgebung | `.env` → Container | `JNPT_*`-Variablen, Durchreiche nach dem Muster `${VAR:-}` (leer = aus) |
| Master-Key | Host → Container | Docker-Secret `jnpt_master_key` (64 Hex-Zeichen, uid 65532, 0400) |
| Daten | Volume | `jnpt_jnpt-data` → `/data` (SQLite, lokale Sicherungen) |
| Ablage | Host-Mount → Container | `/srv/jnpt/ablage` → `/ablage`, `propagation: rslave` |
| Tunnel-Schlüssel | Container → Host (tmpfs) | Das Gateway legt ihn aus dem Tresor nach `/run/jnpt/tunnel/`, der Tunnel-Stack liest ihn |
| HTTP | Proxy → Container | nur `jnpt:8484` im Netz `jnpt_default` (feste Proxy-Adressen `10.201.44.10–12`) |
| Sicherung | Host → Container | `docker compose exec jnpt /jnpt backup` — dieselbe Mechanik, die der Update-Aktuator nutzt |

Die Hülle schreibt nie in die Datenbank und liest keine Geheimnisse aus dem
Gateway. Sicherung, Wiederherstellung, Update und Lizenz sind Funktionen des
Produkts; die Hülle ruft sie nur auf.

## Voraussetzungen

- Ein Mini-PC der Klasse Intel N100/N150, lüfterlos, für Dauerbetrieb, mit
  **TPM 2.0** und kabelgebundenem LAN; 8 GB RAM, 500 GB SSD reichen.
- Debian stable (minimal), Docker CE mit Compose-Plugin.
- Ein gepinnter JanuaPort-Tag und eine Lizenz.
- Beim Kunden: eine DHCP-Reservierung für die Box und ausgehendes HTTPS.

## Enthaltene Dateien

| Datei | Inhalt |
|---|---|
| `docker-compose.box.yml` | der Stack: `jnpt`, `caddy` (443), `caddy-admin` (8443), `tailscale` (Profil `wartung`), festes Netz |
| `.env.example` | Vorlage der `.env`, alle Variablen mit Begründung |
| `Caddyfile` | Werkzeug-Fläche: Whitelist + Default-404 (die Sicherheitsgrenze) |
| `Caddyfile.admin` | Betreiber-Fläche: volle Instanz, interne CA, nur LAN |
| `tailscale-serve.json` | Wartungs-Fläche im Tailnet (volle Instanz) |
| `secrets/*.example` | Platzhalter der Docker-Secrets |
| `scripts/hardening.sh` | idempotente Host-Härtung in sieben Schritten |
| `scripts/lasttest.py` | Lastsuite gegen `/mcp` (nur Zähler und Perzentile) |
| `boot/` | Boot-Ordnung „Netz vor Docker" (von `hardening.sh` installiert) |
| `tunnel/` | Profil-Vorlage des OpenAI-Tunnel-Clients |
| `sharepoint/` | optionale Bringer: Sicherung und Ablage-Abgleich mit SharePoint |
| `smb/` | optionale Mount-Units und Sicherung auf eine SMB-Freigabe |
| `RUNBOOK.md` | die Anleitung |
| `CLAUDE.md` | Hinweise für KI-Agenten, die an diesem Ordner arbeiten |

**Nicht in diesem Ordner:** der Update-Aktuator und der Tunnel-Stack (Compose,
Units). Beide gelten für jede JanuaPort-Anlage und werden mit JanuaPort
ausgeliefert. Der optionale Bankabruf liegt nebenan in
[`../ebics-abholer/`](../ebics-abholer/).

## Status

**Gebaut; läuft seit August 2026 auf einer Pilot-Anlage.** Am Gerät belegt sind
unter anderem die Boot-Ordnung (Reboot-Serie ohne Handgriff), der
TPM2-Auto-Unlock über Clevis, das automatische Update über den Aktuator, die
Betreiber-Fläche mit interner CA, der Tunnel-Weg zu ChatGPT im Alltagsbetrieb, die Tailnet-ACL mit Standard-Sperre und der
SharePoint-Weg samt Rücksicherung. **Nicht belegt:** die Gegenprobe der
Tailnet-ACL von einem Konto mit fremder Identität, die pfadgenaue Form des
Funnel-Rückfalls, der Ersatzbox-Durchlauf mit Zeitmessung und der SMB-Weg im
Dauerbetrieb (gebaut und geprüft, aber nicht im Einsatz).

Diese Fassung ist gegenüber der Pilot-Anlage **verallgemeinert**: neutrale
Namen und Pfade, der Installationsort `/opt/jnpt/box`, das Wartungsprofil über
`COMPOSE_PROFILES` in der `.env`. In genau dieser Form ist sie noch nicht auf
einem Gerät gelaufen.

**Herkunft:** Bereinigter Stand aus dem (nicht öffentlichen) Produkt-Repository,
Stand ea613985; bis nach dem GoLive wird dort gepflegt, die Zusammenführung ist
geplant (JanuaPort/januaport#876).
